// Package doctor checks that the stack is ready to run and says how to fix what isn't.
package doctor

import (
	"fmt"
	"strings"

	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/profile"
)

// Level is the severity of a result.
type Level int

const (
	OK Level = iota
	Warn
	Fail
)

func (l Level) String() string { return [...]string{"ok", "warn", "FAIL"}[l] }

// Result is one check's outcome.
type Result struct {
	Name   string
	Level  Level
	Detail string
	Fix    string // what to do, when Level != OK
}

// System is everything the checks look at. Tests substitute fakes.
type System struct {
	SystemdErr    error // nil if a systemd user session works
	LookPath      func(bin string) (string, error)
	Glob          func(pattern string) ([]string, error)
	Cards         func() []string           // ALSA card ids
	Access        func(path string) error   // nil if the current user can read and write path
	Group         func(path string) string  // owning group name of path
	Listening     func(port int) bool       // something accepts TCP on 127.0.0.1:port
	ServiceActive func(service string) bool // "rigctld@hf", "mercury@hf" or "pat"
	FileExists    func(path string) bool
	UnitsState    func() (installed, current bool) // unit files present / identical to what install would write
	RigModels     func() (map[int]string, error)   // radio models the installed hamlib supports (model -> driver status)
	HamlibVersion func() string
	SerialGroup   func() string // group that owns USB serial ports on this system
	GroupAdvice   func(name string) (exists bool, problem, fix string)
	PatLocator    func() (string, bool)                       // the locator in the active station's Pat config
	SiteHint      func() (site, why string, ok bool)          // another site whose radios are the ones plugged in
	PatHFPort     func() (port int, ok bool)                  // the VARA HF modem port the active station's Pat config points at
	PatPacketPort func() (port int, ok bool)                  // the AGWPE (Direwolf) port that Pat config uses for AX.25
	AnyEnabled    func() bool                                 // at least one hamstack service is set to start at login
	Linger        func() (bool, error)                        // user services run without a login session (start at boot)
	GPSDPath      func() (string, error)                      // where gpsd is installed (it lives in /usr/sbin on Debian)
	GPSDevice     func() (string, error)                      // the GPS receiver hamstack's gpsd would open, found right now
	SystemGPSD    func() string                               // the system-wide gpsd unit that is running, if any
	GPSFix        func() (fix bool, detail string, err error) // ask gpsd for a position (short wait)
}

// Inputs is the user's configuration plus the system.
type Inputs struct {
	Cfg      config.Config
	Profiles []profile.Profile
	Sys      System
}

// Run performs every check.
func Run(in Inputs) []Result {
	var rs []Result
	add := func(r Result) { rs = append(rs, r) }
	s := in.Sys
	cfg := in.Cfg

	if s.SystemdErr != nil {
		add(Result{"systemd user session", Fail, s.SystemdErr.Error(), "hamstack runs services as systemd user units; log in through a normal session"})
	} else {
		add(Result{"systemd user session", OK, "available", ""})
	}

	var missing []string
	for _, b := range cfg.Components() {
		if _, err := s.LookPath(b); err != nil {
			missing = append(missing, b)
		}
	}
	switch {
	case len(missing) > 0:
		add(Result{"programs", Fail, "not found: " + strings.Join(missing, ", "), "run: hamstack install"})
	case len(cfg.Components()) > 0:
		add(Result{"programs", OK, strings.Join(cfg.Components(), ", ") + " installed", ""})
	}

	if cfg.NeedsStation() {
		if st, ok := cfg.ActiveStation(); !ok {
			add(Result{"station", Fail, "no active station", "run: hamstack station add ID --callsign YOURCALL (or hamstack station import-pat)"})
		} else {
			add(Result{"station", OK, fmt.Sprintf("%s (%s)", st.ID, st.Callsign), ""})
			if cfg.Has(config.Pat) {
				if !s.FileExists(st.PatConfig) {
					add(Result{"station Pat config", Fail, st.PatConfig + " is missing", "re-add the station, or restore the file"})
				} else {
					add(Result{"station Pat config", OK, st.PatConfig, ""})
				}
			}
		}
	}

	rigs := cfg.ActiveRigs()
	gridCheck(cfg, s, add)
	siteCheck(cfg, s, add)
	patModemCheck(cfg, s, add)
	patPacketCheck(cfg, s, add)
	gpsChecks(cfg, s, add)
	for _, rig := range rigs {
		prefix := ""
		if len(rigs) > 1 {
			prefix = rig.ID + ": "
		}
		rigChecks(rig, in, prefix, add)
	}
	if len(rigs) > 1 {
		sharedHardware(rigs, in, add)
	}

	if installed, current := s.UnitsState(); !installed {
		add(Result{"service units", Warn, "not installed", "run: hamstack units install"})
	} else if !current {
		add(Result{"service units", Warn, "out of date with your settings", "run: hamstack units install"})
	} else {
		add(Result{"service units", OK, "installed and current", ""})
	}

	if s.AnyEnabled != nil && s.Linger != nil && s.AnyEnabled() {
		on, err := s.Linger()
		switch {
		case err != nil:
			add(Result{"start at boot", Warn, "could not check: " + err.Error(), ""})
		case !on:
			add(Result{"start at boot", Warn, "services start when you log in, not when the machine boots", "for a headless or always-on machine run: hamstack linger on"})
		default:
			add(Result{"start at boot", OK, "services start at boot (linger is on)", ""})
		}
	}

	if cfg.UI && cfg.Has(config.Mercury) {
		add(Result{"Mercury web UI", Warn, "enabled: listens on all interfaces with no password", "turn off with: hamstack config set ui off, unless this network is trusted"})
	}
	return rs
}

func findProfile(profiles []profile.Profile, id string) *profile.Profile {
	for i := range profiles {
		if profiles[i].ID == id {
			return &profiles[i]
		}
	}
	return nil
}

// rigChecks runs everything that concerns one rig.
func rigChecks(rig config.Rig, in Inputs, prefix string, add func(Result)) {
	s := in.Sys
	name := func(n string) string { return prefix + n }
	prof := findProfile(in.Profiles, rig.Profile)
	if prof == nil {
		add(Result{name("radio profile"), Fail, fmt.Sprintf("rig %s uses %q, which is not a known profile", rig.ID, rig.Profile), "run: hamstack profiles, then hamstack rig set " + rig.ID + " profile ID"})
		return
	}
	add(Result{name("radio profile"), OK, prof.Name, ""})
	if prof.HasCAT() {
		modelCheck(prof, s, name, add)
	}
	catDevice(rig, prof, s, name, add)
	soundCard(rig, prof, s, name, add)
	groupChecks(rig, prof, in.Cfg, s, name, add)

	portCheck := func(label string, port int, service string) {
		switch {
		case !s.Listening(port):
			add(Result{name(label), OK, fmt.Sprintf("port %d is free", port), ""})
		case s.ServiceActive(service):
			add(Result{name(label), OK, fmt.Sprintf("port %d is in use by the hamstack %s service", port, service), ""})
		default:
			fix := fmt.Sprintf("something else is using port %d: stop it first", port)
			if label == "modem port" && port == config.ArqPortBase {
				fix += " (VARA HF uses the same port as Mercury; never run both)"
			}
			add(Result{name(label), Warn, fmt.Sprintf("port %d is in use by another program", port), fix})
		}
	}
	if !rig.NoCAT {
		portCheck("rig control port", rig.RigPort, "rigctld@"+rig.ID)
	}
	switch rig.Modem {
	case config.Mercury:
		portCheck("modem port", rig.ArqPort, "mercury@"+rig.ID)
	case config.Direwolf:
		agw, _ := config.DirewolfPorts(rig)
		portCheck("packet TNC port", agw, "direwolf@"+rig.ID)
	}
}

// modelCheck asks the installed hamlib whether it knows the profile's radio. Distributions
// often ship an older hamlib than a newly released radio needs.
func modelCheck(prof *profile.Profile, s System, name func(string) string, add func(Result)) {
	label := name("radio supported by hamlib")
	if s.RigModels == nil {
		return
	}
	models, err := s.RigModels()
	if err != nil {
		add(Result{label, Warn, "could not ask rigctld which radios it supports: " + err.Error(), "run: hamstack install hamlib"})
		return
	}
	ver := "unknown version"
	if s.HamlibVersion != nil {
		if v := s.HamlibVersion(); v != "" {
			ver = v
		}
	}
	status, ok := models[prof.Rig.Model]
	if !ok {
		add(Result{label, Fail, fmt.Sprintf("hamlib %s has no driver for %s (model %d)", ver, prof.Name, prof.Rig.Model),
			"your hamlib is older than this radio: run 'hamstack install hamlib --from-source' (builds a newer one; slow on small hardware), or pick another profile"})
		return
	}
	detail := fmt.Sprintf("%s: model %d in hamlib %s", prof.Name, prof.Rig.Model, ver)
	if status != "" && !strings.EqualFold(status, "Stable") {
		detail += fmt.Sprintf(" (driver status: %s)", status)
	}
	add(Result{label, OK, detail, ""})
}

// catPath is the device a rig's rigctld will open: its saved assignment, or the first match of the
// profile's pattern.
func catPath(rig config.Rig, prof *profile.Profile, s System) (path string, assigned bool) {
	if rig.CAT != "" {
		return rig.CAT, true
	}
	if g := prof.Rig.DeviceGlob; g != "" {
		if m, _ := s.Glob(g); len(m) > 0 {
			return m[0], false
		}
		return g, false
	}
	return "", false
}

func catDevice(rig config.Rig, prof *profile.Profile, s System, name func(string) string, add func(Result)) {
	label := name("radio (CAT port)")
	if !prof.HasCAT() {
		label = name("PTT serial port") // an interface such as a Digirig: the port only keys the radio
	}
	path, assigned := catPath(rig, prof, s)
	if path == "" {
		if prof.HasCAT() && prof.Rig.Baud > 0 {
			// A serial radio from hamlib's list: hamstack cannot recognise its port, so one has to be assigned.
			add(Result{label, Warn, "no serial port is assigned to this radio", "plug it in and run: hamstack rig identify " + rig.ID})
		}
		return // the dummy radio has no device
	}
	exists := s.FileExists(path)
	if !assigned {
		exists = false
		if m, _ := s.Glob(prof.Rig.DeviceGlob); len(m) > 0 {
			exists, path = true, m[0]
		}
	}
	if !exists {
		fix := "plug in and power on the radio, then run: hamstack doctor"
		if assigned {
			fix = "plug it in, or point the rig at its current port: hamstack rig identify " + rig.ID
		}
		add(Result{label, Warn, "not detected: " + path, fix})
		return
	}
	if err := s.Access(path); err != nil {
		fix := "grant your user access to " + path
		if g := s.Group(path); g != "" {
			fix = fmt.Sprintf("add yourself to the %q group: hamstack groups fix, then log out and in", g)
		}
		add(Result{label, Fail, "found " + path + " but cannot open it: " + err.Error(), fix})
		return
	}
	add(Result{label, OK, path, ""})
}

func soundCard(rig config.Rig, prof *profile.Profile, s System, name func(string) string, add func(Result)) {
	if rig.Modem == "" || prof.Audio.Null {
		return
	}
	hints := prof.Audio.CardHints
	if rig.Audio != "" {
		hints = append([]string{rig.Audio}, hints...)
	}
	if len(hints) == 0 {
		return
	}
	cards := s.Cards()
	found := ""
	for _, h := range hints {
		for _, c := range cards {
			if strings.EqualFold(c, h) && found == "" {
				found = c
			}
		}
	}
	if found == "" {
		add(Result{name("radio sound card"), Warn, fmt.Sprintf("none of %v among %v", hints, cards), "plug in the radio, check 'cat /proc/asound/cards', and assign it: hamstack rig identify " + rig.ID})
		return
	}
	add(Result{name("radio sound card"), OK, found, ""})
}

// sharedHardware catches two rigs that claim the same serial port or sound card, which would make
// both misbehave.
func sharedHardware(rigs []config.Rig, in Inputs, add func(Result)) {
	cat, snd := map[string]string{}, map[string]string{}
	clash := false
	for _, r := range rigs {
		if r.CAT != "" {
			if other, dup := cat[r.CAT]; dup {
				add(Result{"shared radio port", Fail, fmt.Sprintf("rigs %s and %s both use %s", other, r.ID, r.CAT), "assign each rig its own radio: hamstack rig identify " + r.ID})
				clash = true
			}
			cat[r.CAT] = r.ID
		}
		if r.Modem != "" && r.AudioUSB != "" {
			if other, dup := snd[r.AudioUSB]; dup {
				add(Result{"shared sound card", Fail, fmt.Sprintf("rigs %s and %s both use the sound card %s", other, r.ID, r.AudioUSB), "assign each rig its own sound card: hamstack rig identify " + r.ID})
				clash = true
			}
			snd[r.AudioUSB] = r.ID
		}
	}
	if !clash {
		add(Result{"rig hardware", OK, "no two rigs share a serial port or sound card", ""})
	}
}

// groupChecks verifies the group memberships that open USB serial ports and, for a service that
// runs with nobody logged in, sound cards.
func groupChecks(rig config.Rig, prof *profile.Profile, cfg config.Config, s System, name func(string) string, add func(Result)) {
	if s.GroupAdvice == nil {
		return
	}
	report := func(label, group string) {
		exists, problem, fix := s.GroupAdvice(group)
		switch {
		case !exists:
		case problem != "":
			add(Result{name(label), Warn, problem, fix})
		default:
			add(Result{name(label), OK, fmt.Sprintf("you are in the %q group and your services have it", group), ""})
		}
	}
	if (prof.Rig.DeviceGlob != "" || rig.CAT != "") && s.SerialGroup != nil {
		report("serial port permission", s.SerialGroup())
	}
	if !prof.Audio.Null && rig.Modem != "" && s.Linger != nil {
		// A desktop login gets sound access automatically. With linger, services run with nobody
		// logged in and need the group.
		if on, err := s.Linger(); err == nil && on {
			report("sound card permission", "audio")
		}
	}
}

// Worst returns the most severe level in rs.
func Worst(rs []Result) Level {
	w := OK
	for _, r := range rs {
		if r.Level > w {
			w = r.Level
		}
	}
	return w
}

// patModemCheck makes sure Pat's HF modem connection leads to a Mercury that is part of the setup.
// Existing Pat configs are never rewritten, so a rig change can leave Pat pointing at a modem that no
// longer exists.
func patModemCheck(cfg config.Config, s System, add func(Result)) {
	if !cfg.PatEnabled() || !cfg.Has(config.Mercury) || s.PatHFPort == nil {
		return
	}
	port, ok := s.PatHFPort()
	if !ok {
		add(Result{"Pat HF modem", Warn, "Pat has no VARA HF modem set, so it cannot use Mercury", "run: hamstack pat sync (sets Pat's HF modem to your Mercury; a backup of Pat's config is kept)"})
		return
	}
	var ports []string
	for _, r := range cfg.ActiveRigs() {
		if r.Modem == config.Mercury {
			if r.ArqPort == port {
				add(Result{"Pat HF modem", OK, fmt.Sprintf("Pat's VARA HF address (port %d) is the Mercury of rig %s", port, r.ID), ""})
				return
			}
			ports = append(ports, fmt.Sprintf("%d (rig %s)", r.ArqPort, r.ID))
		}
	}
	add(Result{"Pat HF modem", Warn, fmt.Sprintf("Pat's VARA HF address uses port %d, which is not a Mercury of yours (%s)", port, strings.Join(ports, ", ")),
		"run: hamstack pat sync (points Pat at your Mercury; a backup of Pat's config is kept)"})
}

// patPacketCheck makes sure Pat's AX.25 connection leads to a Direwolf that is part of the setup.
func patPacketCheck(cfg config.Config, s System, add func(Result)) {
	if !cfg.PatEnabled() || !cfg.Has(config.Direwolf) || s.PatPacketPort == nil {
		return
	}
	port, ok := s.PatPacketPort()
	if !ok {
		add(Result{"Pat packet TNC", Warn, "Pat is not set up for packet through Direwolf (AX.25 engine agwpe), so it cannot use it", "run: hamstack pat sync (sets Pat's AX.25 to your Direwolf; a backup of Pat's config is kept)"})
		return
	}
	var ports []string
	for _, r := range cfg.ActiveRigs() {
		if r.Modem == config.Direwolf {
			if agw, _ := config.DirewolfPorts(r); agw == port {
				add(Result{"Pat packet TNC", OK, fmt.Sprintf("Pat's AX.25 address (port %d) is the Direwolf of rig %s", port, r.ID), ""})
				return
			} else {
				ports = append(ports, fmt.Sprintf("%d (rig %s)", agw, r.ID))
			}
		}
	}
	add(Result{"Pat packet TNC", Warn, fmt.Sprintf("Pat's AX.25 address uses port %d, which is not a Direwolf of yours (%s)", port, strings.Join(ports, ", ")),
		"run: hamstack pat sync (points Pat at your Direwolf; a backup of Pat's config is kept)"})
}

// gridCheck compares the grid square you are at with the locator in Pat's config. Moving to another site
// changes the grid but never rewrites Pat's file, so the two drift apart; Pat uses its locator for position
// reports and to pick nearby gateways.
func gridCheck(cfg config.Config, s System, add func(Result)) {
	if !cfg.PatEnabled() || s.PatLocator == nil {
		return
	}
	want := cfg.EffectiveGrid()
	if want == "" {
		return
	}
	have, ok := s.PatLocator()
	if !ok {
		return
	}
	// A 4-character locator is the start of a 6-character one in the same square: not a mismatch.
	a, b := strings.ToLower(have), strings.ToLower(want)
	if strings.HasPrefix(a, b) || strings.HasPrefix(b, a) {
		add(Result{"Pat locator", OK, fmt.Sprintf("Pat's locator (%s) matches your grid (%s)", have, want), ""})
		return
	}
	add(Result{"Pat locator", Warn, fmt.Sprintf("Pat's locator is %s but your grid here is %s", have, want),
		"update just that field (a backup is kept): hamstack pat locator"})
}

// siteCheck suggests a switch when the radios that are plugged in belong to a different site.
func siteCheck(cfg config.Config, s System, add func(Result)) {
	if s.SiteHint == nil || len(cfg.Sites) == 0 {
		return
	}
	if site, why, ok := s.SiteHint(); ok {
		add(Result{"site", Warn, why, "switch with: hamstack site use " + site})
		return
	}
	if cur, ok := cfg.Here(); ok {
		add(Result{"site", OK, "you are at " + cur.ID, ""})
	}
}

// gpsChecks looks at the GPS setup: gpsd installed, the receiver there and readable, no other gpsd in
// the way, and a position coming out.
func gpsChecks(cfg config.Config, s System, add func(Result)) {
	g := cfg.GPS
	if g == nil || g.Off {
		return
	}
	if cfg.RunsGPSD() {
		if s.GPSDPath != nil {
			p, err := s.GPSDPath()
			if err != nil {
				add(Result{"gpsd", Fail, "gpsd is not installed", "run: hamstack install gpsd"})
				return
			}
			add(Result{"gpsd", OK, "installed (" + p + ")", ""})
		}
		if s.GPSDevice != nil {
			dev, err := s.GPSDevice()
			switch {
			case err != nil:
				add(Result{"GPS receiver", Fail, err.Error(), "plug it in, or pick it again: hamstack gps use"})
				return
			case s.Access != nil && s.Access(dev) != nil:
				fix := "run: hamstack groups fix"
				add(Result{"GPS receiver", Fail, dev + " is there but you cannot open it (permissions)", fix})
				return
			default:
				add(Result{"GPS receiver", OK, dev, ""})
			}
		}
		if s.SystemGPSD != nil {
			if u := s.SystemGPSD(); u != "" {
				add(Result{"system gpsd", Warn, u + " is running: a system-wide gpsd takes over GPS receivers when they are plugged in, and then hamstack's cannot open this one",
					"turn it off: sudo systemctl disable --now gpsd.socket gpsd.service"})
			}
		}
		if s.ServiceActive != nil && !s.ServiceActive("gpsd") {
			add(Result{"gpsd service", Warn, "not running", "run: hamstack start gpsd"})
			return
		}
	}
	if s.GPSFix != nil {
		fix, detail, err := s.GPSFix()
		switch {
		case err != nil:
			add(Result{"GPS fix", Warn, err.Error(), "check the receiver and gpsd: hamstack gps"})
		case !fix:
			add(Result{"GPS fix", Warn, detail, "a receiver needs a view of the sky; a cold start can take minutes"})
		default:
			add(Result{"GPS fix", OK, detail, ""})
		}
	}
}
