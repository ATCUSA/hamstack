package hamlib

import (
	"slices"
	"strings"
	"testing"
)

const sample = ` Rig #  Mfg                    Model                   Version         Status      Macro
     1  Hamlib                 Dummy                   20250811.0      Stable      RIG_MODEL_DUMMY
     2  Hamlib                 NET rigctl              20250211.0      Stable      RIG_MODEL_NETRIGCTL
  1042  Yaesu                  FTDX-10                 20241118.9      Stable      RIG_MODEL_FTDX10
  3073  Icom                   IC-7300                 20250517.14     Stable      RIG_MODEL_IC7300
  3094  Icom                   IC-7300MK2              20250517.14     Beta        RIG_MODEL_IC7300MK2
 29001  Elecraft               K3 / KX3                 20250101.1      Stable      RIG_MODEL_K3
`

func TestParse(t *testing.T) {
	m := Parse(strings.NewReader(sample))
	for id, want := range map[int]string{1: "Stable", 2: "Stable", 1042: "Stable", 3094: "Beta", 29001: "Stable"} {
		if m[id] != want {
			t.Errorf("model %d = %q, want %q", id, m[id], want)
		}
	}
	if _, ok := m[9999]; ok {
		t.Error("unlisted model reported as supported")
	}
	if len(m) != 6 {
		t.Errorf("parsed %d models, want 6 (header must be skipped): %v", len(m), m)
	}
}

const table = `initrigs4_dummy: _init called
 Rig #  Mfg                    Model                   Version         Status      Macro
     1  Hamlib                 Dummy                   20240709.0      Stable      RIG_MODEL_DUMMY
     2  Hamlib                 NET rigctl              20240418.0      Stable      RIG_MODEL_NETRIGCTL
     4  FLRig                                          20250107.0      Stable      RIG_MODEL_FLRIG
  1035  Yaesu                  FT-991                  20241118.18     Stable      RIG_MODEL_FT991
  1036  Yaesu                  FT-891                  20241118.11     Stable      RIG_MODEL_FT891
  1042  Yaesu                  FTDX-10                 20241118.9      Stable      RIG_MODEL_FTDX10
  2029  Elecraft               K3                      20250107.31     Stable      RIG_MODEL_K3
  2045  Elecraft               KX3                     20250107.22     Stable      RIG_MODEL_KX3
  3073  Icom                   IC-7300                 20250107.14     Stable      RIG_MODEL_IC7300
  3085  Icom                   IC-705                  20250107.11     Stable      RIG_MODEL_IC705
  3088  Xiegu                  G90                     20250107.11     Stable      RIG_MODEL_G90
`

func TestList(t *testing.T) {
	es := List(strings.NewReader(table))
	if len(es) != 11 {
		t.Fatalf("got %d entries", len(es))
	}
	want := map[int]string{2: "Hamlib NET rigctl", 4: "FLRig", 3073: "Icom IC-7300", 1042: "Yaesu FTDX-10"}
	for _, e := range es {
		if w, ok := want[e.ID]; ok && e.Label() != w {
			t.Errorf("%d: label %q, want %q", e.ID, e.Label(), w)
		}
		if e.ID == 3073 && (e.Status != "Stable" || e.Macro != "RIG_MODEL_IC7300") {
			t.Errorf("IC-7300: %+v", e)
		}
	}
}

func TestFilter(t *testing.T) {
	es := List(strings.NewReader(table))
	ids := func(q string) []int {
		var out []int
		for _, e := range Filter(es, q) {
			out = append(out, e.ID)
		}
		return out
	}
	eq := func(q string, want ...int) {
		t.Helper()
		if got := ids(q); !slices.Equal(got, want) {
			t.Errorf("%q: got %v, want %v", q, got, want)
		}
	}
	eq("ic7300", 3073)
	eq("IC-7300", 3073)
	eq("ic 7300", 3073)
	eq("icom", 3085, 3073) // equal matches: alphabetical
	eq("705", 3085)
	eq("ft991", 1035)     // a prefix of FT-991
	eq("yaesu 891", 1036) // two words, both must match
	eq("k3", 2029, 2045)  // K3 first (name starts with it), then KX3 by letters in order
	eq("flrig", 4)
	eq("zzz")
	eq("g90 xiegu", 3088)
	if got := Filter(es, "   "); len(got) != len(es) {
		t.Errorf("an empty query lists everything, got %d", len(got))
	}
	// A name that starts with the query beats one that merely contains it.
	if got := ids("ft"); got[0] != 1035 && got[0] != 1036 && got[0] != 1042 {
		t.Errorf("ft: %v", got)
	}
}

func TestParseCaps(t *testing.T) {
	c := ParseCaps("Model name:\tIC-7300\nPort type:\tRS-232\nSerial speed: 4800..115200 baud, 8N1, ctrl=NONE\nSerial speed: 4800..115200 baud, 8N1, ctrl=NONE\n")
	if c.Port != "RS-232" || c.MinBaud != 4800 || c.MaxBaud != 115200 || !c.Serial() || c.DefaultBaud() != 9600 {
		t.Errorf("%+v", c)
	}
	n := ParseCaps("Port type:\tNetwork\n")
	if n.Serial() || n.DefaultBaud() != 0 || n.Port != "Network" {
		t.Errorf("%+v", n)
	}
	if got := (Caps{MinBaud: 19200, MaxBaud: 57600}).DefaultBaud(); got != 19200 {
		t.Errorf("range above 9600: %d", got)
	}
	if got := (Caps{MinBaud: 300, MaxBaud: 4800}).DefaultBaud(); got != 4800 {
		t.Errorf("range below 9600: %d", got)
	}
}
