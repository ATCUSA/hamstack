package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/ATCUSA/hamstack/internal/apps"
	"github.com/ATCUSA/hamstack/internal/ax25"
	"github.com/ATCUSA/hamstack/internal/catalog"
	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/distro"
	"github.com/ATCUSA/hamstack/internal/doctor"
	"github.com/ATCUSA/hamstack/internal/hamlib"
	"github.com/ATCUSA/hamstack/internal/netinfo"
	"github.com/ATCUSA/hamstack/internal/profile"
	"github.com/ATCUSA/hamstack/internal/svc"
	"github.com/ATCUSA/hamstack/internal/tui"
	"github.com/ATCUSA/hamstack/internal/units"
	"github.com/ATCUSA/hamstack/internal/updates"
	"github.com/ATCUSA/hamstack/internal/usbdev"
	"github.com/ATCUSA/hamstack/internal/versions"
)

// backend connects the TUI to the real configuration and services. Nothing here prints: printing would
// corrupt the screen, so every method returns a message instead.
type backend struct{}

func loadCfg() config.Config {
	c, _ := config.Load()
	return c
}

// mutate loads the configuration, applies fn, saves it, and brings any installed units in line.
func mutate(fn func(*config.Config) (string, error)) (string, error) {
	cfg, err := config.Load()
	if err != nil {
		return "", err
	}
	before := cloneConfig(cfg)
	msg, err := fn(&cfg)
	if err != nil {
		return "", err
	}
	if err := config.Save(cfg); err != nil {
		return "", err
	}
	note, err := applyUnits(before)
	if err != nil {
		return msg, err
	}
	return joinMsg(msg, note), nil
}

// mutateOnly is mutate for changes that cannot affect the units.
func mutateOnly(fn func(*config.Config) (string, error)) (string, error) {
	cfg, err := config.Load()
	if err != nil {
		return "", err
	}
	before := cloneConfig(cfg)
	msg, err := fn(&cfg)
	if err != nil {
		return "", err
	}
	if err := config.Save(cfg); err != nil {
		return "", err
	}
	if err := recordRestarts(before, cfg); err != nil {
		return msg, err
	}
	return msg, nil
}

func joinMsg(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "; ")
}

// capture returns an asker whose output is collected, and which has no input: anything that would ask a
// question gets no answer, which the operations treat as "none chosen".
func capture() (*asker, *bytes.Buffer) {
	var buf bytes.Buffer
	return &asker{in: bufio.NewReader(strings.NewReader("")), out: &buf}, &buf
}

func firstLines(b *bytes.Buffer, n int) string {
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(b.String()), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "; ")
}

func (backend) Header() tui.Header {
	cfg := loadCfg()
	h := tui.Header{Grid: cfg.EffectiveGrid()}
	if s, ok := cfg.Here(); ok {
		h.Site = s.ID
	}
	var rigs []string
	for _, r := range cfg.ActiveRigs() {
		rigs = append(rigs, r.ID)
	}
	h.Rigs = strings.Join(rigs, ", ")
	if a, ok := cfg.ActiveStation(); ok {
		h.Station = a.ID + " (" + a.Callsign + ")"
	}
	return h
}

func (backend) NeedsSetup() bool {
	cfg := loadCfg()
	return len(cfg.Rigs) == 0 && len(cfg.Stations) == 0
}

// ---- services ----

func (backend) Services() []tui.Service {
	var out []tui.Service
	for _, sv := range units.ServicesFor(loadCfg()) {
		st := svc.Status(sv.Unit())
		s := tui.Service{Name: sv.Name(), Installed: st.Load != "not-found"}
		if !s.Installed {
			s.State, s.Enabled = "not installed", "-"
		} else {
			s.State, s.Enabled, s.Since = st.Display(), orDash(st.Enabled), st.Since
		}
		out = append(out, s)
	}
	return out
}

func (backend) Control(verb, service string) error {
	ss, err := units.Resolve(loadCfg(), service)
	if err != nil {
		return err
	}
	return controlQuiet(verb, ss)
}

// UnitsInstall writes the systemd units for the current setup, like 'hamstack units install'.
func (backend) UnitsInstall() (string, error) {
	if err := svc.Available(); err != nil {
		return "", err
	}
	p, warns, err := buildPlan()
	if err != nil {
		return "", err
	}
	written, err := writeUnits(p)
	if err != nil {
		return "", err
	}
	return joinMsg(append([]string{fmt.Sprintf("installed %d service file(s): start them with S", len(written))}, warns...)...), nil
}

func (backend) Logs(service string, lines int) (string, error) {
	ss, err := units.Resolve(loadCfg(), service)
	if err != nil || len(ss) == 0 {
		return "", err
	}
	return svc.LogsText(ss[0].Unit(), lines)
}

// ---- rigs ----

func (backend) Rigs() []tui.Rig {
	cfg := loadCfg()
	profs, snap := loadProfiles(), snapshotOrEmpty()
	var out []tui.Rig
	for _, r := range cfg.Rigs {
		row := tui.Rig{ID: r.ID, Name: r.Name, Radio: r.Profile, Modem: r.Modem, Bands: strings.Join(r.Bands, ","), On: !r.Off, NoCAT: r.NoCAT, Hardware: "-",
			Profile: r.Profile, Baud: r.Baud, Cat: r.CAT, Audio: r.Audio}
		if p := findProfile(profs, r.Profile); p != nil {
			row.Radio = p.Name
			row.DefaultBaud = p.Rig.Baud
			switch {
			case r.Off:
				row.Hardware = "off"
			case rigHardwarePresent(r, *p, snap):
				row.Hardware = "present"
			default:
				row.Hardware = "NOT DETECTED"
			}
		}
		var ports []string
		if !r.NoCAT {
			ports = append(ports, fmt.Sprintf("rig %d", r.RigPort))
		}
		switch r.Modem {
		case config.Mercury, config.Data2G, config.Ardopcf, config.Xara:
			ports = append(ports, fmt.Sprintf("modem %d", r.ArqPort))
		case config.Direwolf:
			agw, _ := config.DirewolfPorts(r)
			ports = append(ports, fmt.Sprintf("packet %d", agw))
		}
		row.Ports = strings.Join(ports, ", ")
		out = append(out, row)
	}
	return out
}

func (backend) Profiles() []tui.ProfileOpt {
	var out []tui.ProfileOpt
	for _, p := range loadProfiles() {
		out = append(out, tui.ProfileOpt{ID: p.ID, Name: p.Name, HasCAT: p.HasCAT()})
	}
	return out
}

func (backend) SearchRadios(query string) ([]tui.RadioHit, error) {
	rigctld, err := versions.Which("rigctld")
	if err != nil {
		return nil, fmt.Errorf("the radio list comes from hamlib, which is not installed: install it first (Programs tab, i)")
	}
	entries, err := hamlib.InstalledList(rigctld)
	if err != nil {
		return nil, err
	}
	profs := loadProfiles()
	var out []tui.RadioHit
	for _, e := range hamlib.Filter(entries, query) {
		h := tui.RadioHit{Model: e.ID, Label: e.Label(), Status: e.Status}
		for _, p := range profs {
			if p.Rig.Model == e.ID {
				h.Profile = p.ID
				break
			}
		}
		out = append(out, h)
	}
	return out, nil
}

func (backend) RadioBaud(model int) (lo, hi, def int) {
	rigctld, err := versions.Which("rigctld")
	if err != nil {
		return 0, 0, 0
	}
	c, err := hamlib.ModelCaps(filepath.Join(filepath.Dir(rigctld), "rigctl"), model)
	if err != nil {
		return 0, 0, 0
	}
	return c.MinBaud, c.MaxBaud, c.DefaultBaud()
}

func (backend) SaveRadio(model, baud int) (string, error) {
	for _, p := range loadProfiles() {
		if p.Rig.Model == model {
			return p.ID, nil
		}
	}
	rigctld, err := versions.Which("rigctld")
	if err != nil {
		return "", fmt.Errorf("hamlib is not installed")
	}
	entries, err := hamlib.InstalledList(rigctld)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if e.ID == model {
			p := newProfileFor(e, baud, loadProfiles())
			return p.ID, saveUserProfile(p)
		}
	}
	return "", fmt.Errorf("hamlib has no model %d", model)
}

func (backend) RigToggle(id string) (string, error) {
	return mutate(func(cfg *config.Config) (string, error) {
		r, ok := cfg.RigByID(id)
		if !ok {
			return "", fmt.Errorf("no rig %q", id)
		}
		r.Off = !r.Off
		cfg.SyncSite()
		if r.Off {
			return "rig " + id + " is off", nil
		}
		return "rig " + id + " is on (start it with S, or restart)", nil
	})
}

func (backend) RigModem(id, modem string) (string, error) {
	msg, err := mutate(func(cfg *config.Config) (string, error) {
		r, ok := cfg.RigByID(id)
		if !ok {
			return "", fmt.Errorf("no rig %q", id)
		}
		if err := setRigField(r, "modem", modem); err != nil {
			return "", err
		}
		return "rig " + id + ": modem " + orDash(modem), nil
	})
	if err == nil {
		if note := patFollowUp(loadCfg()); note != "" {
			msg += ". " + note
		}
	}
	return msg, err
}

func (backend) RigAdd(profileID, id, modem string) (string, error) {
	return mutate(func(cfg *config.Config) (string, error) {
		a, buf := capture()
		if _, err := addRigNamed(a, cfg, rigSpec{ID: id, Profile: profileID, Modem: modem, Detect: true}); err != nil {
			return "", err
		}
		cfg.SyncSite() // a rig added now is on now, so it belongs to the site you are at
		return firstLines(buf, 4), nil
	})
}

func (backend) RigRemove(id string) (string, error) {
	return mutate(func(cfg *config.Config) (string, error) {
		if err := cfg.RemoveRig(id); err != nil {
			return "", err
		}
		return "removed rig " + id, nil
	})
}

// ---- sites ----

func (backend) Sites() []tui.Site {
	cfg := loadCfg()
	scores := scoreSites(cfg)
	var out []tui.Site
	for i, s := range cfg.Sites {
		radios := "-"
		if sc := scores[i]; sc.Known > 0 {
			radios = fmt.Sprintf("%d of %d", sc.Present, sc.Known)
		}
		out = append(out, tui.Site{ID: s.ID, Name: s.Name, Rigs: strings.Join(s.Rigs, ","), Station: s.Station, Grid: s.Grid, Radios: radios, Current: s.ID == cfg.CurrentSite})
	}
	return out
}

func (backend) SiteHint() (string, string, bool) {
	cfg := loadCfg()
	if len(cfg.Sites) == 0 {
		return "", "", false
	}
	if b, ok := betterSite(cfg, scoreSites(cfg)); ok {
		return b.Site.ID, fmt.Sprintf("the radios of site %q are plugged in, not those of %q", b.Site.ID, cfg.CurrentSite), true
	}
	return "", "", false
}

func (backend) SiteUse(id string) (string, error) {
	cfg, err := config.Load()
	if err != nil {
		return "", err
	}
	before := cloneConfig(cfg)
	if err := cfg.UseSite(id); err != nil {
		return "", err
	}
	if err := config.Save(cfg); err != nil {
		return "", err
	}
	msgs, err := siteChangeMessages(before)
	return joinMsg(append([]string{"at " + id}, msgs...)...), err
}

func (backend) SiteAdd(id string) (string, error) {
	return mutateOnly(func(cfg *config.Config) (string, error) {
		s := config.Site{ID: id, Station: cfg.Active, Grid: cfg.EffectiveGrid()}
		for _, r := range cfg.ActiveRigs() {
			s.Rigs = append(s.Rigs, r.ID)
		}
		if err := cfg.AddSite(s); err != nil {
			return "", err
		}
		return fmt.Sprintf("saved site %s: rigs %s, station %s, grid %s", id, orDash(strings.Join(s.Rigs, ",")), orDash(s.Station), orDash(s.Grid)), nil
	})
}

func (backend) SiteRemove(id string) (string, error) {
	return mutateOnly(func(cfg *config.Config) (string, error) {
		if err := cfg.RemoveSite(id); err != nil {
			return "", err
		}
		return "removed site " + id, nil
	})
}

func (backend) SiteGrid(id, grid string) (string, error) {
	return mutateOnly(func(cfg *config.Config) (string, error) {
		if err := cfg.SetSiteField(id, "grid", grid); err != nil {
			return "", err
		}
		return "site " + id + ": grid " + config.NormalizeGrid(grid) + " (hamstack pat sync updates Pat to match)", nil
	})
}

// ---- stations ----

func (backend) Stations() []tui.StationRow {
	cfg := loadCfg()
	var out []tui.StationRow
	for _, s := range cfg.Stations {
		out = append(out, tui.StationRow{ID: s.ID, Callsign: s.Callsign, Name: s.Name, Addr: s.WebURL(netinfo.LANAddrs()), Access: s.EffectiveAccess(), Active: s.ID == cfg.Active})
	}
	return out
}

func (backend) Use(id string) (string, error) {
	msgs, _, err := useStation(id)
	return joinMsg(msgs...), err
}

func (backend) StationAdd(id, callsign string) (string, error) {
	cfg, err := config.Load()
	if err != nil {
		return "", err
	}
	st, created, err := addStation(&cfg, id, callsign, "", "", "", "") // saves the config itself
	if err != nil {
		return "", err
	}
	msg := fmt.Sprintf("added station %s (%s), web UI %s", st.ID, st.Callsign, st.WebURL(netinfo.LANAddrs()))
	if created {
		msg += "; set its Winlink password in Pat (hamstack never stores it)"
	}
	return msg, nil
}

func (backend) StationAccess(id, access string) (string, error) {
	msgs, _, err := setStationAccess(id, access)
	if err != nil && len(msgs) == 0 {
		return "", err
	}
	return strings.Join(msgs, "; "), err
}

func (backend) StationImport() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return mutateOnly(func(cfg *config.Config) (string, error) {
		notes, added, err := importStations(cfg, filepath.Join(home, ".config", "pat"))
		if err != nil {
			return "", err
		}
		if added == 0 {
			return "nothing new to import: " + joinMsg(notes...), nil
		}
		return fmt.Sprintf("imported %d station(s) (the Pat files are unchanged)", added), nil
	})
}

func (backend) StationRemove(id string) (string, error) {
	return mutateOnly(func(cfg *config.Config) (string, error) {
		if err := cfg.RemoveStation(id); err != nil {
			return "", err
		}
		return "removed station " + id, nil
	})
}

// ---- devices ----

func serialKey(s usbdev.Serial) string {
	if s.ByID != "" {
		return "serial|" + s.ByID
	}
	return "serial|" + s.Tty
}

func soundKey(c usbdev.Sound) string { return "sound|" + c.ID + "|" + c.USBID() }

func (backend) Network() tui.NetworkInfo {
	cfg := loadCfg()
	var info tui.NetworkInfo
	listeners := netinfo.Listeners("/")
	targets := accessTargets(cfg)
	for _, r := range networkRows(cfg, listeners) {
		url := ""
		if r.Open && (strings.Contains(r.What, "web UI") || r.Service == "web") {
			if l, ok := netinfo.Find(listeners, r.Port); ok {
				host := l.IP.String()
				if l.IP.IsUnspecified() {
					host = "127.0.0.1"
				}
				scheme := "http://"
				if r.Service == "web" {
					scheme = "https://"
				}
				url = scheme + net.JoinHostPort(host, fmt.Sprint(r.Port))
			}
		}
		row := tui.NetRow{Service: r.Service, What: r.What, Port: r.Port, Who: scopeText(r), Exposed: r.Open && r.Scope == netinfo.Network, Note: r.Note, URL: url}
		for _, t := range targets {
			if t.Service == r.Service && t.Port == r.Port {
				row.Target, row.Access, row.Native = t.ID, t.Mode, t.Native
				break
			}
		}
		info.Rows = append(info.Rows, row)
	}
	if ips := netinfo.LANAddrs(); len(ips) > 0 {
		info.Lines = append(info.Lines, "This computer: "+strings.Join(ips, ", "))
	}
	k, release := ax25.Kernel("/")
	switch {
	case k == ax25.KernelMissing:
		info.Lines = append(info.Lines, "AX.25 kernel stack: not in kernel "+release+" (hamstack packet listen|call work without it)")
	default:
		line := "AX.25 kernel stack: " + k.String()
		if ifs := ax25.Interfaces("/"); len(ifs) > 0 {
			line += ", interface " + ifs[0].Name
		} else {
			line += ", no interface (hamstack ax25)"
		}
		info.Lines = append(info.Lines, line)
	}
	return info
}

func (backend) Devices() []tui.Device {
	cfg := loadCfg()
	profs := loadProfiles()
	snap := snapshotOrEmpty()
	usedBy := func(match func(config.Rig) bool) string {
		for _, r := range cfg.Rigs {
			if match(r) {
				return r.ID
			}
		}
		return ""
	}
	var out []tui.Device
	for _, g := range snap.Groups() {
		hub := ""
		if g.Hub != "" {
			hub = "hub " + filepath.Base(g.Hub)
		}
		for _, s := range g.Serials {
			d := tui.Device{Kind: "serial", Key: serialKey(s), Group: hub,
				Label: fmt.Sprintf("%s %s [%s:%s]", s.Tty, label(s), s.VID, s.PID),
				Assigned: usedBy(func(r config.Rig) bool {
					return r.CAT != "" && (r.CAT == s.ByID || r.CAT == s.Tty || (r.CATID != "" && r.CATID == s.USBID()))
				})}
			detail := fmt.Sprintf("interface %d", s.Interface)
			if s.ByID != "" {
				detail += ", stable name " + filepath.Base(s.ByID)
			}
			if guess := profile.Guess(profs, s.VID, s.PID); len(guess) > 0 {
				var names []string
				for _, p := range guess {
					names = append(names, p.Name)
				}
				detail += "; looks like " + strings.Join(names, " or ")
			}
			if s.SerialNumber == "" {
				detail += "; no USB serial number, so an identical device is told apart only by its port"
			}
			d.Detail = detail
			out = append(out, d)
		}
		for _, c := range g.Sounds {
			out = append(out, tui.Device{Kind: "sound", Key: soundKey(c), Group: hub,
				Label:  fmt.Sprintf("card %d [%s] %s [%s:%s]", c.Card, c.ID, c.Product, c.VID, c.PID),
				Detail: "use as plughw:CARD=" + c.ID + ",DEV=0",
				Assigned: usedBy(func(r config.Rig) bool {
					return (r.AudioUSB != "" && r.AudioUSB == c.USBID()) || (r.AudioUSB == "" && r.Audio == c.ID)
				})})
		}
	}
	return out
}

func (backend) AssignDevice(key, rigID string) (string, error) {
	snap, err := scanDevices()
	if err != nil {
		return "", err
	}
	return mutateOnly(func(cfg *config.Config) (string, error) {
		r, ok := cfg.RigByID(rigID)
		if !ok {
			return "", fmt.Errorf("no rig %q", rigID)
		}
		a, buf := capture()
		for _, s := range snap.Serials {
			if serialKey(s) == key {
				applySerial(a, r, s, snap) // also takes the sound card that shares its hub
				return joinMsg("rig "+rigID+" now uses it", firstLines(buf, 3), "restart the rig's services to use it"), nil
			}
		}
		for _, c := range snap.Sounds {
			if soundKey(c) == key {
				r.Audio, r.AudioUSB = c.ID, c.USBID()
				return "rig " + rigID + " now uses that sound card; restart the rig's services to use it", nil
			}
		}
		return "", fmt.Errorf("that device is no longer plugged in")
	})
}

// ---- programs ----

func (backend) Programs() []tui.Program {
	cfg := loadCfg()
	hint := systemUpdateHint(distro.Detect())
	var out []tui.Program
	for _, p := range catalog.All {
		row := tui.Program{Name: p.Name, Desc: p.Desc, Category: p.Category, UpdateHint: hint, CanSource: canBuildFromSource(p.Name), Needs: dependencyLabels(p.Requires), Suggestions: dependencyLabels(p.Suggests)}
		if in := versions.FindInstalled(p); in != nil {
			row.Installed, row.Origin = in.Version, in.Origin
			if row.Installed == "" {
				row.Installed = "unknown"
			}
			row.UserInstall = in.Origin == "user install"
		}
		programFacts(&row, p)
		for _, app := range apps.All {
			if app.Package == p.Name {
				row.OpenPath = app.Path()
				row.AppID = app.Name
				if cfg.RunsWeb() {
					w := *cfg.Web
					w.Access = cfg.NetworkAccess("web", w.EffectiveAccess())
					row.BrowserURL = webURL(w) + "vnc/" + app.Name
				}
				break
			}
		}
		out = append(out, row)
	}
	out = append(out, tui.Program{Name: "hamstack", Desc: "Station manager: CLI, TUI and HTTPS dashboard", Installed: updates.Current(), Origin: "hamstack release", UpdateHint: "hamstack updates self"})
	if prev := previousSelfVersion(); prev != "" { // the program hamstack replaced is kept, so it can be put back
		out[len(out)-1].Rollback, out[len(out)-1].Previous = "self", prev
	}
	return out
}

func (backend) Latest() (map[string]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	snapshot, err := updates.Check(ctx, true)
	out := map[string]string{}
	for _, p := range snapshot.Programs {
		if p.Latest != "" {
			out[p.Name] = p.Latest
		}
	}
	return out, err
}

// ---- settings ----

func (backend) Settings() []tui.Setting {
	cfg := loadCfg()
	onOff := func(b bool) string {
		if b {
			return "on"
		}
		return "off"
	}
	gridHint := "the default grid square"
	if s, ok := cfg.Here(); ok {
		gridHint = "the grid square of site " + s.ID
	}
	out := []tui.Setting{
		{Key: "maintenance", Label: "Pending restarts / reboot", Value: func() string {
			s := maintenanceStatus()
			if len(s.Services) > 0 || s.Reload || s.Reboot || s.Session {
				return "needs attention"
			}
			return "up to date"
		}(), Kind: tui.SettingExec, Args: []string{"maintenance"}, Hint: "review reloads, service restarts, login changes and confirmed reboot"},
		{Key: "updates", Label: "Software updates", Value: "check", Kind: tui.SettingExec, Args: []string{"updates"}, Hint: "installed programs and hamstack releases; checked on load and hourly"},
		{Key: "self-update", Label: "Update hamstack", Value: updates.Current(), Kind: tui.SettingExec, Args: []string{"updates", "self"}, Hint: "review release; checksum verified; previous binary retained"},

		{Key: "web", Label: "HTTPS dashboard / browser VNC", Value: onOff(cfg.RunsWeb()), Kind: tui.SettingExec, Args: func() []string {
			if cfg.RunsWeb() {
				return []string{"web", "off"}
			}
			return []string{"web", "on"}
		}(), Hint: "self-signed HTTPS; password required"},
		{Key: "desktop", Label: "Whole-desktop VNC setup", Value: "check / install", Kind: tui.SettingExec, Args: []string{"web", "on", "--desktop"}, Hint: "checks compatibility; app or full desktop in browser"},
		{Key: "web-password", Label: "Dashboard password", Value: "change", Kind: tui.SettingExec, Args: []string{"web", "password"}, Hint: "password entered in terminal; stored hashed"},

		{Key: "pat", Label: "Pat (Winlink client)", Value: onOff(cfg.PatEnabled()), Kind: tui.SettingToggle, Hint: "run Pat as a service"},
		{Key: "ui", Label: "Mercury's web page on the network", Value: onOff(cfg.UI), Kind: tui.SettingToggle, Level: tui.SettingExpert, Hint: "Mercury's page listens on every interface with no password and no encryption (also turns Data2G's page on)"},
		{Key: "grid", Label: "Grid square", Value: cfg.EffectiveGrid(), Kind: tui.SettingText, Hint: gridHint},
	}
	if slices.ContainsFunc(cfg.Rigs, func(r config.Rig) bool { return slices.Contains(r.HFModems(), config.Data2G) }) {
		out = append(out, tui.Setting{Key: "data2g-page", Label: "Data2G's page through the dashboard", Value: onOff(config.On(cfg.Data2GPage, true)), Kind: tui.SettingToggle,
			Hint: "HTTPS on a port of its own, dashboard logins only; Data2G itself then listens on this computer only"})
	}
	out = append(out, tui.Setting{Key: "app-setup", Label: "Set up radio programs when they start", Value: onOff(config.On(cfg.AppSetup, true)), Kind: tui.SettingToggle,
		Hint: "WSJT-X, JS8Call and fldigi started from hamstack get the radio (rigctld) and, for WSJT-X, the sound card, where nothing is set yet (never what you chose); hamstack setup wires Pat to the rigs"})
	if hasDesktop(desktopRoot) || len(installedLaunchers()) > 0 {
		out = append(out, tui.Setting{Key: "launchers", Label: "Menu entries on this computer's desktop", Value: onOff(config.On(cfg.Launchers, true) && len(installedLaunchers()) > 0), Kind: tui.SettingToggle,
			Hint: "hamstack, its dashboard and each radio program (started with its radio set up) in the application menu; kept up to date as programs are installed"})
	}
	out = append(out, rigSettings(cfg)...)
	if cfg.Web != nil {
		out = append(out, tui.Setting{Key: "expert", Label: "Expert mode", Value: onOff(cfg.Web.Expert), Kind: tui.SettingToggle,
			Hint: "shows the settings that need a licence or give a shell; turning it on asks for the dashboard password"})
	}
	if on, err := svc.LingerEnabled(); err == nil {
		next := "on"
		if on {
			next = "off"
		}
		out = append(out, tui.Setting{Key: "linger", Label: "Start at boot", Value: onOff(on), Kind: tui.SettingExec,
			Args: []string{"linger", next}, Hint: "enter turns it " + next + " (needs systemd lingering)"})
	}
	if rigs := data2gRigs(cfg); len(rigs) > 0 {
		state, args := "set", []string{"data2g", "password"}
		if f, err := data2gSettingsFile(rigs[0].ID); err != nil || !hasData2GPassword(f) {
			state = "NOT SET: the web UI is locked"
		}
		hint := "enter sets or changes the web UI password (asked in the terminal)"
		if len(rigs) > 1 {
			args, hint = []string{"data2g", "password", rigs[0].ID}, "enter sets rig "+rigs[0].ID+"'s password; others: hamstack data2g password RIG"
		}
		out = append(out, tui.Setting{Key: "data2g-password", Label: "Data2G web password", Value: state, Kind: tui.SettingExec, Args: args, Hint: hint})
	}
	access, hint := "ok", "you are in the groups that open serial ports and sound cards"
	for _, r := range groupRows() {
		if p, _ := r.info.Advice(); p != "" {
			access, hint = "needs attention", "enter to fix (adds you to the group)"
		}
	}
	out = append(out, tui.Setting{Key: "groups", Label: "Serial and sound access", Value: access, Kind: tui.SettingExec, Args: []string{"groups", "fix"}, Hint: hint})
	return out
}

// rigSettings are each radio's switches, at their levels.
func rigSettings(cfg config.Config) []tui.Setting {
	onOff := func(b bool) string {
		if b {
			return "on"
		}
		return "off"
	}
	var out []tui.Setting
	for _, r := range cfg.Rigs {
		name := rigLabel(r)
		add := func(key, label, value, level, hint string) {
			out = append(out, tui.Setting{Key: "rig:" + r.ID + ":" + key, Label: name + ": " + label, Value: value, Kind: tui.SettingToggle, Level: level, Hint: hint})
		}
		hf := r.HFModems()
		if slices.Contains(hf, config.Xara) {
			add("xara-gui", "xara's screen", onOff(r.XaraScreenOn()), "", "view only, on this computer, shown through the dashboard")
			add("xara-monitor", "xara's monitor", onOff(r.XaraMonitorOn()), "", "reports the VARA HF links it hears (Radios page, Heard on the air); listens only")
		}
		if !r.NoCAT {
			add("shared-audio", "shared listening on the sound card", onOff(r.SharedAudio), tui.SettingAdvanced, "other programs can listen while the modem runs; the modem transmits on the card directly")
			add("radio-auto-tx", "fix transmit-side radio settings by itself", onOff(r.RadioAutoTX), tui.SettingAdvanced,
				"the radio settings check also sets transmit-side settings outside their range (RPORT GAIN, say) when a program takes the radio; receive-side ones always are")
		}
		if slices.Contains(hf, config.Ardopcf) {
			add("ardop-gui", "ardopcf's screen", onOff(ardopScreenOn(r)), tui.SettingAdvanced, "its drive, Two-tone and ID buttons make the radio transmit if it keys on audio; only ever started while the firewall keeps it to this computer")
		}
		if slices.Contains(hf, config.Xara) {
			add("xara-registered", "xara's registered speeds", onOff(r.XaraRegistered), tui.SettingExpert, "the faster speeds of a registered copy of VARA: for a VARA licence holder")
		}
	}
	return out
}

// rigSwitchOn is a radio switch's state now, as rigSettings shows it.
func rigSwitchOn(r config.Rig, field string) bool {
	switch field {
	case "xara-gui":
		return r.XaraScreenOn()
	case "xara-monitor":
		return r.XaraMonitorOn()
	case "shared-audio":
		return r.SharedAudio
	case "ardop-gui":
		return ardopScreenOn(r)
	case "xara-registered":
		return r.XaraRegistered
	case "radio-auto-tx":
		return r.RadioAutoTX
	}
	return false
}

func (backend) SetSetting(key, value string) (string, error) {
	if rest, ok := strings.CutPrefix(key, "rig:"); ok {
		id, field, ok := strings.Cut(rest, ":")
		if !ok || (value != "" && value != "on" && value != "off") {
			return "", fmt.Errorf("say on or off for %s", key)
		}
		return mutate(func(c *config.Config) (string, error) {
			r, ok := c.RigByID(id)
			if !ok {
				return "", fmt.Errorf("no rig %q", id)
			}
			if value == "" { // the terminal interface flips a toggle
				value = "on"
				if rigSwitchOn(*r, field) {
					value = "off"
				}
			}
			if err := setRigField(r, field, value); err != nil {
				return "", err
			}
			return rigLabel(*r) + ": " + field + " " + value + " (restart its modem to apply)", nil
		})
	}
	switch key {
	case "app-setup":
		return mutateOnly(func(c *config.Config) (string, error) {
			on := !config.On(c.AppSetup, true)
			if value == "on" || value == "off" {
				on = value == "on"
			}
			c.AppSetup = config.Set(on)
			if on {
				return "Radio programs are set up when they start, where nothing is set yet", nil
			}
			return "Radio programs are left as they are when they start (the recommended setup is still on each program's page)", nil
		})
	case "launchers":
		on := len(installedLaunchers()) == 0
		if value == "on" || value == "off" {
			on = value == "on"
		}
		if err := setLaunchers(on); err != nil {
			return "", err
		}
		if !on {
			n, err := removeLaunchers()
			return fmt.Sprintf("Removed %d menu entries", n), err
		}
		self, err := os.Executable()
		if err != nil {
			return "", err
		}
		written, err := installLaunchers(loadCfg(), self)
		return fmt.Sprintf("%d menu entries are in the application menu", len(written)), err
	case "data2g-page":
		return mutate(func(c *config.Config) (string, error) {
			on := !config.On(c.Data2GPage, true)
			c.Data2GPage = config.Set(on)
			if on {
				return "Data2G's page is served through the dashboard (restart Data2G to apply)", nil
			}
			return "Data2G's page is off (restart Data2G to apply)", nil
		})
	case "expert":
		return mutateOnly(func(c *config.Config) (string, error) {
			if c.Web == nil {
				return "", fmt.Errorf("the dashboard is not set up")
			}
			switch value {
			case "on":
				c.Web.Expert = true
			case "off":
				c.Web.Expert = false
			default:
				c.Web.Expert = !c.Web.Expert
			}
			if c.Web.Expert {
				return "Expert mode is on: the Expert settings show in Settings", nil
			}
			return "Expert mode is off", nil
		})
	case "pat":
		return mutate(func(cfg *config.Config) (string, error) {
			cfg.NoPat = !cfg.NoPat
			if cfg.NoPat {
				return "Pat is off", nil
			}
			return "Pat is on", nil
		})
	case "ui":
		return mutateOnly(func(cfg *config.Config) (string, error) {
			cfg.UI = !cfg.UI
			state := "off"
			if cfg.UI {
				state = "on"
			}
			return "Modem web UI is " + state + " (restart the modem to apply)", nil
		})
	case "grid":
		return mutateOnly(func(cfg *config.Config) (string, error) {
			if err := cfg.Set("grid", value); err != nil {
				return "", err
			}
			return "grid square is " + cfg.EffectiveGrid() + " (hamstack pat sync updates Pat to match)", nil
		})
	}
	return "", fmt.Errorf("unknown setting %q", key)
}

// ---- doctor and the terminal ----

func (backend) Doctor() []doctor.Result {
	cfg, _ := config.Load()
	return doctor.Run(doctor.Inputs{Cfg: cfg, Profiles: loadProfiles(), Sys: realSystem()})
}

// Command runs a hamstack command in the terminal and then waits for Enter, so what it printed can be
// read before the interface comes back.
func (backend) Command(args ...string) *exec.Cmd {
	self, err := os.Executable()
	if err != nil {
		self = "hamstack"
	}
	script := `"$0" "$@"; s=$?; printf '\n[hamstack finished (exit %s): press Enter to return] ' "$s"; read _`
	return exec.Command("sh", append([]string{"-c", script, self}, args...)...)
}

func cmdTUI() error {
	updates.Start()
	if err := svc.Available(); err != nil {
		return err
	}
	if err := offerFirstRunSetup(os.Stdin, os.Stdout, backend{}.NeedsSetup(), isTerminal() && stdinIsTerminal()); err != nil {
		return err
	}
	return tui.Run(backend{})
}

// firstRunMarker records that the user declined the guided setup on first run, so it is offered once and
// not at every start. The interface still shows how to start it until something is set up.
func firstRunMarker() string {
	d, err := config.Dir()
	if err != nil {
		return ""
	}
	return filepath.Join(d, ".declined-first-run")
}

// offerFirstRunSetup suggests the guided setup the first time hamstack is started in a terminal with
// nothing configured.
func offerFirstRunSetup(in io.Reader, out io.Writer, needsSetup, interactive bool) error {
	if !needsSetup || !interactive {
		return nil
	}
	marker := firstRunMarker()
	if marker != "" {
		if _, err := os.Stat(marker); err == nil {
			return nil
		}
	}
	a := &asker{in: bufio.NewReader(in), out: out}
	fmt.Fprintln(out, "Welcome to hamstack. Nothing is set up yet.")
	fmt.Fprintln(out, "The guided setup asks a few questions (radio, callsign, grid square), installs what is missing,")
	fmt.Fprintln(out, "starts everything, and checks it. It shows every command before running it and asks first.")
	if !a.confirm("Run the guided setup now?", true) {
		if marker != "" {
			_ = os.MkdirAll(filepath.Dir(marker), 0o755)
			_ = os.WriteFile(marker, nil, 0o644)
		}
		fmt.Fprintln(out, "Okay. Start it any time with: hamstack setup winlink   (or press w in the interface)")
		return nil
	}
	if err := runSetup(a, setupOpts{}); err != nil {
		return err
	}
	fmt.Fprintln(out)
	if !a.confirm("Open the interface?", true) {
		os.Exit(0)
	}
	return nil
}

func stdinIsTerminal() bool { return isTTY(os.Stdin) }

// isTerminal reports whether stdout is an interactive terminal.
func isTerminal() bool { return isTTY(os.Stdout) }

// isTTY reports whether f is a terminal. Asking the kernel for its terminal settings is the test: /dev/null
// is a character device too, so going by the file's type took a cron job or a unit with no terminal for an
// interactive session (and bare `hamstack` there tried to open the interface).
func isTTY(f *os.File) bool {
	_, err := unix.IoctlGetTermios(int(f.Fd()), unix.TCGETS)
	return err == nil
}

func (backend) PendingRestarts() []string { return maintenanceStatus().Services }
