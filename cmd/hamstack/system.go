package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/distro"
	"github.com/ATCUSA/hamstack/internal/install"
	"github.com/ATCUSA/hamstack/internal/ledger"
	"github.com/ATCUSA/hamstack/internal/svc"
	"github.com/ATCUSA/hamstack/internal/units"
)

// The operating system's own updates and power commands, from the command line and the dashboard.
//
// The dashboard cannot type a password, so these need a one-time rule that lets this user run exactly these
// commands as root without one: "hamstack system allow" writes it to /etc/sudoers.d (checked with visudo before
// it is put in place, recorded for 'hamstack uninstall', and removable with "hamstack system disallow").

const systemSudoersPrefix = "/etc/sudoers.d/hamstack-system-"

var lookPath = exec.LookPath // a variable so a test can name the paths

// systemCommands are the commands that bring the system up to date: refreshing the package lists (where that is
// a separate step) and upgrading. They are fixed, because the sudoers rule names them exactly.
func systemCommands(fam distro.Family) (refresh, upgrade []string, err error) {
	switch fam {
	case distro.Debian:
		return []string{"apt-get", "update"},
			[]string{"apt-get", "-y", "-o", "Dpkg::Options::=--force-confdef", "-o", "Dpkg::Options::=--force-confold", "upgrade"}, nil
	case distro.Arch:
		return nil, []string{"pacman", "-Syu", "--noconfirm"}, nil
	case distro.Fedora:
		return nil, []string{"dnf", "upgrade", "-y"}, nil
	}
	return nil, nil, fmt.Errorf("hamstack does not know this distribution's package manager: update it yourself")
}

func systemUpdateSteps(env install.Env) ([]install.Step, error) {
	refresh, upgrade, err := systemCommands(env.Distro.Family)
	if err != nil {
		return nil, err
	}
	var steps []install.Step
	for _, s := range []struct {
		desc string
		cmd  []string
	}{{"Refresh the package lists", refresh}, {"Update the system's packages", upgrade}} {
		if s.cmd == nil {
			continue
		}
		argv, err := env.Priv(s.cmd...)
		if err != nil {
			return nil, err
		}
		steps = append(steps, install.Step{Desc: s.desc, Cmd: argv})
	}
	return steps, nil
}

// parseUpgradable counts what the package manager says could be upgraded.
func parseUpgradable(fam distro.Family, out string) []string {
	var names []string
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		switch fam {
		case distro.Debian: // "libfoo/stable 1.2 arm64 [upgradable from: 1.1]"
			if strings.Contains(line, "[upgradable from:") {
				names = append(names, strings.SplitN(f[0], "/", 2)[0])
			}
		case distro.Arch: // "linux 6.1-1 -> 6.2-1"
			names = append(names, f[0])
		case distro.Fedora: // "kernel.x86_64  6.2  updates"; the "Obsoleting Packages" list is not upgrades
			if strings.HasPrefix(line, "Obsoleting") {
				return names
			}
			if len(f) >= 3 && strings.Contains(f[0], ".") {
				names = append(names, f[0])
			}
		}
	}
	return names
}

type systemCheck struct {
	sync.Mutex
	updates []string
	at      time.Time
	note    string
}

var lastSystemCheck systemCheck

// checkSystemUpdates lists the packages with updates. Where the rule is in place the lists are refreshed first, so the
// answer is current; without it they are as old as the system's own timer left them, which the note says.
func checkSystemUpdates(env install.Env, allowed bool) ([]string, string, error) {
	fam := env.Distro.Family
	note := ""
	if fam == distro.Debian && allowed {
		refresh, _, _ := systemCommands(fam)
		if argv, err := env.Priv(refresh...); err == nil {
			if out, err := exec.Command(argv[0], append([]string{"-n"}, argv[1:]...)...).CombinedOutput(); err != nil && os.Geteuid() != 0 {
				note = "could not refresh the package lists (" + strings.TrimSpace(string(out)) + "): the list below may be out of date"
			}
		}
	} else if fam == distro.Debian {
		note = "the package lists are as the system last refreshed them" + listsAge() + "; allow the dashboard to run system updates to refresh them on demand"
	}
	var cmd *exec.Cmd
	switch fam {
	case distro.Debian:
		cmd = exec.Command("apt", "list", "--upgradable")
	case distro.Arch:
		if _, err := lookPath("checkupdates"); err == nil {
			cmd = exec.Command("checkupdates")
		} else {
			cmd = exec.Command("pacman", "-Qu")
		}
	case distro.Fedora:
		cmd = exec.Command("dnf", "-q", "check-update")
	default:
		return nil, "", fmt.Errorf("hamstack does not know this distribution's package manager")
	}
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.Output()
	if err != nil {
		// checkupdates exits 2 and dnf exits 100 for "nothing" and "some": neither is a failure.
		if ee, ok := err.(*exec.ExitError); !ok || (ee.ExitCode() != 100 && ee.ExitCode() != 2) {
			return nil, note, fmt.Errorf("checking for updates: %w", err)
		}
	}
	list := parseUpgradable(fam, string(out))
	lastSystemCheck.Lock()
	lastSystemCheck.updates, lastSystemCheck.at, lastSystemCheck.note = list, time.Now(), note
	lastSystemCheck.Unlock()
	return list, note, nil
}

func listsAge() string {
	for _, p := range []string{"/var/lib/apt/periodic/update-success-stamp", "/var/lib/apt/lists"} {
		if fi, err := os.Stat(p); err == nil {
			d := time.Since(fi.ModTime()).Round(time.Minute)
			switch {
			case d < time.Hour:
				return fmt.Sprintf(" (%d minutes ago)", int(d.Minutes()))
			case d < 48*time.Hour:
				return fmt.Sprintf(" (%d hours ago)", int(d.Hours()))
			}
			return fmt.Sprintf(" (%d days ago)", int(d.Hours()/24))
		}
	}
	return ""
}

// sudoAllowed reports whether this user may restart the computer without a password, which is what the rule gives.
func sudoAllowed() bool {
	if os.Geteuid() == 0 {
		return true
	}
	// Asking sudo leaves a line in the system log each time, and the page asks as it refreshes: ask every couple of
	// minutes. The answer comes from really running the probe: "sudo -l" says yes for a command that merely needs
	// a password, which is not what is being asked.
	sudoAsked.Lock()
	defer sudoAsked.Unlock()
	if !sudoAsked.at.IsZero() && time.Since(sudoAsked.at) < 2*time.Minute {
		return sudoAsked.ok
	}
	return askSudo()
}

// sudoAllowedNow asks again whatever was answered a moment ago (the person has just run 'hamstack system allow').
func sudoAllowedNow() bool {
	if os.Geteuid() == 0 {
		return true
	}
	sudoAsked.Lock()
	defer sudoAsked.Unlock()
	return askSudo()
}

func askSudo() bool { // sudoAsked is held
	sc, err := lookPath("systemctl")
	ok := err == nil && exec.Command("sudo", "-n", sc, "--version").Run() == nil
	sudoAsked.at, sudoAsked.ok = time.Now(), ok
	return ok
}

var sudoAsked struct {
	sync.Mutex
	at time.Time
	ok bool
}

// sudoersEscape escapes what a sudoers command argument may not contain bare.
func sudoersEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(`,:=\`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

var sudoUser = regexp.MustCompile(`^[a-z_][a-z0-9_-]*[$]?$`)

// systemRule is the sudoers file: this user, these exact commands, nothing else.
func systemRule(username string, fam distro.Family) (string, error) {
	refresh, upgrade, err := systemCommands(fam)
	if err != nil {
		return "", err
	}
	if !sudoUser.MatchString(username) {
		return "", fmt.Errorf("unsupported username for a sudoers rule: %q", username)
	}
	var cmds [][]string
	if refresh != nil {
		cmds = append(cmds, refresh)
	}
	// "systemctl --version" changes nothing: it is how the dashboard tells whether the rule is in place.
	cmds = append(cmds, upgrade, []string{"systemctl", "--version"}, []string{"systemctl", "reboot"}, []string{"systemctl", "poweroff"})
	var list []string
	for _, c := range cmds {
		path, err := lookPath(c[0])
		if err != nil {
			return "", fmt.Errorf("%s is not installed", c[0])
		}
		parts := []string{path}
		for _, a := range c[1:] {
			parts = append(parts, sudoersEscape(a))
		}
		list = append(list, strings.Join(parts, " "))
	}
	return "# Written by hamstack ('hamstack system allow'): lets " + username + " update and restart this computer from the\n" +
		"# hamstack dashboard without a password. These commands only. Remove with: hamstack system disallow\n" +
		username + " ALL=(root) NOPASSWD: " + strings.Join(list, ", ") + "\n", nil
}

func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// systemAllowStep installs the rule.
func systemAllowStep(env install.Env, u *user.User) (install.Step, error) {
	content, err := systemRule(u.Username, env.Distro.Family)
	if err != nil {
		return install.Step{}, err
	}
	path := systemSudoersPrefix + u.Uid
	return sudoersFileStep(env, "Let "+u.Username+" update and restart this computer without a password (checked with visudo first): "+path+"\n     "+
		strings.TrimSpace(strings.SplitN(content, "NOPASSWD: ", 2)[1]), path, content, "system")
}

// sudoersFileStep installs a sudoers rule. It is written to a temporary file and checked with visudo first, then
// put in place with its mode already 0440: a sudoers file with a mistake in it stops sudo working at all, for
// every later step too, so a bad or half-written one must never reach /etc/sudoers.d.
func sudoersFileStep(env install.Env, desc, path, content, feature string) (install.Step, error) {
	script := "set -e; t=$(mktemp); trap 'rm -f \"$t\"' EXIT; printf %s " + shq(content) + " > \"$t\"; visudo -cf \"$t\" >/dev/null; " +
		"if [ -f " + shq(path) + " ] && [ ! -e " + shq(path+".hamstack-bak") + " ]; then cp -a " + shq(path) + " " + shq(path+".hamstack-bak") + "; fi; " +
		"install -m 0440 -o root -g root \"$t\" " + shq(path)
	argv, err := env.Priv("sh", "-c", script)
	if err != nil {
		return install.Step{}, err
	}
	return install.Step{Desc: desc, Cmd: argv,
		Track: func() func() []ledger.Change {
			_, statErr := os.Stat(path)
			return func() []ledger.Change {
				return []ledger.Change{{Kind: ledger.RootFile, Role: ledger.Support, For: feature, Path: path, Existed: statErr == nil}}
			}
		}}, nil
}

// systemInfo is what the dashboard shows about the operating system.
type systemInfo struct {
	Distro         string
	Allowed        bool
	RebootRequired bool
	Active         []string // what a restart or shutdown would cut off
	Updates        []string
	Checked        time.Time
	Note           string
}

func gatherSystemInfo() systemInfo {
	env := install.HostEnv()
	info := systemInfo{Distro: env.Distro.Name, Allowed: sudoAllowed(), Active: activeSessions(loadCfg())}
	for _, p := range []string{"/run/reboot-required", "/var/run/reboot-required"} {
		if fileExists(p) {
			info.RebootRequired = true
		}
	}
	lastSystemCheck.Lock()
	info.Updates, info.Checked, info.Note = lastSystemCheck.updates, lastSystemCheck.at, lastSystemCheck.note
	lastSystemCheck.Unlock()
	return info
}

// activeSessions lists what is running that a restart or shutdown would cut off.
func activeSessions(cfg config.Config) []string {
	var out []string
	for _, s := range units.ServicesFor(cfg) {
		if s.Kind != config.Web && svc.Status(s.Unit()).Active == "active" {
			out = append(out, serviceWhat(s))
		}
	}
	for _, name := range []string{"wsjtx", "js8call", "fldigi", "flrig", "flmsg", "flamp", "xastir"} {
		if svc.Status(vncUnit(name)).Active == "active" {
			out = append(out, name+" (open in the browser)")
		}
	}
	return out
}

// powerCommand reboots or powers off. secret is the administrator password the person typed on the dashboard, when
// the account has no passwordless rule; from a terminal it is empty and sudo asks there.
func powerCommand(verb, secret string) error {
	if os.Geteuid() == 0 {
		if out, err := exec.Command("systemctl", verb).CombinedOutput(); err != nil {
			return fmt.Errorf("%s failed: %s: %w", verb, strings.TrimSpace(string(out)), err)
		}
		return nil
	}
	if secret != "" {
		return runPrivileged(secret, "systemctl", verb)
	}
	if out, err := exec.Command("sudo", "-n", "systemctl", verb).CombinedOutput(); err != nil {
		if isTerminal() && stdinIsTerminal() { // a person at a terminal can type it themselves
			c := exec.Command("sudo", "systemctl", verb)
			c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
			return c.Run()
		}
		_ = out
		what := map[string]string{"reboot": "Restarting the computer", "poweroff": "Shutting the computer down"}[verb]
		return needSudo(what+" needs administrator rights (sudo).", []string{"sudo systemctl " + verb},
			"hamstack system "+map[string]string{"reboot": "reboot", "poweroff": "shutdown"}[verb])
	}
	return nil
}

func poweroffConfirmed(secret string) error { return powerAction("poweroff", secret) }

// powerAction is what really restarts or powers off the computer. A variable so that a test never does.
var powerAction = powerCommand

// powerDryRun shows what a restart or shutdown would run, and runs nothing.
func powerDryRun(verb string) error {
	cmd := "sudo systemctl " + verb
	if os.Geteuid() == 0 {
		cmd = "systemctl " + verb
	}
	fmt.Printf("Would run: %s\n(dry run: nothing was changed)\n", cmd)
	return nil
}

// shutdownWarning is what a person is told before the computer is powered off.
func shutdownWarning(active []string) string {
	w := "Shutting down POWERS THE COMPUTER OFF.\n" +
		"  - hamstack, the dashboard and this connection cannot turn it back on: someone has to press its power button.\n" +
		"  - Rig control, the modem, Pat and every open program stop, and a Winlink or radio session in progress is cut off.\n"
	if len(active) > 0 {
		w += "  - Running now: " + strings.Join(active, ", ") + ".\n"
	}
	return w
}

func cmdSystem(args []string) error {
	sub := "status"
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	yes, dry := false, false
	for _, a := range args {
		switch a {
		case "--yes":
			yes = true
		case "--dry-run":
			dry = true
		default:
			return fmt.Errorf("unknown option %q", a)
		}
	}
	env := install.HostEnv()
	a := &asker{in: bufioStdin(), out: os.Stdout}
	switch sub {
	case "status", "check":
		allowed := sudoAllowedNow()
		list, note, err := checkSystemUpdates(env, allowed && sub == "check")
		if err != nil {
			return err
		}
		fmt.Printf("%s\n", env.Distro.Name)
		switch {
		case len(list) == 0:
			fmt.Println("no package updates are waiting")
		case len(list) <= 12 || sub == "check":
			fmt.Printf("%d package updates waiting: %s\n", len(list), strings.Join(list, " "))
		default:
			fmt.Printf("%d package updates waiting (hamstack system check lists them)\n", len(list))
		}
		if note != "" {
			fmt.Println("note:", note)
		}
		if gatherSystemInfo().RebootRequired {
			fmt.Println("a restart is needed to finish a previous update (hamstack system reboot)")
		}
		fmt.Printf("the dashboard may update and restart this computer: %v", allowed)
		if !allowed {
			fmt.Print(" (hamstack system allow)")
		}
		fmt.Println()
		return nil
	case "update":
		steps, err := systemUpdateSteps(env)
		if err != nil {
			return err
		}
		if err := install.Run(steps, install.Options{Yes: yes, DryRun: dry, Notice: "Updates this computer's packages with its package manager."}); err != nil {
			return err
		}
		if !dry {
			return offerMaintenance(a, yes)
		}
		return nil
	case "reboot":
		if dry {
			return powerDryRun("reboot")
		}
		if !yes && !a.confirm("WARNING: a restart disconnects every user and stops all radio sessions. Restart this computer now?", false) {
			return fmt.Errorf("cancelled")
		}
		return rebootConfirmed("")
	case "shutdown":
		fmt.Print(shutdownWarning(activeSessions(loadCfg())))
		if dry {
			return powerDryRun("poweroff")
		}
		if !yes {
			word, _ := a.line("Type SHUT DOWN to power the computer off: ")
			if word != "SHUT DOWN" {
				return fmt.Errorf("cancelled")
			}
		}
		return poweroffConfirmed("")
	case "allow":
		u, err := user.Current()
		if err != nil {
			return err
		}
		step, err := systemAllowStep(env, u)
		if err != nil {
			return err
		}
		return install.Run([]install.Step{step}, install.Options{Yes: yes, DryRun: dry,
			Notice: "Lets the dashboard update and restart this computer. It can run these commands as root without a password and nothing else; 'hamstack uninstall' removes it."})
	case "disallow":
		u, err := user.Current()
		if err != nil {
			return err
		}
		path := systemSudoersPrefix + u.Uid
		argv, err := env.Priv("rm", "-f", path, path+".hamstack-bak")
		if err != nil {
			return err
		}
		if err := install.Run([]install.Step{{Desc: "Remove " + path, Cmd: argv}}, install.Options{Yes: yes, DryRun: dry}); err != nil {
			return err
		}
		if home, err := os.UserHomeDir(); err == nil && !dry {
			_ = ledger.Drop(home, func(c ledger.Change) bool { return c.Kind == ledger.RootFile && c.Path == path })
		}
		return nil
	}
	return fmt.Errorf("usage: hamstack system [status|check|update|reboot|shutdown|allow|disallow] [--yes] [--dry-run]")
}
