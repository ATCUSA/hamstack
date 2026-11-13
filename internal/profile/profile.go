// Package profile describes supported radios and interfaces as data.
package profile

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed profiles/*.json
var builtin embed.FS

// Profile is one radio/interface combination.
type Profile struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Rig  struct {
		Model      int    `json:"model"`       // hamlib model number
		Baud       int    `json:"baud"`        // CAT baud; 0 for none (dummy rig)
		DeviceGlob string `json:"device_glob"` // glob for the CAT serial device; "" for none
	} `json:"rig"`
	Packet struct {
		// How the radio is keyed for packet: "rig" (through rigctld, needs CAT), "rts" or "dtr" (a serial
		// modem-control line, as on a Digirig), "cm108" (a GPIO pin on a USB sound chip) or "none" (VOX).
		PTT string `json:"ptt"`
	} `json:"packet"`
	USBIDs []string `json:"usb_ids"` // vendor:product of the radio's USB serial chip, used to recognise it
	Audio  struct {
		CardHints []string `json:"card_hints"` // ALSA card ids to look for, in order
		Null      bool     `json:"null"`       // no sound card (testing)
	} `json:"audio"`
	Notes string `json:"notes"`
}

// All returns the built-in profiles plus those in userDir (a user file wins on an id clash). A user file that
// cannot be read or is not a valid profile is left out rather than failing everything: every service start
// reads the profiles, so one broken file must not stop radios that have nothing to do with it. Problems
// reports such files.
func All(userDir string) ([]Profile, error) {
	out, _, err := load(userDir)
	return out, err
}

// Problems describes the files in userDir that All left out, one error per file.
func Problems(userDir string) []error {
	_, bad, _ := load(userDir)
	return bad
}

func load(userDir string) (out []Profile, bad []error, err error) {
	byID := map[string]Profile{}
	entries, err := builtin.ReadDir("profiles")
	if err != nil {
		return nil, nil, err
	}
	for _, e := range entries {
		b, err := builtin.ReadFile("profiles/" + e.Name())
		if err != nil {
			return nil, nil, err
		}
		p, err := parse(b, e.Name())
		if err != nil {
			return nil, nil, err
		}
		byID[p.ID] = p
	}
	if userDir != "" {
		files, _ := filepath.Glob(filepath.Join(userDir, "*.json"))
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				bad = append(bad, err)
				continue
			}
			p, err := parse(b, f)
			if err != nil {
				bad = append(bad, err)
				continue
			}
			byID[p.ID] = p
		}
	}
	out = make([]Profile, 0, len(byID))
	for _, p := range byID {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, bad, nil
}

func parse(b []byte, name string) (Profile, error) {
	var p Profile
	if err := json.Unmarshal(b, &p); err != nil {
		return p, fmt.Errorf("profile %s: %w", name, err)
	}
	if p.ID == "" {
		return p, fmt.Errorf("profile %s: id is required", name)
	}
	return p, nil
}

// HasCAT reports whether the radio can be controlled over CAT (it has a hamlib model). An interface
// that only keys the radio and carries audio, such as a Digirig, has no CAT.
func (p Profile) HasCAT() bool { return p.Rig.Model > 0 }

// PacketPTT is how the radio is keyed for packet, defaulting to rigctld for a radio with CAT and to
// VOX (no keying line) for one without.
func (p Profile) PacketPTT() string {
	if p.Packet.PTT != "" {
		return p.Packet.PTT
	}
	if p.HasCAT() {
		return "rig"
	}
	return "none"
}

// Detect returns the profiles whose CAT device is present right now. glob is filepath.Glob in
// normal use. Profiles without a device pattern (the dummy radio) never match.
func Detect(profiles []Profile, glob func(string) ([]string, error)) []Profile {
	var out []Profile
	for _, p := range profiles {
		if p.Rig.DeviceGlob == "" {
			continue
		}
		if m, err := glob(p.Rig.DeviceGlob); err == nil && len(m) > 0 {
			out = append(out, p)
		}
	}
	return out
}

// Guess returns the profiles whose usb_ids include vid:pid. Many radios share one serial chip, so
// more than one profile can match; the caller should offer them rather than choose.
func Guess(profiles []Profile, vid, pid string) []Profile {
	want := strings.ToLower(vid + ":" + pid)
	var out []Profile
	for _, p := range profiles {
		for _, id := range p.USBIDs {
			if strings.ToLower(id) == want {
				out = append(out, p)
				break
			}
		}
	}
	return out
}

// Find returns the profile with the given id.
func Find(userDir, id string) (Profile, error) {
	all, bad, err := load(userDir)
	if err != nil {
		return Profile{}, err
	}
	for _, p := range all {
		if p.ID == id {
			return p, nil
		}
	}
	if len(bad) > 0 { // the missing profile may well be the file that could not be read
		return Profile{}, fmt.Errorf("unknown profile %q (see: hamstack profiles); note: %v", id, bad[0])
	}
	return Profile{}, fmt.Errorf("unknown profile %q (see: hamstack profiles)", id)
}
