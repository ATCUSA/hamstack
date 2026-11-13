package patcfg

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// Wiring is what a station's Pat config should say about the radios at the current site.
type Wiring struct {
	Grid      string   // locator; "" leaves it alone
	Rigs      []RigRef // rigctld connections, by rig name; entries here are set, other entries are kept
	HFRig     string   // the rig whose Mercury Pat's VARA HF uses; "" leaves varahf alone
	HFArqPort int
	PacketRig string // the rig Pat tunes for packet ("" for a rig with no CAT)
	Packet    bool   // Pat's AX.25 should use Direwolf's AGWPE port
	AGWPort   int
	GPSAddr   string // gpsd's address ("host:port"); "" leaves Pat's gpsd settings alone
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

// Wire updates a Pat config to match w and returns the top-level settings it changed. Only the locator,
// hamlib_rigs, varahf, ax25, agwpe and gpsd are touched, and inside them only the members named here; every
// other setting (passwords included) is kept. If anything changes, the original is first copied to
// path+".hamstack-bak" and the file is rewritten with sorted keys. Nothing is written if nothing differs.
func Wire(path string, w Wiring) (backup string, changed []string, err error) {
	orig, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(orig, &fields); err != nil {
		return "", nil, fmt.Errorf("%s is not valid JSON: %w", path, err)
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
			return "", nil, err
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
			return "", nil, err
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
	if w.GPSAddr != "" {
		gd, err := object(fields, "gpsd") // only the address: Pat's own switches (forms, locator updates) stay as the user set them
		if err != nil {
			return "", nil, err
		}
		put(gd, "addr", w.GPSAddr)
		store(fields, "gpsd", gd)
	}
	if w.Packet {
		ax, err := object(fields, "ax25")
		if err != nil {
			return "", nil, err
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
			return "", nil, err
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
		return "", nil, nil
	}
	out, err := json.MarshalIndent(fields, "", "  ")
	if err != nil {
		return "", nil, err
	}
	backup = path + ".hamstack-bak"
	if err := os.WriteFile(backup, orig, 0o600); err != nil {
		return "", nil, err
	}
	return backup, changed, os.WriteFile(path, append(out, '\n'), 0o600)
}
