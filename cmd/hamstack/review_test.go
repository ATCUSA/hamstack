package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/hamlib"
	"github.com/ATCUSA/hamstack/internal/profile"
	"github.com/ATCUSA/hamstack/internal/usbdev"
)

// Re-running setup and choosing another radio for the existing rig used to change only the profile name:
// the "no CAT" flag, the modem setup had just said it would switch, and the old radio's saved USB devices
// all stayed as they were.
func TestChangeRigRadioKeepsTheRigConsistent(t *testing.T) {
	profs, _ := profile.All("")
	dig, ic := findProfile(profs, "digirig"), findProfile(profs, "ic7300mk2")
	if dig == nil || ic == nil {
		t.Fatal("built-in profiles missing")
	}
	a, out := quiet("")
	r := config.Rig{ID: "hf", Profile: "ic7300mk2", Modem: config.Mercury, CAT: "/dev/serial/by-id/old-radio", CATID: "0c26:0052:1", Audio: "CODEC", AudioUSB: "0d8c:0013"}
	modem := reconcileModem(a, *dig, config.Mercury)
	changeRigRadio(a, config.Config{}, &r, *dig, modem, usbdev.Snapshot{})
	if !r.NoCAT || r.Modem != config.Direwolf || r.Profile != "digirig" {
		t.Errorf("after choosing a Digirig: %+v", r)
	}
	if r.CAT != "" || r.CATID != "" || r.Audio != "" || r.AudioUSB != "" {
		t.Errorf("the old radio's devices were kept: %+v", r)
	}
	if !strings.Contains(out.String(), "hamstack rig identify hf") {
		t.Errorf("with the new radio unplugged, say how to find it later:\n%s", out.String())
	}
	// Back to a CAT radio that is plugged in: it is found, and the flag follows.
	changeRigRadio(a, config.Config{}, &r, *ic, config.Mercury, oneRadio())
	if r.NoCAT || r.Modem != config.Mercury || r.CAT == "" {
		t.Errorf("after choosing the IC-7300 MK2: %+v", r)
	}
	// Choosing the same radio again keeps its devices.
	cat := r.CAT
	changeRigRadio(a, config.Config{}, &r, *ic, config.Mercury, usbdev.Snapshot{})
	if r.CAT != cat {
		t.Errorf("the same radio lost its port: %+v", r)
	}
}

func TestNewProfileIDsAreAlwaysUsable(t *testing.T) {
	existing := []profile.Profile{{ID: "acme-one"}}
	for _, c := range []struct {
		e    hamlib.Entry
		want string
	}{
		{hamlib.Entry{ID: 2045, Mfg: "Elecraft", Name: "KX3"}, "elecraft-kx3"},
		{hamlib.Entry{ID: 77, Mfg: "ACME", Name: "One"}, "acme-one-77"}, // the plain id belongs to another model
		{hamlib.Entry{ID: 9, Mfg: "", Name: "???"}, "hamlib-9"},
		{hamlib.Entry{ID: 5, Mfg: "A Very Long Maker Name Indeed", Name: "With A Very Long Model Name"}, "a-very-long-maker-name-indeed-wi"},
	} {
		p := newProfileFor(c.e, 9600, existing)
		if p.ID != c.want || !config.ValidID(p.ID) {
			t.Errorf("%+v: id %q (valid %v), want %q", c.e, p.ID, config.ValidID(p.ID), c.want)
		}
	}
}

func TestAX25PortNameIsChecked(t *testing.T) {
	var cfg config.Config
	rig := config.Rig{ID: "pkt", Modem: config.Direwolf}
	if _, err := ax25PortFrom(cfg, rig, config.AX25{Call: "K0TEST-1", Port: "bad name\nevil 1 2 3 4"}); err == nil {
		t.Error("a port name with spaces or newlines must not reach the axports file")
	}
	if p, err := ax25PortFrom(cfg, rig, config.AX25{Call: "K0TEST-1"}); err != nil || p.Name != "hamstack" {
		t.Errorf("%+v %v", p, err)
	}
}

func TestAConflictInsideARequirementIsCaught(t *testing.T) {
	// Chosen programs that conflict directly.
	if _, err := expandSelection(strings.NewReader(""), io.Discard, []string{"wsjtx", "wsjtx-improved"}, false); err == nil || !strings.Contains(err.Error(), "cannot both be installed") {
		t.Errorf("direct conflict: %v", err)
	}
	if got, err := expandSelection(strings.NewReader(""), io.Discard, []string{"fldigi", "flrig"}, false); err != nil || len(got) != 2 {
		t.Errorf("no conflict: %v %v", got, err)
	}
}

func TestPathHint(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	capture := func(path string) string {
		t.Setenv("PATH", path)
		r, w, _ := os.Pipe()
		old := os.Stderr
		os.Stderr = w
		pathHint()
		w.Close()
		os.Stderr = old
		var b bytes.Buffer
		b.ReadFrom(r)
		return b.String()
	}
	if out := capture("/usr/bin:" + home + "/.local/bin/"); out != "" { // a trailing slash is the same directory
		t.Errorf("~/.local/bin is on PATH, but: %q", out)
	}
	if out := capture("/usr/bin"); !strings.Contains(out, ".local/bin is not on your PATH") {
		t.Errorf("a missing PATH entry is explained: %q", out)
	}
}
