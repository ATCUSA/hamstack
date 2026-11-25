package web

import (
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/net/websocket"
)

func TestPasswordHash(t *testing.T) {
	h, err := HashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !CheckPassword(h, "correct horse") || CheckPassword(h, "wrong") || CheckPassword(h, "") {
		t.Error("check")
	}
	h2, _ := HashPassword("correct horse")
	if h == h2 {
		t.Error("each hash has its own salt")
	}
	for _, bad := range []string{"", "plain", "pbkdf2-sha256$1$x$y", "md5$1000$AAAA$AAAA", "pbkdf2-sha256$abc$AAAA$AAAA"} {
		if CheckPassword(bad, "x") {
			t.Errorf("%q accepted", bad)
		}
	}
	if _, err := HashPassword(""); err == nil {
		t.Error("an empty password must be refused")
	}
}

func TestSafeNext(t *testing.T) {
	for in, want := range map[string]string{"/": "/", "/vnc/wsjtx": "/vnc/wsjtx", "//evil.com": "/", "https://evil.com": "/", "/\\evil": "/", "": "/"} {
		if got := safeNext(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}

type fakeBackend struct {
	port     int
	controls []string
}

func (f *fakeBackend) State() State {
	return State{Station: "N0CALL", Services: []Service{{Name: "pat", What: "Pat", State: "running", Control: true}}}
}
func (f *fakeBackend) Control(s, v string) error {
	f.controls = append(f.controls, s+" "+v)
	return nil
}
func (f *fakeBackend) OpenApp(string) error  { return nil }
func (f *fakeBackend) CloseApp(string) error { return nil }
func (f *fakeBackend) OpenDesktop() error    { return nil }
func (f *fakeBackend) CloseDesktop() error   { return nil }
func (f *fakeBackend) Target(name string) (int, bool) {
	return f.port, name == "wsjtx" && f.port > 0
}

func setup(t *testing.T, be *fakeBackend) (*httptest.Server, *http.Client) {
	t.Helper()
	dir := t.TempDir()
	pwf := filepath.Join(dir, "pw")
	h, _ := HashPassword("secret-pass")
	if err := SavePasswordHash(pwf, h); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(pwf); fi.Mode().Perm() != 0o600 {
		t.Errorf("password file mode %v", fi.Mode().Perm())
	}
	nov := filepath.Join(dir, "novnc")
	os.MkdirAll(nov, 0o755)
	os.WriteFile(filepath.Join(nov, "vnc.html"), []byte("novnc page"), 0o644)
	s := New(be, Options{PasswordFile: pwf, NoVNCDir: nov, Logf: func(string, ...any) {}})
	ts := httptest.NewTLSServer(s.Handler())
	t.Cleanup(ts.Close)
	jar, _ := cookiejar.New(nil)
	c := ts.Client()
	c.Jar = jar
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return ts, c
}

func login(t *testing.T, ts *httptest.Server, c *http.Client, pw string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("POST", ts.URL+"/login", strings.NewReader(url.Values{"password": {pw}, "next": {"/"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", ts.URL)
	r, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	return r
}

func TestLoginGuardsEverything(t *testing.T) {
	be := &fakeBackend{}
	ts, c := setup(t, be)
	for _, p := range []string{"/", "/apps", "/apps/pat", "/services", "/updates", "/settings", "/novnc/vnc.html", "/vnc/wsjtx"} {
		r, _ := c.Get(ts.URL + p)
		if r.StatusCode != http.StatusSeeOther || !strings.HasPrefix(r.Header.Get("Location"), "/login") {
			t.Errorf("%s without a login: %d %s", p, r.StatusCode, r.Header.Get("Location"))
		}
	}
	if r, _ := c.Get(ts.URL + "/api/state"); r.StatusCode != http.StatusUnauthorized {
		t.Errorf("api without a login: %d", r.StatusCode)
	}
	if r := login(t, ts, c, "wrong"); r.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong password: %d", r.StatusCode)
	}
	r := login(t, ts, c, "secret-pass")
	if r.StatusCode != http.StatusSeeOther {
		t.Fatalf("login: %d", r.StatusCode)
	}
	var ck *http.Cookie
	for _, x := range r.Cookies() {
		if x.Name == cookieName {
			ck = x
		}
	}
	if ck == nil || !ck.HttpOnly || !ck.Secure || ck.SameSite != http.SameSiteStrictMode {
		t.Errorf("session cookie must be HttpOnly, Secure and SameSite=Strict: %+v", ck)
	}
	if r, _ := c.Get(ts.URL + "/api/state"); r.StatusCode != 200 {
		t.Errorf("state after login: %d", r.StatusCode)
	}
	if r, _ := c.Get(ts.URL + "/novnc/vnc.html"); r.StatusCode != 200 {
		t.Errorf("noVNC after login: %d", r.StatusCode)
	}
}

func TestActionsNeedSameOriginAndHeader(t *testing.T) {
	be := &fakeBackend{}
	ts, c := setup(t, be)
	login(t, ts, c, "secret-pass")
	do := func(origin, hdr string) int {
		req, _ := http.NewRequest("POST", ts.URL+"/api/service/pat/restart", nil)
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		if hdr != "" {
			req.Header.Set("X-Hamstack", hdr)
		}
		r, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		return r.StatusCode
	}
	if code := do("https://evil.example", "1"); code != http.StatusForbidden {
		t.Errorf("another site: %d", code)
	}
	if code := do(ts.URL, ""); code != http.StatusForbidden {
		t.Errorf("no X-Hamstack header (a plain form): %d", code)
	}
	if len(be.controls) != 0 {
		t.Fatalf("refused requests must not act: %v", be.controls)
	}
	if code := do(ts.URL, "1"); code != 200 || len(be.controls) != 1 || be.controls[0] != "pat restart" {
		t.Errorf("the dashboard's own request: %d %v", code, be.controls)
	}
}

func TestLoginLockout(t *testing.T) {
	if testing.Short() {
		t.Skip("each wrong password waits a second")
	}
	ts, c := setup(t, &fakeBackend{})
	for i := 0; i < failLimit; i++ {
		login(t, ts, c, "nope")
	}
	if r := login(t, ts, c, "secret-pass"); r.StatusCode == http.StatusSeeOther {
		t.Error("after too many wrong passwords even the right one waits")
	}
}

func TestWebSocketRelay(t *testing.T) {
	// A fake VNC server that greets like one and echoes.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				c.Write([]byte("RFB 003.008\n"))
				io.Copy(c, c)
			}()
		}
	}()
	be := &fakeBackend{port: l.Addr().(*net.TCPAddr).Port}
	ts, c := setup(t, be)
	login(t, ts, c, "secret-pass")
	u, _ := url.Parse(ts.URL)

	dial := func(origin string, withCookie bool) (*websocket.Conn, error) {
		cfg, _ := websocket.NewConfig("wss://"+u.Host+"/ws/wsjtx", origin)
		cfg.Protocol = []string{"binary"}
		cfg.TlsConfig = &tls.Config{InsecureSkipVerify: true}
		if withCookie {
			for _, ck := range c.Jar.Cookies(u) {
				cfg.Header.Add("Cookie", ck.Name+"="+ck.Value)
			}
		}
		return websocket.DialConfig(cfg)
	}
	if _, err := dial(ts.URL, false); err == nil {
		t.Error("a WebSocket without a login must be refused")
	}
	if _, err := dial("https://evil.example", true); err == nil {
		t.Error("a WebSocket from another site must be refused")
	}
	ws, err := dial(ts.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	buf := make([]byte, 64)
	n, err := ws.Read(buf)
	if err != nil || string(buf[:n]) != "RFB 003.008\n" {
		t.Fatalf("greeting: %q %v", buf[:n], err)
	}
	ws.Write([]byte("hello"))
	n, _ = io.ReadAtLeast(ws, buf, 5)
	if string(buf[:n]) != "hello" {
		t.Errorf("echo: %q", buf[:n])
	}
	if ws.Config().Protocol[0] != "binary" {
		t.Errorf("subprotocol %v", ws.Config().Protocol)
	}
}

func (f *fakeBackend) ManagementState() any { return map[string]string{"state": "ready"} }
func (f *fakeBackend) Manage(r ManagementRequest) (any, error) {
	f.controls = append(f.controls, "manage "+r.Action)
	switch r.Action {
	case "needs-sudo":
		if r.Secret == "" {
			return nil, &SudoNeeded{User: "ham", Why: "why", Commands: []string{"sudo x"}}
		}
		if r.Secret != "right" {
			return nil, &SudoRejected{Message: "nope"}
		}
	}
	return "done", nil
}

func TestManagementRequiresAuthenticationOriginAndExplicitHeader(t *testing.T) {
	be := &fakeBackend{}
	ts, client := setup(t, be)
	defer ts.Close()
	// Existing test helper performs the ordinary login flow.
	login(t, ts, client, "secret-pass").Body.Close()
	req, _ := http.NewRequest("POST", ts.URL+"/api/manage", strings.NewReader(`{"action":"test"}`))
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden || len(be.controls) != 0 {
		t.Fatal("management accepted an unmarked mutation")
	}
	req, _ = http.NewRequest("POST", ts.URL+"/api/manage", strings.NewReader(`{"action":"test"}`))
	req.Header.Set("X-Hamstack", "1")
	req.Header.Set("Origin", ts.URL)
	response, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK || len(be.controls) != 1 {
		t.Fatalf("valid management request failed: %d %v", response.StatusCode, be.controls)
	}
	req, _ = http.NewRequest("POST", ts.URL+"/api/manage", strings.NewReader(`{"action":"test"} {"action":"other"}`))
	req.Header.Set("X-Hamstack", "1")
	req.Header.Set("Origin", ts.URL)
	response, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest || len(be.controls) != 1 {
		t.Fatal("accepted trailing JSON")
	}
}

// noVNC 1.6+ resolves a lone path against /novnc/vnc.html, so the redirect must name the host and port too.
func TestRelayHostPort(t *testing.T) {
	for host, want := range map[string][2]string{
		"radio.example":      {"radio.example", "443"},
		"radio.example:8443": {"radio.example", "8443"},
		"10.0.0.5":           {"10.0.0.5", "443"},
		"[fd00::1]:8443":     {"[fd00::1]", "8443"},
		"[fd00::1]":          {"[fd00::1]", "443"},
	} {
		h, p := relayHostPort(&http.Request{Host: host})
		if h != want[0] || p != want[1] {
			t.Errorf("%s: got %s %s, want %s %s", host, h, p, want[0], want[1])
		}
	}
}

func TestManagementAsksForTheAdministratorPassword(t *testing.T) {
	be := &fakeBackend{}
	ts, client := setup(t, be)
	defer ts.Close()
	login(t, ts, client, "secret-pass").Body.Close()
	post := func(body string) (int, string) {
		req, _ := http.NewRequest("POST", ts.URL+"/api/manage", strings.NewReader(body))
		req.Header.Set("X-Hamstack", "1")
		req.Header.Set("Origin", ts.URL)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, body := post(`{"action":"needs-sudo"}`); code != http.StatusPreconditionRequired || !strings.Contains(body, `"needsSudo"`) || !strings.Contains(body, `"sudo x"`) {
		t.Fatalf("want 428 with the question, got %d %s", code, body)
	}
	code, body := post(`{"action":"needs-sudo","secret":"bad"}`)
	if code != http.StatusForbidden || !strings.Contains(body, "sudo-rejected") || strings.Contains(body, "bad\"") {
		t.Fatalf("wrong password: %d %s", code, body)
	}
	if code, body := post(`{"action":"needs-sudo","secret":"right"}`); code != 200 || strings.Contains(body, "right") {
		t.Fatalf("right password: %d %s", code, body)
	}
}

func TestLogoAndFaviconNeedNoLogin(t *testing.T) {
	ts, client := setup(t, &fakeBackend{})
	defer ts.Close()
	for path, ctype := range map[string]string{"/favicon.svg": "image/svg+xml", "/logo.svg": "image/svg+xml", "/favicon.ico": "image/x-icon", "/apple-touch-icon.png": "image/png"} {
		resp, err := client.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != ctype || len(b) < 100 {
			t.Errorf("%s: %d %q (%d bytes)", path, resp.StatusCode, resp.Header.Get("Content-Type"), len(b))
		}
	}
	resp, _ := client.Get(ts.URL + "/login")
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), `rel="icon"`) {
		t.Error("the login page has no favicon link")
	}
}
