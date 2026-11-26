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
