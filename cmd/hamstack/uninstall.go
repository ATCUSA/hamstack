package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/ATCUSA/hamstack/internal/ax25"
	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/install"
	"github.com/ATCUSA/hamstack/internal/ledger"
	"github.com/ATCUSA/hamstack/internal/svc"
	"github.com/ATCUSA/hamstack/internal/units"
)

const uninstallUsage = `usage: hamstack uninstall [--keep-apps | --remove-apps] [--dry-run] [--yes]

Removes hamstack and undoes the changes it made to this computer: its services, settings, the packages it
added for itself, the root-owned files it wrote. It asks whether to keep the ham radio programs it installed.

  --keep-apps     keep the ham radio programs, and what they need to run (the default)
  --remove-apps   also remove the ham radio programs hamstack installed
  --dry-run       show what would be done, change nothing

Either way the programs' own data (Pat's mailbox and settings, logbooks, contacts) is never deleted.
`

// uninstallFacts is what the plan is made from. It is gathered once, so the plan itself can be tested.
type uninstallFacts struct {
	home, username, uid string
	env                 install.Env
	changes             []ledger.Change
	cfgDir, stateDir    string
	cacheDir            string // ~/.cache/hamstack
	shareDir            string // ~/.local/share/hamstack: the ledger and the versions of what was built
	unitDir             string
	unitFiles           []string          // hamstack-*.service files
	loaded              []string          // hamstack units systemd knows of now
	patConfigs          map[string]string // station id -> its Pat config, for those kept in hamstack's folder
	patDir              string            // ~/.config/pat
	exe                 string            // this program, and whether it lies in the user's home
	exeInHome           bool
	exists              func(string) bool
	sysctl              func(key string) string
	lingerOn            bool
	removal             func(pkgs []string) (install.Removal, error)
	builtBefore         []string // programs built from source before the ledger existed
	appData             []string // the programs' own data folders that exist: named, never touched
}

func gatherUninstallFacts() (uninstallFacts, error) {
	var f uninstallFacts
	u, err := user.Current()
	if err != nil {
		return f, err
	}
	f.env = install.HostEnv()
	f.home, f.username, f.uid = f.env.Home, u.Username, u.Uid
	if f.changes, err = ledger.Load(f.home); err != nil {
		return f, fmt.Errorf("reading %s: %w", ledger.Path(f.home), err)
	}
	if f.cfgDir, err = config.Dir(); err != nil {
		return f, err
	}
	f.stateDir = stateDir()
	if d, err := os.UserCacheDir(); err == nil {
		f.cacheDir = filepath.Join(d, "hamstack")
	}
	f.shareDir = filepath.Dir(ledger.Path(f.home))
	f.unitDir, _ = units.Dir()
	f.unitFiles, _ = units.Installed()
	if out, err := exec.Command("systemctl", "--user", "list-units", "--all", "--plain", "--no-legend", "hamstack-*").Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if fs := strings.Fields(line); len(fs) > 0 && strings.HasPrefix(fs[0], "hamstack-") {
				f.loaded = append(f.loaded, fs[0])
			}
		}
	}
	cfg, _ := config.Load()
	f.patConfigs = map[string]string{}
	for _, st := range cfg.Stations {
		if rel, err := filepath.Rel(f.cfgDir, st.PatConfig); err == nil && !strings.HasPrefix(rel, "..") && fileExists(st.PatConfig) {
			f.patConfigs[st.ID] = st.PatConfig
		}
	}
	if d, err := os.UserConfigDir(); err == nil {
		f.patDir = filepath.Join(d, "pat")
	}
	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		f.exe = exe
		f.exeInHome = strings.HasPrefix(exe, f.home+string(filepath.Separator))
	}
	f.exists = fileExists
	f.sysctl = func(key string) string {
		b, _ := os.ReadFile("/proc/sys/" + strings.ReplaceAll(key, ".", "/"))
		return strings.TrimSpace(string(b))
	}
	f.lingerOn, _ = svc.LingerEnabled()
	f.removal = func(pkgs []string) (install.Removal, error) { return install.PlanRemoval(f.env, pkgs) }
	if entries, err := os.ReadDir(filepath.Join(f.shareDir, "installed")); err == nil {
		for _, e := range entries {
			f.builtBefore = append(f.builtBefore, e.Name())
		}
	}
	for _, d := range []string{".local/share/pat", ".config/pat", ".local/share/WSJT-X", ".config/WSJT-X.ini", ".local/share/JS8Call", ".config/JS8Call.ini",
		".fldigi", ".flrig", ".flmsg", ".flamp", ".xastir", ".config/direwolf"} {
		if p := filepath.Join(f.home, d); fileExists(p) {
			f.appData = append(f.appData, p)
		}
	}
	return f, nil
}

// The root-owned files hamstack writes under fixed names. Finding one means hamstack put it there, whether or
// not the ledger says so: installs from before the ledger existed are undone too.
const (
	sysctlFile    = "/etc/sysctl.d/60-hamstack-web.conf"
	modulesFile   = "/etc/modules-load.d/hamstack-ax25.conf"
	firewallUnit  = "/etc/systemd/system/hamstack-network-access.service"
	firewallEtc   = "/etc/hamstack-network-access"
	firewallState = "/var/lib/hamstack-network-access"
	portStartKey  = "net.ipv4.ip_unprivileged_port_start"
)

// withFound adds to the ledger's records the changes that can be recognised on the computer itself.
func withFound(f uninstallFacts) (changes []ledger.Change, notes []string) {
	changes = slices.Clone(f.changes)
	has := func(match func(ledger.Change) bool) bool { return slices.ContainsFunc(changes, match) }
	rootFile := func(path string) bool {
		return has(func(c ledger.Change) bool { return c.Kind == ledger.RootFile && c.Path == path })
	}
	if f.exists(sysctlFile) && !rootFile(sysctlFile) {
		changes = append(changes, ledger.Change{Kind: ledger.RootFile, Role: ledger.Support, For: "web", Path: sysctlFile})
	}
	if f.exists(sysctlFile) && !has(func(c ledger.Change) bool { return c.Kind == ledger.Sysctl && c.Key == portStartKey }) {
		if v, err := strconv.Atoi(f.sysctl(portStartKey)); err == nil && v < 1024 {
			changes = append(changes, ledger.Change{Kind: ledger.Sysctl, Role: ledger.Support, For: "web", Key: portStartKey, Prev: "1024"})
			notes = append(notes, "The lowest port ordinary programs may listen on was changed before hamstack kept records; it is set back to Linux's default, 1024.")
		}
	}
	if f.exists(modulesFile) && !rootFile(modulesFile) {
		changes = append(changes, ledger.Change{Kind: ledger.RootFile, Role: ledger.App, For: "ax25", Path: modulesFile})
	}
	if f.exists(ax25.PortsFile+".hamstack-bak") && !rootFile(ax25.PortsFile) {
		changes = append(changes, ledger.Change{Kind: ledger.RootFile, Role: ledger.App, For: "ax25", Path: ax25.PortsFile, Existed: true})
	}
	if (f.exists(firewallHelper) || f.exists(firewallUnit)) && !has(func(c ledger.Change) bool { return c.Kind == ledger.Firewall }) {
		changes = append(changes, ledger.Change{Kind: ledger.Firewall, Role: ledger.Support, For: "firewall", User: f.uid})
	}
	if f.lingerOn && !has(func(c ledger.Change) bool { return c.Kind == ledger.Linger }) {
		notes = append(notes, "Starting your services at boot (systemd lingering) is on, and hamstack has no record of turning it on, so it is left on. To turn it off: loginctl disable-linger "+f.username)
	}
	return changes, notes
}

func sq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// uninstallPlan is every step of an uninstall, and what to tell the user about what stays.
func uninstallPlan(f uninstallFacts, keepApps bool) (steps []install.Step, notes []string, err error) {
	changes, notes := withFound(f)
	undo := func(c ledger.Change) bool { return !keepApps || c.Role == ledger.Support || c.Role == "" }
	priv := func(desc string, optional bool, cmd ...string) error {
		argv, err := f.env.Priv(cmd...)
		if err != nil {
			return err
		}
		steps = append(steps, install.Step{Desc: desc, Cmd: argv, Optional: optional})
		return nil
	}

	// 1. hamstack's own services.
	if len(f.loaded) > 0 || len(f.unitFiles) > 0 {
		steps = append(steps, install.Step{Desc: "Stop hamstack's services and remove them from your login (" + strconv.Itoa(len(f.unitFiles)) + " unit files in " + f.unitDir + ")", Do: func() error {
			if len(f.loaded) > 0 {
				if err := svc.Run("stop", f.loaded...); err != nil {
					fmt.Println("  " + err.Error())
				}
			}
			links, _ := filepath.Glob(filepath.Join(f.unitDir, "*.wants", "hamstack-*.service"))
			for _, l := range links {
				_ = os.Remove(l)
			}
			if _, err := units.RemoveFiles(f.unitFiles); err != nil {
				return err
			}
			_ = svc.Run("daemon-reload")
			_ = svc.Run("reset-failed")
			if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
				_ = os.Remove(filepath.Join(dir, "hamstack-wayvnc.sock"))
			}
			return nil
		}})
	}

	// 2. Pat's settings for each station hold the Winlink account: they are the user's, not hamstack's.
	ids := make([]string, 0, len(f.patConfigs))
	for id := range f.patConfigs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		from, to := f.patConfigs[id], filepath.Join(f.patDir, "hamstack-"+id+".json")
		steps = append(steps, install.Step{Desc: "Keep station " + id + "'s Pat settings: move " + from + " to " + to, Do: func() error {
			if err := os.MkdirAll(f.patDir, 0o700); err != nil {
				return err
			}
			dest := to
			for i := 2; fileExists(dest); i++ {
				dest = strings.TrimSuffix(to, ".json") + "-" + strconv.Itoa(i) + ".json"
			}
			return os.Rename(from, dest)
		}})
		notes = append(notes, "Pat settings for station "+id+" are kept as "+to+" (use them with: pat --config "+to+" http).")
	}

	// 3. Root-owned changes.
	firewall := false
	for _, c := range changes {
		if c.Kind == ledger.Firewall && undo(c) {
			firewall = true
			uid := c.User
			if uid == "" {
				uid = f.uid
			}
			if err := priv("Remove hamstack's firewall table", true, "nft", "delete", "table", "inet", "hamstack_"+uid); err != nil {
				return nil, nil, err
			}
			// The helper and its watcher are shared by every account that uses hamstack here: they go only
			// with the last one.
			script := fmt.Sprintf("rm -f %[1]s/%[2]s.json %[3]s/%[2]s.json /etc/sudoers.d/hamstack-network-%[2]s /etc/sudoers.d/hamstack-network-%[2]s.hamstack-bak; "+
				"if ! ls %[1]s/*.json >/dev/null 2>&1; then systemctl disable --now hamstack-network-access.service 2>/dev/null; "+
				"rm -f %[4]s %[4]s.hamstack-bak %[5]s %[5]s.hamstack-bak; rm -rf %[1]s %[3]s; systemctl daemon-reload; fi",
				firewallEtc, uid, firewallState, firewallHelper, firewallUnit)
			if err := priv("Remove the firewall helper, its watcher service and its sudo rule", false, "sh", "-c", script); err != nil {
				return nil, nil, err
			}
		}
	}
	firewallPath := func(p string) bool {
		return p == firewallHelper || p == firewallUnit || strings.HasPrefix(p, firewallEtc+"/") || strings.HasPrefix(p, "/etc/sudoers.d/hamstack-network-")
	}
	for _, c := range changes {
		if !undo(c) {
			continue
		}
		switch c.Kind {
		case ledger.RootFile:
			if firewallPath(c.Path) {
				if !firewall {
					notes = append(notes, c.Path+" is left: it belongs to the firewall helper, which is not being removed.")
				}
				continue
			}
			if !strings.HasPrefix(c.Path, "/") || strings.Contains(c.Path, "..") {
				continue
			}
			bak := c.Path + ".hamstack-bak"
			if c.Existed {
				err = priv("Put back "+c.Path+" as it was before hamstack", false, "sh", "-c",
					"if [ -e "+sq(bak)+" ]; then mv -f "+sq(bak)+" "+sq(c.Path)+"; else echo 'no copy of the original was kept: leaving it'; fi")
			} else {
				err = priv("Remove "+c.Path, false, "rm", "-f", c.Path, bak)
			}
		case ledger.Sysctl:
			if c.Key != "" && c.Prev != "" && f.sysctl(c.Key) != c.Prev {
				err = priv("Set "+c.Key+" back to "+c.Prev, true, "sysctl", "-w", c.Key+"="+c.Prev)
			}
		case ledger.Modules:
			if len(c.Names) > 0 {
				mods := slices.Clone(c.Names)
				slices.Reverse(mods) // mkiss before ax25, which it uses
				err = priv("Unload the kernel modules hamstack loaded", true, append([]string{"modprobe", "-r"}, mods...)...)
			}
		case ledger.Group:
			for _, g := range c.Names {
				acct := c.User
				if acct == "" {
					acct = f.username
				}
				if err = priv("Take you out of the "+strconv.Quote(g)+" group again", true, "gpasswd", "-d", acct, g); err != nil {
					break
				}
				notes = append(notes, "Leaving the "+g+" group takes effect at your next login.")
			}
		case ledger.AppConfig:
			// The radio hamstack filled in points at a rigctld that goes with hamstack. Only the keys it set are
			// undone, and only while they still hold what it gave them; the rest of the settings are the person's.
			c := c
			steps = append(steps, install.Step{Desc: "Take hamstack's radio setup out of " + c.For + "'s settings (" + c.Path + ")", Optional: true, Do: func() error {
				msg, err := revertAppConfig(c)
				if msg != "" {
					fmt.Println("  " + msg)
				}
				return err
			}})
			if n := "The callsign and grid hamstack put in your programs' settings are left: they are yours to keep."; !slices.Contains(notes, n) {
				notes = append(notes, n)
			}
		case ledger.Linger:
			if f.lingerOn {
				steps = append(steps, install.Step{Desc: "Turn off starting your services at boot (hamstack turned it on)", Optional: true, Do: func() error { return svc.SetLinger(false) }})
			}
		}
		if err != nil {
			return nil, nil, err
		}
	}

	// 4. Packages.
	var pkgs, keptApps, keptBuild []string
	for _, c := range changes {
		if c.Kind != ledger.Packages {
			continue
		}
		switch {
		case undo(c):
			pkgs = append(pkgs, c.Names...)
		case c.Role == ledger.Build:
			keptBuild = append(keptBuild, c.Names...)
		default:
			keptApps = append(keptApps, c.Names...)
		}
	}
	if len(pkgs) > 0 {
		r, err := f.removal(pkgs)
		if err != nil {
			notes = append(notes, "Could not check which packages are safe to remove ("+err.Error()+"); none are removed. hamstack added: "+strings.Join(pkgs, " "))
		} else {
			for _, cmd := range r.Cmds {
				steps = append(steps, install.Step{Desc: fmt.Sprintf("Remove the %d packages hamstack added that nothing else uses", len(r.Remove)), Cmd: cmd})
			}
			needed := make([]string, 0, len(r.Needed))
			for p := range r.Needed {
				needed = append(needed, p)
			}
			sort.Strings(needed)
			for _, p := range needed {
				notes = append(notes, p+" was added by hamstack but is left installed: "+strings.Join(r.Needed[p], ", ")+" now needs it.")
			}
		}
	}
	if len(keptApps) > 0 {
		notes = append(notes, fmt.Sprintf("Kept: the %d packages of the ham radio programs hamstack installed.", len(keptApps)))
	}
	if len(keptBuild) > 0 {
		notes = append(notes, fmt.Sprintf("Kept: %d packages (compilers and libraries) hamstack built programs with, because those programs may need the libraries.", len(keptBuild)))
	}

	// 5. Programs under ~/.local.
	recorded := map[string]bool{}
	for _, c := range changes {
		if c.Kind != ledger.Files {
			continue
		}
		recorded[c.For] = true
		if !undo(c) {
			continue
		}
		name, paths := c.For, c.Names
		steps = append(steps, install.Step{Desc: fmt.Sprintf("Remove %s from %s (%d files)", name, f.env.Prefix(), len(paths)), Do: func() error {
			n, skipped := install.RemoveFiles(f.env.Prefix(), paths)
			fmt.Printf("  removed %d\n", n)
			for _, s := range skipped {
				fmt.Println("  left: " + s)
			}
			return nil
		}})
	}
	if !keepApps {
		var unknown []string
		for _, n := range f.builtBefore {
			if !recorded[n] {
				unknown = append(unknown, n)
			}
		}
		if len(unknown) > 0 {
			notes = append(notes, "Installed before hamstack kept records, so not removed (their files are under "+f.env.Prefix()+"): "+strings.Join(unknown, ", ")+".")
		}
		notes = append(notes, "Programs and packages installed before hamstack kept records are not removed: it cannot tell them from ones you installed yourself.")
	}

	// 6. hamstack's own folders, and what is left of the ledger.
	left := slices.DeleteFunc(slices.Clone(f.changes), undo)
	steps = append(steps, install.Step{Desc: "Remove hamstack's settings, state and cache: " + f.cfgDir + ", " + f.stateDir + ", " + f.cacheDir, Do: func() error {
		dirs := []string{f.cfgDir, f.stateDir}
		if f.cacheDir != "" { // the update checks' answers from GitHub
			dirs = append(dirs, f.cacheDir)
		}
		for _, d := range dirs {
			if d == "" || d == f.home || filepath.Base(d) != "hamstack" {
				return fmt.Errorf("refusing to remove %q", d)
			}
			if err := os.RemoveAll(d); err != nil {
				return err
			}
		}
		if filepath.Base(f.shareDir) != "hamstack" {
			return fmt.Errorf("refusing to remove %q", f.shareDir)
		}
		if len(left) == 0 {
			return os.RemoveAll(f.shareDir)
		}
		// What was kept stays on record, so a later uninstall can still remove it.
		return ledger.Drop(f.home, undo)
	}})
	if len(left) > 0 {
		notes = append(notes, "The record of what was kept stays in "+ledger.Path(f.home)+", so 'hamstack uninstall --remove-apps' can remove it later if you install hamstack again.")
	}

	// 7. hamstack itself.
	switch {
	case f.exe == "":
	case f.exeInHome:
		steps = append(steps, install.Step{Desc: "Remove hamstack itself: " + f.exe, Do: func() error {
			_ = os.Remove(f.exe + ".previous")
			return os.Remove(f.exe)
		}})
	case strings.HasPrefix(f.exe, "/usr/local/") || strings.HasPrefix(f.exe, "/opt/"):
		if err := priv("Remove hamstack itself", false, "rm", "-f", f.exe, f.exe+".previous"); err != nil {
			return nil, nil, err
		}
	default:
		notes = append(notes, f.exe+" was installed by your package manager: remove the hamstack package with it.")
	}

	if len(f.appData) > 0 {
		notes = append(notes, "Not touched, whichever you choose: the programs' own data (mail, logs, contacts, settings) in "+strings.Join(f.appData, ", ")+".")
	}
	return steps, notes, nil
}

func cmdUninstall(args []string) error {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, uninstallUsage) }
	keep := fs.Bool("keep-apps", false, "keep the ham radio programs")
	remove := fs.Bool("remove-apps", false, "also remove the ham radio programs hamstack installed")
	dry := fs.Bool("dry-run", false, "show the plan, change nothing")
	yes := fs.Bool("yes", false, "do not ask for confirmation")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *keep && *remove {
		return fmt.Errorf("choose one of --keep-apps and --remove-apps")
	}
	f, err := gatherUninstallFacts()
	if err != nil {
		return err
	}
	a := &asker{in: bufioStdin(), out: os.Stdout}
	keepApps := !*remove
	if !*keep && !*remove && !*yes && isTerminal() && stdinIsTerminal() {
		fmt.Println("hamstack can remove just itself, or also the ham radio programs it installed for you (Pat, hamlib, WSJT-X and so on).")
		fmt.Println("Either way their own data (mailboxes, logbooks, contacts, settings) is not deleted.")
		keepApps = a.confirm("Keep the ham radio programs installed?", true)
	}
	steps, notes, err := uninstallPlan(f, keepApps)
	if err != nil {
		return err
	}
	what := "Uninstalling hamstack and keeping the ham radio programs."
	if !keepApps {
		what = "Uninstalling hamstack and the ham radio programs it installed."
	}
	notice := what
	for _, n := range notes {
		notice += "\n  - " + n
	}
	if err := install.Run(steps, install.Options{DryRun: *dry, Yes: *yes, Notice: notice + "\n", In: a.in, Out: a.out}); err != nil {
		return err
	}
	if !*dry {
		fmt.Println("\nhamstack is uninstalled.")
	}
	return nil
}
