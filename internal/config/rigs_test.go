package config

import (
	"slices"
	"testing"
)

func TestAddRigAllocatesPorts(t *testing.T) {
	var c Config
	for _, id := range []string{"hf", "vhf", "uhf"} {
		if err := c.AddRig(Rig{ID: id, Profile: "dummy", Modem: Mercury}); err != nil {
			t.Fatal(err)
		}
	}
	for i, r := range c.Rigs {
		if r.RigPort != 4532+i || r.ArqPort != 8300+10*i {
			t.Errorf("rig %s: rig port %d arq port %d", r.ID, r.RigPort, r.ArqPort)
		}
	}
	// Ports are kept when a rig is removed, so the others never renumber under Pat's config.
	if err := c.RemoveRig("vhf"); err != nil {
		t.Fatal(err)
	}
	if err := c.AddRig(Rig{ID: "new", Profile: "dummy"}); err != nil {
		t.Fatal(err)
	}
	if r, _ := c.RigByID("uhf"); r.RigPort != 4534 || r.ArqPort != 8320 {
		t.Errorf("removing a rig renumbered another: %+v", r)
	}
	if r, _ := c.RigByID("new"); r.RigPort != 4533 || r.ArqPort != 8310 {
		t.Errorf("a new rig should reuse the freed ports: %+v", r)
	}
}

func TestMercuryPortsNeverCollide(t *testing.T) {
	var c Config
	seen := map[int]string{}
	for _, id := range []string{"a", "b", "c", "d"} {
		c.AddRig(Rig{ID: id, Profile: "dummy", Modem: Mercury})
		r, _ := c.RigByID(id)
		arq, data, bc, ui := MercuryPorts(*r)
		for name, p := range map[string]int{"arq": arq, "data": data, "broadcast": bc, "ui": ui} {
			if other, dup := seen[p]; dup {
				t.Errorf("rig %s %s port %d collides with %s", id, name, p, other)
			}
			seen[p] = id + " " + name
		}
	}
	r, _ := c.RigByID("a")
	if arq, data, bc, ui := MercuryPorts(*r); arq != 8300 || data != 8301 || bc != 8100 || ui != 10000 {
		t.Errorf("the first rig keeps Mercury's defaults: %d %d %d %d", arq, data, bc, ui)
	}
}

func TestAddRigValidation(t *testing.T) {
	var c Config
	for _, bad := range []Rig{
		{ID: "Bad Id", Profile: "x"}, {ID: "ok"}, {ID: "ok", Profile: "x", Modem: "vara"}, {ID: "ok", Profile: "x", Bands: []string{"hf", "cb"}},
	} {
		if err := c.AddRig(bad); err == nil {
			t.Errorf("should reject %+v", bad)
		}
	}
	if len(c.Rigs) != 0 {
		t.Error("rejected rigs must not be added")
	}
	if err := c.AddRig(Rig{ID: "a", Profile: "x", Bands: []string{" HF ", "vhf", "hf"}}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(c.Rigs[0].Bands, []string{"hf", "vhf"}) {
		t.Errorf("bands = %v", c.Rigs[0].Bands)
	}
	if err := c.AddRig(Rig{ID: "a", Profile: "x"}); err == nil {
		t.Error("duplicate id")
	}
}

func TestOffRigsAreIgnored(t *testing.T) {
	var c Config
	c.AddRig(Rig{ID: "hf", Profile: "x", Modem: Mercury})
	c.AddRig(Rig{ID: "vhf", Profile: "x"})
	if !c.Has(RigCtld) || !c.Has(Mercury) || !c.Has(Pat) || !c.NeedsRadio() {
		t.Fatal("expected everything on")
	}
	c.Rigs[0].Off = true
	if c.Has(Mercury) {
		t.Error("the only Mercury rig is off")
	}
	if hf, ok := c.PatHFRig(); ok {
		t.Errorf("no active Mercury rig, but PatHFRig = %+v", hf)
	}
	c.Rigs[1].Off = true
	if c.NeedsRadio() || c.Has(RigCtld) {
		t.Error("all rigs off means no radio")
	}
	c.NoPat = true
	if len(c.Components()) != 0 || c.NeedsStation() {
		t.Errorf("nothing in use: %v", c.Components())
	}
}

func TestMigrateFromSingleRadioConfig(t *testing.T) {
	cases := []struct {
		name      string
		legacy    string
		chosen    []string
		wantRigs  int
		wantModem string
		wantNoPat bool
	}{
		{"default: everything", "ic7300mk2", nil, 1, Mercury, false},
		{"full explicit", "ftdx10", []string{"rigctld", "mercury", "pat"}, 1, Mercury, false},
		{"pat only", "", []string{"pat"}, 0, "", false},
		{"modem for other software", "ic7300mk2", []string{"rigctld", "mercury"}, 1, Mercury, true},
		{"rig control only", "ic7300mk2", []string{"rigctld"}, 1, "", true},
		{"radio profile left from a pat-only setup", "dummy", []string{"pat"}, 0, "", false},
	}
	for _, tc := range cases {
		c := Config{LegacyProfile: tc.legacy, Chosen: tc.chosen}
		if !c.migrate() {
			t.Errorf("%s: should migrate", tc.name)
			continue
		}
		if len(c.Rigs) != tc.wantRigs || c.NoPat != tc.wantNoPat || c.LegacyProfile != "" || c.Chosen != nil {
			t.Errorf("%s: %+v", tc.name, c)
			continue
		}
		if tc.wantRigs == 1 {
			r := c.Rigs[0]
			if r.Modem != tc.wantModem || r.Profile != tc.legacy || r.ID != tc.legacy || r.RigPort != 4532 || r.ArqPort != 8300 {
				t.Errorf("%s: rig %+v", tc.name, r)
			}
		}
		if c.migrate() {
			t.Errorf("%s: migrating twice must change nothing", tc.name)
		}
	}
	// A config that already uses rigs is left alone.
	c := Config{}
	c.AddRig(Rig{ID: "a", Profile: "x"})
	if c.migrate() {
		t.Error("a modern config must not be migrated")
	}
}

func TestSetProfileShorthand(t *testing.T) {
	var c Config
	if err := c.Set("profile", "dummy"); err == nil {
		t.Error("no rig yet: should point at rig add")
	}
	c.AddRig(Rig{ID: "a", Profile: "x"})
	if err := c.Set("profile", "y"); err != nil || c.Rigs[0].Profile != "y" {
		t.Errorf("single rig: %v %+v", err, c.Rigs)
	}
	c.AddRig(Rig{ID: "b", Profile: "x"})
	if err := c.Set("profile", "z"); err == nil || c.Rigs[0].Profile != "y" {
		t.Errorf("several rigs: ambiguous, must refuse and change nothing: %v", err)
	}
	if err := c.Set("components", "pat"); err == nil {
		t.Error("components is gone; the error should explain the new commands")
	}
}

func TestDirewolfRigs(t *testing.T) {
	var c Config
	c.AddRig(Rig{ID: "hf", Profile: "x", Modem: Mercury})
	c.AddRig(Rig{ID: "pkt", Profile: "x", Modem: Direwolf})
	c.AddRig(Rig{ID: "pkt2", Profile: "x", Modem: Direwolf})
	if !c.Has(Direwolf) || !c.Has(Mercury) || !c.NeedsStation() {
		t.Fatal("both modems in use")
	}
	if !slices.Equal(c.Components(), []string{RigCtld, Mercury, Direwolf, Pat}) {
		t.Errorf("components = %v", c.Components())
	}
	if pk, ok := c.PatPacketRig(); !ok || pk.ID != "pkt" {
		t.Errorf("Pat's packet rig is the first Direwolf rig: %+v %v", pk, ok)
	}
	agw0, kiss0 := DirewolfPorts(c.Rigs[0])
	agw1, kiss1 := DirewolfPorts(c.Rigs[1])
	agw2, _ := DirewolfPorts(c.Rigs[2])
	if agw0 != 8000 || kiss0 != 8001 || agw1 != 8002 || kiss1 != 8003 || agw2 != 8004 {
		t.Errorf("ports %d %d %d %d %d", agw0, kiss0, agw1, kiss1, agw2)
	}
	// None of them may land on Mercury's block (8300+ and 8100+ for broadcast).
	for _, r := range c.Rigs {
		a, d := DirewolfPorts(r)
		arq, data, bc, ui := MercuryPorts(r)
		for _, p := range []int{a, d} {
			if p == arq || p == data || p == bc || p == ui {
				t.Errorf("rig %s: direwolf port %d collides with a Mercury port", r.ID, p)
			}
		}
	}
	c.Rigs[1].Off, c.Rigs[2].Off = true, true
	if c.Has(Direwolf) {
		t.Error("the only active rigs left have no Direwolf")
	}
}

func TestNoCATRigs(t *testing.T) {
	var c Config
	if err := c.AddRig(Rig{ID: "dig", Profile: "digirig", NoCAT: true, Modem: Mercury}); err == nil {
		t.Error("Mercury is keyed through rigctld: a radio with no CAT cannot have it")
	}
	if err := c.AddRig(Rig{ID: "dig", Profile: "digirig", NoCAT: true, Modem: Direwolf}); err != nil {
		t.Fatal(err)
	}
	if c.Has(RigCtld) {
		t.Error("the only rig has no CAT, so nothing needs rigctld")
	}
	if !c.NeedsRadio() || !c.Has(Direwolf) {
		t.Error("it is still a radio with a modem")
	}
	c.AddRig(Rig{ID: "hf", Profile: "ic7300mk2"})
	if !c.Has(RigCtld) {
		t.Error("a rig with CAT needs rigctld")
	}
}

func TestRemovingARigForgetsItsKernelAX25Setup(t *testing.T) {
	var c Config
	c.AddRig(Rig{ID: "pkt", Profile: "digirig", Modem: Direwolf, NoCAT: true})
	c.AddRig(Rig{ID: "hf", Profile: "dummy"})
	c.AX25 = &AX25{Rig: "pkt", Port: "hamstack"}
	if err := c.RemoveRig("hf"); err != nil || c.AX25 == nil {
		t.Fatalf("removing another rig must leave it: %v %v", err, c.AX25)
	}
	if err := c.RemoveRig("pkt"); err != nil || c.AX25 != nil {
		t.Errorf("the AX.25 setup still names the removed rig: %v %+v", err, c.AX25)
	}
}
