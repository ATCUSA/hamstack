package main

import (
	"testing"

	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/tui"
	"github.com/ATCUSA/hamstack/internal/web"
)

// Each radio's switches show at their level: safe ones in the normal list, what changes the air or the network under
// Advanced, licence-level under Expert.
func TestRadioSwitchesAreAtTheirLevels(t *testing.T) {
	old := ardopFirewallReady
	ardopFirewallReady = func(config.Rig) bool { return false }
	defer func() { ardopFirewallReady = old }()
	cfg := config.Config{Rigs: []config.Rig{{ID: "hf", Profile: "dummy", Modem: config.Xara, Alt: []string{config.Ardopcf}, SharedAudio: true}}}
	got := map[string]tui.Setting{}
	for _, s := range rigSettings(cfg) {
		got[s.Key] = s
	}
	want := map[string]struct{ level, value string }{
		"rig:hf:xara-gui":        {"", "on"}, // on by default
		"rig:hf:xara-monitor":    {"", "on"},
		"rig:hf:shared-audio":    {tui.SettingAdvanced, "on"},
		"rig:hf:ardop-gui":       {tui.SettingAdvanced, "off"}, // no firewall helper here
		"rig:hf:xara-registered": {tui.SettingExpert, "off"},
		"rig:hf:radio-auto-tx":   {tui.SettingAdvanced, "off"},
	}
	for k, w := range want {
		s, ok := got[k]
		if !ok || s.Level != w.level || s.Value != w.value || s.Kind != tui.SettingToggle {
			t.Errorf("%s: %+v, want level %q value %q", k, s, w.level, w.value)
		}
	}
	if len(got) != len(want) {
		t.Errorf("extra switches: %v", got)
	}
}

func TestRigSwitchOnFollowsTheDefaults(t *testing.T) {
	r := config.Rig{ID: "hf", Modem: config.Xara}
	if !rigSwitchOn(r, "xara-gui") || !rigSwitchOn(r, "xara-monitor") || rigSwitchOn(r, "xara-registered") {
		t.Error("defaults: screen and monitor on, registered off")
	}
	r.XaraGUI = config.Set(false)
	if rigSwitchOn(r, "xara-gui") {
		t.Error("turned off stays off")
	}
}

// Turning on a shell, a carrier or VARA licence speeds is an Expert setting; turning them off never is.
func TestExpertOnlyRequests(t *testing.T) {
	for _, r := range []web.ManagementRequest{
		{Action: "dashboard.console", Value: "on"}, {Action: "dashboard.tune", Value: "on"},
		{Action: "setting.set", ID: "rig:hf:xara-registered", Value: "on"}, {Action: "rig.set", Value: "xara-registered", Extra: "on"},
	} {
		if !expertOnly(r) {
			t.Errorf("%+v must need Expert mode", r)
		}
	}
	for _, r := range []web.ManagementRequest{
		{Action: "dashboard.console", Value: "off"}, {Action: "dashboard.tune", Value: "off"},
		{Action: "setting.set", ID: "rig:hf:xara-registered", Value: "off"}, {Action: "setting.set", ID: "rig:hf:xara-gui", Value: "on"},
	} {
		if expertOnly(r) {
			t.Errorf("%+v must not", r)
		}
	}
}
