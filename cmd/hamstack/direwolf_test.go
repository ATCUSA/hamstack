package main

import (
	"strings"
	"testing"

	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/profile"
	"github.com/ATCUSA/hamstack/internal/units"
)

func TestSetPacketField(t *testing.T) {
	var o config.PacketOpts
	for _, c := range []struct{ k, v string }{{"baud", "9600"}, {"call", "n0call-10"}, {"txdelay", "40"}, {"txtail", "5"}, {"persist", "100"}, {"slottime", "20"}, {"digipeat", "on"}} {
		if err := setPacketField(&o, c.k, c.v); err != nil {
			t.Fatalf("%s %s: %v", c.k, c.v, err)
		}
	}
	want := config.PacketOpts{Baud: 9600, Call: "N0CALL-10", TxDelay: 40, TxTail: 5, Persist: 100, SlotTime: 20, Digipeat: true}
	if o != want {
		t.Errorf("got %+v", o)
	}
	// A rejected value changes nothing.
	before := o
	for _, c := range []struct{ k, v string }{{"baud", "1234"}, {"baud", "fast"}, {"call", "not a call"}, {"txdelay", "300"}, {"persist", "-1"}, {"digipeat", "maybe"}, {"colour", "red"}} {
		if err := setPacketField(&o, c.k, c.v); err == nil {
			t.Errorf("%s %s was accepted", c.k, c.v)
		}
	}
	if o != before {
		t.Errorf("a rejected value changed the settings: %+v", o)
	}
	if err := setPacketField(&o, "call", ""); err != nil || o.Call != "" {
		t.Errorf("an empty call returns to the station's: %q %v", o.Call, err)
	}
}

func direwolfTestConfig(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var cfg config.Config
	if err := cfg.AddRig(config.Rig{ID: "pkt", Profile: "dummy", Modem: config.Direwolf}); err != nil {
		t.Fatal(err)
	}
	if err := cfg.AddRig(config.Rig{ID: "hf", Profile: "dummy", Modem: config.Mercury}); err != nil {
		t.Fatal(err)
	}
	if err := cfg.AddStation(config.Station{ID: "me", Callsign: "N0CALL", PatConfig: "/p.json"}); err != nil {
		t.Fatal(err)
	}
	cfg.Active = "me"
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
}

func TestDirewolfRigChoice(t *testing.T) {
	direwolfTestConfig(t)
	cfg, _ := config.Load()
	if r, err := direwolfRig(cfg, ""); err != nil || r.ID != "pkt" {
		t.Errorf("default: %v %v", r.ID, err)
	}
	if _, err := direwolfRig(cfg, "hf"); err == nil || !strings.Contains(err.Error(), "does not use Direwolf") {
		t.Errorf("an HF rig: %v", err)
	}
	if _, err := direwolfRig(cfg, "nope"); err == nil {
		t.Error("unknown rig")
	}
}

func TestDirewolfSetupAsksAndSaves(t *testing.T) {
	direwolfTestConfig(t)
	// 9600 baud, a callsign with an SSID, adjust timing (txdelay 45, others kept), digipeat: asked, yes, confirm yes.
	a, out := scripted("9600", "n0call-10", "y", "45", "", "", "", "y", "y")
	if err := direwolfSetupWith(a, ""); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	cfg, _ := config.Load()
	r, _ := cfg.RigByIDValue("pkt")
	want := config.PacketOpts{Baud: 9600, Call: "N0CALL-10", TxDelay: 45, Digipeat: true}
	if r.Packet != want {
		t.Errorf("saved %+v, want %+v\n%s", r.Packet, want, out.String())
	}
	if !strings.Contains(out.String(), "harms a whole area") {
		t.Errorf("digipeating comes with its warning:\n%s", out.String())
	}
	// Pressing Enter all the way through keeps everything.
	a, _ = scripted("", "", "n", "n")
	if err := direwolfSetupWith(a, "pkt"); err != nil {
		t.Fatal(err)
	}
	cfg, _ = config.Load()
	if r, _ := cfg.RigByIDValue("pkt"); r.Packet.Baud != 9600 || r.Packet.Call != "N0CALL-10" || r.Packet.TxDelay != 45 {
		t.Errorf("Enter changed the settings: %+v", r.Packet)
	}
	if r, _ := cfg.RigByIDValue("pkt"); r.Packet.Digipeat {
		t.Error("answering no to the digipeater question turns it off")
	}
	// A bad answer is asked again.
	a, out = scripted("12", "1200", "-", "n", "n")
	if err := direwolfSetupWith(a, ""); err != nil || !strings.Contains(out.String(), "one of") {
		t.Errorf("%v\n%s", err, out.String())
	}
	cfg, _ = config.Load()
	if r, _ := cfg.RigByIDValue("pkt"); r.Packet.Baud != 0 || r.Packet.Call != "" {
		t.Errorf("1200 is Direwolf's default so it is stored as unset, and - clears the callsign: %+v", r.Packet)
	}
}

func TestDirewolfConfigUsesTheSettings(t *testing.T) {
	var pr profile.Profile
	pr.ID, pr.Name = "dummy", "Dummy"
	pr.Audio.Null = true
	rig := config.Rig{ID: "pkt", ArqPort: 8300, Packet: config.PacketOpts{Baud: 9600, Call: "N0CALL-10", TxDelay: 40, Digipeat: true}}
	got := units.DirewolfConfigWith(rig, pr, config.Station{Callsign: "N0CALL"}, "", "", "PBEACON delay=1 every=30\n")
	for _, want := range []string{"MYCALL N0CALL-10\n", "MODEM 9600\n", "TXDELAY 40\n", "DIGIPEAT 0 0", "PBEACON delay=1 every=30"} {
		if !strings.Contains(got, want) {
			t.Errorf("lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "TXTAIL") || strings.Contains(got, "PERSIST") || strings.Contains(got, "SLOTTIME") {
		t.Errorf("settings left at their default are not written:\n%s", got)
	}
	if strings.Index(got, "PBEACON") < strings.Index(got, "KISSPORT") {
		t.Errorf("the user's lines come last, so they can override:\n%s", got)
	}
	// With nothing set the configuration is exactly what it always was.
	plain := units.DirewolfConfig(config.Rig{ID: "pkt", ArqPort: 8300}, pr, config.Station{Callsign: "N0CALL"}, "", "")
	for _, want := range []string{"MYCALL N0CALL\n", "MODEM 1200\n"} {
		if !strings.Contains(plain, want) {
			t.Errorf("default config lacks %q:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "DIGIPEAT") || strings.Contains(plain, "TXDELAY") {
		t.Errorf("defaults add nothing:\n%s", plain)
	}
}

func TestAX25PortDefaultsFollowTheRig(t *testing.T) {
	var cfg config.Config
	cfg.AddStation(config.Station{ID: "me", Callsign: "N0CALL", PatConfig: "/p.json"})
	cfg.Active = "me"
	rig := config.Rig{ID: "pkt", Modem: config.Direwolf}
	p, err := ax25PortFrom(cfg, rig, config.AX25{})
	if err != nil || p.Name != "hamstack" || p.Call != "N0CALL" || p.Speed != 1200 || p.Paclen != 255 || p.Window != 2 {
		t.Errorf("defaults: %+v %v", p, err)
	}
	// The speed follows what Direwolf runs at, unless set on the port itself.
	rig.Packet.Baud = 9600
	if p, _ := ax25PortFrom(cfg, rig, config.AX25{}); p.Speed != 9600 {
		t.Errorf("speed should follow Direwolf: %+v", p)
	}
	p, _ = ax25PortFrom(cfg, rig, config.AX25{Call: "N0CALL-1", Speed: 4800, Paclen: 128, Window: 4, Port: "vhf"})
	if p.Name != "vhf" || p.Call != "N0CALL-1" || p.Speed != 4800 || p.Paclen != 128 || p.Window != 4 {
		t.Errorf("explicit: %+v", p)
	}
	for _, bad := range []config.AX25{{Call: "x y"}, {Speed: 100}, {Paclen: 9999}, {Window: 9}} {
		if _, err := ax25PortFrom(cfg, rig, bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	var none config.Config
	if _, err := ax25PortFrom(none, rig, config.AX25{}); err == nil {
		t.Error("no station and no callsign must be an error")
	}
}
