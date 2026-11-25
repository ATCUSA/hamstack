// Package web is hamstack's browser dashboard: the services and their state, controls, links to the programs'
// own web UIs, and desktop programs or the whole desktop in the browser through noVNC. The dashboard is the
// only way in: the VNC servers listen on localhost, and the dashboard relays a browser's WebSocket to them
// only after a login.
package web

import (
	"crypto/rand"
	"crypto/tls"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ATCUSA/hamstack/internal/updates"
	"golang.org/x/net/websocket"
)

// Service is one row of the services table.
type Service struct {
	Name     string `json:"name"`  // "rigctld@hf", "pat"
	What     string `json:"what"`  // "rig control"
	State    string `json:"state"` // "running", "stopped", "failed", "no radio", "starting", "not installed"
	Since    string `json:"since,omitempty"`
	Enabled  bool   `json:"enabled"`            // starts at login/boot
	Control  bool   `json:"control"`            // the dashboard may start and stop it
	Self     bool   `json:"self,omitempty"`     // it is the dashboard itself: it can only be restarted from here
	LinkPort int    `json:"linkPort,omitempty"` // its own web UI, on this computer, if any
	Scheme   string `json:"scheme,omitempty"`   // "http" or "https"
	Note     string `json:"note,omitempty"`     // for instance "web UI on this computer only"
}

// App is one desktop program the dashboard can open in the browser.
type App struct {
	Name      string `json:"name"`
	Title     string `json:"title"`
	Package   string `json:"package"`
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
	Enabled   bool   `json:"enabled"` // it starts at login
	Radio     bool   `json:"radio"`   // uses the radio's sound card
	Kind      string `json:"kind"`    // browser: a program shown through the browser; web: a program with a web page of its own
	LinkPort  int    `json:"linkPort,omitempty"`
	Scheme    string `json:"scheme,omitempty"`
	Note      string `json:"note,omitempty"`
}

type RemoteCheck struct {
	Name   string `json:"name"`
	Ready  bool   `json:"ready"`
	Detail string `json:"detail"`
}

// State is what the dashboard shows.
type State struct {
	BrowserReady   bool             `json:"browserReady"`
	Updates        updates.Snapshot `json:"updates"`
	Pending        []string         `json:"pending"`
	Notices        []string         `json:"notices"`
	Reboot         bool             `json:"reboot"`
	RemoteChecks   []RemoteCheck    `json:"remoteChecks"`
	DesktopNote    string           `json:"desktopNote"`
	Station        string           `json:"station"`
	Site           string           `json:"site"`
	Grid           string           `json:"grid"`
	RadioUser      string           `json:"radioUser,omitempty"` // the program that has the radio's sound card now
	Services       []Service        `json:"services"`
	Apps           []App            `json:"apps"`
	Unused         []Unused         `json:"unused,omitempty"` // installed programs that run as a service but that no rig uses yet
	Desktop        bool             `json:"desktop"`          // the computer's desktop can be shared
	DesktopRunning bool             `json:"desktopRunning"`
}

// Unused is a program that is installed and could run as a service, but nothing is set up to run it: it would
// otherwise be invisible. Rigs are the rigs it could be set up on.
type Unused struct {
	Name string   `json:"name"`
	Desc string   `json:"desc"`
	Kind string   `json:"kind"` // what setting it up means: "modem"
	Rigs []string `json:"rigs"` // rigs that have no modem yet and could use it
	Note string   `json:"note,omitempty"`
}

// Backend is what the dashboard needs from hamstack.
type Backend interface {
	State() State
	Control(service, verb string) error // verb: start, stop, restart
	OpenApp(name string) error          // start the program on its display (stopping the HF modem first if it needs the radio)
	CloseApp(name string) error
	OpenDesktop() error
	CloseDesktop() error
	Target(name string) (port int, ok bool) // the localhost VNC port of a running program, or of "desktop"
}

// Options configure the server.
type Options struct {
	Addr         string // ":443"
	CertFile     string
	KeyFile      string
	PasswordFile string
	SessionFile  string        // where logins are kept so they survive a restart; empty keeps them in memory only
	NoVNCPath    func() string // dynamically resolve assets after installing them
	NoVNCDir     string        // noVNC's web files; "" if noVNC is not installed
	Logf         func(format string, args ...any)
}

// Server is the dashboard.
type Server struct {
	be  Backend
	opt Options

	mu       sync.Mutex
	sessions map[string]time.Time // hash of the login cookie -> expiry
	lastSave time.Time
	pwPrint  string    // fingerprint of the password the logins were made under
	pwCheck  time.Time // when it was last compared
	failures map[string][]time.Time
}

const (
	cookieName   = "hamstack_session"
	sessionTTL   = 12 * time.Hour
	failWindow   = 10 * time.Minute
	failLimit    = 5
	lockDuration = 5 * time.Minute
)

//go:embed page/*
var pages embed.FS

//go:embed assets/*
var assets embed.FS

// LogoSVG is hamstack's logo, for the desktop launchers as well as the dashboard.
func LogoSVG() []byte { b, _ := assets.ReadFile("assets/logo.svg"); return b }

// icon serves one of the logo files. They need no login: the browser asks for them on the login page too, and
// there is nothing in them to protect.
func icon(file, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		b, err := assets.ReadFile("assets/" + file)
		if err != nil {
			http.NotFound(w, nil)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "public, max-age=86400")
		_, _ = w.Write(b)
	}
}

var tmpl = template.Must(template.ParseFS(pages, "page/*.html"))

// New makes a dashboard server.
func New(be Backend, o Options) *Server {
	if o.Logf == nil {
		o.Logf = log.Printf
	}
	s := &Server{be: be, opt: o, sessions: map[string]time.Time{}, failures: map[string][]time.Time{}}
	s.loadSessions()
	s.pwPrint = s.passwordPrint()
	return s
}

// Handler is the dashboard's HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /logo.svg", icon("logo.svg", "image/svg+xml"))
	mux.HandleFunc("GET /favicon.svg", icon("logo.svg", "image/svg+xml"))
	mux.HandleFunc("GET /favicon.ico", icon("favicon.ico", "image/x-icon"))
	mux.HandleFunc("GET /apple-touch-icon.png", icon("apple-touch-icon.png", "image/png"))
	mux.HandleFunc("GET /login", s.loginPage)
	mux.HandleFunc("POST /login", s.login)
	mux.HandleFunc("POST /logout", s.logout)
	for _, p := range pagesList {
		path := "GET " + p.Path
		if p.Path == "/" {
			path = "GET /{$}"
		}
		mux.HandleFunc(path, s.auth(s.page(p)))
	}
	mux.HandleFunc("GET /api/state", s.auth(s.state))
	mux.HandleFunc("GET /api/manage", s.auth(s.management))
	mux.HandleFunc("POST /api/manage", s.auth(s.action(s.management)))
	mux.HandleFunc("POST /api/service/{name}/{verb}", s.auth(s.action(s.control)))
	mux.HandleFunc("POST /api/app/{name}/close", s.auth(s.action(s.closeApp)))
	mux.HandleFunc("POST /api/app/{name}/{verb}", s.auth(s.action(s.appVerb)))
	mux.HandleFunc("GET /apps/{name}", s.auth(s.appPageView))
	mux.HandleFunc("GET /api/apps/{name}", s.auth(s.appPageData))
	mux.HandleFunc("GET /api/jobs", s.auth(s.jobList))
	mux.HandleFunc("GET /api/jobs/{id}", s.auth(s.jobGet))
	mux.HandleFunc("POST /api/jobs/{id}/cancel", s.auth(s.action(s.jobCancel)))
	mux.HandleFunc("GET /api/logs/{name}", s.auth(s.logsGet))
	mux.HandleFunc("POST /api/desktop/close", s.auth(s.action(s.closeDesktop)))
	mux.HandleFunc("GET /api/rigs", s.auth(s.rigStates))
	mux.HandleFunc("POST /api/rig/{id}/set", s.auth(s.action(s.rigSet)))
	mux.HandleFunc("POST /api/rig/{id}/tune", s.auth(s.action(s.rigTune)))
	mux.HandleFunc("POST /api/rig/{id}/abort", s.auth(s.action(s.rigAbort)))
	mux.HandleFunc("GET /vnc/{name}", s.auth(s.vnc))
	mux.HandleFunc("GET /ws/{name}", s.auth(s.ws))
	mux.HandleFunc("GET /novnc/", s.auth(func(w http.ResponseWriter, r *http.Request) {
		dir := s.noVNCDir()
		if dir == "" {
			http.NotFound(w, r)
			return
		}
		http.StripPrefix("/novnc/", http.FileServer(http.Dir(dir))).ServeHTTP(w, r)
	}))

	return securityHeaders(mux)
}

// ListenAndServe serves the dashboard over HTTPS. A renewed certificate is picked up on the next connection,
// with no restart.
func (s *Server) ListenAndServe() error {
	cl := &certLoader{cert: s.opt.CertFile, key: s.opt.KeyFile}
	if _, err := cl.get(); err != nil {
		return err
	}
	srv := &http.Server{
		Addr:              s.opt.Addr,
		Handler:           s.Handler(),
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return cl.get() }},
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          log.New(io.Discard, "", 0), // TLS handshake noise from scanners and browsers that distrust the cert
	}
	s.opt.Logf("dashboard on https://%s", s.opt.Addr)
	return srv.ListenAndServeTLS("", "")
}

// certLoader reloads the certificate when its files change (step ca renew, certbot...).
type certLoader struct {
	cert, key string
	mu        sync.Mutex
	loaded    *tls.Certificate
	mtime     time.Time
}

func (c *certLoader) get() (*tls.Certificate, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fi, err := os.Stat(c.cert)
	if err != nil {
		if c.loaded != nil {
			return c.loaded, nil
		}
		return nil, err
	}
	if c.loaded == nil || !fi.ModTime().Equal(c.mtime) {
		crt, err := tls.LoadX509KeyPair(c.cert, c.key)
		if err != nil {
			if c.loaded != nil {
				return c.loaded, nil // mid-renewal: keep serving the old one
			}
			return nil, err
		}
		c.loaded, c.mtime = &crt, fi.ModTime()
	}
	return c.loaded, nil
}

func securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		// noVNC is shown in its own tab; nothing should frame the dashboard.
		w.Header().Set("X-Frame-Options", "DENY")
		h.ServeHTTP(w, r)
	})
}

// ---- sessions and login ----

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) loggedIn(r *http.Request) bool {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if time.Since(s.pwCheck) > 5*time.Second { // a changed password ends every login, at once
		s.pwCheck = time.Now()
		if p := s.passwordPrint(); p != s.pwPrint {
			s.pwPrint = p
			s.sessions = map[string]time.Time{}
			s.saveSessionsLocked()
			return false
		}
	}
	key := hashToken(c.Value)
	exp, ok := s.sessions[key]
	if !ok || time.Now().After(exp) {
		delete(s.sessions, key)
		return false
	}
	s.sessions[key] = time.Now().Add(sessionTTL) // sliding
	if time.Since(s.lastSave) > saveEvery {
		s.saveSessionsLocked()
	}
	return true
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.loggedIn(r) {
			next(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/ws/") {
			http.Error(w, "not logged in", http.StatusUnauthorized)
			return
		}
		http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
	}
}

// sameOrigin rejects requests made by other sites. The session cookie is SameSite=Strict already; this also
// covers browsers that send no SameSite information.
func sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return r.Header.Get("Sec-Fetch-Site") == "" || r.Header.Get("Sec-Fetch-Site") == "same-origin" || r.Header.Get("Sec-Fetch-Site") == "none"
	}
	u, err := url.Parse(o)
	return err == nil && u.Host == r.Host
}

// action guards a state-changing API call: same origin, and the header the dashboard's script sends (a form
// on another site cannot send it).
func (s *Server) action(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) || r.Header.Get("X-Hamstack") != "1" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func (s *Server) locked(ip string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	var recent []time.Time
	for _, t := range s.failures[ip] {
		if time.Since(t) < failWindow {
			recent = append(recent, t)
		}
	}
	s.failures[ip] = recent
	return len(recent) >= failLimit && time.Since(recent[len(recent)-1]) < lockDuration
}

func (s *Server) failed(ip string) {
	s.mu.Lock()
	s.failures[ip] = append(s.failures[ip], time.Now())
	s.mu.Unlock()
}

type loginView struct {
	Next, Error string
	NoPassword  bool
}

func safeNext(n string) string {
	if !strings.HasPrefix(n, "/") || strings.HasPrefix(n, "//") || strings.Contains(n, "\\") {
		return "/"
	}
	return n
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	if s.loggedIn(r) {
		http.Redirect(w, r, safeNext(r.URL.Query().Get("next")), http.StatusSeeOther)
		return
	}
	render(w, "login.html", loginView{Next: safeNext(r.URL.Query().Get("next")), NoPassword: readPasswordHash(s.opt.PasswordFile) == ""})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	next := safeNext(r.FormValue("next"))
	view := loginView{Next: next}
	ip := clientIP(r)
	hash := readPasswordHash(s.opt.PasswordFile)
	switch {
	case !sameOrigin(r):
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	case hash == "":
		view.NoPassword = true
	case s.locked(ip):
		view.Error = "Too many wrong passwords. Try again in a few minutes."
	case !CheckPassword(hash, r.FormValue("password")):
		s.failed(ip)
		s.opt.Logf("dashboard: wrong password from %s", ip)
		time.Sleep(time.Second)
		view.Error = "Wrong password."
	default:
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		tok := hex.EncodeToString(b)
		s.mu.Lock()
		s.sessions[hashToken(tok)] = time.Now().Add(sessionTTL)
		delete(s.failures, ip)
		s.saveSessionsLocked()
		s.mu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: cookieName, Value: tok, Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
		s.opt.Logf("dashboard: login from %s", ip)
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}
	w.WriteHeader(http.StatusUnauthorized)
	render(w, "login.html", view)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if c, err := r.Cookie(cookieName); err == nil {
		s.mu.Lock()
		delete(s.sessions, hashToken(c.Value))
		s.saveSessionsLocked()
		s.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := tmpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// ---- dashboard and API ----

// The dashboard's pages, in the order the navigation shows them. Tabs are the Control center tabs a page carries.
type dashPage struct{ Path, Label, Template, Tabs string }

var pagesList = []dashPage{
	{"/", "Overview", "overview.html", ""},
	{"/apps", "Apps", "apps.html", ""},
	{"/services", "Services", "services.html", ""},
	{"/updates", "Updates", "updates.html", "Updates,Programs,System,Maintenance"},
	{"/settings", "Settings", "settings.html", "Rigs,Sites,Stations,Apps,Devices,Settings,Network,Remote access,GPS,Doctor"},
}

type navItem struct {
	Href, Label string
	Current     bool
}

type pageData struct {
	Title string
	Nav   []navItem
	NoVNC bool
	Tabs  string
	Name  string // the app, on an app's page
}

func (s *Server) page(p dashPage) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		d := pageData{Title: p.Label, NoVNC: s.noVNCDir() != "", Tabs: p.Tabs}
		for _, q := range pagesList {
			d.Nav = append(d.Nav, navItem{Href: q.Path, Label: q.Label, Current: q.Path == p.Path})
		}
		render(w, p.Template, d)
	}
}

func (s *Server) state(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(s.be.State())
}

func reply(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"ok": "1"})
}

func (s *Server) control(w http.ResponseWriter, r *http.Request) {
	verb := r.PathValue("verb")
	if verb != "start" && verb != "stop" && verb != "restart" && verb != "enable" && verb != "disable" {
		reply(w, fmt.Errorf("unknown action %q", verb))
		return
	}
	reply(w, s.be.Control(r.PathValue("name"), verb))
}

func (s *Server) closeApp(w http.ResponseWriter, r *http.Request) {
	reply(w, s.be.CloseApp(r.PathValue("name")))
}

func (s *Server) closeDesktop(w http.ResponseWriter, r *http.Request) { reply(w, s.be.CloseDesktop()) }

// vnc starts a program (or shares the desktop) if needed, then sends the browser to noVNC pointed at its relay.
// It is a GET so the dashboard can open it in a new tab with a plain link; the SameSite=Strict session cookie
// means another site cannot make a logged-in browser open one.
func (s *Server) vnc(w http.ResponseWriter, r *http.Request) {
	if s.noVNCDir() == "" {
		http.Error(w, "noVNC is not installed (hamstack dashboard on installs it)", http.StatusNotFound)
		return
	}
	name := r.PathValue("name")
	if _, ok := s.be.Target(name); !ok {
		var err error
		if name == "desktop" {
			err = s.be.OpenDesktop()
		} else {
			err = s.be.OpenApp(name)
		}
		if err != nil {
			http.Error(w, "could not open "+name+": "+err.Error(), http.StatusBadGateway)
			return
		}
	}
	// A program's own display follows the browser window's size; the real desktop cannot, so it is scaled.
	resize := "remote"
	if name == "desktop" {
		resize = "scale"
	}
	q := url.Values{"path": {"ws/" + name}, "autoconnect": {"1"}, "reconnect": {"1"}, "resize": {resize}, "show_dot": {"1"}}
	// noVNC 1.6 and later resolve a path given alone against vnc.html, which makes it /novnc/ws/NAME (not
	// found); given the host and port too, every version builds wss://HOST:PORT/ws/NAME.
	host, port := relayHostPort(r)
	q.Set("host", host)
	q.Set("port", port)
	http.Redirect(w, r, "/novnc/vnc.html?"+q.Encode(), http.StatusSeeOther)
}

// relayHostPort is the host and port the browser used to reach the dashboard, for noVNC to open its
// WebSocket on. The dashboard is HTTPS only, so a Host header with no port means 443.
func relayHostPort(r *http.Request) (string, string) {
	host, port, err := net.SplitHostPort(r.Host)
	if err != nil {
		host, port = strings.Trim(r.Host, "[]"), "443"
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]" // IPv6: noVNC puts this straight into a URL
	}
	return host, port
}

// ws relays a browser WebSocket to the program's VNC server on localhost.
func (s *Server) ws(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	port, ok := s.be.Target(name)
	if !ok {
		http.Error(w, name+" is not running", http.StatusNotFound)
		return
	}
	srv := websocket.Server{
		Handshake: func(cfg *websocket.Config, req *http.Request) error {
			if !sameOrigin(req) {
				return fmt.Errorf("cross-origin WebSocket refused")
			}
			var proto []string
			for _, p := range cfg.Protocol {
				if p == "binary" {
					proto = []string{"binary"}
				}
			}
			cfg.Protocol = proto
			return nil
		},
		Handler: func(ws *websocket.Conn) {
			defer ws.Close()
			ws.PayloadType = websocket.BinaryFrame
			c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 5*time.Second)
			if err != nil {
				s.opt.Logf("dashboard: %s: %v", name, err)
				return
			}
			defer c.Close()
			done := make(chan struct{}, 2)
			go func() { _, _ = io.Copy(c, ws); done <- struct{}{} }()
			go func() { _, _ = io.Copy(ws, c); done <- struct{}{} }()
			<-done
		},
	}
	srv.ServeHTTP(w, r)
}

func (s *Server) noVNCDir() string {
	if s.opt.NoVNCPath != nil {
		return s.opt.NoVNCPath()
	}
	return s.opt.NoVNCDir
}
