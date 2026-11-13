// Package patcfg creates and reads Pat (Winlink client) config files.
package patcfg

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Options says what a starter Pat config should contain.
type Options struct {
	Callsign, HTTPAddr string
	Grid               string   // locator; omitted when empty
	Rigs               []RigRef // every rig, so Pat can tune any of them
	HFRig              string   // the rig whose Mercury Pat's VARA HF connection uses; "" for none
	HFArqPort          int      // that Mercury's ARQ port
	Packet             bool     // Direwolf is part of the setup: use its AGWPE interface for AX.25
	PacketRig          string   // the rig Pat tunes for packet connections ("" if it has no CAT)
	AGWPort            int
}

// RigRef names a rig and the port its rigctld listens on.
type RigRef struct {
	Name string
	Port int
}

// Minimal returns a starter Pat config for a station that talks to Mercury through
// rigctld. Pat fills in every other setting with its own defaults. The Winlink
// password is NOT set: the user adds it in Pat (web UI settings or `pat configure`).
//
// With modem=false (Mercury is not part of the setup) only the callsign and web UI address
// are written, which is enough for Winlink over the internet.
func Minimal(o Options) ([]byte, error) {
	cfg := map[string]any{
		"mycall":    o.Callsign,
		"http_addr": o.HTTPAddr,
	}
	if o.Grid != "" {
		cfg["locator"] = o.Grid
	}
	if len(o.Rigs) > 0 {
		rigs := map[string]any{}
		for _, r := range o.Rigs {
			rigs[r.Name] = map[string]any{"network": "tcp", "address": "localhost:" + itoa(r.Port)}
		}
		cfg["hamlib_rigs"] = rigs
	}
	if o.HFRig != "" {
		cfg["varahf"] = map[string]any{
			"addr": "localhost:" + itoa(o.HFArqPort), "bandwidth": 2300, "rig": o.HFRig, "ptt_ctrl": true,
		}
	}
	if o.Packet {
		// VHF/UHF packet through Direwolf's AGWPE interface.
		ax := map[string]any{"engine": "agwpe"}
		if o.PacketRig != "" {
			ax["rig"] = o.PacketRig
		}
		cfg["ax25"] = ax
		cfg["agwpe"] = map[string]any{"addr": "localhost:" + itoa(o.AGWPort), "radio_port": 0}
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	return append(b, '\n'), err
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// HFPort returns the port of the VARA HF modem a Pat config points at (varahf.addr), and whether it
// has one. Only that field is read.
func HFPort(path string) (int, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	var c struct {
		VaraHF struct {
			Addr string `json:"addr"`
		} `json:"varahf"`
	}
	if json.Unmarshal(b, &c) != nil || c.VaraHF.Addr == "" {
		return 0, false
	}
	i := strings.LastIndex(c.VaraHF.Addr, ":")
	if i < 0 {
		return 0, false
	}
	n, err := strconv.Atoi(c.VaraHF.Addr[i+1:])
	return n, err == nil
}

// Locator returns the locator set in a Pat config, and whether there is one. Only that field is read.
func Locator(path string) (string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var c struct {
		Locator string `json:"locator"`
	}
	if json.Unmarshal(b, &c) != nil || strings.TrimSpace(c.Locator) == "" {
		return "", false
	}
	return strings.TrimSpace(c.Locator), true
}

// SetLocator changes only the locator in a Pat config. Every other value is kept exactly as it is
// (passwords included, which are never looked at), but the file is rewritten with sorted keys, so the
// original is first copied to path+".hamstack-bak". It returns the backup's path.
func SetLocator(path, grid string) (backup string, err error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		return "", fmt.Errorf("%s is not valid JSON: %w", path, err)
	}
	loc, _ := json.Marshal(grid)
	fields["locator"] = loc
	out, err := json.MarshalIndent(fields, "", "  ")
	if err != nil {
		return "", err
	}
	backup = path + ".hamstack-bak"
	if err := os.WriteFile(backup, b, 0o600); err != nil {
		return "", err
	}
	return backup, os.WriteFile(path, append(out, '\n'), 0o600)
}

// PacketPort returns the port of the AGWPE TNC (Direwolf) a Pat config uses for AX.25, and whether it is
// set up for AGWPE at all. Only those fields are read.
func PacketPort(path string) (int, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	var c struct {
		AX25 struct {
			Engine string `json:"engine"`
		} `json:"ax25"`
		AGWPE struct {
			Addr string `json:"addr"`
		} `json:"agwpe"`
	}
	if json.Unmarshal(b, &c) != nil || c.AX25.Engine != "agwpe" || c.AGWPE.Addr == "" {
		return 0, false
	}
	i := strings.LastIndex(c.AGWPE.Addr, ":")
	if i < 0 {
		return 0, false
	}
	n, err := strconv.Atoi(c.AGWPE.Addr[i+1:])
	return n, err == nil
}

// Found is a Pat config discovered on disk.
type Found struct {
	File     string
	ID       string // suggested station id
	Callsign string
	HTTPAddr string
	Default  bool   // config.json (Pat's default config) points at this file
	Grid     string // the locator in that file, if any (a suggestion for the operator's grid)
}

var badID = regexp.MustCompile(`[^a-z0-9_-]+`)

// Scan reads the top-level *.json files in dir and returns those that name a callsign.
// Only mycall, http_addr and locator are read; nothing else (passwords included) is looked at.
// Files that are symlinks to another file in the same scan are reported once.
func Scan(dir string) ([]Found, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	// Named files first, generic config.json last: when config.json is a symlink to
	// a named profile, the named one is the one reported.
	sort.SliceStable(files, func(i, j int) bool {
		ci, cj := filepath.Base(files[i]) == "config.json", filepath.Base(files[j]) == "config.json"
		if ci != cj {
			return cj
		}
		return files[i] < files[j]
	})
	seen := map[string]int{} // resolved path -> index in out
	var out []Found
	for _, f := range files {
		real, err := filepath.EvalSymlinks(f)
		if err != nil {
			continue
		}
		if i, dup := seen[real]; dup {
			if filepath.Base(f) == "config.json" {
				out[i].Default = true
			}
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var c struct {
			MyCall   string `json:"mycall"`
			HTTPAddr string `json:"http_addr"`
			Locator  string `json:"locator"`
		}
		if json.Unmarshal(b, &c) != nil || strings.TrimSpace(c.MyCall) == "" {
			continue
		}
		seen[real] = len(out)
		id := strings.ToLower(strings.TrimSuffix(filepath.Base(f), ".json"))
		id = strings.Trim(badID.ReplaceAllString(id, "-"), "-")
		if id == "" || id == "config" {
			id = badID.ReplaceAllString(strings.ToLower(strings.TrimSpace(c.MyCall)), "-")
		}
		out = append(out, Found{File: f, ID: id, Callsign: strings.ToUpper(strings.TrimSpace(c.MyCall)), HTTPAddr: c.HTTPAddr,
			Grid: strings.TrimSpace(c.Locator), Default: filepath.Base(f) == "config.json"})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
