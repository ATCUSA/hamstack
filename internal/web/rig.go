package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
)

// RigState is what the radio panel shows for one rig: what rigctld says the radio is doing now.
// Meters that a radio does not offer are nil, so the page leaves them out instead of showing a zero.
type RigState struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Online bool   `json:"online"`
	Error  string `json:"error,omitempty"` // why it is not online

	Services []RigService `json:"services,omitempty"` // rig control and the modem, with what they are doing
	Modem    string       `json:"modem,omitempty"`    // the modem it runs
	Switch   []string     `json:"switch,omitempty"`   // other HF modems set up for it, to switch to
	// ArdopScreen is where the dashboard shows ardopcf's own page for this rig ("" when it shows none);
	// ArdopScreenOff is true when the rig runs ardopcf with its screen turned off.
	ArdopScreen    string `json:"ardopScreen,omitempty"`
	ArdopScreenOff bool   `json:"ardopScreenOff,omitempty"`
	// XaraScreen and XaraScreenOff are the same for xara's own page.
	XaraScreen    string `json:"xaraScreen,omitempty"`
	XaraScreenOff bool   `json:"xaraScreenOff,omitempty"`

	Freq  int64    `json:"freq"` // hertz
	Mode  string   `json:"mode"`
	Width int      `json:"width,omitempty"`
	Modes []string `json:"modes,omitempty"` // what the radio accepts

	Power    *float64 `json:"power,omitempty"` // 0..1 of full power
	PowerMin float64  `json:"powerMin"`
	PowerMax float64  `json:"powerMax"`
	FullW    float64  `json:"fullWatts,omitempty"` // the watts at power 1, when the radio's power meter says

	PTT      bool     `json:"ptt"`
	Strength *float64 `json:"strength,omitempty"` // S meter, dB relative to S9 (-54 is S0)
	PowerW   *float64 `json:"powerWatts,omitempty"`
	SWR      *float64 `json:"swr,omitempty"`
	ALC      *float64 `json:"alc,omitempty"`
	Comp     *float64 `json:"comp,omitempty"`
	Meters   []string `json:"meters,omitempty"`  // which meters the radio has (s, p, swr, alc, c, id, vd), so the card can show all of them from the start
	Voltage  *float64 `json:"voltage,omitempty"` // the radio's supply voltage meter, as a fraction of its scale
	Current  *float64 `json:"current,omitempty"` // the final amplifier's current meter, as a fraction of its scale

	Tuner     *bool `json:"tuner,omitempty"` // the radio's antenna tuner is in line
	CanTune   bool  `json:"canTune"`         // the radio can run a tuning cycle
	TuneOn    bool  `json:"tuneEnabled"`     // and the owner has allowed it from the browser
	Tuning    bool  `json:"tuning"`
	TunePower int   `json:"tunePercent,omitempty"` // the power a tuning cycle runs at, in percent
}

// RigService is one of the services that make a rig work (its rig control, its modem), with the state the
// panel shows and the buttons that go with it. Name is what /api/service/{name}/{verb} takes.
type RigService struct {
	Name    string `json:"name"`
	What    string `json:"what"`
	State   string `json:"state"` // running, stopped, failed, no radio, starting, stopping, not installed
	Enabled bool   `json:"enabled"`
}

// RigSetting is a change the panel asks for. A nil or empty field is left alone.
type RigSetting struct {
	Freq  *int64   `json:"freq,omitempty"`
	Mode  string   `json:"mode,omitempty"`
	Power *float64 `json:"power,omitempty"`
	Tuner *bool    `json:"tuner,omitempty"`
}

// TuneResult is how a tuning cycle went.
type TuneResult struct {
	Message string   `json:"message"`
	SWR     *float64 `json:"swr,omitempty"` // the highest ratio seen while it transmitted
}

// RigControl is the part of the backend that talks to the radios. It is separate from Backend so a backend
// without radios (or a test double) need not have it, and the panel then does not appear.
type RigControl interface {
	RigStates() []RigState
	RigSet(id string, s RigSetting) error
	RigTune(ctx context.Context, id string) (TuneResult, error)
	RigAbort(id string) error
}

func (s *Server) rigs() (RigControl, bool) {
	rc, ok := s.be.(RigControl)
	return rc, ok
}

func (s *Server) rigStates(w http.ResponseWriter, r *http.Request) {
	rc, ok := s.rigs()
	if !ok {
		writeJSON(w, http.StatusOK, []RigState{})
		return
	}
	writeJSON(w, http.StatusOK, rc.RigStates())
}

func (s *Server) rigSet(w http.ResponseWriter, r *http.Request) {
	rc, ok := s.rigs()
	if !ok {
		http.Error(w, "no radio control here", http.StatusNotFound)
		return
	}
	var set RigSetting
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&set); err != nil {
		replyErr(w, http.StatusBadRequest, fmt.Errorf("bad request: %w", err))
		return
	}
	if err := rc.RigSet(r.PathValue("id"), set); err != nil {
		replyErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) rigTune(w http.ResponseWriter, r *http.Request) {
	rc, ok := s.rigs()
	if !ok {
		http.Error(w, "no radio control here", http.StatusNotFound)
		return
	}
	res, err := rc.RigTune(r.Context(), r.PathValue("id"))
	if err != nil {
		replyErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) rigAbort(w http.ResponseWriter, r *http.Request) {
	rc, ok := s.rigs()
	if !ok {
		http.Error(w, "no radio control here", http.StatusNotFound)
		return
	}
	if err := rc.RigAbort(r.PathValue("id")); err != nil {
		replyErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func replyErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

// ValidateRigSetting checks a change against what the radio said it can do, so a bad request never reaches it.
// freq limits are generous: the radio itself refuses what it cannot tune.
func ValidateRigSetting(set RigSetting, st RigState) error {
	if set.Freq == nil && set.Mode == "" && set.Power == nil && set.Tuner == nil {
		return fmt.Errorf("nothing to change")
	}
	if st.PTT && (set.Freq != nil || set.Mode != "" || set.Power != nil) {
		return fmt.Errorf("the radio is transmitting: wait until it stops")
	}
	if set.Freq != nil && (*set.Freq < 30_000 || *set.Freq > 3_000_000_000) {
		return fmt.Errorf("%d Hz is not a frequency this radio could use", *set.Freq)
	}
	if set.Mode != "" && len(st.Modes) > 0 && !slices.Contains(st.Modes, set.Mode) {
		return fmt.Errorf("this radio has no %q mode", set.Mode)
	}
	if set.Power != nil && (*set.Power < st.PowerMin-1e-9 || *set.Power > st.PowerMax+1e-9) {
		return fmt.Errorf("power must be between %.0f%% and %.0f%%", st.PowerMin*100, st.PowerMax*100)
	}
	return nil
}
