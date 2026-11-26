package web

import (
	"encoding/json"
	"net/http"
)

// One page per radio: everything about a rig in one place (its controls, its modems and their own pages, its sound
// card, Pat's settings for it, its ports), instead of spread over Overview, Services, Apps and Settings.

// RadioPage is what the radio page shows for one rig.
type RadioPage struct {
	ID     string       `json:"id"`
	Name   string       `json:"name"`
	Model  string       `json:"model"`
	Rigs   []RadioRef   `json:"rigs"` // every radio, for the tabs
	Modems []RadioModem `json:"modems"`
	Add    []string     `json:"add,omitempty"` // installed HF modems the rig could also have, to switch to
	Sound  RadioSound   `json:"sound"`
	Pat    RadioPat     `json:"pat"`
	Ports  []RadioPort  `json:"ports"`
}

// RadioRef is a radio in the tabs.
type RadioRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Off  bool   `json:"off,omitempty"`
}

// RadioModem is one of a rig's modems: the one it runs, or one it can switch to.
type RadioModem struct {
	Kind      string `json:"kind"`  // mercury, data2g, ardopcf, xara, direwolf
	Unit      string `json:"unit"`  // its service, "mercury@ftdx10"
	What      string `json:"what"`  // "Mercury HF modem"
	State     string `json:"state"` // running, stopped, failed, standby...
	Running   bool   `json:"running"`
	Link      string `json:"link,omitempty"`      // its own page: a path on this dashboard ("/ardop/ftdx10/") or ":port"
	LinkLabel string `json:"linkLabel,omitempty"` // "Open web UI", "Open screen"
	Note      string `json:"note,omitempty"`
	ScreenOff bool   `json:"screenOff,omitempty"` // ardopcf or xara with its screen turned off
}

// RadioSound is the rig's sound card.
type RadioSound struct {
	Card   string `json:"card"`   // ALSA card id, "" when none is found
	Device string `json:"device"` // what the modems open
	Shared bool   `json:"shared"` // shared listening is on
	Note   string `json:"note,omitempty"`
}

// RadioPat is how Pat's config stands against this radio.
type RadioPat struct {
	Station string      `json:"station,omitempty"`
	Changes []AppChange `json:"changes,omitempty"` // what the recommended setup would change
	Note    string      `json:"note,omitempty"`
}

// RadioPort is one of the rig's network ports.
type RadioPort struct {
	What   string `json:"what"`
	Port   int    `json:"port"`
	Access string `json:"access"` // local, network, all
	Open   bool   `json:"open"`   // something listens on it now
	Note   string `json:"note,omitempty"`
}

// RadioLevel is a measurement of the rig's receive audio.
type RadioLevel struct {
	RMS     float64 `json:"rms"`
	Peak    float64 `json:"peak"`
	Clipped int     `json:"clipped"`
	Target  float64 `json:"target"`
	Advice  string  `json:"advice"`
	Good    bool    `json:"good"`
}

// Radios is implemented by a backend that can show radio pages.
type Radios interface {
	RadioPage(id string) (RadioPage, error) // id "" is the first radio
	RadioLevel(id string) (RadioLevel, error)
}

func (s *Server) radioData(w http.ResponseWriter, r *http.Request) {
	be, ok := s.be.(Radios)
	if !ok {
		http.NotFound(w, r)
		return
	}
	p, err := be.RadioPage(r.URL.Query().Get("rig"))
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(p)
}

func (s *Server) radioLevel(w http.ResponseWriter, r *http.Request) {
	be, ok := s.be.(Radios)
	if !ok {
		http.NotFound(w, r)
		return
	}
	l, err := be.RadioLevel(r.PathValue("id"))
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(l)
}
