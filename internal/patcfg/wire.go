package patcfg

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
)

// Wiring is what a station's Pat config should say about the radios at the current site.
type Wiring struct {
	Grid      string   // locator; "" leaves it alone
	Rigs      []RigRef // rigctld connections, by rig name; entries here are set, other entries are kept
	HFRig     string   // the rig whose Mercury Pat's VARA HF uses; "" leaves varahf alone
	HFArqPort int
	ARDOPPort int    // the command port of hamstack's ardopcf, when one is set up: Pat's ARDOP then connects to it and keys the radio through rigctld (0 leaves that to the person)
	ARDOPRig  string // the rig Pat's ARDOP transport tunes and keys; only filled in when ardop.rig is empty ("" leaves ardop alone)
	// ARDOPPrevPort is where hamstack's ardopcf listened before its rig gained a VARA-style modem and it moved to
	// ports of its own: Pat pointing there is hamstack's setting, not the person's, so it follows (0: none).
	ARDOPPrevPort int
	PacketRig     string // the rig Pat tunes for packet ("" for a rig with no CAT)
	Packet        bool   // Pat's AX.25 should use Direwolf's AGWPE port
	AGWPort       int
	GPSAddr       string // gpsd's address ("host:port"); "" leaves Pat's gpsd settings alone
}

// Change is one setting that differs. Path is dotted ("ardop.rig", "hamlib_rigs.ftdx10.address"); Old and New are
// the values as JSON text. WasSet is false when the setting was not there at all, which undoing removes again.
type Change struct {
	Path   string `json:"path"`
	Old    string `json:"old"`
	New    string `json:"new"`
	WasSet bool   `json:"wasSet"`
}

// object reads a JSON object field as raw members, or an empty one if the field is absent.
func object(fields map[string]json.RawMessage, key string) (map[string]json.RawMessage, error) {
	raw, ok := fields[key]
	if !ok {
		return map[string]json.RawMessage{}, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return nil, fmt.Errorf("%q is not an object", key)
	}
	return m, nil
}

func put(m map[string]json.RawMessage, key string, v any) {
	b, _ := json.Marshal(v)
	m[key] = b
}

func store(fields map[string]json.RawMessage, key string, m map[string]json.RawMessage) {
	b, _ := json.Marshal(m) // object members are sorted; their values are kept verbatim
	fields[key] = b
}

func isEmptyString(raw json.RawMessage) bool {
	var s string
	return json.Unmarshal(raw, &s) == nil && strings.TrimSpace(s) == ""
}

// Wire updates a Pat config to match w and returns the top-level settings it changed. Only the locator,
// hamlib_rigs, varahf, ardop, ax25, agwpe and gpsd are touched, and inside them only the members named here; every
// other setting (passwords included) is kept. If anything changes, the config as it was before hamstack first
// changed it is copied to path+".hamstack-bak" (once: later changes leave that copy alone) and the file is
// rewritten with sorted keys. Nothing is written if nothing differs.
func Wire(path string, w Wiring) (backup string, changed []string, err error) {
	backup, changed, _, err = wire(path, w, false)
	return
}

// Plan says what Wire would change, member by member, and changes nothing.
func Plan(path string, w Wiring) ([]Change, error) {
	_, _, changes, err := wire(path, w, true)
	return changes, err
}

// Apply is Wire, and also returns what it changed member by member, so it can be undone with Revert.
func Apply(path string, w Wiring) (backup string, changes []Change, err error) {
	backup, _, changes, err = wire(path, w, false)
	return
}

func wire(path string, w Wiring, dry bool) (backup string, changed []string, changes []Change, err error) {
	orig, err := os.ReadFile(path)
	if err != nil {
		return "", nil, nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(orig, &fields); err != nil {
		return "", nil, nil, fmt.Errorf("%s is not valid JSON: %w", path, err)
	}
	before := map[string]string{}
	for k, v := range fields {
		before[k] = string(v)
	}

	if w.Grid != "" {
		put(fields, "locator", w.Grid)
	}
	if len(w.Rigs) > 0 {
		rigs, err := object(fields, "hamlib_rigs")
		if err != nil {
			return "", nil, nil, err
		}
		for _, r := range w.Rigs {
			entry, _ := object(rigs, r.Name) // keep any other members the user set on this rig
			put(entry, "network", "tcp")
			put(entry, "address", "localhost:"+itoa(r.Port))
			store(rigs, r.Name, entry)
		}
		store(fields, "hamlib_rigs", rigs)
	}
	if w.HFRig != "" {
		vh, err := object(fields, "varahf")
		if err != nil {
			return "", nil, nil, err
		}
		put(vh, "addr", "localhost:"+itoa(w.HFArqPort))
		put(vh, "rig", w.HFRig)
		if _, ok := vh["bandwidth"]; !ok {
			put(vh, "bandwidth", 2300)
		}
		if _, ok := vh["ptt_ctrl"]; !ok {
			put(vh, "ptt_ctrl", true)
		}
		store(fields, "varahf", vh)
	}
	if w.ARDOPRig != "" {
		// Only the rig, and only when none is set: with a rig named, Pat can tune the radio for ARDOP. Who keys the
		// transmitter (Pat through rigctld, or the TNC) and where the TNC listens depend on which ARDOP program is
		// used, so those are left to the person.
		ar, err := object(fields, "ardop")
		if err != nil {
			return "", nil, nil, err
		}
		if raw, ok := ar["rig"]; !ok || isEmptyString(raw) {
			put(ar, "rig", w.ARDOPRig)
			store(fields, "ardop", ar)
		}
		// hamstack runs ardopcf for this rig: point Pat at it, and have Pat key the radio, because ardopcf cannot
		// reach rigctld itself. Only when the modem address is empty or already ours: an address the person
		// chose (another computer, another program) is left alone, with its keying.
		if w.ARDOPPort > 0 {
			ours := "localhost:" + itoa(w.ARDOPPort)
			prev := `"localhost:` + itoa(w.ARDOPPrevPort) + `"`
			if raw, ok := ar["addr"]; !ok || isEmptyString(raw) || string(raw) == `"`+ours+`"` || (w.ARDOPPrevPort > 0 && string(raw) == prev) {
				put(ar, "addr", ours)
				put(ar, "ptt_ctrl", true)
				store(fields, "ardop", ar)
			}
		}
	}
	if w.GPSAddr != "" {
		gd, err := object(fields, "gpsd") // only the address: Pat's own switches (forms, locator updates) stay as the user set them
		if err != nil {
			return "", nil, nil, err
		}
		put(gd, "addr", w.GPSAddr)
		store(fields, "gpsd", gd)
	}
	if w.Packet {
		ax, err := object(fields, "ax25")
		if err != nil {
			return "", nil, nil, err
		}
		put(ax, "engine", "agwpe")
		if w.PacketRig != "" {
			put(ax, "rig", w.PacketRig)
		} else {
			delete(ax, "rig")
		}
		store(fields, "ax25", ax)
		agw, err := object(fields, "agwpe")
		if err != nil {
			return "", nil, nil, err
		}
		put(agw, "addr", "localhost:"+itoa(w.AGWPort))
		if _, ok := agw["radio_port"]; !ok {
			put(agw, "radio_port", 0)
		}
		store(fields, "agwpe", agw)
	}

	// Compare semantically (key order inside the objects we rebuilt is sorted), not byte for byte.
	same := func(a, b string) bool {
		var x, y any
		if json.Unmarshal([]byte(a), &x) != nil || json.Unmarshal([]byte(b), &y) != nil {
			return a == b
		}
		xb, _ := json.Marshal(x)
		yb, _ := json.Marshal(y)
		return bytes.Equal(xb, yb)
	}
	for k, v := range fields {
		if old, had := before[k]; !had || !same(old, string(v)) {
			changed = append(changed, k)
		}
	}
	sort.Strings(changed)
	if len(changed) == 0 {
		return "", nil, nil, nil
	}
	for _, k := range changed {
		var o, n any
		old, had := before[k]
		if had {
			_ = json.Unmarshal([]byte(old), &o)
		}
		_ = json.Unmarshal(fields[k], &n)
		changes = append(changes, diffAny(k, o, n, had)...)
	}
	if dry {
		return "", changed, changes, nil
	}
	out, err := json.MarshalIndent(fields, "", "  ")
	if err != nil {
		return "", nil, nil, err
	}
	backup = path + ".hamstack-bak"
	if _, statErr := os.Stat(backup); statErr != nil { // the config as it was before hamstack first changed it: not replaced later
		if err := os.WriteFile(backup, orig, 0o600); err != nil {
			return "", nil, nil, err
		}
	}
	return backup, changed, changes, os.WriteFile(path, append(out, '\n'), 0o600)
}

// diffAny lists the leaf settings that differ between two decoded JSON values.
func diffAny(path string, old, new any, hadOld bool) []Change {
	om, oOK := old.(map[string]any)
	nm, nOK := new.(map[string]any)
	if nOK && (oOK || !hadOld) {
		keys := map[string]bool{}
		for k := range om {
			keys[k] = true
		}
		for k := range nm {
			keys[k] = true
		}
		var names []string
		for k := range keys {
			names = append(names, k)
		}
		sort.Strings(names)
		var out []Change
		for _, k := range names {
			ov, had := om[k]
			nv, nowHas := nm[k]
			if !nowHas {
				continue // a member that is gone is not something the wiring does
			}
			out = append(out, diffAny(path+"."+k, ov, nv, had && hadOld)...)
		}
		return out
	}
	if hadOld && reflect.DeepEqual(old, new) {
		return nil
	}
	ob, _ := json.Marshal(old)
	nb, _ := json.Marshal(new)
	c := Change{Path: path, New: string(nb), WasSet: hadOld}
	if hadOld {
		c.Old = string(ob)
	}
	return []Change{c}
}
