package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCallsign(t *testing.T) {
	good := []string{"K1ABC", "K7XYZ", "N1TEST", "W1AW", "VE3ABC-7", "2E0ABC", "N0CALL"}
	bad := []string{"", "hello", "123", "KF7", "K1ABC-", "K1ABC-123"}
	for _, s := range good {
		if !ValidCallsign(s) {
			t.Errorf("%q should be valid", s)
		}
	}
	for _, s := range bad {
		if ValidCallsign(s) {
			t.Errorf("%q should be invalid", s)
		}
	}
}

func TestGrid(t *testing.T) {
	for in, want := range map[string]string{"em10": "EM10", "EM10DG": "EM10dg", " fn31PR ": "FN31pr", "FN31pr77": "FN31pr77"} {
		if !ValidGrid(in) || NormalizeGrid(in) != want {
			t.Errorf("%q -> %q (valid=%v), want %q", in, NormalizeGrid(in), ValidGrid(in), want)
		}
	}
	for _, bad := range []string{"", "EM1", "ZZ16", "EM10zz", "1616", "EM10d", "EM10dgx"} {
		if ValidGrid(bad) {
			t.Errorf("%q should not be a grid", bad)
		}
	}
	var c Config
	if err := c.Set("grid", "em10DG"); err != nil || c.Grid != "EM10dg" {
		t.Errorf("Set grid: %v %q", err, c.Grid)
	}
	if err := c.Set("grid", "nope"); err == nil || c.Grid != "EM10dg" {
		t.Errorf("a bad grid must be rejected and leave the saved one alone: %v %q", err, c.Grid)
	}
}

func TestSet(t *testing.T) {
	var c Config
	if err := c.Set("ui", "on"); err != nil || !c.UI {
		t.Fatalf("ui: %v", err)
	}
	if err := c.Set("callsign", "N0CALL"); err == nil {
		t.Error("callsign is no longer a config key (use stations)")
	}
	if err := c.Set("nope", "x"); err == nil {
		t.Error("unknown key should fail")
	}
}

func TestSaveIsAtomicAndLeavesNoTempFiles(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var c Config
	c.Grid = "EM10dg"
	for i := 0; i < 20; i++ {
		c.UI = i%2 == 0
		if err := Save(c); err != nil {
			t.Fatal(err)
		}
		got, err := Load()
		if err != nil || got.Grid != "EM10dg" || got.UI != c.UI {
			t.Fatalf("round %d: %+v %v", i, got, err)
		}
	}
	dir, _ := Dir()
	left, _ := os.ReadDir(dir)
	if len(left) != 1 || left[0].Name() != "config.json" {
		t.Errorf("only config.json should remain, got %v", left)
	}
	if st, _ := os.Stat(filepath.Join(dir, "config.json")); st.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v", st.Mode())
	}
}
