package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ATCUSA/hamstack/internal/catalog"
	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/distro"
	"github.com/ATCUSA/hamstack/internal/doctor"
	"github.com/ATCUSA/hamstack/internal/hamlib"
	"github.com/ATCUSA/hamstack/internal/install"
	"github.com/ATCUSA/hamstack/internal/patcfg"
	"github.com/ATCUSA/hamstack/internal/profile"
	"github.com/ATCUSA/hamstack/internal/svc"
	"github.com/ATCUSA/hamstack/internal/usbdev"
	"github.com/ATCUSA/hamstack/internal/versions"
)

// asker prompts on out and reads answers from one shared buffered reader.
func bufioStdin() *bufio.Reader { return bufio.NewReader(os.Stdin) }

type asker struct {
	in  *bufio.Reader
	out io.Writer
}

func (a *asker) line(prompt string) (string, bool) {
	fmt.Fprint(a.out, prompt)
	s, err := a.in.ReadString('\n')
	if err != nil && s == "" {
		return "", false
	}
	return strings.TrimSpace(s), true
}

func (a *asker) confirm(q string, def bool) bool { return install.Confirm(a.in, a.out, q, def) }

var idUnsafe = regexp.MustCompile(`[^a-z0-9_-]+`)

func cmdSetup(args []string) error {
	if len(args) == 0 || args[0] != "winlink" {
		return fmt.Errorf("usage: hamstack setup winlink [--radio ID] [--callsign CALL] [--station ID] [--ui on|off] [--skip-install] [--yes] [--dry-run]")
	}
	fs := flag.NewFlagSet("setup winlink", flag.ContinueOnError)
	comps := fs.String("components", "", "what to set up: comma list of rigctld,mercury,pat (default: ask)")
	radio := fs.String("radio", "", "radio profile id (see: hamstack profiles)")
	grid := fs.String("grid", "", "your grid square, e.g. EM10dg (saved and reused)")
	call := fs.String("callsign", "", "your callsign")
	stID := fs.String("station", "", "station id (default: derived from the callsign)")
	ui := fs.String("ui", "", "Mercury web UI: on or off")
	skip := fs.Bool("skip-install", false, "do not install missing programs")
	noGroups := fs.Bool("skip-groups", false, "do not offer to add you to the serial-port group")
	allowSrc := fs.Bool("allow-source-build", false, "with --yes, allow building hamlib from source if the radio needs a newer one")
	yes := fs.Bool("yes", false, "assume yes to confirmations")
	dry := fs.Bool("dry-run", false, "show what would be installed, write nothing")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	a := &asker{in: bufio.NewReader(os.Stdin), out: os.Stdout}
	return runSetup(a, setupOpts{grid: *grid, components: *comps, radio: *radio, call: *call, stationID: *stID, ui: *ui, skipInstall: *skip, yes: *yes, dry: *dry, allowSource: *allowSrc, skipGroups: *noGroups})
}

type setupOpts struct {
	components, radio, call, stationID, ui, grid string
	skipInstall, yes, dry                        bool
	allowSource, skipGroups                      bool
}

func runSetup(a *asker, o setupOpts) error {
	d := distro.Detect()
	fmt.Fprintf(a.out, "hamstack setup: Winlink\nSystem: %s (%s)\n\n", d.Name, d.Manager)
	if err := svc.Available(); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	profs, err := profile.All(userProfileDir())
	if err != nil {
		return err
	}
	n := 0
	step := func(title string) {
		n++
		fmt.Fprintf(a.out, "\n== %d. %s\n", n, title)
	}

	step("What to set up")
	lay, err := chooseLayout(a, cfg, o.components)
	if err != nil {
		return err
	}
	if lay.PickOnly {
		return pickAndInstall(a, o.yes, o.dry)
	}
	modem := lay.Modem
	cfg.NoPat = !lay.Pat
	switch {
	case !lay.Rig:
		for i := range cfg.Rigs { // kept in the config, just not run
			cfg.Rigs[i].Off = true
		}
	case len(cfg.Rigs) > 1:
		fmt.Fprintf(a.out, "You have %d rigs set up; manage them with 'hamstack rigs'. They are kept as they are.\n", len(cfg.Rigs))
	default:
		for i := range cfg.Rigs {
			cfg.Rigs[i].Off = false
			cfg.Rigs[i].Modem = modem
		}
	}
	if !o.dry {
		if err := config.Save(cfg); err != nil {
			return err
		}
	}

	if lay.Rig && len(cfg.Rigs) <= 1 {
		step("Radio")
		current := ""
		if len(cfg.Rigs) == 1 {
			current = cfg.Rigs[0].Profile
		}
		id, err := chooseRadio(a, profs, o.radio, current, profile.Detect(profs, filepath.Glob))
		if err != nil {
			return err
		}
		profs, _ = profile.All(userProfileDir()) // a search may have saved a new profile
		if p := findProfile(profs, id); p != nil {
			modem = reconcileModem(a, *p, modem)
			lay.Modem = modem
		}
		switch {
		case o.dry:
			fmt.Fprintf(a.out, "(dry run) would use radio %s\n", id)
			// Keep the rig in memory (nothing is saved) so the steps after this plan what it needs.
			if p := findProfile(profs, id); p != nil && len(cfg.Rigs) == 0 {
				_ = cfg.AddRig(config.Rig{ID: id, Profile: id, Modem: modem, NoCAT: !p.HasCAT()})
			}
		case len(cfg.Rigs) == 0:
			if _, err := addRigNamed(a, &cfg, rigSpec{ID: id, Profile: id, Modem: modem, Detect: true}); err != nil {
				return err
			}
		default:
			if p := findProfile(profs, id); p != nil {
				changeRigRadio(a, cfg, &cfg.Rigs[0], *p, modem, snapshotOrEmpty())
			}
		}
		if !o.dry {
			if err := config.Save(cfg); err != nil {
				return err
			}
		}
	}

	if lay.Rig && !o.skipGroups {
		step("Permissions")
		if err := ensureGroups(a, cfg, o); err != nil {
			return err
		}
	}

	if lay.Pat || lay.Modem != "" {
		step("Your station (callsign and location)")
		if o.dry {
			fmt.Fprintln(a.out, "(dry run) skipping station setup")
		} else {
			// The grid first: starter Pat configs are written with it. Saved so it is asked once.
			if err := chooseGrid(a, &cfg, o); err != nil {
				return err
			}
			if err := config.Save(cfg); err != nil {
				return err
			}
			if err := chooseStation(a, &cfg, o); err != nil {
				return err
			}
		}
	}

	if lay.Modem == config.Mercury {
		step("Mercury web UI")
		switch strings.ToLower(o.ui) {
		case "on":
			cfg.UI = true
		case "off":
			cfg.UI = false
		default:
			fmt.Fprintln(a.out, "Mercury can serve a web page with the modem's status and waterfall.")
			fmt.Fprintln(a.out, "It listens on every network interface and has no password.")
			cfg.UI = a.confirm("Enable it?", false)
		}
	}
	if !o.dry {
		if err := config.Save(cfg); err != nil {
			return err
		}
	}

	step("Programs")
	if o.skipInstall {
		fmt.Fprintln(a.out, "skipped (--skip-install)")
	} else {
		steps, notice, err := buildInstallStepsSrc(packagesFor(cfg.Components()), false, nil)
		if err != nil {
			return err
		}
		if err := install.Run(steps, install.Options{DryRun: o.dry, Yes: o.yes, In: a.in, Out: a.out, Notice: notice}); err != nil {
			return err
		}
	}
	if o.dry {
		fmt.Fprintln(a.out, "\n(dry run) stopping before writing configuration.")
		return nil
	}
	if (cfg.NeedsRadio() || cfg.Has(config.Direwolf)) && !o.skipInstall {
		if err := offerNewerSources(a, cfg, profs, o); err != nil {
			return err
		}
	}

	step("Services")
	p, warns, err := buildPlan()
	if err != nil {
		return err
	}
	for _, w := range warns {
		fmt.Fprintln(a.out, "warning:", w)
	}
	if err := installUnits(p); err != nil {
		return err
	}

	step("Check")
	results := doctor.Run(doctor.Inputs{Cfg: cfg, Profiles: profs, Sys: realSystem()})
	printDoctor(results)
	if doctor.Worst(results) == doctor.Fail {
		fmt.Fprintln(a.out, "\nSetup wrote your configuration but some checks failed; fix them and run 'hamstack doctor'.")
		return nil
	}

	fmt.Fprintln(a.out)
	if missing := missingRadios(cfg); len(missing) > 0 {
		for _, r := range missing {
			fmt.Fprintf(a.out, "The radio for rig %s is not plugged in (or not found). Its services will wait and show \"no radio\" until it is.\n", r.ID)
		}
		fmt.Fprintln(a.out, "Plug it in and power it on, then run: hamstack start   (the rest, such as Pat, starts now)")
	}
	if o.yes || a.confirm("Start the services now?", true) {
		if err := cmdControl("start", nil); err != nil {
			return err
		}
	}
	if o.yes || a.confirm("Also start them automatically when you log in?", false) {
		if err := cmdControl("enable", nil); err != nil {
			return err
		}
		// Enabled services start at login by default; at boot needs lingering. Offer it, never
		// force it: it keeps your services running with nobody logged in.
		if on, err := svc.LingerEnabled(); err == nil && !on {
			fmt.Fprintln(a.out, "\nOn a headless or always-on machine you will want them to start at boot, even with nobody logged in.")
			if o.yes {
				fmt.Fprintln(a.out, "To do that later: hamstack linger on")
			} else if a.confirm("Start them at boot too? (loginctl enable-linger)", false) {
				if err := svc.SetLinger(true); err != nil {
					fmt.Fprintf(a.out, "could not turn it on: %v\n", err)
				} else {
					fmt.Fprintln(a.out, "linger is now on")
				}
			}
		}
	}
	fmt.Fprintf(a.out, "\nDone.")
	if cfg.Has(config.Pat) {
		if st, ok := cfg.ActiveStation(); ok {
			fmt.Fprintf(a.out, " Pat's web UI: http://%s (set your Winlink password there if you have not).", st.HTTPAddr)
		}
	}
	fmt.Fprintln(a.out, "\nSee 'hamstack status', 'hamstack doctor', and run 'hamstack' for the interface.")
	printNextSteps(a.out, cfg)
	return nil
}

// changeRigRadio points an existing rig at the radio chosen in setup. The CAT flag and the modem follow the
// new radio (a Digirig has no CAT and cannot key Mercury), and when the model really changed, the USB devices
// saved for the old radio are dropped and looked for again: they belong to the old radio, and rigctld would
// otherwise open the old radio's port with the new radio's driver, or wait for a radio that is gone.
func changeRigRadio(a *asker, cfg config.Config, r *config.Rig, p profile.Profile, modem string, snap usbdev.Snapshot) {
	changed := r.Profile != p.ID
	r.Profile, r.NoCAT, r.Modem = p.ID, !p.HasCAT(), modem
	if !changed {
		return
	}
	r.CAT, r.CATID, r.Audio, r.AudioUSB = "", "", "", ""
	if len(p.USBIDs) > 0 || p.Rig.DeviceGlob != "" || (p.HasCAT() && p.Rig.Baud > 0) {
		assignDetected(a, cfg, r, p, snap)
	}
}

// printNextSteps lists the optional things that fit this setup, so they are found without reading the README.
func printNextSteps(w io.Writer, cfg config.Config) {
	var tips []string
	if cfg.Has(config.Direwolf) {
		tips = append(tips,
			"hamstack packet listen   watch the packets Direwolf hears; hamstack packet call CALL  to connect to a station",
			"hamstack ax25            the kernel's AX.25 stack (axcall, axlisten) if you want the classic tools")
	}
	if cfg.GPS == nil {
		tips = append(tips, "hamstack gps use         a GPS receiver gives you your grid square (hamstack gps grid)")
	}
	tips = append(tips, "hamstack network         every port the stack uses and who on your network can reach it")
	if len(cfg.Sites) == 0 {
		tips = append(tips, "hamstack site add       save this place (home, EOC, club) so switching radios later is one command")
	}
	fmt.Fprintln(w, "\nOptional next steps:")
	for _, t := range tips {
		fmt.Fprintln(w, "  "+t)
	}
}

// layout is what the user wants running: a radio with rig control, optionally a modem on it, and Pat.
type layout struct {
	Rig   bool
	Modem string // "", "mercury" or "direwolf"
	Pat   bool
	// PickOnly is not a set-up at all: the user wants to choose programs to install from the whole list
	// (fldigi, WSJT-X, Xastir...) and stop there.
	PickOnly bool
}

// layoutChoices are the menu presets for chooseLayout.
var layoutChoices = []struct {
	label string
	l     layout
}{
	{"Full Winlink over HF radio: rig control + Mercury modem + Pat", layout{Rig: true, Modem: config.Mercury, Pat: true}},
	{"Pat only: Winlink over the internet, no radio", layout{Pat: true}},
	{"HF radio modem for other software: rig control + Mercury (no Pat)", layout{Rig: true, Modem: config.Mercury}},
	{"Rig control only (rigctld)", layout{Rig: true}},
	{"VHF/UHF packet: Direwolf soundcard TNC + Pat (a Digirig, or a radio with CAT)", layout{Rig: true, Modem: config.Direwolf, Pat: true}},
	{"VHF/UHF packet modem for other software: Direwolf (no Pat)", layout{Rig: true, Modem: config.Direwolf}},
}

// layoutFromComponents reads the --components flag: a list of rigctld, mercury, direwolf and pat.
func layoutFromComponents(list []string) (layout, error) {
	var l layout
	rigctld, any := false, false
	for _, c := range list {
		switch strings.ToLower(strings.TrimSpace(c)) {
		case "":
			continue
		case config.RigCtld:
			rigctld = true
		case config.Mercury, config.Direwolf:
			m := strings.ToLower(strings.TrimSpace(c))
			if l.Modem != "" && l.Modem != m {
				return l, fmt.Errorf("one modem per radio: choose mercury (HF) or direwolf (VHF/UHF packet); add a second radio with 'hamstack rig add' for the other")
			}
			l.Modem = m
		case config.Pat:
			l.Pat = true
		default:
			return l, fmt.Errorf("unknown component %q (rigctld, mercury, direwolf, pat)", c)
		}
		any = true
	}
	if !any {
		return l, fmt.Errorf("choose at least one component (rigctld, mercury, direwolf, pat)")
	}
	if l.Modem == config.Mercury && !rigctld {
		return l, fmt.Errorf("mercury is keyed through rigctld: add rigctld, or drop mercury")
	}
	l.Rig = rigctld || l.Modem != ""
	return l, nil
}

func currentLayout(cfg config.Config) layout {
	l := layout{Rig: cfg.NeedsRadio(), Pat: cfg.PatEnabled()}
	switch {
	case cfg.Has(config.Mercury):
		l.Modem = config.Mercury
	case cfg.Has(config.Direwolf):
		l.Modem = config.Direwolf
	}
	return l
}

func chooseLayout(a *asker, cfg config.Config, flagVal string) (layout, error) {
	if flagVal != "" {
		l, err := layoutFromComponents(strings.Split(flagVal, ","))
		if err != nil {
			return l, err
		}
		fmt.Fprintf(a.out, "components: %s\n", flagVal)
		return l, nil
	}
	cur := currentLayout(cfg)
	configured := len(cfg.Rigs) > 0 || cfg.NoPat
	def := "1"
	for i, c := range layoutChoices {
		mark := ""
		if configured && c.l == cur {
			mark = "  (current)"
			def = strconv.Itoa(i + 1)
		}
		fmt.Fprintf(a.out, "  %d) %s%s\n", i+1, c.label, mark)
	}
	fmt.Fprintf(a.out, "  %d) Custom: choose each one\n", len(layoutChoices)+1)
	fmt.Fprintf(a.out, "  %d) Just install programs: pick from the whole list (fldigi, WSJT-X, JS8Call, Xastir, flrig...) and stop\n", len(layoutChoices)+2)
	for {
		ans, ok := a.line("Choose [" + def + "]: ")
		if !ok {
			return layout{}, fmt.Errorf("nothing chosen")
		}
		if ans == "" {
			ans = def
		}
		n, err := strconv.Atoi(ans)
		switch {
		case err == nil && n >= 1 && n <= len(layoutChoices):
			return layoutChoices[n-1].l, nil
		case err == nil && n == len(layoutChoices)+2:
			return layout{PickOnly: true}, nil
		case err == nil && n == len(layoutChoices)+1:
			var picked []string
			for _, c := range []string{config.RigCtld, config.Mercury, config.Direwolf, config.Pat} {
				def := c != config.Direwolf // the HF modem is the usual choice; packet is opt-in
				if a.confirm("Set up "+c+"?", def) {
					picked = append(picked, c)
				}
			}
			l, err := layoutFromComponents(picked)
			if err != nil {
				fmt.Fprintln(a.out, err)
				continue
			}
			return l, nil
		}
		fmt.Fprintln(a.out, "not a valid choice")
	}
}

func chooseStation(a *asker, cfg *config.Config, o setupOpts) error {
	if o.call == "" && len(cfg.Stations) > 0 {
		if st, ok := cfg.ActiveStation(); ok {
			fmt.Fprintf(a.out, "using the active station %s (%s); change it later with 'hamstack use ID'\n", st.ID, st.Callsign)
			return nil
		}
	}
	if o.call == "" && len(cfg.Stations) == 0 {
		home, _ := os.UserHomeDir()
		patDir := filepath.Join(home, ".config", "pat")
		if found, _ := patcfg.Scan(patDir); len(found) > 0 {
			fmt.Fprintf(a.out, "Found %d existing Pat configuration(s) in %s:\n", len(found), patDir)
			for _, f := range found {
				fmt.Fprintf(a.out, "  %s (%s)\n", f.ID, f.Callsign)
			}
			if a.confirm("Import them as stations? (the files are not changed)", true) {
				if err := stationImport(*cfg, patDir); err != nil {
					return err
				}
				reloaded, err := config.Load()
				if err != nil {
					return err
				}
				*cfg = reloaded
				return nil
			}
		}
	}
	call := o.call
	for call == "" {
		ans, ok := a.line("Your callsign: ")
		if !ok {
			return fmt.Errorf("no callsign given")
		}
		if config.ValidCallsign(config.NormalizeCallsign(ans)) {
			call = ans
		} else {
			fmt.Fprintln(a.out, "that does not look like a callsign")
		}
	}
	call = config.NormalizeCallsign(call)
	id := o.stationID
	if id == "" {
		id = strings.Trim(idUnsafe.ReplaceAllString(strings.ToLower(call), "-"), "-")
	}
	st, created, err := addStation(cfg, id, call, "", "", "")
	if err != nil {
		return err
	}
	fmt.Fprintf(a.out, "added station %s (%s), Pat web UI %s\n", st.ID, st.Callsign, st.HTTPAddr)
	if created {
		fmt.Fprintf(a.out, "created %s\nSet your Winlink password in Pat's web UI settings. hamstack never stores it.\n", st.PatConfig)
	}
	return nil
}

// stalePackage describes a distribution package that is older than the program's latest release.
type stalePackage struct {
	Name, Installed, Latest, Reason string
	MustHave                        bool // the setup cannot work without the newer one (a radio the old hamlib does not know)
}

// findStale looks at the programs the setup uses that came from the distribution's package manager and
// compares them with upstream. Distributions lag: Debian ships Direwolf 1.7 when 1.8.1 is current, and
// an old hamlib may not know a new radio. latest looks up the newest release ("" when it cannot).
func findStale(cfg config.Config, profs []profile.Profile, installed func(name string) *versions.Installed, latest func(name string) string) []stalePackage {
	var out []stalePackage
	check := func(name, why string, must bool) {
		in := installed(name)
		if in == nil || in.Version == "" || in.Origin != "system package" { // ~/.local is hamstack's own, newer build
			return
		}
		l := latest(name)
		if l == "" || versions.Compare(in.Version, l) >= 0 {
			return
		}
		out = append(out, stalePackage{Name: name, Installed: in.Version, Latest: l, Reason: why, MustHave: must})
	}
	if cfg.NeedsRadio() {
		why, must := "", false
		if unsupported := unsupportedRadios(cfg, profs); len(unsupported) > 0 {
			why, must = "it has no driver for "+strings.Join(unsupported, ", "), true
		}
		check(install.Hamlib, why, must)
	}
	if cfg.Has(config.Direwolf) {
		check(install.Direwolf, "", false)
	}
	return out
}

// unsupportedRadios names the chosen radios that the installed hamlib has no driver for.
func unsupportedRadios(cfg config.Config, profs []profile.Profile) []string {
	path, err := versions.Which("rigctld")
	if err != nil {
		return nil
	}
	models, err := hamlib.Installed(path)
	if err != nil {
		return nil
	}
	var out []string
	for _, r := range cfg.ActiveRigs() {
		for _, pr := range profs {
			if pr.ID == r.Profile && pr.Rig.Model > 2 { // the dummy and network rigs exist in every hamlib
				if _, ok := models[pr.Rig.Model]; !ok {
					out = append(out, fmt.Sprintf("%s (model %d)", pr.Name, pr.Rig.Model))
				}
			}
		}
	}
	return out
}

// offerNewerSources tells the user when a distribution package is older than upstream's release and lets
// them choose whether to build the newer one from source. A source build is slow, installs compilers
// and runs code downloaded from the project, so it is never started without consent: a prompt (the
// default is no, except when the setup cannot work without it), or --allow-source-build for unattended runs.
func offerNewerSources(a *asker, cfg config.Config, profs []profile.Profile, o setupOpts) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stale := findStale(cfg, profs, func(name string) *versions.Installed {
		pkg, ok := catalogPkg(name)
		if !ok {
			return nil
		}
		return versions.FindInstalled(pkg)
	}, func(name string) string {
		pkg, _ := catalogPkg(name)
		v, err := versions.Latest(ctx, pkg)
		if err != nil {
			return ""
		}
		return v
	})
	for _, sp := range stale {
		fmt.Fprintf(a.out, "\nYour distribution's %s is %s; the newest release is %s.", sp.Name, sp.Installed, sp.Latest)
		if sp.Reason != "" {
			fmt.Fprintf(a.out, " That is too old: %s.", sp.Reason)
		}
		fmt.Fprintln(a.out)
		if o.yes && !o.allowSource {
			fmt.Fprintf(a.out, "Not building it unattended. To build the newest: hamstack install %s --from-source\n", sp.Name)
			continue
		}
		if !o.yes && !o.allowSource {
			fmt.Fprintln(a.out, "You can keep the distribution's package (simple, updated by your package manager) or build the newest from")
			fmt.Fprintln(a.out, "source (slow, installs compilers, and the code is downloaded from the project).")
			if !a.confirm("Build "+sp.Name+" "+sp.Latest+" from source?", sp.MustHave) {
				fmt.Fprintf(a.out, "Keeping the distribution's. Later: hamstack install %s --from-source\n", sp.Name)
				continue
			}
		}
		steps, notice, err := buildInstallStepsSrc([]string{sp.Name}, true, map[string]bool{sp.Name: true})
		if err != nil {
			return err
		}
		if err := install.Run(steps, install.Options{Notice: notice, Yes: o.allowSource, In: a.in, Out: a.out}); err != nil {
			fmt.Fprintf(a.out, "Skipped (%v). Later: hamstack install %s --from-source\n", err, sp.Name)
		}
	}
	return nil
}

// suggestGrid looks for a locator in existing Pat configs (reading only that field).
func suggestGrid() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	found, _ := patcfg.Scan(filepath.Join(home, ".config", "pat"))
	best := ""
	for _, f := range found {
		if config.ValidGrid(f.Grid) {
			if f.Default {
				return config.NormalizeGrid(f.Grid)
			}
			if best == "" {
				best = config.NormalizeGrid(f.Grid)
			}
		}
	}
	return best
}

// chooseGrid settles the operator's grid square: a flag, the saved value, or a prompt that
// suggests the locator found in an existing Pat config. It is saved so it is asked only once.
func chooseGrid(a *asker, cfg *config.Config, o setupOpts) error {
	if o.grid != "" {
		g := config.NormalizeGrid(o.grid)
		if !config.ValidGrid(g) {
			return fmt.Errorf("%q is not a grid square (examples: EM10, EM10dg)", o.grid)
		}
		cfg.SetGrid(g)
		fmt.Fprintf(a.out, "grid square: %s\n", g)
		return nil
	}
	if g := cfg.EffectiveGrid(); g != "" {
		fmt.Fprintf(a.out, "using your saved grid square %s (change it with: hamstack config set grid ...)\n", g)
		return nil
	}
	sug := suggestGrid()
	prompt := "Your grid square, e.g. EM10dg (optional, Enter to skip): "
	if sug != "" {
		prompt = fmt.Sprintf("Your grid square [%s, from your Pat config] (Enter accepts, - skips): ", sug)
	}
	for {
		ans, ok := a.line(prompt)
		if !ok {
			return nil
		}
		switch {
		case ans == "" && sug != "":
			cfg.SetGrid(sug)
			return nil
		case ans == "" || ans == "-":
			return nil
		case config.ValidGrid(ans):
			cfg.SetGrid(config.NormalizeGrid(ans))
			return nil
		}
		fmt.Fprintln(a.out, "that does not look like a grid square (examples: EM10, EM10dg)")
	}
}

// reconcileModem fixes a modem choice that the radio cannot use. An interface with no CAT control, such as a
// Digirig, cannot key Mercury (which is keyed through rigctld), but it is exactly what Direwolf needs.
func reconcileModem(a *asker, p profile.Profile, modem string) string {
	if modem != config.Mercury || p.HasCAT() {
		return modem
	}
	fmt.Fprintf(a.out, "%s has no CAT control, and Mercury (the HF modem) is keyed through CAT.\n", p.Name)
	fmt.Fprintln(a.out, "It works well with Direwolf, the VHF/UHF packet modem. Using Direwolf for this radio; choose a radio with CAT for HF.")
	return config.Direwolf
}

// pickAndInstall lets the user choose programs from the whole catalog, adds what they need, offers what they
// suggest, and installs them: no radio, station or service is set up.
func pickAndInstall(a *asker, yes, dry bool) error {
	cfg, _ := config.Load()
	var missing []string
	for _, n := range packagesFor(cfg.Components()) {
		if p, ok := catalog.ByName(n); ok && versions.FindInstalled(p) == nil {
			missing = append(missing, n)
		}
	}
	fmt.Fprintln(a.out)
	pos, err := pickPrograms(a, missing)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		fmt.Fprintln(a.out, "Nothing chosen.")
		return nil
	}
	pos, err = expandSelection(a.in, a.out, pos, !yes && !dry)
	if err != nil {
		return err
	}
	steps, notice, err := buildInstallStepsSrc(pos, false, nil)
	if err != nil {
		return err
	}
	// A dry run stays a dry run here too: this used to install for real under 'setup winlink --dry-run'.
	if err := install.Run(steps, install.Options{DryRun: dry, Yes: yes, In: a.in, Out: a.out, Notice: notice}); err != nil {
		return err
	}
	if !dry {
		pathHint()
	}
	return nil
}
