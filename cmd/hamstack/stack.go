package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/hamlib"
	"github.com/ATCUSA/hamstack/internal/profile"
	"github.com/ATCUSA/hamstack/internal/svc"
	"github.com/ATCUSA/hamstack/internal/units"
	"github.com/ATCUSA/hamstack/internal/usbdev"
	"github.com/ATCUSA/hamstack/internal/versions"
)

func userProfileDir() string {
	d, err := config.Dir()
	if err != nil {
		return ""
	}
	return filepath.Join(d, "profiles")
}

func cmdProfiles() error {
	all, err := profile.All(userProfileDir())
	if err != nil {
		return err
	}
	cfg, _ := config.Load()
	users := map[string][]string{}
	for _, r := range cfg.Rigs {
		users[r.Profile] = append(users[r.Profile], r.ID)
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tHAMLIB\tBAUD\t")
	for _, p := range all {
		mark := ""
		if u := users[p.ID]; len(u) > 0 {
			mark = "(used by rig " + strings.Join(u, ", ") + ")"
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%s\n", p.ID, p.Name, p.Rig.Model, p.Rig.Baud, mark)
	}
	tw.Flush()
	for _, bad := range profile.Problems(userProfileDir()) {
		fmt.Fprintf(os.Stderr, "warning: ignoring a profile that cannot be read: %v\n", bad)
	}
	fmt.Printf("\nA profile is a radio model. Use one for a rig: hamstack rig add ID --profile PROFILE\nAdd your own models as JSON files in %s\n", userProfileDir())
	return nil
}

func cmdConfig(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if len(args) == 0 || args[0] == "show" {
		dir, _ := config.Dir()
		st := "-"
		if a, ok := cfg.ActiveStation(); ok {
			st = a.ID + " (" + a.Callsign + ")"
		}
		pat := "on"
		if cfg.NoPat {
			pat = "off"
		}
		site := "-"
		if s, ok := cfg.Here(); ok {
			site = s.ID
		}
		fmt.Printf("config:   %s\nsite:     %s\npat:      %s\nstation:  %s\ngrid:     %s\nui:       %t (Mercury web UI; listens on all interfaces, no auth)\nrigs:\n",
			filepath.Join(dir, "config.json"), site, pat, st, orDash(cfg.EffectiveGrid()), cfg.UI)
		if len(cfg.Rigs) == 0 {
			fmt.Println("  (none: add one with 'hamstack rig add')")
		}
		for _, r := range cfg.Rigs {
			fmt.Println("  " + describeRig(r))
		}
		return nil
	}
	if args[0] != "set" || len(args) != 3 {
		return fmt.Errorf("usage: hamstack config set KEY VALUE   (keys: grid, ui, profile)")
	}
	if args[1] == "profile" {
		if _, err := profile.Find(userProfileDir(), args[2]); err != nil {
			return err
		}
	}
	if err := cfg.Set(args[1], args[2]); err != nil {
		return err
	}
	if args[1] == "profile" && len(cfg.Rigs) == 1 { // keep the rig's CAT flag in step with its new model
		if p, err := profile.Find(userProfileDir(), args[2]); err == nil {
			cfg.Rigs[0].NoCAT = !p.HasCAT()
		}
	}
	if err := config.Save(cfg); err != nil {
		return err
	}
	fmt.Println("saved. Services pick it up the next time they start (hamstack restart).")
	return nil
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func loadProfiles() []profile.Profile {
	p, _ := profile.All(userProfileDir())
	return p
}

// buildPlan assembles what the units need from the saved configuration and the machine.
func buildPlan() (units.Plan, []string, error) {
	var p units.Plan
	var warns []string
	cfg, err := config.Load()
	if err != nil {
		return p, nil, err
	}
	p.Config = cfg
	profs := loadProfiles()

	for _, r := range cfg.ActiveRigs() {
		prof := (*profile.Profile)(nil)
		for i := range profs {
			if profs[i].ID == r.Profile {
				prof = &profs[i]
			}
		}
		if prof == nil {
			return p, nil, fmt.Errorf("rig %s uses the profile %q, which does not exist (see: hamstack profiles)", r.ID, r.Profile)
		}
		p.Rigs = append(p.Rigs, units.RigPlan{Rig: r, Profile: *prof})
	}

	var missing []string
	if len(p.Rigs) > 0 {
		if path, err := versions.Which("rigctld"); err != nil {
			missing = append(missing, "rigctld")
		} else {
			warns = append(warns, rigWarnings(p, path)...)
		}
	}
	if cfg.Has(config.Mercury) {
		if _, err := versions.Which("mercury"); err != nil {
			missing = append(missing, "mercury")
		}
	}
	if cfg.Has(config.Direwolf) {
		if _, err := versions.Which("direwolf"); err != nil {
			missing = append(missing, "direwolf")
		}
	}
	if cfg.RunsGPSD() {
		if _, err := versions.WhichSbin("gpsd"); err != nil {
			missing = append(missing, "gpsd")
		}
	}
	if cfg.PatEnabled() {
		path, err := versions.Which("pat")
		if err != nil {
			missing = append(missing, "pat")
		} else if abs, err := filepath.Abs(path); err == nil {
			p.Pat = abs
		}
	}
	if len(missing) > 0 {
		return p, nil, fmt.Errorf("not installed: %s (run 'hamstack install')", strings.Join(missing, ", "))
	}
	exe, err := os.Executable()
	if err != nil {
		return p, nil, fmt.Errorf("cannot locate the hamstack binary: %w", err)
	}
	p.Hamstack = exe

	if cfg.NeedsStation() {
		st, ok := cfg.ActiveStation()
		if !ok {
			return p, nil, fmt.Errorf("no station: add one with 'hamstack station add ID --callsign YOURCALL' (or 'hamstack station import-pat')")
		}
		if cfg.PatEnabled() {
			if _, err := os.Stat(st.PatConfig); err != nil {
				return p, nil, fmt.Errorf("station %s: Pat config %s not found", st.ID, st.PatConfig)
			}
		}
		p.Station = st
	}
	p.StateDir = stateDir()
	return p, warns, nil
}

// rigWarnings are the things worth knowing before the services start, per rig.
func rigWarnings(p units.Plan, rigctld string) []string {
	var warns []string
	models, modelsErr := hamlib.Installed(rigctld)
	snap, _ := scanDevices()
	for _, rp := range p.Rigs {
		who := ""
		if len(p.Rigs) > 1 {
			who = "rig " + rp.Rig.ID + ": "
		}
		if !rigHardwarePresent(rp.Rig, rp.Profile, snap) {
			warns = append(warns, who+"the radio is not detected right now; its rigctld service will say so when started")
		}
		if rp.Profile.Rig.Model > 2 && modelsErr == nil {
			if _, ok := models[rp.Profile.Rig.Model]; !ok {
				warns = append(warns, fmt.Sprintf("%syour hamlib does not support %s (model %d): rigctld will fail to start. Fix: hamstack install hamlib --from-source", who, rp.Profile.Name, rp.Profile.Rig.Model))
			}
		}
	}
	return warns
}

// rigHardwarePresent reports whether a rig's CAT device exists now. A rig with no device (the dummy
// radio) is always present.
func rigHardwarePresent(r config.Rig, prof profile.Profile, snap usbdev.Snapshot) bool {
	switch {
	case r.CAT != "":
		if _, err := os.Stat(r.CAT); err == nil {
			return true
		}
		if r.CATID != "" {
			for _, s := range snap.Serials {
				if s.USBID() == r.CATID {
					return true
				}
			}
		}
		return false
	case prof.Rig.DeviceGlob != "":
		m, _ := filepath.Glob(prof.Rig.DeviceGlob)
		return len(m) > 0
	}
	return true
}

// stateDir is where hamstack keeps the logs of the services it runs. It is hamstack's own directory so
// it never overwrites logs written by other tools (Pat truncates its log on start).
func stateDir() string {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "hamstack")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "hamstack")
}

// everyService lists every service hamstack could have installed for this config, whatever the rigs'
// state: switched-off rigs are included because their instances may still be enabled. The order puts
// each rig's modems before its rig control and Pat first, and stopServices stops in reverse, so a
// service that is a client of rigctld always goes before the rigctld it talks to. rigctld does not exit
// promptly while it still has a client, which used to cost a 90 second stop timeout.
func everyService(cfg config.Config) []units.Service {
	all := []units.Service{{Kind: config.GPSD}}
	for _, r := range cfg.Rigs {
		all = append(all, units.Service{Kind: config.RigCtld, Rig: r.ID}, units.Service{Kind: config.Mercury, Rig: r.ID}, units.Service{Kind: config.Direwolf, Rig: r.ID})
	}
	return append(all, units.Service{Kind: config.Pat})
}

// stopServices stops and disables services (for example ones that were just removed from the setup).
func stopServices(ss []units.Service) {
	for _, s := range slices.Backward(ss) {
		_ = svc.Run("disable", "--now", s.Unit())
	}
}

// writeUnits writes the units and reloads systemd, quietly. Unit files nothing needs any more are
// removed; the services they ran must already have been stopped by the caller.
func writeUnits(p units.Plan) ([]string, error) {
	if stale, err := units.Stale(p); err == nil && len(stale) > 0 {
		for _, f := range stale {
			// A template's instances are named after the rigs; legacy units are plain.
			kind, _, isTemplate := strings.Cut(strings.TrimSuffix(strings.TrimPrefix(f, "hamstack-"), ".service"), "@")
			if isTemplate {
				for _, r := range p.Config.Rigs {
					_ = svc.Run("disable", "--now", "hamstack-"+kind+"@"+r.ID+".service")
				}
			} else {
				_ = svc.Run("disable", "--now", f)
			}
		}
		_, _ = units.RemoveFiles(stale)
	}
	written, err := units.Install(p)
	if err != nil {
		return nil, err
	}
	return written, svc.Run("daemon-reload")
}

// installUnits is writeUnits plus a listing of what was written.
func installUnits(p units.Plan) error {
	written, err := writeUnits(p)
	for _, w := range written {
		fmt.Println("wrote", w)
	}
	return err
}

// controlQuiet runs a systemctl verb on services in dependency order without printing.
func controlQuiet(verb string, ss []units.Service) error {
	ss = slices.Clone(ss)
	if verb == "stop" || verb == "disable" {
		slices.Reverse(ss)
	}
	for _, s := range ss {
		if svc.Status(s.Unit()).Load == "not-found" {
			return fmt.Errorf("%s is not installed: run 'hamstack units install' first", s.Name())
		}
		if err := svc.Run(verb, s.Unit()); err != nil {
			return err
		}
	}
	return nil
}

// applyUnits regenerates the units after a configuration change, if they have been installed, and
// stops the services that are no longer part of the setup.
func applyUnits(before config.Config) (note string, err error) {
	if svc.Available() != nil {
		return "", nil
	}
	installed, _ := units.Installed()
	if len(installed) == 0 {
		return "units are not installed yet: run 'hamstack units install'", nil
	}
	after, err := config.Load()
	if err != nil {
		return "", err
	}
	gone := map[string]units.Service{}
	for _, s := range units.ServicesFor(before) {
		gone[s.Unit()] = s
	}
	for _, s := range units.ServicesFor(after) {
		delete(gone, s.Unit())
	}
	var stop []units.Service
	for _, s := range units.ServicesFor(before) {
		if _, ok := gone[s.Unit()]; ok {
			stop = append(stop, s)
		}
	}
	stopServices(stop)
	p, _, err := buildPlan()
	if err != nil {
		return "", err
	}
	if _, err := writeUnits(p); err != nil {
		return "", err
	}
	if len(stop) > 0 {
		var names []string
		for _, s := range stop {
			names = append(names, s.Name())
		}
		return "stopped " + strings.Join(names, ", "), nil
	}
	return "units updated", nil
}

func cmdUnits(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: hamstack units install|show|remove")
	}
	switch args[0] {
	case "install", "show":
		p, warns, err := buildPlan()
		if err != nil {
			return err
		}
		for _, w := range warns {
			fmt.Fprintln(os.Stderr, "warning:", w)
		}
		if args[0] == "show" {
			gen := units.Generate(p)
			names := make([]string, 0, len(gen))
			for n := range gen {
				names = append(names, n)
			}
			slices.Sort(names)
			for _, n := range names {
				fmt.Printf("=== %s ===\n%s\n", n, gen[n])
			}
			snap := snapshotOrEmpty()
			for _, rp := range p.Rigs {
				switch rp.Rig.Modem {
				case config.Mercury:
					fmt.Printf("=== mercury.ini for rig %s (written each time the service starts) ===\n%s\n", rp.Rig.ID,
						units.MercuryConfig(p.Config, rp.Rig, rp.Profile, p.Station, resolveAudio(rp.Rig, rp.Profile, snap), p.StateDir))
				case config.Direwolf:
					ptt, _, err := direwolfPTTLine(rp.Rig, rp.Profile, snap)
					if err != nil {
						ptt = "(" + err.Error() + ")"
					}
					fmt.Printf("=== direwolf.conf for rig %s (written each time the service starts) ===\n%s\n", rp.Rig.ID,
						units.DirewolfConfig(rp.Rig, rp.Profile, p.Station, resolveAudio(rp.Rig, rp.Profile, snap), ptt))
				}
			}
			return nil
		}
		if err := svc.Available(); err != nil {
			return err
		}
		if err := installUnits(p); err != nil {
			return err
		}
		fmt.Println("\nNext: hamstack start   (hamstack enable to also start at login)")
	case "remove":
		if err := svc.Available(); err != nil {
			return err
		}
		cfg, _ := config.Load()
		stopServices(everyService(cfg))
		gone, err := units.Remove()
		if err != nil {
			return err
		}
		_ = svc.Run("daemon-reload")
		for _, g := range gone {
			fmt.Println("removed", g)
		}
	default:
		return fmt.Errorf("usage: hamstack units install|show|remove")
	}
	return nil
}

func snapshotOrEmpty() usbdev.Snapshot {
	s, _ := scanDevices()
	return s
}

func cmdStatus() error {
	if err := svc.Available(); err != nil {
		return err
	}
	cfg, _ := config.Load()
	st := "-"
	if a, ok := cfg.ActiveStation(); ok {
		st = a.ID + " (" + a.Callsign + ", web UI " + a.HTTPAddr + ")"
	}
	var rigs []string
	for _, r := range cfg.ActiveRigs() {
		rigs = append(rigs, r.ID)
	}
	fmt.Printf("rigs: %s   station: %s\n\n", orDash(strings.Join(rigs, ", ")), st)
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SERVICE\tSTATE\tAT LOGIN\tSINCE")
	for _, s := range units.ServicesFor(cfg) {
		stt := svc.Status(s.Unit())
		if stt.Load == "not-found" {
			fmt.Fprintf(tw, "%s\tnot installed\t-\t-\n", s.Name())
			continue
		}
		state := fmt.Sprintf("%s (%s)", stt.Active, stt.Sub)
		if stt.NoRadio() {
			state = "no radio (plug it in, then: hamstack start " + s.Name() + ")"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", s.Name(), state, orDash(stt.Enabled), orDash(stt.Since))
	}
	return tw.Flush()
}

// resolveServices turns command-line words into services; no words means everything.
func resolveServices(args []string) ([]units.Service, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	if len(args) == 0 {
		args = []string{"all"}
	}
	var out []units.Service
	seen := map[string]bool{}
	for _, a := range args {
		ss, err := units.Resolve(cfg, a)
		if err != nil {
			return nil, err
		}
		for _, s := range ss {
			if !seen[s.Unit()] {
				seen[s.Unit()] = true
				out = append(out, s)
			}
		}
	}
	return out, nil
}

func cmdControl(verb string, args []string) error {
	if err := svc.Available(); err != nil {
		return err
	}
	ss, err := resolveServices(args)
	if err != nil {
		return err
	}
	if verb == "stop" || verb == "disable" {
		ss = slices.Clone(ss)
		slices.Reverse(ss) // dependents first
	}
	for _, s := range ss {
		if svc.Status(s.Unit()).Load == "not-found" {
			return fmt.Errorf("%s is not installed: run 'hamstack units install' first", s.Name())
		}
		if err := svc.Run(verb, s.Unit()); err != nil {
			return err
		}
		fmt.Printf("%s %s: ok\n", verb, s.Name())
	}
	if verb == "start" || verb == "restart" {
		noteMissingRadios(ss)
	}
	if verb == "enable" {
		if on, err := svc.LingerEnabled(); err == nil && !on {
			fmt.Println("note: they will start when you log in. To start at boot on a headless machine: hamstack linger on")
		}
	}
	return nil
}

// cmdLinger shows or changes whether user services outlive login sessions.
func cmdLinger(args []string) error {
	on, err := svc.LingerEnabled()
	if err != nil {
		return err
	}
	if len(args) == 0 {
		if on {
			fmt.Println("linger is on: enabled services start at boot and keep running after you log out.")
		} else {
			fmt.Println("linger is off: services start when you log in and stop when your last session ends.")
			fmt.Println("turn on with: hamstack linger on")
		}
		return nil
	}
	switch args[0] {
	case "on", "off":
		want := args[0] == "on"
		if want == on {
			fmt.Println("linger is already", args[0])
			return nil
		}
		if err := svc.SetLinger(want); err != nil {
			return err
		}
		fmt.Println("linger is now", args[0])
		return nil
	}
	return fmt.Errorf("usage: hamstack linger [on|off]")
}

func cmdLogs(args []string) error {
	follow := false
	var rest []string
	for _, a := range args {
		if a == "-f" {
			follow = true
		} else {
			rest = append(rest, a)
		}
	}
	if len(rest) != 1 {
		return fmt.Errorf("usage: hamstack logs SERVICE [-f]   (for example rigctld@hf, mercury@hf, pat)")
	}
	cfg, _ := config.Load()
	ss, err := units.Resolve(cfg, rest[0])
	if err != nil {
		return err
	}
	if len(ss) != 1 {
		return fmt.Errorf("%q matches several services; name one: %s", rest[0], strings.Join(serviceNames(ss), ", "))
	}
	return svc.Logs(ss[0].Unit(), follow, 50)
}

func serviceNames(ss []units.Service) []string {
	var out []string
	for _, s := range ss {
		out = append(out, s.Name())
	}
	return out
}

// cmdWaitPort waits until something listens on 127.0.0.1:PORT.
func cmdWaitPort(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: hamstack wait-port PORT [SECONDS]")
	}
	port, err := strconv.Atoi(args[0])
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("bad port %q", args[0])
	}
	secs := 10
	if len(args) > 1 {
		if secs, err = strconv.Atoi(args[1]); err != nil || secs < 1 {
			return fmt.Errorf("bad timeout %q", args[1])
		}
	}
	return waitPort(port, time.Duration(secs)*time.Second)
}

func waitPort(port int, d time.Duration) error {
	deadline := time.Now().Add(d)
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	for time.Now().Before(deadline) {
		if c, err := net.DialTimeout("tcp", addr, 250*time.Millisecond); err == nil {
			c.Close()
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("nothing is listening on port %d after %s", port, d.Round(time.Second))
}

// missingRadios lists the active rigs with CAT control whose radio is not plugged in right now.
func missingRadios(cfg config.Config) []config.Rig {
	snap := snapshotOrEmpty()
	var out []config.Rig
	for _, r := range cfg.ActiveRigs() {
		if r.NoCAT {
			continue
		}
		prof, err := findProfileFor(cfg, r)
		if err != nil {
			continue
		}
		if dev, err := resolveCAT(r, prof, snap); err != nil || (dev == "" && prof.Rig.DeviceGlob != "") {
			out = append(out, r)
		}
	}
	return out
}

func findProfileFor(cfg config.Config, r config.Rig) (profile.Profile, error) {
	_, prof, err := findRig(cfg, r.ID)
	return prof, err
}

// noteMissingRadios explains, after a start, why services of an unplugged radio are not running. They exit
// quietly rather than restarting in a loop, and start working as soon as the radio is plugged in and
// the service is started again.
func noteMissingRadios(started []units.Service) {
	cfg, _ := config.Load()
	rigs := map[string]bool{}
	for _, s := range started {
		if s.Rig != "" {
			rigs[s.Rig] = true
		}
	}
	for _, r := range missingRadios(cfg) {
		if rigs[r.ID] {
			fmt.Printf("note: the radio for rig %s is not plugged in (or not found), so its services stop and wait. Plug it in and power it on, then: hamstack start %s\n", r.ID, r.ID)
		}
	}
}
