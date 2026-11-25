package main

import (
	"strings"
	"testing"

	"github.com/ATCUSA/hamstack/internal/config"
)

func TestSetRigBaud(t *testing.T) {
	r := &config.Rig{ID: "ftdx10"}
	for value, want := range map[string]int{"38400": 38400, "115200": 115200, "300": 300, "auto": 0, "": 0, "0": 0} {
		r.Baud = 9600
		if err := setRigField(r, "baud", value); err != nil || r.Baud != want {
			t.Errorf("%q: baud %d, err %v, want %d", value, r.Baud, err, want)
		}
	}
	for _, bad := range []string{"fast", "-1", "12", "9999999", "96 00"} {
		r.Baud = 4800
		if err := setRigField(r, "baud", bad); err == nil || r.Baud != 4800 {
			t.Errorf("%q must be refused and leave the speed alone: baud %d, err %v", bad, r.Baud, err)
		}
	}
	nocat := &config.Rig{ID: "tnc", NoCAT: true}
	if err := setRigField(nocat, "baud", "9600"); err == nil || !strings.Contains(err.Error(), "no CAT") {
		t.Errorf("a radio without CAT has no CAT speed: %v", err)
	}
}

// A speed set on the rig changes the rig, so the saved change must mark rig control as needing a restart,
// the way every other change to a rig does.
func TestChangingTheBaudMarksRigControlForRestart(t *testing.T) {
	before := config.Config{Rigs: []config.Rig{{ID: "ftdx10", Profile: "ftdx10", RigPort: 4533}}}
	after := config.Config{Rigs: []config.Rig{{ID: "ftdx10", Profile: "ftdx10", RigPort: 4533, Baud: 19200}}}
	got := changedServices(before, after)
	found := false
	for _, n := range got {
		found = found || n == "rigctld@ftdx10"
	}
	if !found {
		t.Errorf("changed services: %v", got)
	}
}
