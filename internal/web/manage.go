package web

import (
	"encoding/json"
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
		reply(w, err)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"result": value})
}
