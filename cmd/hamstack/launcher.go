package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ATCUSA/hamstack/internal/apps"
	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/ledger"
	"github.com/ATCUSA/hamstack/internal/svc"
	"github.com/ATCUSA/hamstack/internal/web"
)

// Desktop launchers: menu entries for the dashboard, the terminal interface and each installed program, so they are
// a click away on the radio computer's own desktop. A program's entry runs "hamstack launch", which sets up its radio
// the way the dashboard does, starts it, and has a "Launch with debug output" action that shows what it prints.
//
// They are files in ~/.local/share/applications, recorded in the ledger so 'hamstack uninstall' removes them.

const launcherMarker = "X-Hamstack-Launcher=true"

type launcherEntry struct {
	File, Name, Comment, Icon string
	Exec                      []string
	Terminal                  bool
	Debug                     []string // the "Launch with debug output" action, if any
}

func launcherDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "applications")
}

// execField quotes a command for a .desktop Exec key. Characters that need layers of escaping are refused rather
// than half-escaped: a path with one in it is not one to put in a launcher.
func execField(argv []string) (string, error) {
	var out []string
	for _, a := range argv {
		if strings.ContainsAny(a, "\"$`\\%\n") {
			return "", fmt.Errorf("%q has a character a desktop launcher cannot quote safely", a)
		}
		if strings.ContainsAny(a, " \t'<>~|&;*?#()") {
			a = `"` + a + `"`
		}
		out = append(out, a)
	}
	return strings.Join(out, " "), nil
}

// launcherEntries are the entries for this setup: the dashboard (if it runs), the terminal interface, and each
// installed program.
func launcherEntries(cfg config.Config, self string) []launcherEntry {
	var out []launcherEntry
	if cfg.RunsWeb() {
		w := *cfg.Web
		w.Access = cfg.NetworkAccess("web", w.EffectiveAccess())
		out = append(out, launcherEntry{File: "hamstack-dashboard.desktop", Name: "Hamstack dashboard", Comment: "Radio, apps, services and updates in the browser",
			Icon: "hamstack", Exec: []string{"xdg-open", webURL(w)}})
	}
	out = append(out, launcherEntry{File: "hamstack-tui.desktop", Name: "Hamstack", Comment: "Set up and manage the radio software (terminal interface)",
		Icon: "hamstack", Exec: []string{self, "tui"}, Terminal: true})
	for _, a := range apps.All {
		if a.Name == "console" || a.Path() == "" {
			continue
		}
		out = append(out, launcherEntry{File: "hamstack-app-" + a.Name + ".desktop", Name: a.Title + " (hamstack)",
			Comment: a.Title + " with its radio set up by hamstack", Icon: a.Name,
			Exec: []string{self, "launch", a.Name}, Debug: []string{self, "launch", a.Name, "--debug"}})
	}
	return out
}

func desktopFile(e launcherEntry) (string, error) {
	exe, err := execField(e.Exec)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("[Desktop Entry]\nType=Application\nVersion=1.0\n")
	fmt.Fprintf(&b, "Name=%s\nComment=%s\nExec=%s\n", e.Name, e.Comment, exe)
	if e.Icon != "" {
		fmt.Fprintf(&b, "Icon=%s\n", e.Icon)
	}
	fmt.Fprintf(&b, "Terminal=%v\nCategories=HamRadio;Network;Utility;\n%s\n", e.Terminal, launcherMarker)
	if e.Debug != nil {
		dbg, err := execField(e.Debug)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "Actions=debug;\n\n[Desktop Action debug]\nName=Launch with debug output\nExec=%s\n", dbg)
	}
	return b.String(), nil
}

// installedLaunchers are the launcher files hamstack has written (recognised by their marker, not just their name).
func installedLaunchers() []string {
	files, _ := filepath.Glob(filepath.Join(launcherDir(), "hamstack-*.desktop"))
	var out []string
	for _, f := range files {
		if b, err := os.ReadFile(f); err == nil && strings.Contains(string(b), launcherMarker) {
			out = append(out, f)
		}
	}
	sort.Strings(out)
	return out
}

// installLaunchers writes the entries, removes ones for programs that are gone, and records them.
func installLaunchers(cfg config.Config, self string) ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(launcherDir(), 0o755); err != nil {
		return nil, err
	}
	var written []string
	keep := map[string]bool{}
	for _, e := range launcherEntries(cfg, self) {
		text, err := desktopFile(e)
		if err != nil {
			return written, err
		}
		path := filepath.Join(launcherDir(), e.File)
		if old, err := os.ReadFile(path); err == nil && !strings.Contains(string(old), launcherMarker) {
			continue // someone else's file with our name: not ours to replace
		}
		if err := writeFileAtomic(path, []byte(text), 0o644); err != nil {
			return written, err
		}
		written = append(written, path)
		keep[path] = true
	}
	for _, f := range installedLaunchers() { // a program that is gone has no entry
		if !keep[f] {
			_ = os.Remove(f)
		}
	}
	if icon, err := installIcon(); err == nil && icon != "" {
		written = append(written, icon)
	}
	hidden, edited := hideOwnEntries(launcherDir(), systemAppDirs(), func(a apps.App) bool { return keep[filepath.Join(launcherDir(), "hamstack-app-"+a.Name+".desktop")] })
	written = append(written, hidden...)
	for _, c := range edited { // put back from their copies on uninstall, never removed: they are the programs' own files
		if err := ledger.Add(home, c); err != nil {
			return written, err
		}
	}
	if err := ledger.Add(home, ledger.Change{Kind: ledger.Files, Role: ledger.Support, For: "launcher", Names: written}); err != nil {
		return written, err
	}
	for _, c := range edited {
		written = append(written, c.Path+" (hidden from the menu)")
	}
	return written, nil
}

// hasDesktop says whether the computer has a graphical desktop installed: a session a display manager offers (X11 or
// Wayland). Not $DISPLAY: setup runs over SSH on a Raspberry Pi that has a desktop. IceWM's sessions do not count:
// the dashboard installs IceWM to frame the programs it shows in the browser, and its package brings a session file
// (seen on Debian and Fedora servers with no desktop). root is "/" but for tests.
func hasDesktop(root string) bool {
	for _, d := range []string{"usr/share/xsessions", "usr/share/wayland-sessions"} {
		m, _ := filepath.Glob(filepath.Join(root, d, "*.desktop"))
		for _, f := range m {
			if !strings.HasPrefix(filepath.Base(f), "icewm") {
				return true
			}
		}
	}
	return false
}

// desktopRoot is where hasDesktop looks. A variable so a test can give a fake one.
var desktopRoot = "/"

// refreshLaunchers keeps hamstack's menu entries up to date by itself (setup, and after programs are installed): on a
// computer with a desktop, unless the owner turned them off ("hamstack launcher remove", or Settings). It says what it
// did on w.
func refreshLaunchers(cfg config.Config, w io.Writer) {
	if !config.On(cfg.Launchers, true) || !hasDesktop(desktopRoot) && len(installedLaunchers()) == 0 {
		return
	}
	self, err := os.Executable()
	if err != nil {
		return
	}
	written, err := installLaunchers(cfg, self)
	if err != nil {
		fmt.Fprintf(w, "hamstack could not update its menu entries: %v\n", err)
		return
	}
	fmt.Fprintf(w, "Menu entries for hamstack and the radio programs are in the desktop's application menu (%d files; turn off: hamstack launcher remove).\n", len(written))
}

// setLaunchers records whether the menu entries are wanted, so a removal is not undone by the next install.
func setLaunchers(on bool) error {
	_, err := mutateOnly(func(c *config.Config) (string, error) { c.Launchers = config.Set(on); return "", nil })
	return err
}

const iconMarker = "<!-- written by hamstack -->"

func iconPath() string {
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		home, _ := os.UserHomeDir()
		data = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(data, "icons", "hicolor", "scalable", "apps", "hamstack.svg")
}

// installIcon puts hamstack's logo where desktop menus look for an icon named "hamstack". Someone else's file of that
// name is left alone.
func installIcon() (string, error) {
	path := iconPath()
	if old, err := os.ReadFile(path); err == nil && !strings.Contains(string(old), iconMarker) {
		return "", nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	svg := string(web.LogoSVG())
	if i := strings.Index(svg, "<svg"); i >= 0 {
		svg = svg[:i] + iconMarker + "\n" + svg[i:]
	}
	return path, writeFileAtomic(path, []byte(svg), 0o644)
}

// removeLaunchers takes away every launcher hamstack wrote, and the record of them.
func removeLaunchers() (int, error) {
	n := 0
	for _, f := range installedLaunchers() {
		if err := os.Remove(f); err != nil {
			return n, err
		}
		n++
	}
	if b, err := os.ReadFile(iconPath()); err == nil && strings.Contains(string(b), iconMarker) {
		_ = os.Remove(iconPath())
	}
	n += unhideOwnEntries(launcherDir())
	if home, err := os.UserHomeDir(); err == nil {
		_ = ledger.Drop(home, func(c ledger.Change) bool {
			return (c.Kind == ledger.Files || c.Kind == ledger.AppConfig) && c.For == "launcher"
		})
	}
	return n, nil
}

func cmdLauncher(args []string) error {
	sub := "list"
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "install", "refresh":
		self, err := os.Executable()
		if err != nil {
			return err
		}
		if err := setLaunchers(true); err != nil {
			return err
		}
		written, err := installLaunchers(loadCfg(), self)
		for _, f := range written {
			fmt.Println("wrote", f)
		}
		if err == nil {
			fmt.Printf("%d launchers are in your application menu (they may take a moment to appear).\n", len(written))
		}
		return err
	case "remove":
		if err := setLaunchers(false); err != nil {
			return err
		}
		n, err := removeLaunchers()
		fmt.Printf("removed %d launchers (they are not put back by themselves; hamstack launcher install adds them again)\n", n)
		return err
	case "list":
		files := installedLaunchers()
		if len(files) == 0 {
			fmt.Println("no launchers installed: hamstack launcher install")
		}
		for _, f := range files {
			fmt.Println(f)
		}
		return nil
	}
	return fmt.Errorf("usage: hamstack launcher [install|remove|list]")
}

// terminals are what a debug launch can open itself in when it was started from a menu: the first one installed.
var terminals = [][]string{{"x-terminal-emulator", "-e"}, {"gnome-terminal", "--"}, {"kgx", "-e"}, {"konsole", "-e"}, {"xfce4-terminal", "-x"},
	{"alacritty", "-e"}, {"kitty"}, {"foot"}, {"wezterm", "start", "--"}, {"xterm", "-e"}}

func terminalCommand() ([]string, bool) {
	for _, t := range terminals {
		if p, err := lookPath(t[0]); err == nil {
			return append([]string{p}, t[1:]...), true
		}
	}
	return nil, false
}

// cmdLaunch is what a program's menu entry runs: set up its radio, make room for it on the sound card, start it.
// With --debug it shows the program's output in a terminal (opening one if it was started from a menu) and keeps a
// copy in a log file; without, the output still goes to the log, for the next time something does not work.
func cmdLaunch(args []string) error {
	debug, name := false, ""
	for _, a := range args {
		switch {
		case a == "--debug":
			debug = true
		case name == "" && !strings.HasPrefix(a, "-"):
			name = a
		default:
			return fmt.Errorf("usage: hamstack launch PROGRAM [--debug]")
		}
	}
	if name == "" {
		return fmt.Errorf("usage: hamstack launch PROGRAM [--debug]")
	}
	app, ok := apps.Find(name)
	if !ok || app.Name == "console" {
		return fmt.Errorf("no program %q (see: hamstack app list)", name)
	}
	path := app.Path()
	if path == "" {
		return fmt.Errorf("%s is not installed (hamstack install %s)", app.Title, app.Package)
	}
	if debug && !isTerminal() {
		term, ok := terminalCommand()
		if !ok {
			return fmt.Errorf("no terminal program found to show the debug output in: run it in a terminal: hamstack launch %s --debug", name)
		}
		self, err := os.Executable()
		if err != nil {
			return err
		}
		return exec.Command(term[0], append(term[1:], self, "launch", name, "--debug")...).Start()
	}
	cfg := loadCfg()
	say := func(format string, a ...any) {
		if debug {
			fmt.Printf("hamstack: "+format+"\n", a...)
		}
	}
	var stopped []string
	if app.Radio {
		if other, busy := radioApp(app.Name); busy {
			return fmt.Errorf("%s has the radio: close it first", other.Title)
		}
		for _, u := range hfModemUnits(cfg) {
			say("%s needs the radio: stopping %s", app.Title, u)
			if err := svc.Run("stop", u); err != nil {
				return err
			}
			stopped = append(stopped, u)
		}
		var w io.Writer = io.Discard
		if debug {
			w = os.Stdout
		}
		readyForRadioApp(cfg, w)
	}
	// After the modems have let go of the sound card: PipeWire offers the card both ways only then, and WSJT-X's sound
	// is set by those names.
	for _, line := range autoSetupApp(app, cfg) {
		say("%s", line)
	}
	defer func() {
		for _, u := range stopped {
			say("%s closed: starting %s again", app.Title, u)
			_ = svc.Run("start", u)
		}
	}()
	logPath := filepath.Join(stateDir(), "launch-"+app.Name+".log")
	_ = os.MkdirAll(stateDir(), 0o755)
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	cmd := exec.Command(path)
	if home, err := os.UserHomeDir(); err == nil {
		cmd.Dir = home
	}
	var out io.Writer = logf
	if debug {
		out = io.MultiWriter(os.Stdout, logf)
		cmd.Stdin = os.Stdin
		fmt.Printf("hamstack: launching %s (%s). Its output follows; a copy is kept in %s\n\n", app.Title, path, logPath)
	}
	cmd.Stdout, cmd.Stderr = out, out
	if app.Radio {
		stopWatch := radioAppWatch(cfg, app, out)
		defer stopWatch()
	}
	err = cmd.Run()
	if debug {
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
		fmt.Printf("\nhamstack: %s ended (exit status %d). Press Enter to close this window.", app.Title, code)
		_, _ = fmt.Scanln()
	}
	if _, ok := err.(*exec.ExitError); ok {
		return nil // the program's own exit status is its business; the log has what it said
	}
	return err
}
