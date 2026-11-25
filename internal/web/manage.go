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
