package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Logins survive a restart of the dashboard (an update, a reboot of the service), so the person is not asked to sign in
// again each time. They are kept in a file only the owner can read. What is stored is a hash of each login cookie, not
// the cookie itself, so reading the file does not let anyone sign in. All logins end when the password is changed.

// saveEvery limits how often the sliding expiry is written: it moves on every request.
const saveEvery = time.Minute

type sessionFile struct {
	Password string           `json:"password"` // fingerprint of the password file the logins were made under
	Sessions map[string]int64 `json:"sessions"` // hash of cookie -> expiry (unix seconds)
}

// hashToken is what is kept for a login cookie, in memory and on disk.
func hashToken(tok string) string {
	h := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(h[:])
}

// passwordPrint identifies the current password, so changing it ends every login. Empty if there is none yet.
func (s *Server) passwordPrint() string {
	if s.opt.PasswordFile == "" {
		return ""
	}
	b, err := os.ReadFile(s.opt.PasswordFile)
	if err != nil {
		return ""
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// loadSessions restores the saved logins that are still valid and were made under the current password.
func (s *Server) loadSessions() {
	if s.opt.SessionFile == "" {
		return
	}
	b, err := os.ReadFile(s.opt.SessionFile)
	if err != nil {
		return
	}
	var f sessionFile
	if json.Unmarshal(b, &f) != nil || f.Password == "" || f.Password != s.passwordPrint() {
		return
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, exp := range f.Sessions {
		if t := time.Unix(exp, 0); t.After(now) {
			s.sessions[k] = t
		}
	}
}

// saveSessionsLocked writes the logins; s.mu must be held. A failure is logged and otherwise ignored: the dashboard
// still works, the logins just will not outlive a restart.
func (s *Server) saveSessionsLocked() {
	if s.opt.SessionFile == "" {
		return
	}
	now := time.Now()
	f := sessionFile{Password: s.passwordPrint(), Sessions: map[string]int64{}}
	for k, exp := range s.sessions {
		if exp.After(now) {
			f.Sessions[k] = exp.Unix()
		}
	}
	b, _ := json.Marshal(f)
	tmp := s.opt.SessionFile + ".tmp"
	if err := os.MkdirAll(filepath.Dir(s.opt.SessionFile), 0o755); err == nil {
		err = os.WriteFile(tmp, b, 0o600)
		if err == nil {
			err = os.Rename(tmp, s.opt.SessionFile)
		}
		if err != nil {
			s.opt.Logf("dashboard: could not save logins: %v", err)
		}
	}
	s.lastSave = now
}
