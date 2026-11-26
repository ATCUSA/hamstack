package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ATCUSA/hamstack/internal/apps"
	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/ledger"
	"github.com/ATCUSA/hamstack/internal/svc"
	"github.com/ATCUSA/hamstack/internal/units"
	"github.com/ATCUSA/hamstack/internal/versions"
)

// vncUnit is the unit name of a program's display.
func vncUnit(name string) string { return units.Service{Kind: config.VNC, Rig: name}.Unit() }

// desktopUnit is the unit name of the desktop share.
func desktopUnit() string { return units.Service{Kind: config.Desktop}.Unit() }

// radioApp returns the program (other than except) that has the radio's sound card open now, if any.
func radioApp(except string) (apps.App, bool) {
	for _, a := range apps.All {
		if a.Radio && a.Name != except && svc.Status(vncUnit(a.Name)).Active == "active" {
			return a, true
		}
	}
	return apps.App{}, false
}

// hfModemUnits are the running HF modems (Mercury, Data2G, ardopcf, xara) of the active rigs: the programs that hold the
// radio's sound card while a radio app wants it.
func hfModemUnits(cfg config.Config) []string {
	var out []string
	for _, r := range cfg.ActiveRigs() {
		if config.IsHFModem(r.Modem) {
			u := units.Service{Kind: r.Modem, Rig: r.ID}.Unit()
			if svc.Status(u).Active == "active" {
				out = append(out, u)
			}
		}
	}
	return out
}

// xvncArgs is the virtual display's command line: localhost only and no VNC password, because the only way to
// it is the dashboard's relay, which needs a login.
func xvncArgs(a apps.App) []string {
	return []string{"Xvnc", ":" + strconv.Itoa(a.Display()),
		"-rfbport", strconv.Itoa(a.Port()), "-localhost", "-SecurityTypes", "None",
		"-geometry", "1600x900", "-depth", "24", "-AlwaysShared", "-nolisten", "tcp", "-desktop", a.Title}
}

// appEnv is the program's environment: on its own X display, never on the computer's Wayland desktop.
func appEnv(base []string, display int) []string {
	var out []string
	for _, e := range base {
		k, _, _ := strings.Cut(e, "=")
		switch k {
		case "DISPLAY", "WAYLAND_DISPLAY", "QT_QPA_PLATFORM", "GDK_BACKEND", "XDG_SESSION_TYPE":
			continue
		}
		out = append(out, e)
	}
	return append(out, "DISPLAY=:"+strconv.Itoa(display), "QT_QPA_PLATFORM=xcb", "GDK_BACKEND=x11", "XDG_SESSION_TYPE=x11")
}

// stop asks a process to end, and kills it if it has not after a while.
func stop(p *exec.Cmd, done <-chan struct{}, grace time.Duration) {
	if p == nil || p.Process == nil {
		return
	}
	_ = p.Process.Signal(syscall.SIGTERM)
	select {
	case <-done:
	case <-time.After(grace):
		_ = p.Process.Kill()
		<-done
	}
}

func startWatched(c *exec.Cmd) (<-chan struct{}, error) {
	if err := c.Start(); err != nil {
		return nil, err
	}
	done := make(chan struct{})
	go func() { _ = c.Wait(); close(done) }()
	return done, nil
}

// runVNC is what hamstack-vnc@APP runs: the program on its own virtual display until it closes or the unit is
// stopped. A program that uses the radio first stops the HF modem, and starts it again afterwards.
func runVNC(name string) error {
	a, ok := apps.Find(name)
	if !ok {
		return fmt.Errorf("no app %q (see: hamstack app list)", name)
	}
	bin := a.Path()
	if bin == "" {
		fmt.Fprintf(os.Stderr, "hamstack: %s is not installed (hamstack install %s)\n", a.Title, a.Package)
		os.Exit(exitRadioMissing)
	}
	xvnc, err := apps.VNCServer()
	if err != nil {
		return fmt.Errorf("%v: hamstack dashboard on installs it", err)
	}
	cfg, _ := config.Load()
	if a.Name == "console" && (cfg.Web == nil || !cfg.Web.Console) {
		return fmt.Errorf("browser console is not enabled: review it in the dashboard Remote access section")
	}
	var stopped []string
	if a.Radio {
		if other, busy := radioApp(a.Name); busy {
			return fmt.Errorf("%s has the radio: close it first", other.Title)
		}
		for _, u := range hfModemUnits(cfg) {
			fmt.Fprintf(os.Stderr, "hamstack: %s needs the radio: stopping %s\n", a.Title, u)
			if err := svc.Run("stop", u); err != nil {
				return err
			}
			stopped = append(stopped, u)
		}
		readyForRadioApp(cfg, os.Stderr)
	}
	// After the modems have let go of the sound card (see cmdLaunch).
	for _, line := range autoSetupApp(a, cfg) {
		fmt.Fprintln(os.Stderr, "hamstack: "+line)
	}
	restart := func() {
		for _, u := range stopped {
			fmt.Fprintf(os.Stderr, "hamstack: %s closed: starting %s again\n", a.Title, u)
			_ = svc.Run("start", u)
		}
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)

	x := exec.Command(xvnc, xvncArgs(a)[1:]...)
	x.Stdout, x.Stderr = os.Stderr, os.Stderr
	xdone, err := startWatched(x)
	if err != nil {
		restart()
		return err
	}
	sock := filepath.Join("/tmp/.X11-unix", "X"+strconv.Itoa(a.Display()))
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	env := appEnv(os.Environ(), a.Display())

	// A window manager, if the system has one, so a program's other windows (WSJT-X's waterfall, fldigi's dialogs)
	// have frames, can be moved and closed, and can be switched to from a taskbar. It runs with its own defaults.
	var wm *exec.Cmd
	var wmdone <-chan struct{}
	if p, args, ok := windowManager(); ok {
		wm = exec.Command(p, args...)
		wm.Env, wm.Stdout, wm.Stderr = env, os.Stderr, os.Stderr
		if filepath.Base(p) == "icewm" {
			if dir := iceWMConfigDir(); dir != "" {
				wm.Env = append(slices.Clone(env), "ICEWM_PRIVCFG="+dir)
			}
		}
		if wmdone, err = startWatched(wm); err != nil {
			wm = nil
		}
	}

	app := exec.Command(bin)
	if a.Name == "console" {
		self, err := os.Executable()
		if err != nil {
			return err
		}
		app = exec.Command(bin, "-fa", "Monospace", "-fs", "12", "-e", "/bin/sh", "-lc", `"$1"; exec /bin/sh`, "hamstack-console", self)
	}
	app.Env, app.Stdout, app.Stderr = env, os.Stderr, os.Stderr
	if home, err := os.UserHomeDir(); err == nil {
		app.Dir = home
	}
	fmt.Fprintf(os.Stderr, "hamstack: %s on display :%d (VNC port %d, localhost only)\n", a.Title, a.Display(), a.Port())
	appdone, err := startWatched(app)
	if err != nil {
		stop(x, xdone, 3*time.Second)
		restart()
		return err
	}
	if a.Radio {
		stopWatch := radioAppWatch(cfg, a, os.Stderr)
		defer stopWatch()
	}
	if a.Name == "flrig" {
		if note := flrigNote(cfg); note != "" {
			fmt.Fprintln(os.Stderr, "hamstack: "+note)
		}
		go flrigTakesOver(cfg, os.Stderr, 30*time.Second)
	}
	select {
	case <-appdone:
	case <-xdone: // the display went away: nothing to show the program on
	case <-sig:
	}
	stop(app, appdone, 8*time.Second)
	if wm != nil {
		stop(wm, wmdone, 2*time.Second)
	}
	stop(x, xdone, 3*time.Second)
	restart()
	return nil
}

// waylandDisplay finds the computer's Wayland desktop session, or "" when nobody is logged in to one.
func waylandDisplay() string {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = "/run/user/" + strconv.Itoa(os.Getuid())
	}
	if w := os.Getenv("WAYLAND_DISPLAY"); w != "" {
		if _, err := os.Stat(filepath.Join(dir, w)); err == nil {
			return w
		}
	}
	m, _ := filepath.Glob(filepath.Join(dir, "wayland-[0-9]"))
	for _, p := range m {
		if fi, err := os.Stat(p); err == nil && fi.Mode()&os.ModeSocket != 0 {
			return filepath.Base(p)
		}
	}
	return ""
}

// runDesktop is what hamstack-desktop runs: a wayvnc of its own sharing the desktop on localhost, for the
// dashboard's relay. The computer's own VNC server (if any) is left alone.
func runDesktop() error {
	bin, reason := desktopCapability()
	if bin == "" {
		return fmt.Errorf("desktop sharing unavailable: %s", reason)
	}
	if bin == "x0vncserver" {
		path, err := desktopPath(bin)
		if err != nil {
			return fmt.Errorf("x0vncserver is not installed (hamstack dashboard on --desktop)")
		}
		return syscall.Exec(path, []string{bin, "-display", os.Getenv("DISPLAY"), "-localhost", "-SecurityTypes", "None", "-rfbport", strconv.Itoa(apps.DesktopPort)}, os.Environ())
	}
	w := waylandDisplay()
	if w == "" {
		fmt.Fprintln(os.Stderr, "hamstack: no Wayland desktop session is running for this user")
		os.Exit(exitRadioMissing)
	}
	path, err := versions.Which("wayvnc")
	if err != nil {
		return fmt.Errorf("wayvnc is not installed")
	}
	if err := os.MkdirAll(stateDir(), 0o755); err != nil {
		return err
	}
	conf := filepath.Join(stateDir(), "wayvnc-dashboard.conf")
	body := fmt.Sprintf("# Written by hamstack: the desktop for the dashboard, on localhost only.\naddress=127.0.0.1\nport=%d\nenable_auth=false\n", apps.DesktopPort)
	if err := os.WriteFile(conf, []byte(body), 0o644); err != nil {
		return err
	}
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = "/run/user/" + strconv.Itoa(os.Getuid())
	}
	argv := []string{"wayvnc", "-C", conf, "-S", filepath.Join(dir, "hamstack-wayvnc.sock"), "127.0.0.1", strconv.Itoa(apps.DesktopPort)}
	env := append(os.Environ(), "WAYLAND_DISPLAY="+w)
	fmt.Fprintf(os.Stderr, "hamstack: sharing %s on 127.0.0.1:%d\n", w, apps.DesktopPort)
	return syscall.Exec(path, argv, env)
}

// windowManager finds the window manager to run on an app's virtual display. IceWM is the one hamstack installs: it is in
// the official repositories of Arch, Debian and Fedora alike and works as it is, with a taskbar listing every window.
// Only the window manager itself is started (never icewm-session, which would also run the person's own startup
// script on every virtual display). A computer that already has matchbox, which hamstack used before, keeps using it.
func windowManager() (path string, args []string, ok bool) {
	if p, err := desktopPath("icewm"); err == nil {
		return p, nil, true
	}
	if p, err := desktopPath("matchbox-window-manager"); err == nil {
		return p, []string{"-use_titlebar", "no"}, true
	}
	return "", nil, false
}

// iceWMTheme is the look hamstack gives its app displays. It is one of the themes the IceWM package itself ships.
const iceWMTheme = "Infadel2"

var iceWMThemeDirs = []string{"/usr/share/icewm/themes", "/usr/local/share/icewm/themes"}

// iceWMConfigDir is a folder inside hamstack's own data holding the one setting (the theme), which IceWM is pointed at
// with ICEWM_PRIVCFG. The person's own ~/.icewm is never read or written, and removing hamstack's data removes this.
// It is empty when this IceWM does not ship the theme, and IceWM then runs with its defaults.
func iceWMConfigDir() string {
	found := false
	for _, d := range iceWMThemeDirs {
		if _, err := os.Stat(filepath.Join(d, iceWMTheme, "default.theme")); err == nil {
			found = true
		}
	}
	home, err := os.UserHomeDir()
	if !found || err != nil {
		return ""
	}
	dir := filepath.Join(filepath.Dir(ledger.Path(home)), "icewm")
	want := "Theme=\"" + iceWMTheme + "/default.theme\"\n"
	if have, err := os.ReadFile(filepath.Join(dir, "theme")); err == nil && string(have) == want {
		return dir
	}
	if os.MkdirAll(dir, 0o755) != nil || os.WriteFile(filepath.Join(dir, "theme"), []byte(want), 0o644) != nil {
		return ""
	}
	return dir
}
