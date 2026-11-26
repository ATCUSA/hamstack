package radiosetup

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// fakeRadio answers rigctld commands from a table of settings, as the FT-DX10 did on the Pi (AGC reads 6 for AUTO, 0
// for OFF; COMP is refused; raw EX menu reads echo with the value).
type fakeRadio struct {
	levels, funcs map[string]string
	menu          map[string]string // "EX010417" -> "050"
	mode          string
	sent          []string
}

func (f *fakeRadio) Do(cmd string) ([]string, error) {
	f.sent = append(f.sent, cmd)
	parts := strings.Fields(cmd)
	switch parts[0] {
	case "m":
		return []string{f.mode, "2400"}, nil
	case "M":
		f.mode = parts[1]
		return nil, nil
	case "l":
		if v, ok := f.levels[parts[1]]; ok {
			return []string{v}, nil
		}
		return nil, fmt.Errorf("RPRT -11")
	case "L":
		f.levels[parts[1]] = parts[2]
		return nil, nil
	case "u":
		if v, ok := f.funcs[parts[1]]; ok {
			return []string{v}, nil
		}
		return nil, fmt.Errorf("Command rejected by the rig")
	case "U":
		f.funcs[parts[1]] = parts[2]
		return nil, nil
	case "w":
		c := strings.TrimSuffix(parts[1], ";")
		if len(c) == 8 { // a read: EX010417
			return []string{c + f.menu[c] + ";\x00"}, nil
		}
		f.menu[c[:8]] = c[8:] // a set: EX010417050
		return nil, nil
	}
	return nil, fmt.Errorf("unknown %q", cmd)
}

func ftdx10() *fakeRadio {
	return &fakeRadio{
		levels: map[string]string{"AGC": "0", "RFPOWER": "0.5"},
		funcs:  map[string]string{"NB": "0", "NR": "0", "ANF": "0", "MN": "0", "APF": "0"}, // COMP: refused
		menu:   map[string]string{"EX010415": "1", "EX010416": "1", "EX010417": "005", "EX010408": "1500", "EX010413": "050", "EX010414": "1"},
		mode:   "PKTUSB",
	}
}

func byID(rs []Result) map[string]Result {
	m := map[string]Result{}
	for _, r := range rs {
		m[r.Step.ID] = r
	}
	return m
}

func TestCheckTheFTDX10AsFoundOnThePi(t *testing.T) {
	p, ok := Find(1042, "winlink-vara")
	if !ok {
		t.Fatal("no bundled FT-DX10 Winlink VARA profile")
	}
	r := ftdx10()
	got := byID(Check(r, p, false))
	cases := []struct {
		id, state string
		auto      bool
	}{
		{"agc", Outside, true},         // OFF: wrong, receive-side: fixed by itself
		{"rport-gain", Outside, false}, // 005, below 30-70: wrong, but transmit-side: a Fix button only
		{"mode", OK, false},
		{"nb-off", OK, false},
		{"comp-off", Unreadable, false}, // the radio refuses the command
		{"power", OK, false},            // 0.5 as suggested
		{"data-out-level", OK, false},   // check only, no value to compare
		{"width", ManualStep, false},    // set on the radio itself
		{"data-mod-source", OK, false},  // REAR
		{"tx-bpf", OK, false},           // check only, inside one_of
	}
	for _, c := range cases {
		g, ok := got[c.id]
		if !ok || g.State != c.state || g.Auto != c.auto {
			t.Errorf("%s: %+v, want state %s auto %v", c.id, g, c.state, c.auto)
		}
	}
	if g := got["data-mod-source"]; g.NowFor != "REAR" {
		t.Errorf("values are shown by their names: %q", g.NowFor)
	}
	if got["data-out-level"].CanFix || got["tx-bpf"].CanFix {
		t.Error("check-only steps can never be fixed")
	}
	if !byID(Check(r, p, true))["rport-gain"].Auto {
		t.Error("with transmit-side fixes allowed, an out-of-range RPORT GAIN is fixed by itself")
	}
	// Different from the suggestion but within range is never fixed by itself, even with transmit allowed.
	r.levels["RFPOWER"] = "0.3"
	if g := byID(Check(r, p, true))["power"]; g.State != Differs || g.Auto {
		t.Errorf("a power setting the operator chose: %+v", g)
	}
}

func TestFixSetsAndReadsBack(t *testing.T) {
	p, _ := Find(1042, "winlink-vara")
	r := ftdx10()
	steps := map[string]Step{}
	for _, s := range p.Steps {
		steps[s.ID] = s
	}
	if err := Fix(r, steps["agc"]); err != nil || r.levels["AGC"] != "6" {
		t.Fatalf("AGC: %v %v", err, r.levels)
	}
	if err := Fix(r, steps["rport-gain"]); err != nil || r.menu["EX010417"] != "050" {
		t.Fatalf("RPORT GAIN with its zero-padded set: %v %v (sent %v)", err, r.menu, r.sent)
	}
	if err := Fix(r, steps["tx-bpf"]); err == nil {
		t.Error("a check-only step must never be set")
	}
	if err := Fix(r, steps["width"]); err == nil {
		t.Error("a manual step cannot be set")
	}
}

// Every raw CAT command in the bundle is a settings-menu read or write (Yaesu's EX menu), nothing else: the profiles
// are data, and nothing in them may key a transmitter or change the radio otherwise.
func TestBundledRawCommandsAreMenuSettingsOnly(t *testing.T) {
	read := regexp.MustCompile(`^EX\d{6};$`)
	set := regexp.MustCompile(`^EX\d{6}\{value(:0?\d+)?\};$`)
	n := 0
	for _, p := range All() {
		for _, s := range p.Steps {
			if s.Kind != "cat" || s.Cat == nil || s.Cat.Protocol != "ascii" {
				continue
			}
			n++
			if !read.MatchString(s.Cat.Read) || s.Cat.Set != "" && !set.MatchString(s.Cat.Set) {
				t.Errorf("%s %s: read %q set %q", p.ID, s.ID, s.Cat.Read, s.Cat.Set)
			}
		}
	}
	if n == 0 {
		t.Fatal("no raw commands found: the test would prove nothing")
	}
}

func TestBundleCarriesNoCallsigns(t *testing.T) {
	if regexp.MustCompile(`\b[A-Z]{1,2}[0-9][A-Z]{1,4}\b`).Match(bundle) {
		t.Error("the bundle names a callsign (AGENTS.md rule 1): rerun scripts/bundle-radio-profiles.py")
	}
}
