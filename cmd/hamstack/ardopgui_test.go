package main

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/ATCUSA/hamstack/internal/config"
)

func TestArdopcfScreenOnlyStartsWhenTheFirewallGuardAllows(t *testing.T) {
	rig := config.Rig{ID: "hf", Modem: config.Ardopcf, ArqPort: 8310, ArdopGUI: true}
	old := ardopGUIGuard
	defer func() { ardopGUIGuard = old }()
	ardopGUIGuard = func(config.Config, config.Rig) error { return errors.New("no firewall") }
	if argv := ardopcfArgv(config.Config{}, rig, "plughw:1,0", "/tmp/l"); slices.Contains(argv, "--webgui") {
		t.Fatalf("started without the firewall: %v", argv)
	}
	ardopGUIGuard = func(config.Config, config.Rig) error { return nil }
	argv := ardopcfArgv(config.Config{}, rig, "plughw:1,0", "/tmp/l")
	if i := slices.Index(argv, "--webgui"); i < 0 || argv[i+1] != "8312" {
		t.Fatalf("the screen on its port: %v", argv)
	}
	rig.ArdopGUI = false
	if argv := ardopcfArgv(config.Config{}, rig, "plughw:1,0", "/tmp/l"); slices.Contains(argv, "--webgui") || strings.Join(argv, " ") != "ardopcf 8310 plughw:1,0 plughw:1,0 --logdir /tmp/l" {
		t.Fatalf("a rig with the screen off runs ardopcf exactly as before: %v", argv)
	}
}

func TestTheScreenCannotBeTurnedOnWithoutTheFirewall(t *testing.T) {
	r := config.Rig{ID: "hf", Modem: config.Ardopcf, ArqPort: 8310}
	if firewallAvailable() {
		t.Skip("this computer has the firewall helper installed")
	}
	if err := setRigField(&r, "ardop-gui", "on"); err == nil || !strings.Contains(err.Error(), "firewall") || r.ArdopGUI {
		t.Fatalf("%v %v", err, r.ArdopGUI)
	}
	m := config.Rig{ID: "hf", Modem: config.Mercury}
	if err := setRigField(&m, "ardop-gui", "on"); err == nil || !strings.Contains(err.Error(), "no ardopcf") {
		t.Fatalf("a rig without ardopcf: %v", err)
	}
}

func TestTheScreenPortIsInTheFirewallPolicyOnlyWhileTurnedOn(t *testing.T) {
	cfg := config.Config{Rigs: []config.Rig{{ID: "hf", Profile: "ftdx10", Modem: config.Ardopcf, ArqPort: 8310, RigPort: 4533}}}
	has := func() bool {
		for _, p := range firewallPolicy(cfg) {
			if p.Port == 8312 {
				return p.Mode == config.AccessLocal
			}
		}
		return false
	}
	if has() {
		t.Fatal("off: a firewall that never approved the port must not be asked to restrict it")
	}
	cfg.Rigs[0].ArdopGUI = true
	if !has() {
		t.Fatalf("on: the screen is kept to this computer: %v", firewallPolicy(cfg))
	}
}
