package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
)

type ManagementRequest struct {
	Action  string `json:"action"`
	ID      string `json:"id"`
	Value   string `json:"value"`
	Extra   string `json:"extra"`
	Confirm bool   `json:"confirm"`
	// Secret is the administrator password, sent only after the page has shown why it is needed and the person agreed.
	// It is used for the one action and never stored, logged or returned.
	Secret string `json:"secret,omitempty"`
	// Current is the dashboard password, typed again for a change only the owner should make (ownerOnly). Checked
	// here, within the login limits, and never passed on.
	Current string `json:"current,omitempty"`
}

// ownerOnly is a change a logged-in browser alone must not be able to make: a borrowed browser or a stolen cookie
// could otherwise open a shell (the console), key the transmitter (tuning), or lock the owner out (the password).
// Turning the console or tuning off makes nothing more dangerous, so that needs no password.
func ownerOnly(r ManagementRequest) bool {
	switch r.Action {
	case "dashboard.password":
		return true
	case "dashboard.console", "dashboard.tune":
		return r.Value == "on"
	case "rig.set": // ardopcf's screen has buttons that make it send audio; xara's faster speeds are for VARA licence holders
		return (r.Value == "ardop-gui" || r.Value == "ardop_gui" || r.Value == "xara-registered" || r.Value == "xara_registered") && r.Extra == "on"
	}
	return false
}

type ManagementBackend interface {
	ManagementState() any
	Manage(ManagementRequest) (any, error)
}

var managementMu sync.Mutex

func (s *Server) management(w http.ResponseWriter, r *http.Request) {
	be, ok := s.be.(ManagementBackend)
	if !ok {
		http.Error(w, "management unavailable", http.StatusNotImplemented)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodGet {
		_ = json.NewEncoder(w).Encode(be.ManagementState())
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var req ManagementRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		reply(w, fmt.Errorf("invalid request: %w", err))
		return
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		reply(w, fmt.Errorf("request must contain exactly one JSON object"))
		return
	}
	if ownerOnly(req) {
		if req.Current == "" {
			w.WriteHeader(http.StatusPreconditionRequired)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": "dashboard-password-needed", "error": "Type the dashboard password again to confirm this change."})
			return
		}
		if ok, locked := s.tryPassword(clientIP(r), req.Current); !ok {
			msg := "That is not the dashboard password."
			if locked {
				msg = "Too many wrong passwords. Try again in a few minutes."
			}
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": "dashboard-password-rejected", "error": msg})
			return
		}
	}
	req.Current = ""
	managementMu.Lock()
	defer managementMu.Unlock()
	value, err := be.Manage(req)
	if err != nil {
		var need *SudoNeeded
		var rejected *SudoRejected
		switch {
		case errors.As(err, &need):
			w.WriteHeader(http.StatusPreconditionRequired) // 428: not an error, a question for the person
			_ = json.NewEncoder(w).Encode(map[string]any{"error": need.Error(), "code": "sudo-needed", "needsSudo": need})
		case errors.As(err, &rejected):
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": rejected.Error(), "code": rejected.code()})
		default:
			reply(w, err)
		}
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"result": value})
}
