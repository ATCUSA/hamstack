package web

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sessionServer(t *testing.T) (*Server, Options) {
	t.Helper()
	dir := t.TempDir()
	pw := filepath.Join(dir, "web-password")
	h, _ := HashPassword("secret-pass")
	if err := SavePasswordHash(pw, h); err != nil {
		t.Fatal(err)
	}
	o := Options{PasswordFile: pw, SessionFile: filepath.Join(dir, "web-sessions"), Logf: func(string, ...any) {}}
	return New(&fakeBackend{}, o), o
}

func TestLoginsSurviveARestartButNotInTheClear(t *testing.T) {
	s, o := sessionServer(t)
	s.mu.Lock()
	s.sessions[hashToken("cookie-one")] = time.Now().Add(time.Hour)
	s.sessions[hashToken("cookie-old")] = time.Now().Add(-time.Hour)
	s.saveSessionsLocked()
	s.mu.Unlock()

	raw, _ := os.ReadFile(o.SessionFile)
	if strings.Contains(string(raw), "cookie-one") {
		t.Fatal("the cookie itself must not be on disk, only its hash")
	}
	if st, _ := os.Stat(o.SessionFile); st.Mode().Perm() != 0o600 {
		t.Errorf("the file is private: %v", st.Mode())
	}

	again := New(&fakeBackend{}, o) // the dashboard restarted
	if _, ok := again.sessions[hashToken("cookie-one")]; !ok {
		t.Error("a login made before the restart is still valid")
	}
	if _, ok := again.sessions[hashToken("cookie-old")]; ok {
		t.Error("an expired login is dropped")
	}
}

func TestChangingThePasswordEndsEveryLogin(t *testing.T) {
	s, o := sessionServer(t)
	s.mu.Lock()
	s.sessions[hashToken("cookie-one")] = time.Now().Add(time.Hour)
	s.saveSessionsLocked()
	s.mu.Unlock()

	h, _ := HashPassword("another-pass")
	if err := SavePasswordHash(o.PasswordFile, h); err != nil {
		t.Fatal(err)
	}
	if after := New(&fakeBackend{}, o); len(after.sessions) != 0 {
		t.Error("after a restart, logins made under the old password are gone")
	}
	// and while it keeps running
	s.pwCheck = time.Time{}
	req, _ := http_NewRequestWithCookie("cookie-one")
	if s.loggedIn(req) {
		t.Error("a running dashboard also refuses logins made under the old password")
	}
}

func TestWithoutASessionFileLoginsStayInMemory(t *testing.T) {
	s := New(&fakeBackend{}, Options{Logf: func(string, ...any) {}})
	s.mu.Lock()
	s.saveSessionsLocked() // must do nothing, and not fail
	s.mu.Unlock()
}

func http_NewRequestWithCookie(v string) (*http.Request, error) {
	r, err := http.NewRequest("GET", "/", nil)
	if err == nil {
		r.AddCookie(&http.Cookie{Name: cookieName, Value: v})
	}
	return r, err
}
