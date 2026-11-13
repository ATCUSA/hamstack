package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/patcfg"
	"github.com/ATCUSA/hamstack/internal/profile"
	"github.com/ATCUSA/hamstack/internal/usbdev"
)

func describeRig(r config.Rig) string {
	s := r.ID
	if r.Name != "" {
		s += " (" + r.Name + ")"
	}
	s += ": " + r.Profile
	if r.Modem != "" {
		s += ", modem " + r.Modem
	}
	if len(r.Bands) > 0 {
		s += ", " + strings.Join(r.Bands, "/")
	}
	if r.Off {
		s += "  [off]"
	}
	return s
}

func cmdRigs() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if len(cfg.Rigs) == 0 {
		fmt.Println("No rigs yet. A rig is one radio you own and how it is wired up.")
		fmt.Println("Add one: hamstack rig add ID --profile PROFILE   (see: hamstack profiles; plug the radio in first to have it detected)")
		return nil
	}
	profs := loadProfiles()
	snap := snapshotOrEmpty()
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "RIG\tRADIO\tMODEM\tBANDS\tPORTS\tCAT PORT\tSOUND CARD\tSTATE")
	for _, r := range cfg.Rigs {
		prof := findProfile(profs, r.Profile)
		model := r.Profile
		hw := "-"
		if prof != nil {
			model = prof.Name
			switch {
			case r.Off:
				hw = "off"
			case rigHardwarePresent(r, *prof, snap):
				hw = "present"
			default:
				hw = "NOT DETECTED"
			}
		}
		ports := ""
		if !r.NoCAT {
			ports = fmt.Sprintf("rig %d", r.RigPort)
		}
		switch r.Modem {
		case config.Mercury:
			ports += fmt.Sprintf(", modem %d", r.ArqPort)
		case config.Direwolf:
			agw, _ := config.DirewolfPorts(r)
			ports += fmt.Sprintf(", packet %d", agw)
		}
		ports = strings.TrimPrefix(ports, ", ")
		cat := "(profile default)"
		if r.CAT != "" {
			cat = filepath.Base(r.CAT)
		}
		snd := orDash(r.Audio)
		if r.Audio == "" && r.Modem == "" {
			snd = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", r.ID, model, orDash(r.Modem), orDash(strings.Join(r.Bands, ",")), ports, cat, snd, hw)
	}
	return tw.Flush()
}

func findProfile(profs []profile.Profile, id string) *profile.Profile {
	for i := range profs {
		if profs[i].ID == id {
			return &profs[i]
		}
	}
	return nil
}

func cmdRig(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: hamstack rig add|remove|set|enable|disable|identify ...  (list them with: hamstack rigs)")
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "add":
		return rigAdd(rest)
	case "remove", "enable", "disable", "identify":
		if len(rest) != 1 {
			return fmt.Errorf("usage: hamstack rig %s ID", verb)
		}
	case "set":
		if len(rest) != 3 {
			return fmt.Errorf("usage: hamstack rig set ID KEY VALUE   (keys: name, profile, modem, bands, cat, audio)")
		}
	default:
		return fmt.Errorf("unknown rig command %q", verb)
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	before := cloneConfig(cfg)
	rig, ok := cfg.RigByID(rest[0])
	if !ok {
		return fmt.Errorf("no rig %q (see: hamstack rigs)", rest[0])
	}
	switch verb {
	case "remove":
		if err := cfg.RemoveRig(rest[0]); err != nil {
			return err
		}
	case "enable":
		rig.Off = false
		cfg.SyncSite()
	case "disable":
		rig.Off = true
		cfg.SyncSite()
	case "set":
		if err := setRigField(rig, rest[1], rest[2]); err != nil {
			return err
		}
	case "identify":
		a := &asker{in: bufioStdin(), out: os.Stdout}
		prof, err := profile.Find(userProfileDir(), rig.Profile)
		if err != nil {
			return err
		}
		if err := identifyRig(a, &cfg, rig, prof, scanDevices, time.Sleep, 40*time.Second); err != nil {
			return err
		}
		// Unplugging and replugging takes a while: put the result on the configuration as it is now.
		found := *rig
		if cfg, err = config.Load(); err != nil {
			return err
		}
		now, ok := cfg.RigByID(found.ID)
		if !ok {
			return fmt.Errorf("rig %s was removed in the meantime", found.ID)
		}
		now.CAT, now.CATID, now.Audio, now.AudioUSB = found.CAT, found.CATID, found.Audio, found.AudioUSB
	}
	if err := config.Save(cfg); err != nil {
		return err
	}
	fmt.Printf("%s %s: done\n", verb, rest[0])
	return afterChange(before)
}

// afterChange brings installed units in line with the new configuration and says what happened.
func afterChange(before config.Config) error {
	note, err := applyUnits(before)
	if err != nil {
		return err
	}
	if note != "" {
		fmt.Println(note)
	}
	fmt.Println("Running services keep their old settings until restarted: hamstack restart")
	return nil
}

func setRigField(r *config.Rig, key, value string) error {
	switch key {
	case "name":
		r.Name = value
	case "profile":
		prof, err := profile.Find(userProfileDir(), value)
		if err != nil {
			return err
		}
		if !prof.HasCAT() && r.Modem == config.Mercury {
			return fmt.Errorf("%s has no CAT control, and mercury is keyed through rigctld: change the modem first (hamstack rig set %s modem direwolf)", prof.Name, r.ID)
		}
		r.Profile, r.NoCAT = value, !prof.HasCAT()
	case "modem":
		switch strings.ToLower(value) {
		case "none", "":
			r.Modem = ""
		case config.Mercury:
			if r.NoCAT {
				return fmt.Errorf("mercury is keyed through rigctld, and this radio has no CAT control: use direwolf")
			}
			r.Modem = config.Mercury
		case config.Direwolf:
			r.Modem = config.Direwolf
		default:
			return fmt.Errorf("unknown modem %q (mercury, direwolf, or none)", value)
		}
	case "bands":
		b, err := config.NormalizeBands(strings.Split(value, ","))
		if err != nil {
			return err
		}
		r.Bands = b
	case "cat":
		if value == "auto" || value == "" {
			r.CAT, r.CATID = "", ""
		} else {
			r.CAT, r.CATID = value, ""
		}
	case "audio":
		if value == "auto" || value == "" {
			r.Audio, r.AudioUSB = "", ""
		} else {
			r.Audio, r.AudioUSB = value, ""
		}
	default:
		return fmt.Errorf("unknown key %q (name, profile, modem, bands, cat, audio)", key)
	}
	return nil
}

func rigAdd(args []string) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return fmt.Errorf("usage: hamstack rig add ID --profile PROFILE [--name LABEL] [--modem mercury|direwolf|none] [--bands hf,vhf] [--cat DEVICE] [--audio CARD] [--no-detect]")
	}
	id := args[0]
	fs := flag.NewFlagSet("rig add", flag.ContinueOnError)
	profID := fs.String("profile", "", "radio model (see: hamstack profiles)")
	name := fs.String("name", "", "label")
	modem := fs.String("modem", "", "mercury (HF), direwolf (VHF/UHF packet), or none (default none)")
	bands := fs.String("bands", "", "hf, vhf, uhf (comma separated)")
	cat := fs.String("cat", "", "the CAT serial device (default: detect it)")
	snd := fs.String("audio", "", "the sound card's ALSA id (default: detect it)")
	noDetect := fs.Bool("no-detect", false, "do not look for the radio's USB devices")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *profID == "" {
		return fmt.Errorf("--profile is required (see: hamstack profiles)")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	before := cloneConfig(cfg)
	a := &asker{in: bufioStdin(), out: os.Stdout}
	spec := rigSpec{ID: id, Name: *name, Profile: *profID, Modem: *modem, CAT: *cat, Audio: *snd, Detect: !*noDetect && *cat == ""}
	spec.Bands = strings.Split(*bands, ",")
	if _, err := addRigNamed(a, &cfg, spec); err != nil {
		return err
	}
	cfg.SyncSite() // a rig added now is on now, so it belongs to the site you are at
	if s, ok := cfg.Here(); ok {
		fmt.Printf("it is part of site %s (change that with: hamstack site set %s rigs ...)\n", s.ID, s.ID)
	}
	if err := config.Save(cfg); err != nil {
		return err
	}
	return afterChange(before)
}

type rigSpec struct {
	ID, Name, Profile, Modem, CAT, Audio string
	Bands                                []string
	Detect                               bool
}

// addRigNamed creates a rig, assigning the radio's USB devices when it can recognise them.
func addRigNamed(a *asker, cfg *config.Config, sp rigSpec) (config.Rig, error) {
	prof, err := profile.Find(userProfileDir(), sp.Profile)
	if err != nil {
		return config.Rig{}, err
	}
	modem := strings.ToLower(sp.Modem)
	if modem == "none" {
		modem = ""
	}
	r := config.Rig{ID: sp.ID, Name: sp.Name, Profile: prof.ID, Modem: modem, Bands: sp.Bands, CAT: sp.CAT, Audio: sp.Audio, NoCAT: !prof.HasCAT()}
	// A radio with no USB identity (the dummy rig) has nothing to look for.
	if sp.Detect && (len(prof.USBIDs) > 0 || prof.Rig.DeviceGlob != "" || (prof.HasCAT() && prof.Rig.Baud > 0)) {
		snap, _ := scanDevices()
		assignDetected(a, *cfg, &r, prof, snap)
	}
	if err := cfg.AddRig(r); err != nil {
		return r, err
	}
	r, _ = cfg.RigByIDValue(sp.ID)
	fmt.Fprintf(a.out, "added rig %s: %s", r.ID, prof.Name)
	if !r.NoCAT {
		fmt.Fprintf(a.out, ", rig control port %d", r.RigPort)
	}
	switch r.Modem {
	case config.Mercury:
		fmt.Fprintf(a.out, ", modem port %d", r.ArqPort)
	case config.Direwolf:
		agw, _ := config.DirewolfPorts(r)
		fmt.Fprintf(a.out, ", packet TNC port %d", agw)
	}
	fmt.Fprintln(a.out)
	return r, nil
}

// claimedPaths are the CAT devices other rigs already use.
func claimedPaths(cfg config.Config, except string) map[string]bool {
	m := map[string]bool{}
	for _, o := range cfg.Rigs {
		if o.ID != except && o.CAT != "" {
			m[o.CAT] = true
		}
	}
	return m
}

// assignDetected looks for the rig's radio among the plugged-in USB devices and, if it can pick one
// without guessing, records its serial port and sound card on r.
func assignDetected(a *asker, cfg config.Config, r *config.Rig, prof profile.Profile, snap usbdev.Snapshot) {
	cands := candidateSerials(snap, prof, claimedPaths(cfg, r.ID))
	switch {
	case len(cands) == 0:
		fmt.Fprintf(a.out, "no USB serial port for the %s is plugged in (plug it in, then run: hamstack rig identify %s)\n", prof.Name, r.ID)
		return
	case len(cands) > 1:
		var opts []string
		for _, c := range cands {
			opts = append(opts, c.Describe())
		}
		i, ok := pick(a, "More than one device could be this radio:", opts)
		if !ok {
			fmt.Fprintf(a.out, "several candidates, none chosen: run: hamstack rig identify %s\n", r.ID)
			return
		}
		cands = cands[i : i+1]
	}
	applySerial(a, r, cands[0], snap)
}

// candidateSerials are the unclaimed serial ports that look like the profile's radio: by USB id,
// narrowed by the profile's device pattern (which picks the right interface of a multi-port radio).
func candidateSerials(snap usbdev.Snapshot, prof profile.Profile, claimed map[string]bool) []usbdev.Serial {
	var byID, byGlob []usbdev.Serial
	for _, s := range snap.Serials {
		if claimed[s.ByID] || claimed[s.Tty] {
			continue
		}
		for _, id := range prof.USBIDs {
			if strings.EqualFold(id, s.VID+":"+s.PID) {
				byID = append(byID, s)
				break
			}
		}
		if g := prof.Rig.DeviceGlob; g != "" && s.ByID != "" {
			if ok, _ := filepath.Match(g, s.ByID); ok {
				byGlob = append(byGlob, s)
			}
		}
	}
	if len(byGlob) > 0 {
		return byGlob
	}
	if len(prof.USBIDs) == 0 && prof.Rig.DeviceGlob == "" {
		// A radio hamstack knows nothing about (one picked from hamlib's list): offer every unclaimed serial
		// port that is not a GPS receiver, and let the user say which.
		var any []usbdev.Serial
		for _, s := range snap.Serials {
			if !claimed[s.ByID] && !claimed[s.Tty] && !s.LooksLikeGPS() {
				any = append(any, s)
			}
		}
		return any
	}
	return byID
}

// applySerial records a serial port on the rig, and the sound card that shares its USB hub.
func applySerial(a *asker, r *config.Rig, s usbdev.Serial, snap usbdev.Snapshot) {
	r.CAT, r.CATID = s.ByID, s.USBID()
	if r.CAT == "" {
		r.CAT = s.Tty
		fmt.Fprintf(a.out, "note: %s has no stable by-id name, so this assignment follows the port name and may need redoing after a reboot\n", s.Tty)
	}
	fmt.Fprintf(a.out, "CAT port: %s\n", s.Describe())
	for _, g := range snap.Groups() {
		for _, gs := range g.Serials {
			if gs.Tty != s.Tty {
				continue
			}
			switch {
			case len(g.Sounds) == 1:
				r.Audio, r.AudioUSB = g.Sounds[0].ID, g.Sounds[0].USBID()
				fmt.Fprintf(a.out, "sound card: %s\n", g.Sounds[0].Describe())
			case len(g.Sounds) > 1:
				var opts []string
				for _, c := range g.Sounds {
					opts = append(opts, c.Describe())
				}
				if i, ok := pick(a, "Which sound card belongs to it?", opts); ok {
					r.Audio, r.AudioUSB = g.Sounds[i].ID, g.Sounds[i].USBID()
				}
			}
		}
	}
}

// pick offers numbered choices and returns the index chosen, or ok=false if none was.
func pick(a *asker, title string, opts []string) (int, bool) {
	fmt.Fprintln(a.out, title)
	for i, o := range opts {
		fmt.Fprintf(a.out, "  %d) %s\n", i+1, o)
	}
	for {
		ans, ok := a.line("Choose a number (Enter to skip): ")
		if !ok || ans == "" {
			return 0, false
		}
		var n int
		if _, err := fmt.Sscanf(ans, "%d", &n); err == nil && n >= 1 && n <= len(opts) {
			return n - 1, true
		}
	}
}

// identifyRig finds a rig's radio by watching what appears when it is plugged in: the user unplugs it,
// then plugs it back in, and whatever new serial ports and sound cards show up are its. It works for
// hardware hamstack does not recognise, and for telling two identical radios apart.
func identifyRig(a *asker, cfg *config.Config, r *config.Rig, prof profile.Profile, scan func() (usbdev.Snapshot, error), sleep func(time.Duration), wait time.Duration) error {
	fmt.Fprintf(a.out, "Finding the USB devices of rig %s (%s).\n", r.ID, prof.Name)
	if _, ok := a.line("1. Unplug the radio's USB cable now (if it is already unplugged, skip this), then press Enter: "); !ok {
		return fmt.Errorf("cancelled")
	}
	before, err := scan()
	if err != nil {
		return err
	}
	fmt.Fprintln(a.out, "2. Plug it back in. Waiting for it to appear...")
	deadline := time.Now().Add(wait)
	var added []usbdev.Event
	var after usbdev.Snapshot
	for time.Now().Before(deadline) {
		sleep(time.Second)
		after, err = scan()
		if err != nil {
			return err
		}
		added = nil
		for _, e := range usbdev.Diff(before, after) {
			if e.Added {
				added = append(added, e)
			}
		}
		if len(added) > 0 {
			sleep(2 * time.Second) // let the radio's other ports and its sound card finish appearing
			if after, err = scan(); err != nil {
				return err
			}
			break
		}
	}
	if len(added) == 0 {
		return fmt.Errorf("nothing new appeared within %s: is the cable plugged in and the radio on?", wait.Round(time.Second))
	}
	newSerials := serialsAdded(before, after)
	if len(newSerials) == 0 {
		return fmt.Errorf("a device appeared but it has no USB serial port, so it cannot be a CAT port")
	}
	chosen := newSerials
	if g := prof.Rig.DeviceGlob; g != "" {
		var m []usbdev.Serial
		for _, s := range newSerials {
			if ok, _ := filepath.Match(g, s.ByID); ok {
				m = append(m, s)
			}
		}
		if len(m) > 0 {
			chosen = m
		}
	}
	if len(chosen) > 1 {
		var opts []string
		for _, c := range chosen {
			opts = append(opts, c.Describe())
		}
		i, ok := pick(a, "Several serial ports appeared. Which one is the CAT port?", opts)
		if !ok {
			return fmt.Errorf("no port chosen")
		}
		chosen = chosen[i : i+1]
	}
	r.CAT, r.CATID, r.Audio, r.AudioUSB = "", "", "", ""
	applySerial(a, r, chosen[0], after)
	return nil
}

func serialsAdded(before, after usbdev.Snapshot) []usbdev.Serial {
	had := map[string]bool{}
	for _, s := range before.Serials {
		had[s.Tty+"|"+s.USBID()] = true
	}
	var out []usbdev.Serial
	for _, s := range after.Serials {
		if !had[s.Tty+"|"+s.USBID()] {
			out = append(out, s)
		}
	}
	return out
}

func cmdPat(args []string) error {
	if len(args) == 1 && args[0] == "locator" {
		return patLocator()
	}
	if len(args) == 1 && args[0] == "sync" {
		return patSync()
	}
	if len(args) != 1 || (args[0] != "on" && args[0] != "off") {
		return fmt.Errorf("usage: hamstack pat on|off|locator|sync")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	before := cloneConfig(cfg)
	cfg.NoPat = args[0] == "off"
	if err := config.Save(cfg); err != nil {
		return err
	}
	fmt.Println("pat is now", args[0])
	return afterChange(before)
}

// patLocator sets the locator in the active station's Pat config to the grid square in effect, touching
// nothing else in the file.
func patLocator() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	st, ok := cfg.ActiveStation()
	if !ok {
		return fmt.Errorf("no active station")
	}
	grid := cfg.EffectiveGrid()
	if grid == "" {
		return fmt.Errorf("no grid square set: hamstack config set grid EM10dg")
	}
	if have, ok := patcfg.Locator(st.PatConfig); ok && have == grid {
		fmt.Printf("Pat's locator is already %s\n", grid)
		return nil
	}
	backup, err := patcfg.SetLocator(st.PatConfig, grid)
	if err != nil {
		return err
	}
	fmt.Printf("Pat's locator for station %s is now %s (every other setting unchanged; the previous file is %s)\n", st.ID, grid, backup)
	fmt.Println("Restart Pat to pick it up: hamstack restart pat")
	return nil
}

// patWiring is what the active station's Pat config should say for the rigs and grid in effect.
func patWiring(cfg config.Config) patcfg.Wiring {
	w := patcfg.Wiring{Grid: cfg.EffectiveGrid()}
	for _, r := range cfg.ActiveRigs() {
		if !r.NoCAT { // a rig with no CAT has no rigctld for Pat to talk to
			w.Rigs = append(w.Rigs, patcfg.RigRef{Name: r.ID, Port: r.RigPort})
		}
	}
	if hf, ok := cfg.PatHFRig(); ok {
		w.HFRig, w.HFArqPort = hf.ID, hf.ArqPort
	}
	if pk, ok := cfg.PatPacketRig(); ok {
		w.Packet = true
		w.AGWPort, _ = config.DirewolfPorts(pk)
		if !pk.NoCAT {
			w.PacketRig = pk.ID
		}
	}
	w.GPSAddr = cfg.GPSAddr()
	return w
}

// patSync wires the active station's Pat config to the rigs and grid at the current site: the locator,
// the rig connections, the HF modem and the packet TNC. Only those settings change; a backup is kept.
func patSync() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	st, ok := cfg.ActiveStation()
	if !ok {
		return fmt.Errorf("no active station")
	}
	backup, changed, err := patcfg.Wire(st.PatConfig, patWiring(cfg))
	if err != nil {
		return err
	}
	if len(changed) == 0 {
		fmt.Printf("Pat's config for station %s already matches your rigs and grid.\n", st.ID)
		return nil
	}
	fmt.Printf("Pat's config for station %s updated: %s (every other setting unchanged; the previous file is %s)\n", st.ID, strings.Join(changed, ", "), backup)
	fmt.Println("Restart Pat to pick it up: hamstack restart pat")
	return nil
}
