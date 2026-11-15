package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ATCUSA/hamstack/internal/apps"
	"github.com/ATCUSA/hamstack/internal/config"
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

// hfModemUnits are the running HF modems (Mercury or Data2G) of the active rigs: the programs that hold the
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
		return fmt.Errorf("Xvnc is not installed (hamstack web on installs it)")
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

	// A window manager that keeps the program full size, if there is one: without it Qt windows have no frame
	// and open at their own size.
	var wm *exec.Cmd
	var wmdone <-chan struct{}
	if p, err := versions.Which("matchbox-window-manager"); err == nil {
		wm = exec.Command(p, "-use_titlebar", "no")
		wm.Env, wm.Stdout, wm.Stderr = env, os.Stderr, os.Stderr
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
			return fmt.Errorf("x0vncserver is not installed (hamstack web on --desktop)")
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
