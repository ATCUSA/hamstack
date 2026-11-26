package config

import (
	"reflect"
	"testing"
)

func changedEverywhere() Config {
	on, off := Set(true), Set(false)
	return Config{
		NoPat: true, UI: true, Data2GPage: off, PatPage: off, AppSetup: off, AppSetupOff: []string{"wsjtx"}, Launchers: off,
		Grid: "FN31pr", Active: "k0test", Stations: []Station{{ID: "k0test", Callsign: "K0TEST"}},
		Web: &WebSettings{Expert: true, RigTune: true, Console: true, Port: 8443},
		Rigs: []Rig{
			{ID: "hf", Profile: "ftdx10", Modem: Xara, XaraGUI: off, XaraMonitor: on, XaraRegistered: true, ArdopGUI: on,
				SharedAudio: false, RadioAutoTX: true, RadioProfiles: map[string]string{"wsjtx": "none"}},
			{ID: "packet", Profile: "digirig", Modem: Direwolf, NoCAT: true, SharedAudio: true, XaraGUI: off, XaraMonitor: on,
				XaraRegistered: true, ArdopGUI: off, RadioAutoTX: true, RadioProfiles: map[string]string{"fldigi": "none"}}, // stale values from an earlier modem count too
		},
	}
}

func TestEverySettingGoesBackToItsDefault(t *testing.T) {
	c := changedEverywhere()
	changed := c.ChangedSettings()
	if len(changed) != len(c.SettingDefaults()) {
		var keys []string
		for _, d := range changed {
			keys = append(keys, d.Key)
		}
		t.Errorf("every setting was changed, %d listed: %v", len(changed), keys)
	}
	for _, d := range c.SettingDefaults() {
		one := changedEverywhere()
		d.Reset(&one)
		if !d.IsDefault(one) {
			t.Errorf("%s: still not the default after Reset", d.Key)
		}
		if len(one.ChangedSettings()) != len(changed)-1 {
			t.Errorf("%s: Reset changed other settings too", d.Key)
		}
		if d.Label == "" || d.Default == "" {
			t.Errorf("%s: needs a label and a default for people", d.Key)
		}
	}
	for _, d := range c.SettingDefaults() {
		d.Reset(&c)
	}
	if got := c.ChangedSettings(); len(got) != 0 {
		t.Errorf("after putting every one back: %d still changed", len(got))
	}
	// What describes the station stays.
	if c.Grid != "FN31pr" || len(c.Stations) != 1 || len(c.Rigs) != 2 || c.Rigs[0].Modem != Xara || c.Web.Port != 8443 {
		t.Errorf("a reset touched the station itself: %+v", c)
	}
	if !c.Rigs[0].SharedAudio || c.Rigs[1].SharedAudio {
		t.Errorf("shared listening's default is on for a rig with CAT control, off without: %v %v", c.Rigs[0].SharedAudio, c.Rigs[1].SharedAudio)
	}
	if fresh := (Config{Rigs: []Rig{{ID: "hf", SharedAudio: true}}}); len(fresh.ChangedSettings()) != 0 {
		t.Errorf("a new rig is all defaults: %v", fresh.ChangedSettings())
	}
}

// A new switch on a rig needs its default here, or neither "Back to default" nor the full reset knows it.
func TestEveryRigSwitchHasADefault(t *testing.T) {
	notSwitches := map[string]bool{"NoCAT": true, "Off": true} // facts about the radio and where it is, not settings
	covered := map[string]bool{"ArdopGUI": true, "XaraGUI": true, "XaraRegistered": true, "XaraMonitor": true,
		"RadioAutoTX": true, "RadioProfiles": true, "SharedAudio": true}
	rt := reflect.TypeOf(Rig{})
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		k := f.Type.Kind()
		isSwitch := k == reflect.Bool || (k == reflect.Pointer && f.Type.Elem().Kind() == reflect.Bool) || f.Name == "RadioProfiles"
		if isSwitch && !notSwitches[f.Name] && !covered[f.Name] {
			t.Errorf("Rig.%s looks like a setting: give it a default in SettingDefaults", f.Name)
		}
	}
}

// Pat's page goes through the dashboard's login by default while the dashboard runs, and Pat then listens on this
// computer only; off, or without the dashboard, Pat follows the station's access.
func TestPatBehindTheDashboardByDefault(t *testing.T) {
	st := Station{ID: "k0test", HTTPAddr: "0.0.0.0:8081", Access: AccessAll}
	c := Config{Web: &WebSettings{}, Stations: []Station{st}, Active: "k0test"}
	if !c.PatRelayed() || c.PatAccess(st) != AccessLocal || PatPagePort(8081) != 9081 {
		t.Errorf("with the dashboard: relayed %v, access %q", c.PatRelayed(), c.PatAccess(st))
	}
	c.PatPage = Set(false)
	if c.PatRelayed() || c.PatAccess(st) != AccessAll {
		t.Errorf("turned off: relayed %v, access %q", c.PatRelayed(), c.PatAccess(st))
	}
	c.PatPage = nil
	c.Web.Off = true
	if c.PatRelayed() {
		t.Error("no dashboard, nothing to relay through")
	}
	c.Web.Off, c.NoPat = false, true
	if c.PatRelayed() {
		t.Error("no Pat, no page")
	}
}

// A full reset: every setting to its default, every access rule narrowed to this computer (never removed, never
// wider), the station itself and the dashboard's own access kept; and it lists what it changes first.
func TestFullReset(t *testing.T) {
	c := changedEverywhere()
	c.Web.Access = AccessAll
	c.Network = map[string]string{"xara@hf/8310": AccessAll, "rigctld@hf/4533": AccessLocal}
	c.Sites = []Site{{ID: "home", Network: map[string]string{"pat": AccessNetwork}}}
	c.Stations[0].Access = AccessAll
	changes := c.ResetChanges()
	if len(changes) != len(c.SettingDefaults())+3 {
		t.Errorf("%d changes listed: %q", len(changes), changes)
	}
	c.ResetToDefaults()
	if len(c.ChangedSettings()) != 0 || len(c.ResetChanges()) != 0 {
		t.Errorf("left after the reset: %q", c.ResetChanges())
	}
	if c.Network["xara@hf/8310"] != AccessLocal || c.Network["rigctld@hf/4533"] != AccessLocal || c.Sites[0].Network["pat"] != AccessLocal || c.Stations[0].Access != AccessLocal {
		t.Errorf("rules: %v %v %q", c.Network, c.Sites[0].Network, c.Stations[0].Access)
	}
	if c.Web.Access != AccessAll || c.Grid != "FN31pr" || len(c.Rigs) != 2 || c.Rigs[0].Modem != Xara {
		t.Errorf("the station and the dashboard's own access stay: %+v", c)
	}
	if (Config{}).ResetChanges() != nil {
		t.Error("a new config has nothing to reset")
	}
	// The dashboard's own access stays, in the config's map and a site's: narrowing it would lock out the browser.
	d := Config{Network: map[string]string{"web": AccessAll}, Sites: []Site{{ID: "home", Network: map[string]string{"web": AccessNetwork}}}}
	if got := d.ResetChanges(); len(got) != 0 {
		t.Errorf("the dashboard's access is not a reset change: %q", got)
	}
	d.ResetToDefaults()
	if d.Network["web"] != AccessAll || d.Sites[0].Network["web"] != AccessNetwork {
		t.Errorf("the dashboard's access was narrowed: %v %v", d.Network, d.Sites[0].Network)
	}
}

// A value set by hand that equals the default is the default: no "Back to default", nothing for the reset (the Pi's
// monitor was turned off by hand, then off became the default).
func TestAValueEqualToItsDefaultIsTheDefault(t *testing.T) {
	c := Config{Data2GPage: Set(true), PatPage: Set(true), AppSetup: Set(true), Launchers: Set(true),
		Rigs: []Rig{{ID: "hf", SharedAudio: true, XaraGUI: Set(true), XaraMonitor: Set(false)}}}
	if got := c.ChangedSettings(); len(got) != 0 {
		var keys []string
		for _, d := range got {
			keys = append(keys, d.Key)
		}
		t.Errorf("changed: %v", keys)
	}
}
