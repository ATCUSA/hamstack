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
	Heard  *RadioHeard  `json:"heard,omitempty"` // what xara's monitor heard, for a rig with xara
	Setup  *SetupRef    `json:"setup,omitempty"` // the radio settings check's profile, when there is one
	// Settings are the radio's own switches (their home: Settings links here), at their levels; Expert ones only
	// while Expert mode is on.
	Settings []RadioSetting `json:"settings"`
	Expert   bool           `json:"expert"`
}

// RadioSetting is one of a radio's switches.
type RadioSetting struct {
	Key     string `json:"key"`   // for setting.set: "rig:hf:xara-monitor"; "rig-tune" goes through dashboard.tune
	Label   string `json:"label"` // "xara's monitor"
	Value   string `json:"value"` // "on", "off"
	Hint    string `json:"hint"`  //
	Level   string `json:"level"` // "", "advanced", "expert"
	Default string `json:"default,omitempty"`
	Changed bool   `json:"changed"` // differs from its default: Back to default puts it back
}

// SetupRef is the radio settings check for a rig before it runs: the profile in use (of the program that has the radio
// now), the profile each program gets, and the radio's profiles with what each is recommended for.
type SetupRef struct {
	Title    string         `json:"title"`          // the profile in use; "" when nothing with a profile has the radio
	Note     string         `json:"note,omitempty"` // "the profile written for VARA HF, the closest there is for Mercury"
	Profile  string         `json:"profile"`        // its id
	Use      string         `json:"use"`            // the program that has the radio: "Data2G"
	AutoTX   bool           `json:"autoTX"`         // transmit-side settings outside their range are fixed by themselves too
	Uses     []SetupUse     `json:"uses"`
	Profiles []SetupProfile `json:"profiles"`
}

// SetupUse is one program that can have the radio, and its profile.
type SetupUse struct {
	Name        string `json:"name"`  // "wsjtx"
	Title       string `json:"title"` // "WSJT-X"
	Profile     string `json:"profile"`
	Recommended string `json:"recommended"`
	Picked      string `json:"picked"` // the owner's pick: "" for the recommended one, "none", or a profile id
	Note        string `json:"note,omitempty"`
	Now         bool   `json:"now"` // it has the radio now
}

// SetupProfile is one of the radio's profiles.
type SetupProfile struct {
	ID    string   `json:"id"`
	Title string   `json:"title"`
	For   []string `json:"for,omitempty"` // the programs it is recommended for
}

// SetupView is the radio settings check's result: each setting as read, the profile's value and what to do.
type SetupView struct {
	Title   string     `json:"title"`
	Profile string     `json:"profile"`
	Note    string     `json:"note,omitempty"`
	Credit  string     `json:"credit"`
	Rows    []SetupRow `json:"rows"`
}

// SetupRow is one setting checked.
type SetupRow struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Now        string `json:"now"`
	Want       string `json:"want"`
	State      string `json:"state"` // ok, outside, differs, unreadable, manual
	CanFix     bool   `json:"canFix"`
	Auto       bool   `json:"auto"`
	TX         bool   `json:"tx"` // it changes what goes on the air
	CheckOnly  bool   `json:"checkOnly"`
	Note       string `json:"note,omitempty"`
	Confidence string `json:"confidence,omitempty"`
	Menu       string `json:"menu,omitempty"` // for a setting made on the radio itself
}

// RadioHeard is what xara's monitor reports: the VARA HF links it hears between other stations, and the text of the
// rig's own links. Lines come from the air and are cleaned before they get here.
type RadioHeard struct {
	Monitor bool        `json:"monitor"` // the monitor is turned on
	Running bool        `json:"running"` // xara runs now
	Lines   []HeardLine `json:"lines"`
}

// HeardLine is one line of it, with the time it was logged.
type HeardLine struct {
	Time string `json:"time"`
	Text string `json:"text"`
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
	// Password is for a modem whose page has a password of its own (Data2G): "set", or "none" while the page is locked.
	Password string `json:"password,omitempty"`
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
	RadioSetup(id, profile string) (SetupView, error) // reads the radio's settings (a few seconds); profile "" is the one in use
}

func (s *Server) radioSetup(w http.ResponseWriter, r *http.Request) {
	be, ok := s.be.(Radios)
	if !ok {
		http.NotFound(w, r)
		return
	}
	v, err := be.RadioSetup(r.PathValue("id"), r.URL.Query().Get("profile"))
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(v)
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
