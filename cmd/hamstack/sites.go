package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/profile"
	"github.com/ATCUSA/hamstack/internal/svc"
	"github.com/ATCUSA/hamstack/internal/units"
	"github.com/ATCUSA/hamstack/internal/usbdev"
)

// identifyRig tells ScoreSites about one rig: whether its hardware can be recognised (a saved USB device,
// or a profile with a device pattern) and whether it is plugged in now.
func rigIdentifier(profs []profile.Profile, snap usbdev.Snapshot) func(config.Rig) (known, present bool) {
	return func(r config.Rig) (bool, bool) {
		p := findProfile(profs, r.Profile)
		if p == nil {
			return false, false
		}
		if r.CAT == "" && p.Rig.DeviceGlob == "" {
			return false, false // nothing to look for, like the dummy radio
		}
		return true, rigHardwarePresent(r, *p, snap)
	}
}

func scoreSites(cfg config.Config) []config.SiteScore {
	return cfg.ScoreSites(rigIdentifier(loadProfiles(), snapshotOrEmpty()))
}

// betterSite returns a site other than the current one whose radios are all plugged in, when the
// current site's are not. It is only a suggestion; nothing switches by itself.
func betterSite(cfg config.Config, scores []config.SiteScore) (config.SiteScore, bool) {
	var here *config.SiteScore
	for i := range scores {
		if scores[i].Site.ID == cfg.CurrentSite {
			here = &scores[i]
		}
	}
	if here != nil && (here.Known == 0 || here.Present == here.Known) {
		return config.SiteScore{}, false // the current site is fine (or cannot be judged)
	}
	var best *config.SiteScore
	for i := range scores {
		if scores[i].Site.ID != cfg.CurrentSite && scores[i].Complete() && (best == nil || scores[i].Present > best.Present) {
			best = &scores[i]
		}
	}
	if best == nil {
		return config.SiteScore{}, false
	}
	return *best, true
}

func cmdSites() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if len(cfg.Sites) == 0 {
		fmt.Println("No sites yet. A site is a place you operate from (home, the EOC, the club): which radios are there,")
		fmt.Println("which callsign is active, and the grid square. Moving between them is then one command.")
		fmt.Println("Save what you have now as one: hamstack site add home")
		return nil
	}
	scores := scoreSites(cfg)
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SITE\tNAME\tRIGS\tSTATION\tGRID\tRADIOS PLUGGED IN\t")
	for i, s := range cfg.Sites {
		mark := ""
		if s.ID == cfg.CurrentSite {
			mark = "(here)"
		}
		hw := "-"
		if sc := scores[i]; sc.Known > 0 {
			hw = fmt.Sprintf("%d of %d", sc.Present, sc.Known)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", s.ID, s.Name, orDash(strings.Join(s.Rigs, ",")), orDash(s.Station), orDash(s.Grid), hw, mark)
	}
	tw.Flush()
	if b, ok := betterSite(cfg, scores); ok {
		fmt.Printf("\nThe radios of site %q are plugged in, not those of this one. Switch with: hamstack site use %s\n", b.Site.ID, b.Site.ID)
	}
	return nil
}

func cmdSite(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: hamstack site add|set|remove|use|here ...  (list them with: hamstack sites)")
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "add":
		return siteAdd(rest)
	case "here":
		return siteHere(rest)
	case "remove", "use":
		if len(rest) != 1 {
			return fmt.Errorf("usage: hamstack site %s ID", verb)
		}
	case "set":
		if len(rest) != 3 {
			return fmt.Errorf("usage: hamstack site set ID KEY VALUE   (keys: name, rigs, station, grid)")
		}
	default:
		return fmt.Errorf("unknown site command %q", verb)
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	before := cloneConfig(cfg)
	switch verb {
	case "remove":
		if err := cfg.RemoveSite(rest[0]); err != nil {
			return err
		}
	case "set":
		if err := cfg.SetSiteField(rest[0], rest[1], rest[2]); err != nil {
			return err
		}
		if rest[0] == cfg.CurrentSite {
			if err := cfg.UseSite(rest[0]); err != nil { // the site you are at changed: apply it
				return err
			}
		}
	case "use":
		if err := cfg.UseSite(rest[0]); err != nil {
			return err
		}
	}
	if err := config.Save(cfg); err != nil {
		return err
	}
	fmt.Printf("site %s %s: done\n", rest[0], verb)
	if verb == "use" || (verb == "set" && rest[0] == cfg.CurrentSite) {
		describeCurrentSite(cfg)
		return applySiteChange(before)
	}
	return nil
}

// cloneConfig copies a config deeply enough that changing the copy's rigs, sites and stations does not
// touch the original, so it can serve as a "before".
func cloneConfig(c config.Config) config.Config {
	c.Rigs = append([]config.Rig(nil), c.Rigs...)
	c.Stations = append([]config.Station(nil), c.Stations...)
	c.Sites = append([]config.Site(nil), c.Sites...)
	if c.GPS != nil {
		g := *c.GPS
		c.GPS = &g
	}
	if c.AX25 != nil {
		a := *c.AX25
		c.AX25 = &a
	}
	return c
}

func describeCurrentSite(cfg config.Config) {
	var rigs []string
	for _, r := range cfg.ActiveRigs() {
		rigs = append(rigs, r.ID)
	}
	st := "-"
	if a, ok := cfg.ActiveStation(); ok {
		st = a.ID + " (" + a.Callsign + ")"
	}
	fmt.Printf("  rigs on: %s   station: %s   grid: %s\n", orDash(strings.Join(rigs, ", ")), st, orDash(cfg.EffectiveGrid()))
}

// applySiteChange brings the services in line with the site just selected and prints what it did.
func applySiteChange(before config.Config) error {
	msgs, err := siteChangeMessages(before)
	for _, m := range msgs {
		fmt.Println(m)
	}
	return err
}

// siteChangeMessages brings the services in line with the site just selected. If the stack was running,
// the new setup is started (and the old one stopped); if nothing was running, nothing is started. It
// returns what it did instead of printing it.
func siteChangeMessages(before config.Config) ([]string, error) {
	after, err := config.Load()
	if err != nil {
		return nil, err
	}
	running := false
	if svc.Available() == nil {
		for _, s := range units.ServicesFor(before) {
			if svc.Status(s.Unit()).Active == "active" {
				running = true
			}
		}
	}
	var msgs []string
	note, err := applyUnits(before)
	if err != nil {
		return nil, err
	}
	if note != "" {
		msgs = append(msgs, note)
	}
	if !running {
		return append(msgs, "nothing was running, so nothing was started: hamstack start"), nil
	}
	for _, s := range units.ServicesFor(after) {
		if svc.Status(s.Unit()).Load == "not-found" {
			continue
		}
		if err := svc.Run("restart", s.Unit()); err != nil { // starts it if it was stopped
			return msgs, err
		}
		msgs = append(msgs, "restarted "+s.Name())
	}
	return msgs, nil
}

func siteAdd(args []string) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return fmt.Errorf("usage: hamstack site add ID [--name LABEL] [--rigs a,b] [--station ID] [--grid GRID]   (defaults: what is set up right now)")
	}
	id := args[0]
	fs := flag.NewFlagSet("site add", flag.ContinueOnError)
	name := fs.String("name", "", "label")
	rigs := fs.String("rigs", "", "rigs that are on at this site (default: the rigs on now)")
	station := fs.String("station", "", "station active here (default: the active station)")
	grid := fs.String("grid", "", "grid square here (default: the grid in effect now)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	given := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	s := config.Site{ID: id, Name: *name, Station: *station, Grid: *grid}
	if given["rigs"] {
		for _, r := range strings.Split(*rigs, ",") {
			if r = strings.TrimSpace(r); r != "" {
				s.Rigs = append(s.Rigs, r)
			}
		}
	} else {
		for _, r := range cfg.ActiveRigs() {
			s.Rigs = append(s.Rigs, r.ID)
		}
	}
	if !given["station"] {
		s.Station = cfg.Active
	}
	if !given["grid"] {
		s.Grid = cfg.EffectiveGrid()
	}
	if err := cfg.AddSite(s); err != nil {
		return err
	}
	if err := config.Save(cfg); err != nil {
		return err
	}
	added, _ := cfg.SiteByID(id)
	fmt.Printf("added site %s: rigs %s, station %s, grid %s\n", id, orDash(strings.Join(added.Rigs, ",")), orDash(added.Station), orDash(added.Grid))
	if cfg.CurrentSite == id {
		fmt.Println("it is the current site: changes to your rigs and station are recorded in it.")
	} else {
		fmt.Printf("switch to it with: hamstack site use %s\n", id)
	}
	return nil
}

// siteHere compares what is plugged in with each site's radios and says where you probably are.
func siteHere(args []string) error {
	fs := flag.NewFlagSet("site here", flag.ContinueOnError)
	doSwitch := fs.Bool("switch", false, "switch to the matching site")
	yes := fs.Bool("yes", false, "do not ask before switching")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if len(cfg.Sites) == 0 {
		return fmt.Errorf("no sites yet: save one with 'hamstack site add ID'")
	}
	scores := scoreSites(cfg)
	var matches []config.SiteScore
	for _, sc := range scores {
		state := "no radios to recognise"
		if sc.Known > 0 {
			state = fmt.Sprintf("%d of %d radios plugged in", sc.Present, sc.Known)
			if len(sc.Missing) > 0 {
				state += " (missing: " + strings.Join(sc.Missing, ", ") + ")"
			}
		}
		mark := ""
		if sc.Site.ID == cfg.CurrentSite {
			mark = "  <- current"
		}
		fmt.Printf("  %-12s %s%s\n", sc.Site.ID, state, mark)
		if sc.Complete() {
			matches = append(matches, sc)
		}
	}
	switch {
	case len(matches) == 0:
		fmt.Println("\nNo site has all of its radios plugged in. Plug them in, or identify one: hamstack rig identify RIG")
		return nil
	case len(matches) > 1:
		fmt.Println("\nMore than one site matches; name the one you want: hamstack site use ID")
		return nil
	}
	m := matches[0].Site
	if m.ID == cfg.CurrentSite {
		fmt.Printf("\nYou are at %s.\n", m.ID)
		return nil
	}
	fmt.Printf("\nThe radios of %q are plugged in.\n", m.ID)
	if !*doSwitch {
		fmt.Printf("Switch with: hamstack site use %s   (or: hamstack site here --switch)\n", m.ID)
		return nil
	}
	if !*yes {
		a := &asker{in: bufioStdin(), out: os.Stdout}
		if !a.confirm(fmt.Sprintf("Switch to %s now?", m.ID), true) {
			return nil
		}
	}
	return cmdSite([]string{"use", m.ID})
}
