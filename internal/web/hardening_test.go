package web

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

// Parallel wrong passwords must count against the limit as they arrive, not only once each has been checked: 50 at
// once used to be 50 guesses, all passing the lock check before the first failure was recorded.
func TestParallelWrongPasswordsAreLimitedToo(t *testing.T) {
	if testing.Short() {
		t.Skip("each wrong password waits a second")
	}
	var checks atomic.Int32
	old := checkPassword
	checkPassword = func(hash, pw string) bool { checks.Add(1); return CheckPassword(hash, pw) }
	defer func() { checkPassword = old }()
	ts, c := setup(t, &fakeBackend{})
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); login(t, ts, c, "nope") }()
	}
	wg.Wait()
	if n := checks.Load(); n > failLimit {
		t.Fatalf("%d passwords were tried; the limit is %d", n, failLimit)
	}
}

func TestOldLoginFailuresAreForgotten(t *testing.T) {
	s, _ := sessionServer(t)
	old := time.Now().Add(-2 * failWindow)
	for i := 0; i < 1000; i++ {
		s.failures[net.IPv4(10, 0, byte(i/256), byte(i)).String()] = []time.Time{old}
	}
	s.reserve("192.0.2.1")
	s.release("192.0.2.1", true)
	if n := len(s.failures); n > 1 {
		t.Fatalf("%d addresses still remembered; old failures must be dropped", n)
	}
}

func TestSafeNextRefusesWhatABrowserWouldTurnIntoAnotherSite(t *testing.T) {
	for _, in := range []string{"/\t/evil.example", "/\n/evil.example", "/\r/evil.example", "/\x00x", "/\x7fx", "/ /x"} {
		if got := safeNext(in); got != "/" {
			t.Errorf("%q -> %q", in, got)
		}
	}
	if got := safeNext("/apps/pat?x=1#y"); got != "/apps/pat?x=1#y" {
		t.Errorf("an ordinary path must survive: %q", got)
	}
}

func TestLoginRefusesAHugeForm(t *testing.T) {
	ts, c := setup(t, &fakeBackend{})
	body := "password=x&pad=" + strings.Repeat("a", 64<<10)
	req, _ := http.NewRequest("POST", ts.URL+"/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", ts.URL)
	r, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusRequestEntityTooLarge && r.StatusCode != http.StatusBadRequest {
		t.Fatalf("a 64 KB login form: %d", r.StatusCode)
	}
}

// The dashboard can be started before its password is set (hamstack dashboard on does that). The first login must
// not be thrown away by the next request because the password "changed" since the start.
func TestFirstLoginAfterThePasswordIsSetStays(t *testing.T) {
	dir := t.TempDir()
	pwf := filepath.Join(dir, "pw")
	s := New(&fakeBackend{}, Options{PasswordFile: pwf, Logf: func(string, ...any) {}})
	h, _ := HashPassword("secret-pass")
	if err := SavePasswordHash(pwf, h); err != nil {
		t.Fatal(err)
	}
	ts := httptest_TLS(t, s)
	c := clientFor(ts)
	if r := login(t, ts, c, "secret-pass"); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("login: %d", r.StatusCode)
	}
	s.mu.Lock()
	s.pwCheck = time.Time{} // compare the password at once, as five seconds later would
	s.mu.Unlock()
	if r, _ := c.Get(ts.URL + "/api/state"); r.StatusCode != http.StatusOK {
		t.Fatalf("the first login was dropped: %d", r.StatusCode)
	}
}

// Turning on the console (a shell as this user), turning on tuning (keys the transmitter) and changing the
// password are the owner's to decide: a borrowed browser or a stolen cookie must not be enough.
func TestOwnerChangesNeedTheDashboardPasswordAgain(t *testing.T) {
	be := &fakeBackend{}
	ts, c := setup(t, be)
	login(t, ts, c, "secret-pass")
	post := func(body map[string]any) (int, map[string]any) {
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", ts.URL+"/api/manage", bytes.NewReader(b))
		req.Header.Set("Origin", ts.URL)
		req.Header.Set("X-Hamstack", "1")
		r, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(r.Body).Decode(&out)
		return r.StatusCode, out
	}
	for _, a := range []map[string]any{
		{"action": "dashboard.password", "value": "new-password", "extra": "new-password"},
		{"action": "dashboard.console", "value": "on", "confirm": true},
		{"action": "dashboard.tune", "value": "on", "confirm": true},
	} {
		be.controls = nil
		if code, out := post(a); code != http.StatusPreconditionRequired || out["code"] != "dashboard-password-needed" {
			t.Errorf("%v without the password: %d %v", a["action"], code, out)
		}
		a["current"] = "wrong-pass"
		if code, out := post(a); code != http.StatusForbidden || out["code"] != "dashboard-password-rejected" {
			t.Errorf("%v with a wrong password: %d %v", a["action"], code, out)
		}
		if len(be.controls) != 0 {
			t.Errorf("%v reached the backend without the right password", a["action"])
		}
		a["current"] = "secret-pass"
		if code, out := post(a); code != http.StatusOK {
			t.Errorf("%v with the right password: %d %v", a["action"], code, out)
		}
	}
	// Turning them off makes nothing more dangerous: no password needed.
	if code, _ := post(map[string]any{"action": "dashboard.console", "value": "off", "confirm": true}); code != http.StatusOK {
		t.Errorf("turning the console off: %d", code)
	}
}

// An open remote screen (the console is a shell) must close when its login ends: logging out, or a password change
// ("a changed password ends every login, at once").
func TestOpenRemoteScreensCloseWhenTheLoginEnds(t *testing.T) {
	oldEvery := relayCheckEvery
	relayCheckEvery = 50 * time.Millisecond
	defer func() { relayCheckEvery = oldEvery }()
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
			go func() { defer c.Close(); _, _ = c.Write([]byte("RFB 003.008\n")); _, _ = io.Copy(io.Discard, c) }()
		}
	}()
	ts, c := setup(t, &fakeBackend{port: l.Addr().(*net.TCPAddr).Port})
	login(t, ts, c, "secret-pass")
	u, _ := url.Parse(ts.URL)
	cfg, _ := websocket.NewConfig("wss://"+u.Host+"/ws/wsjtx", ts.URL)
	cfg.Protocol = []string{"binary"}
	cfg.TlsConfig = &tls.Config{InsecureSkipVerify: true}
	for _, ck := range c.Jar.Cookies(u) {
		cfg.Header.Add("Cookie", ck.Name+"="+ck.Value)
	}
	ws, err := websocket.DialConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	buf := make([]byte, 64)
	if _, err := ws.Read(buf); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("POST", ts.URL+"/logout", nil)
	req.Header.Set("Origin", ts.URL)
	if r, err := c.Do(req); err == nil {
		r.Body.Close()
	}
	_ = ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := ws.Read(buf); err == nil || os.IsTimeout(err) {
		t.Fatalf("the screen stayed open after logging out: %v", err)
	}
}

func httptest_TLS(t *testing.T, s *Server) *httptest.Server {
	t.Helper()
	ts := httptest.NewTLSServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func clientFor(ts *httptest.Server) *http.Client {
	jar, _ := cookiejar.New(nil)
	c := ts.Client()
	c.Jar = jar
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return c
}

// The noVNC tab makes no request but its WebSocket, so a screen in use must keep its login alive; one left alone
// must not keep it alive by itself.
func TestAScreenInUseKeepsItsLoginAlive(t *testing.T) {
	oldEvery := relayCheckEvery
	relayCheckEvery = 30 * time.Millisecond
	defer func() { relayCheckEvery = oldEvery }()
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
			go func() { defer c.Close(); _, _ = io.Copy(io.Discard, c) }()
		}
	}()
	dir := t.TempDir()
	pwf := filepath.Join(dir, "pw")
	h, _ := HashPassword("secret-pass")
	_ = SavePasswordHash(pwf, h)
	s := New(&fakeBackend{port: l.Addr().(*net.TCPAddr).Port}, Options{PasswordFile: pwf, Logf: func(string, ...any) {}})
	ts := httptest_TLS(t, s)
	c := clientFor(ts)
	login(t, ts, c, "secret-pass")
	u, _ := url.Parse(ts.URL)
	var key string
	cfg, _ := websocket.NewConfig("wss://"+u.Host+"/ws/wsjtx", ts.URL)
	cfg.Protocol = []string{"binary"}
	cfg.TlsConfig = &tls.Config{InsecureSkipVerify: true}
	for _, ck := range c.Jar.Cookies(u) {
		cfg.Header.Add("Cookie", ck.Name+"="+ck.Value)
		key = hashToken(ck.Value)
	}
	ws, err := websocket.DialConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	expiry := func() time.Time { s.mu.Lock(); defer s.mu.Unlock(); return s.sessions[key] }
	s.mu.Lock()
	s.sessions[key] = time.Now().Add(time.Minute)
	s.mu.Unlock()
	time.Sleep(150 * time.Millisecond) // left alone: not extended
	if until := time.Until(expiry()); until > time.Minute {
		t.Fatalf("an idle screen extended its login: %v left", until)
	}
	for i := 0; i < 6; i++ { // in use: typing
		_, _ = ws.Write([]byte("k"))
		time.Sleep(30 * time.Millisecond)
	}
	if until := time.Until(expiry()); until < sessionTTL-time.Minute {
		t.Fatalf("a screen in use did not keep its login alive: %v left", until)
	}
}

func TestLoginGoesWhereTheFormSays(t *testing.T) {
	ts, c := setup(t, &fakeBackend{})
	req, _ := http.NewRequest("POST", ts.URL+"/login", strings.NewReader(url.Values{"password": {"secret-pass"}, "next": {"/apps/pat"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", ts.URL)
	r, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusSeeOther || r.Header.Get("Location") != "/apps/pat" {
		t.Fatalf("%d %q", r.StatusCode, r.Header.Get("Location"))
	}
}
