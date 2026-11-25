package config

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestARigWithOneModemKeepsItsPortsAndConfig(t *testing.T) {
	r := Rig{ID: "hf", Modem: Ardopcf, ArqPort: 8310}
	if c, d := ArdopPorts(r); c != 8310 || d != 8311 {
		t.Fatalf("a rig with ardopcf alone keeps its ports: %d %d", c, d)
	}
	b, _ := json.Marshal(r)
	if string(b) != `{"id":"hf","profile":"","modem":"ardopcf","rig_port":0,"arq_port":8310}` {
		t.Fatalf("its config must be byte-for-byte as before: %s", b)
	}
}

func TestARigWithArdopAndAVARAModemGivesArdopItsOwnPorts(t *testing.T) {
	for _, r := range []Rig{
		{ID: "hf", Modem: Mercury, Alt: []string{Ardopcf}, ArqPort: 8310},
		{ID: "hf", Modem: Ardopcf, Alt: []string{Data2G}, ArqPort: 8310},
	} {
		c, d := ArdopPorts(r)
		m, md, _, _ := MercuryPorts(r)
		if c == m || c == md || d == m || d == md || c < 8310 || d >= 8320 {
			t.Errorf("%v: ardopcf %d/%d must not share the VARA modem's %d/%d and must stay in the rig's block", r.HFModems(), c, d, m, md)
		}
		if p := ArdopGUIPort(r); p == c || p == d || p == m || p == md || p < 8310 || p >= 8320 {
			t.Errorf("%v: the ARDOP GUI port %d collides", r.HFModems(), p)
		}
	}
}

func TestSwitchModem(t *testing.T) {
	r := Rig{ID: "hf", Modem: Mercury, Alt: []string{Ardopcf}}
	if err := r.SwitchModem(Ardopcf); err != nil {
		t.Fatal(err)
	}
	if r.Modem != Ardopcf || !slices.Equal(r.Alt, []string{Mercury}) {
		t.Fatalf("after the switch: %q %v", r.Modem, r.Alt)
	}
	if err := r.SwitchModem(Direwolf); err == nil {
		t.Error("only a modem the rig has can be switched to")
	}
	if err := r.SetAltModems([]string{"mercury", "data2g", "ardopcf"}); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(r.Alt, r.Modem) {
		t.Errorf("the active modem is not also an alternative: %v", r.Alt)
	}
	if err := r.SetAltModems([]string{"direwolf"}); err == nil {
		t.Error("only HF modems can be switched between")
	}
	nocat := Rig{ID: "v", NoCAT: true, Modem: Direwolf}
	if err := nocat.SetAltModems([]string{Mercury}); err == nil {
		t.Error("an HF modem is keyed through rigctld: a radio without CAT cannot have one")
	}
}
