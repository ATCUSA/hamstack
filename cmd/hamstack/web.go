package main

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"

	"github.com/ATCUSA/hamstack/internal/apps"
	"github.com/ATCUSA/hamstack/internal/catalog"
	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/distro"
	"github.com/ATCUSA/hamstack/internal/install"
	"github.com/ATCUSA/hamstack/internal/ledger"
	"github.com/ATCUSA/hamstack/internal/netinfo"
	"github.com/ATCUSA/hamstack/internal/svc"
	"github.com/ATCUSA/hamstack/internal/units"
	"github.com/ATCUSA/hamstack/internal/updates"
	"github.com/ATCUSA/hamstack/internal/versions"
	"github.com/ATCUSA/hamstack/internal/web"
)

const webUsage = `usage:
  hamstack dashboard                          dashboard state and address
  hamstack dashboard on [--port N] [--cert FILE --key FILE] [--yes]
                                        set up the dashboard (installs Xvnc and noVNC; asks before using sudo)
  hamstack dashboard check                    check browser/app/desktop compatibility
  hamstack dashboard on --desktop             also install whole-desktop sharing
  hamstack dashboard password                 set the dashboard password (asked on the terminal)
  hamstack dashboard off                      stop the dashboard and the apps opened from it
  hamstack app list|open APP|close APP  desktop programs in the browser
  hamstack app repair                  fix programs hamstack built that crash for want of an executable stack (WSJT-X's jt9)`

// noVNCDirs are where distributions put noVNC's web files.
var noVNCDirs = []string{"/usr/share/novnc", "/usr/share/webapps/novnc"}

func noVNCDir() string {
	for _, d := range noVNCDirs {
		if _, err := os.Stat(filepath.Join(d, "vnc.html")); err == nil {
			return d
		}
	}
	return ""
}

func webPasswordFile() (string, error) {
	dir, err := config.Dir()
	return filepath.Join(dir, "web-password"), err
}

// webCert returns the certificate and key the dashboard uses: the configured ones, or a self-signed pair hamstack
// makes once (for this computer's names and addresses).
func webCert(w config.WebSettings) (cert, key string, err error) {
	if w.Cert != "" {
		return w.Cert, w.Key, nil
	}
	dir, err := config.Dir()
	if err != nil {
		return "", "", err
	}
	cert, key = filepath.Join(dir, "web", "cert.pem"), filepath.Join(dir, "web", "key.pem")
	if _, err := os.Stat(cert); err == nil {
		return cert, key, nil
	}
	return cert, key, selfSigned(cert, key)
}

func selfSigned(certFile, keyFile string) error {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	host, _ := os.Hostname()
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host, host + ".local", "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(10, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, ip := range append(netinfo.LANAddrs(), "127.0.0.1") {
		tpl.IPAddresses = append(tpl.IPAddresses, net.ParseIP(ip))
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	if err != nil {
		return err
	}
	kb, err := x509.MarshalECPrivateKey(k)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(certFile), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600); err != nil {
		return err
	}
	return os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
}

func cmdWeb(args []string) error {
	if len(args) == 0 {
		return webShow()
	}
	switch args[0] {
	case "on":
		return webOn(args[1:])
	case "console":
		if len(args) != 2 || (args[1] != "on" && args[1] != "off") {
			return fmt.Errorf("usage: hamstack dashboard console on|off (on gives dashboard users full terminal access)")
		}
		if args[1] == "on" && !(&asker{in: bufioStdin(), out: os.Stdout}).confirm("Allow logged-in dashboard users full terminal control as your OS user?", false) {
			return fmt.Errorf("cancelled")
		}
		msg, err := (webBackend{}).Manage(web.ManagementRequest{Action: "dashboard.console", Value: args[1], Confirm: true})
		if msg != nil {
			fmt.Println(msg)
		}
		return err
	case "tune":
		return webTune(args[1:])
	case "check":
		printRemoteChecks()
		return nil
	case "repair": // install what the dashboard needs and an update added, or that was removed since
		steps, err := remoteInstallSteps(false)
		if err != nil {
			return err
		}
		if len(steps) == 0 {
			fmt.Println("Everything the dashboard needs is installed.")
			return nil
		}
		if err := install.Run(steps, install.Options{Yes: slices.Contains(args[1:], "--yes")}); err != nil {
			return err
		}
		fmt.Println("Programs already open keep the setup they started with: close and open them again to use what was just installed.")
		return nil
	case "off":
		return webOff()
	case "password":
		if slices.Contains(args[1:], "--stdin") {
			return webPasswordFrom(os.Stdin)
		}
		return webPassword()
	case "serve": // run by the hamstack-web unit
		return webServe()
	}
	return fmt.Errorf("%s", webUsage)
}

// webURL is the dashboard's address: the name its certificate is for when it has one (so the browser trusts
// it), otherwise this computer's LAN address.
func webURL(w config.WebSettings) string {
	host, _ := os.Hostname()
	if w.Access == config.AccessLocal {
		host = "localhost"
	} else if ips := netinfo.LANAddrs(); len(ips) > 0 {
		host = ips[0]
	}
	if w.Cert != "" {
		if b, err := os.ReadFile(w.Cert); err == nil {
			if blk, _ := pem.Decode(b); blk != nil {
				if c, err := x509.ParseCertificate(blk.Bytes); err == nil && len(c.DNSNames) > 0 {
					host = c.DNSNames[0]
				}
			}
		}
	}
	if p := w.ListenPort(); p != 443 {
		host += ":" + strconv.Itoa(p)
	}
	return "https://" + host + "/"
}

func webShow() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.Web == nil {
		fmt.Println("The web dashboard is not set up: hamstack dashboard on")
		return nil
	}
	w := *cfg.Web
	w.Access = cfg.NetworkAccess("web", w.EffectiveAccess())
	state := "on"
	if w.Off {
		state = "off"
	}
	cert, _, _ := webCert(w)
	pw := "set"
	if f, err := webPasswordFile(); err != nil || !fileExists(f) {
		pw = "NOT SET (nobody can log in): hamstack dashboard password"
	}
	fmt.Printf("dashboard:   %s, %s\nservice:     %s\ncertificate: %s\npassword:    %s\nnoVNC:       %s\n",
		state, webURL(w), svc.Status(units.Service{Kind: config.Web}.Unit()).Display(), cert, pw, orDash(noVNCDir()))
	return nil
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

// webPackages are the distribution packages the dashboard needs that are missing here: Xvnc (each app's
// display), noVNC (the browser side) and IceWM (a window manager, so a program's extra windows can be seen and switched to).
func webPackages(fam distro.Family) []string {
	names := map[string]map[distro.Family]string{
		"Xvnc":  {distro.Debian: "tigervnc-standalone-server", distro.Arch: "tigervnc", distro.Fedora: "tigervnc-server-minimal"},
		"novnc": {distro.Debian: "novnc", distro.Arch: "novnc", distro.Fedora: "novnc"},
		"icewm": {distro.Debian: "icewm", distro.Arch: "icewm", distro.Fedora: "icewm"},
	}
	var out []string
	for _, what := range []string{"Xvnc", "novnc", "icewm"} {
		have := false
		if what == "novnc" {
			have = noVNCDir() != ""
		} else {
			_, err := desktopPath(what)
			have = err == nil
		}
		if pkg := names[what][fam]; !have && pkg != "" {
			out = append(out, pkg)
		}
	}
	return out
}

// unprivilegedPortStart is the lowest port an ordinary user may listen on.
func unprivilegedPortStart() int {
	b, err := os.ReadFile("/proc/sys/net/ipv4/ip_unprivileged_port_start")
	if err != nil {
		return 1024
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 1024
	}
	return n
}

func webOn(args []string) error {
	fs := flag.NewFlagSet("web on", flag.ContinueOnError)
	port := fs.Int("port", 0, "port (default 443)")
	access := fs.String("access", "", "local, network or all (default local)")
	cert := fs.String("cert", "", "TLS certificate file (PEM)")
	key := fs.String("key", "", "its private key")
	yes := fs.Bool("yes", false, "do not ask before installing")
	desktop := fs.Bool("desktop", false, "also install whole-desktop sharing for this graphical session")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	w := config.WebSettings{Port: *port, Cert: *cert, Key: *key, Access: *access}
	if w.Access == "" {
		w.Access = config.AccessLocal
		if cfg.Web != nil {
			w.Access = cfg.Web.EffectiveAccess()
		}
		if w.Access == "" {
			w.Access = config.AccessLocal
		}
	}
	if cfg.Web != nil { // keep what was set before unless given again
		w.Console = cfg.Web.Console
		w.RigTune = cfg.Web.RigTune
		if *port == 0 {
			w.Port = cfg.Web.Port
		}
		if *cert == "" {
			w.Cert, w.Key = cfg.Web.Cert, cfg.Web.Key
		}
	}
	for _, f := range []string{w.Cert, w.Key} {
		if f != "" && !fileExists(f) {
			return fmt.Errorf("%s does not exist", f)
		}
	}
	if w.Cert != "" {
		w.Cert, _ = filepath.Abs(w.Cert)
		w.Key, _ = filepath.Abs(w.Key)
	}
	if err := cfg.SetWeb(w); err != nil {
		return err
	}
	if *access != "" {
		if err := cfg.SetNetworkAccess("web", w.Access); err != nil {
			return err
		}
	}

	env := install.HostEnv()
	printRemoteChecks()
	steps, err := remoteInstallSteps(*desktop)
	if err != nil {
		return err
	}

	if p := w.ListenPort(); p < unprivilegedPortStart() {
		// User services cannot be given the right to a low port; lowering the limit lets one listen on it.
		v := strconv.Itoa(p)
		s, err := install.RootFileStepFor(env, ledger.Support, "web", "Let user services listen on port "+v+" and up (lasts across reboots)",
			"/etc/sysctl.d/60-hamstack-web.conf", "# Written by hamstack: lets its web dashboard (a user service) listen on port "+v+".\nnet.ipv4.ip_unprivileged_port_start = "+v+"\n")
		if err != nil {
			return err
		}
		cmd, err := env.Priv("sysctl", "-w", "net.ipv4.ip_unprivileged_port_start="+v)
		if err != nil {
			return err
		}
		prev := strconv.Itoa(unprivilegedPortStart())
		steps = append(steps, s, install.Step{Desc: "Apply it now", Cmd: cmd, Track: func() func() []ledger.Change {
			return func() []ledger.Change {
				return []ledger.Change{{Kind: ledger.Sysctl, Role: ledger.Support, For: "web", Key: "net.ipv4.ip_unprivileged_port_start", Prev: prev}}
			}
		}})
	}
	if len(steps) > 0 {
		if err := install.Run(steps, install.Options{Yes: *yes}); err != nil {
			return err
		}
	}
	if _, _, err := webCert(w); err != nil {
		return err
	}
	if err := config.Save(cfg); err != nil {
		return err
	}
	if err := svc.Available(); err != nil {
		return err
	}
	p, warns, err := buildPlan()
	for _, m := range warns {
		fmt.Fprintln(os.Stderr, "warning:", m)
	}
	if err != nil {
		return err
	}
	if _, err := writeUnits(p); err != nil {
		return err
	}
	u := units.Service{Kind: config.Web}.Unit()
	if err := svc.Run("enable", u); err != nil {
		return err
	}
	if err := svc.Run("restart", u); err != nil {
		return err
	}
	fmt.Printf("dashboard: %s (starts at login; with linger on, at boot)\n", webURL(w))
	if f, err := webPasswordFile(); err == nil && !fileExists(f) {
		fmt.Println("No password yet, so nobody can log in.")
		if isTerminal() && stdinIsTerminal() {
			return webPassword()
		}
		fmt.Println("Set one with: hamstack dashboard password")
	}
	return nil
}

func webOff() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.Web == nil {
		fmt.Println("The web dashboard is not set up.")
		return nil
	}
	if svc.Available() == nil {
		for _, a := range apps.All {
			if svc.Status(vncUnit(a.Name)).Active == "active" {
				_ = svc.Run("stop", vncUnit(a.Name))
			}
		}
		_ = svc.Run("disable", "--now", desktopUnit())
		_ = svc.Run("disable", "--now", units.Service{Kind: config.Web}.Unit())
	}
	cfg.Web.Off = true
	if err := config.Save(cfg); err != nil {
		return err
	}
	if p, _, err := buildPlan(); err == nil {
		_, _ = writeUnits(p)
	}
	fmt.Println("dashboard off (hamstack dashboard on starts it again with the same settings)")
	return nil
}

// webPasswordFrom sets the dashboard password from the first line of r (hamstack web password --stdin): for a script
// or a password manager, so it never sits in a command line or the shell's history.
func webPasswordFrom(r io.Reader) error {
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && line == "" {
		return fmt.Errorf("no password on standard input")
	}
	pw := strings.TrimRight(line, "\r\n")
	if len(pw) < 8 {
		return fmt.Errorf("use at least 8 characters: the dashboard can key your transmitter")
	}
	return saveWebPassword(pw)
}

func saveWebPassword(pw string) error {
	hash, err := web.HashPassword(pw)
	if err != nil {
		return err
	}
	f, err := webPasswordFile()
	if err != nil {
		return err
	}
	if err := web.SavePasswordHash(f, hash); err != nil {
		return err
	}
	fmt.Println("password saved (stored hashed; the running dashboard uses it right away)")
	return nil
}

func webPassword() error {
	if !stdinIsTerminal() {
		return fmt.Errorf("the password is asked for on the terminal: run this in one")
	}
	read := func(prompt string) (string, error) {
		fmt.Print(prompt)
		b, err := term.ReadPassword(os.Stdin.Fd())
		fmt.Println()
		return string(b), err
	}
	pw, err := read("New dashboard password: ")
	if err != nil {
		return err
	}
	if len(pw) < 8 {
		return fmt.Errorf("use at least 8 characters: the dashboard can key your transmitter")
	}
	again, err := read("Again: ")
	if err != nil {
		return err
	}
	if pw != again {
		return fmt.Errorf("the two did not match; nothing was changed")
	}
	return saveWebPassword(pw)
}

func webServe() error {
	if os.Getenv("HAMSTACK_NETWORK_CHILD") == "" {
		return watchNetwork([]string{"web", "serve"})
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.Web == nil {
		return fmt.Errorf("the web dashboard is not set up: hamstack dashboard on")
	}
	cert, key, err := webCert(*cfg.Web)
	if err != nil {
		return err
	}
	pwf, err := webPasswordFile()
	if err != nil {
		return err
	}
	updates.Start()
	go func() {
		ticker := time.NewTicker(updates.PollInterval)
		defer ticker.Stop()
		for range ticker.C {
			updates.Start()
		}
	}()
	srv := web.New(webBackend{}, web.Options{
		Addr: net.JoinHostPort(config.BindHost(cfg.NetworkAccess("web", cfg.Web.EffectiveAccess()), netinfo.LANAddrs()), strconv.Itoa(cfg.Web.ListenPort())), CertFile: cert, KeyFile: key, PasswordFile: pwf, SessionFile: filepath.Join(filepath.Dir(pwf), "web-sessions"), NoVNCDir: noVNCDir(), NoVNCPath: noVNCDir,
		Logf: func(f string, a ...any) { fmt.Fprintf(os.Stderr, "hamstack: "+f+"\n", a...) },
	})
	return srv.ListenAndServe()
}

// ---- the dashboard's view of hamstack ----

type webBackend struct{}

// serviceWhat says what a service is, for people.
func serviceWhat(s units.Service) string {
	switch s.Kind {
	case config.RigCtld:
		return "rig control"
	case config.Mercury:
		return "Mercury HF modem"
	case config.Data2G:
		return "Data2G HF modem"
	case config.Ardopcf:
		return "ARDOP HF modem (ardopcf)"
	case config.Xara:
		return "xara HF modem (VARA-compatible)"
	case config.Direwolf:
		return "Direwolf packet TNC"
	case config.Pat:
		return "Pat Winlink client"
	case config.GPSD:
		return "GPS"
	case config.Web:
		return "hamstack dashboard (this page)"
	}
	return s.Kind
}

func webState(st svc.State) string {
	switch {
	case st.Load == "not-found":
		return "not installed"
	case st.NoRadio():
		return "no radio"
	case st.Active == "active":
		return "running"
	case st.Active == "activating":
		return "starting"
	case st.Active == "deactivating":
		return "stopping"
	case st.Active == "failed":
		return "failed"
	}
	return "stopped"
}

func (webBackend) State() web.State {
	cfg, _ := config.Load()
	var out web.State
	out.BrowserReady = noVNCDir() != ""
	out.Updates = updates.Fresh()
	maintenance := maintenanceStatus()
	out.Pending, out.Notices, out.Reboot = maintenance.Services, maintenance.Reasons, maintenance.Reboot
	if st, ok := cfg.ActiveStation(); ok {
		out.Station = st.Callsign
	}
	if s, ok := cfg.Here(); ok {
		out.Site = s.ID
	}
	out.Grid = cfg.EffectiveGrid()
	for _, s := range units.ServicesFor(cfg) {
		st := svc.Status(s.Unit())
		row := web.Service{Name: s.Name(), What: serviceWhat(s), State: webState(st), Since: st.Since,
			Enabled: st.Enabled == "enabled", Control: true, Self: s.Kind == config.Web}
		switch s.Kind {
		case config.Pat:
			if a, ok := cfg.ActiveStation(); ok {
				_, p, _ := net.SplitHostPort(a.HTTPAddr)
				if cfg.PatRelayed() {
					n, _ := strconv.Atoi(p)
					row.LinkPort, row.Scheme = config.PatPagePort(n), "https"
				} else if cfg.PatAccess(a) == config.AccessLocal {
					row.Note = "web UI on the radio computer only (hamstack station access " + a.ID + " network)"
				} else {
					row.LinkPort, _ = strconv.Atoi(p)
					row.Scheme = "http"
				}
			}
		case config.Mercury:
			// Mercury's UI port is a WebSocket for the mercury-qt program, not a page a browser can show: no Open link.
			if r, ok := cfg.RigByIDValue(s.Rig); ok && cfg.UI {
				_, _, _, ui := config.MercuryPorts(r)
				row.Note = fmt.Sprintf("UI port %d for mercury-qt (not a web page)", ui)
			}
		case config.Ardopcf:
			// ardopcf has a web page of its own (spectrum, waterfall, levels), shown through the dashboard: say where.
			if r, ok := cfg.RigByIDValue(s.Rig); ok {
				switch {
				case !ardopScreenOn(r):
					row.Note = "its screen (spectrum, waterfall, levels) is off: turn it on from the Radio card on the Overview"
				case row.State == "running":
					row.Link = "/ardop/" + r.ID + "/"
				default:
					row.Note = "its screen shows here while it runs"
				}
			}
		case config.Xara:
			if r, ok := cfg.RigByIDValue(s.Rig); ok {
				switch {
				case !r.XaraScreenOn():
					row.Note = "its screen (link state, waterfall, levels) is off: turn it on from the Radio card"
				case row.State == "running":
					row.Link = "/xara/" + r.ID + "/"
				default:
					row.Note = "its screen shows here while it runs"
				}
			}
		case config.Data2G:
			if r, ok := cfg.RigByIDValue(s.Rig); ok && cfg.Data2GPageOn() {
				_, _, _, w := config.Data2GPorts(r)
				switch {
				case cfg.Data2GRelayed():
					row.LinkPort, row.Scheme = config.Data2GPagePort(r), "https"
				case cfg.NetworkAccess("data2g@"+r.ID+"/web", config.AccessAll) != config.AccessLocal:
					row.LinkPort, row.Scheme = w, "http"
				default:
					row.Note = "web UI is local to the radio computer"
				}
			}
		}
		out.Services = append(out.Services, row)
		if s.Rig != "" && s.Kind == func() string { r, _ := cfg.RigByIDValue(s.Rig); return r.Modem }() {
			if r, ok := cfg.RigByIDValue(s.Rig); ok {
				out.Services = append(out.Services, standbyRows(r)...) // right after the modem it would replace
			}
		}
	}
	for _, a := range apps.All {
		if a.Name == "console" && (cfg.Web == nil || !cfg.Web.Console) {
			continue
		}
		ast := svc.Status(vncUnit(a.Name))
		running := ast.Active == "active"
		row := web.App{Name: a.Name, Title: a.Title, Package: a.Package, Installed: a.Path() != "", Running: running, Enabled: ast.Enabled == "enabled", Radio: a.Radio, Kind: "browser"}
		if a.Name == "flrig" {
			row.Note = flrigNote(cfg)
		}
		out.Apps = append(out.Apps, row)
		if running && a.Radio {
			out.RadioUser = a.Title
		}
	}
	// Pat is an app like the others to the person using it, though it shows through its own web page and not
	// through the browser display the others use: it is on the Apps page too, first.
	patApp := web.App{Name: "pat", Title: "Pat (Winlink)", Package: "pat", Kind: "web"}
	if _, err := versions.Which("pat"); err == nil {
		patApp.Installed = true
	}
	for _, v := range out.Services {
		if v.Name == "pat" {
			patApp.Running, patApp.Enabled, patApp.LinkPort, patApp.Scheme, patApp.Note = v.State == "running", v.Enabled, v.LinkPort, v.Scheme, v.Note
		}
	}
	out.Apps = append([]web.App{patApp}, out.Apps...)
	if out.RadioUser == "" {
		for _, r := range cfg.ActiveRigs() {
			if config.IsHFModem(r.Modem) && svc.Status(units.Service{Kind: r.Modem, Rig: r.ID}.Unit()).Active == "active" {
				out.RadioUser = serviceWhat(units.Service{Kind: r.Modem}) + " (" + r.ID + ")"
				break
			}
		}
	}
	for _, c := range remoteChecks() {
		out.RemoteChecks = append(out.RemoteChecks, web.RemoteCheck{Name: c.Name, Ready: c.Ready, Detail: c.Detail})
	}
	for _, check := range out.RemoteChecks {
		if check.Name == "Whole desktop" {
			out.Desktop = check.Ready
			out.DesktopNote = check.Detail
		}
	}
	out.DesktopRunning = svc.Status(desktopUnit()).Active == "active"
	out.Unused = unusedPrograms(cfg)
	return out
}

func (webBackend) Control(name, verb string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	i := slices.IndexFunc(units.ServicesFor(cfg), func(s units.Service) bool { return s.Name() == name })
	if i < 0 {
		return fmt.Errorf("%q is not one of your services", name)
	}
	s := units.ServicesFor(cfg)[i]
	if s.Kind == config.Web {
		// It can restart itself (the page reloads when it is back), but not stop or disable itself from its own page.
		if verb != "restart" {
			return fmt.Errorf("the dashboard can only be restarted from its own page; to turn it off use: hamstack dashboard off")
		}
		if err := jobGuard("restarting the dashboard"); err != nil {
			return err
		}
		go func() { time.Sleep(time.Second); _ = svc.Run("restart", s.Unit()) }() // after this reply has gone out
		return nil
	}
	return (backend{}).Control(verb, s.Name())
}

func (webBackend) OpenApp(name string) error { return openApp(name) }

func openApp(name string) error {
	if name == "console" {
		cfg := loadCfg()
		if cfg.Web == nil || !cfg.Web.Console {
			return fmt.Errorf("browser console is not enabled")
		}
	}
	if !apps.ValidName(name) {
		return fmt.Errorf("no app %q", name)
	}
	a, ok := apps.Find(name)
	if !ok {
		return fmt.Errorf("no app %q", name)
	}
	if a.Path() == "" {
		return fmt.Errorf("%s is not installed (hamstack install %s)", a.Title, a.Package)
	}
	if a.Radio {
		if other, busy := radioApp(a.Name); busy {
			return fmt.Errorf("%s has the radio: close it first", other.Title)
		}
	}
	if err := svc.Run("start", vncUnit(a.Name)); err != nil {
		return err
	}
	if err := waitPort(a.Port(), 20*time.Second); err != nil {
		return fmt.Errorf("%s did not start (hamstack logs vnc@%s): %w", a.Title, a.Name, err)
	}
	return nil
}

func (webBackend) CloseApp(name string) error {
	if _, ok := apps.Find(name); !ok || !apps.ValidName(name) {
		return fmt.Errorf("no app %q", name)
	}
	return svc.Run("stop", vncUnit(name))
}

func (webBackend) OpenDesktop() error {
	bin, reason := desktopCapability()
	if bin == "" {
		return fmt.Errorf("%s", reason)
	}
	if _, err := desktopPath(bin); err != nil {
		return fmt.Errorf("%s is not installed: hamstack dashboard on --desktop", bin)
	}
	if err := svc.Run("start", desktopUnit()); err != nil {
		return err
	}
	if err := waitPort(apps.DesktopPort, 15*time.Second); err != nil {
		return fmt.Errorf("desktop sharing did not start (hamstack logs desktop): %w", err)
	}
	return nil
}

func (webBackend) CloseDesktop() error { return svc.Run("stop", desktopUnit()) }

func (webBackend) Target(name string) (int, bool) {
	if name == "console" {
		cfg := loadCfg()
		if cfg.Web == nil || !cfg.Web.Console {
			return 0, false
		}
	}
	port := apps.DesktopPort
	unit := desktopUnit()
	if name != "desktop" {
		a, ok := apps.Find(name)
		if !ok || !apps.ValidName(name) {
			return 0, false
		}
		port, unit = a.Port(), vncUnit(name)
	}
	if svc.Status(unit).Active != "active" {
		return 0, false
	}
	return port, portOpen(port)
}

// portOpen reports whether something accepts connections on a localhost port right now.
func portOpen(port int) bool {
	c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 300*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// ---- hamstack app ----

func cmdApp(args []string) error {
	if len(args) == 0 || args[0] == "list" {
		for _, a := range apps.All {
			state := "not installed"
			if a.Path() != "" {
				state = "installed"
			}
			if svc.Status(vncUnit(a.Name)).Active == "active" {
				state = "open"
			}
			radio := ""
			if a.Radio {
				radio = "  (uses the radio)"
			}
			fmt.Printf("%-8s %-8s %s%s\n", a.Name, a.Title, state, radio)
		}
		return nil
	}
	if args[0] == "repair" && len(args) == 1 {
		return appRepair()
	}
	browser := len(args) == 3 && args[2] == "--browser"
	if len(args) != 2 && !browser {
		return fmt.Errorf("%s", webUsage)
	}
	switch args[0] {
	case "open":
		if browser && !loadCfg().RunsWeb() {
			return fmt.Errorf("enable HTTPS browser access first: hamstack dashboard on")
		}
		if err := openApp(args[1]); err != nil {
			return err
		}
		if browser {
			cfg := loadCfg()
			w := *cfg.Web
			w.Access = cfg.NetworkAccess("web", w.EffectiveAccess())
			url := webURL(w) + "vnc/" + args[1]
			fmt.Println("Browser VNC:", url)
			if os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != "" {
				cmd := exec.Command("xdg-open", url)
				if err := cmd.Start(); err != nil {
					return err
				}
				go cmd.Wait()
			}
			return nil
		}
		fmt.Printf("%s is open: on the dashboard, or any VNC viewer through an SSH tunnel to localhost:%d\n", args[1], func() int { a, _ := apps.Find(args[1]); return a.Port() }())
		return nil
	case "close":
		return webBackend{}.CloseApp(args[1])
	}
	return fmt.Errorf("%s", webUsage)
}

// unusedPrograms finds installed modems that no rig runs. Services come from the configuration, so without this an
// installed program that is not configured would show nowhere: neither as an app nor as a service.
func unusedPrograms(cfg config.Config) []web.Unused {
	var out []web.Unused
	for _, kind := range []string{config.Mercury, config.Data2G, config.Ardopcf, config.Xara, config.Direwolf} {
		if cfg.Has(kind) || slices.ContainsFunc(cfg.ActiveRigs(), func(r config.Rig) bool { return slices.Contains(r.Alt, kind) }) {
			continue // running, or set up on a rig to switch to
		}
		if _, err := versions.Which(kind); err != nil {
			continue
		}
		p, _ := catalog.ByName(kind)
		u := web.Unused{Name: kind, Desc: p.Desc, Kind: "modem"}
		for _, r := range cfg.ActiveRigs() {
			switch {
			case kind != config.Direwolf && r.NoCAT: // an HF modem is keyed through rigctld
			case r.Modem == "":
				u.Rigs = append(u.Rigs, r.ID)
			case config.IsHFModem(kind) && config.IsHFModem(r.Modem): // one HF modem for another, on the same radio
				u.Switch = append(u.Switch, web.UnusedSwitch{Rig: r.ID, From: r.Modem, Alt: r.Alt})
			}
		}
		if len(u.Rigs) == 0 && len(u.Switch) == 0 {
			u.Note = "Every rig already has a modem (or no rig can key it). Change a rig's modem in Settings, Rigs."
		}
		out = append(out, u)
	}
	return out
}

// standbyRows are the HF modems a rig can switch to but does not run now: shown next to the running one, so they do
// not disappear from the dashboard once the rig switches away from them.
func standbyRows(r config.Rig) []web.Service {
	var out []web.Service
	for _, m := range r.HFModems() {
		if m == r.Modem {
			continue
		}
		s := units.Service{Kind: m, Rig: r.ID}
		out = append(out, web.Service{Name: s.Name(), What: serviceWhat(s), State: "standby",
			Note: "set up on " + r.ID + ", which runs " + orDash(r.Modem) + " now", SwitchRig: r.ID, SwitchFrom: r.Modem})
	}
	return out
}
