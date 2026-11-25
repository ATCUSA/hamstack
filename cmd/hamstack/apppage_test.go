package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/patcfg"
	"github.com/ATCUSA/hamstack/internal/web"
)

const patSample = `{
  "mycall": "K0TEST",
  "secure_login_password": "hunter2",
  "locator": "EM10mn",
  "ardop": {"addr": "", "rig": "", "ptt_ctrl": false},
  "varahf": {"addr": "localhost:8310", "rig": "", "ptt_ctrl": false},
  "telnet": {"password": "also-secret"}
}
`

// appTest makes a station with a Pat config of its own, and a rig, in a home of its own.
func appTest(t *testing.T) (patPath string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	patPath = filepath.Join(t.TempDir(), "k0test.json")
	if err := os.WriteFile(patPath, []byte(patSample), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Active: "me", Grid: "EM10mn",
		Stations: []config.Station{{ID: "me", Callsign: "K0TEST", PatConfig: patPath, HTTPAddr: "localhost:8081"}},
		Rigs:     []config.Rig{{ID: "ftdx10", Name: "FT-DX10", Profile: "ftdx10", RigPort: 4533}}}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	return patPath
}

func TestPatPageShowsOnlyNamedFieldsAndNeverASecret(t *testing.T) {
	appTest(t)
	info, err := webBackend{}.AppPage("pat")
	if err != nil {
		t.Fatal(err)
	}
	if !info.Installed && info.Name != "pat" {
		t.Fatalf("%+v", info)
	}
	b, _ := json.Marshal(info)
	for _, secret := range []string{"hunter2", "also-secret", "secure_login_password"} {
		if strings.Contains(string(b), secret) {
			t.Errorf("the page data holds %q:\n%s", secret, b)
		}
	}
	keys := map[string]bool{}
	for _, f := range info.Fields {
		keys[f.Key] = true
	}
	for _, want := range []string{"ardop.rig", "ardop.addr", "ardop.ptt_ctrl", "varahf.rig", "varahf.addr", "varahf.ptt_ctrl"} {
		if !keys[want] {
			t.Errorf("missing field %s", want)
		}
	}
	if len(info.Fields) != 6 {
		t.Errorf("only the named fields: %d", len(info.Fields))
	}
	if info.Setup == nil || !info.Setup.CanDo || len(info.Setup.Changes) == 0 {
		t.Fatalf("the recommended setup must say what it would change: %+v", info.Setup)
	}
	found := false
	for _, c := range info.Setup.Changes {
		found = found || (c.Path == "ardop.rig" && c.New == `"ftdx10"`)
	}
	if !found {
		t.Errorf("the ARDOP rig is part of the recommended setup: %+v", info.Setup.Changes)
	}
	if info.Setup.CanUndo {
		t.Error("nothing has been applied yet")
	}
	if _, err := (webBackend{}).AppPage("nosuch"); err == nil {
		t.Error("an unknown app has no page")
	}
	if _, err := (webBackend{}).AppPage("console"); err == nil {
		t.Error("the console is not an app with a page")
	}
}

func TestSetupApplyThenUndoThroughTheActions(t *testing.T) {
	p := appTest(t)
	be := webBackend{}
	if _, err := be.Manage(web.ManagementRequest{Action: "app.setup.apply", ID: "pat"}); err == nil {
		t.Fatal("it must be confirmed")
	}
	if _, err := be.Manage(web.ManagementRequest{Action: "app.setup.apply", ID: "pat", Confirm: true}); err != nil {
		t.Fatal(err)
	}
	vals, _ := patcfg.Fields(p, []string{"ardop.rig", "hamlib_rigs.ftdx10.address", "varahf.rig"})
	if vals["ardop.rig"] != "ftdx10" || vals["hamlib_rigs.ftdx10.address"] != "localhost:4533" {
		t.Fatalf("applied: %v", vals)
	}
	if vals["varahf.rig"] != "" {
		t.Errorf("no HF modem is set up, so the VARA HF entry is not pointed at a rig: %v", vals)
	}
	if bak, _ := os.ReadFile(p + ".hamstack-bak"); string(bak) != patSample {
		t.Error("the original must be kept")
	}
	info, _ := be.AppPage("pat")
	if !info.Setup.CanUndo || len(info.Setup.Changes) != 0 {
		t.Errorf("after applying: nothing more to do, and undo is offered: %+v", info.Setup)
	}

	// The person edits ARDOP's rig afterwards; undoing leaves that, and removes what is still hamstack's.
	if _, err := be.Manage(web.ManagementRequest{Action: "app.field.set", ID: "pat", Value: "ardop.rig", Extra: ""}); err != nil {
		t.Fatal(err)
	}
	msg, err := be.Manage(web.ManagementRequest{Action: "app.setup.undo", ID: "pat", Confirm: true})
	if err != nil {
		t.Fatal(err)
	}
	vals, _ = patcfg.Fields(p, []string{"ardop.rig", "hamlib_rigs.ftdx10.address", "mycall"})
	if vals["hamlib_rigs.ftdx10.address"] != "" || vals["mycall"] != "K0TEST" {
		t.Errorf("undone: %v (%v)", vals, msg)
	}
	if _, err := be.Manage(web.ManagementRequest{Action: "app.setup.undo", ID: "pat", Confirm: true}); err == nil {
		t.Error("with nothing recorded there is nothing to undo")
	}
}

func TestFieldSetChecksWhatItIsGiven(t *testing.T) {
	p := appTest(t)
	be := webBackend{}
	bad := []web.ManagementRequest{
		{Action: "app.field.set", ID: "pat", Value: "mycall", Extra: "W1AW"},                     // not a setting hamstack manages
		{Action: "app.field.set", ID: "pat", Value: "secure_login_password", Extra: "x"},         // certainly not
		{Action: "app.field.set", ID: "pat", Value: "ardop.addr", Extra: "no port"},              // not an address
		{Action: "app.field.set", ID: "pat", Value: "ardop.addr", Extra: "localhost:99999"},      // out of range
		{Action: "app.field.set", ID: "pat", Value: "ardop.rig", Extra: "nosuchrig"},             // not one of the rigs
		{Action: "app.field.set", ID: "pat", Value: "ardop.ptt_ctrl", Extra: "maybe"},            // not on or off
		{Action: "app.field.set", ID: "wsjtx", Value: "ardop.rig", Extra: ""},                    // not Pat
		{Action: "app.field.set", ID: "pat", Value: "ardop.addr", Extra: "localhost:8515\nx: 1"}, // a line break
	}
	for _, r := range bad {
		if _, err := be.Manage(r); err == nil {
			t.Errorf("%+v must be refused", r)
		}
	}
	if b, _ := os.ReadFile(p); string(b) != patSample {
		t.Error("a refused change must leave the file alone")
	}
	for _, r := range []web.ManagementRequest{
		{Action: "app.field.set", ID: "pat", Value: "ardop.addr", Extra: "localhost:8515"},
		{Action: "app.field.set", ID: "pat", Value: "ardop.rig", Extra: "ftdx10"},
		{Action: "app.field.set", ID: "pat", Value: "ardop.ptt_ctrl", Extra: "true"},
	} {
		if _, err := be.Manage(r); err != nil {
			t.Errorf("%+v: %v", r, err)
		}
	}
	vals, _ := patcfg.Fields(p, []string{"ardop.addr", "ardop.rig", "ardop.ptt_ctrl", "telnet.password"})
	if vals["ardop.addr"] != "localhost:8515" || vals["ardop.rig"] != "ftdx10" || vals["ardop.ptt_ctrl"] != "true" {
		t.Errorf("%v", vals)
	}
	if raw, _ := os.ReadFile(p); !strings.Contains(string(raw), "also-secret") || !strings.Contains(string(raw), "hunter2") {
		t.Error("passwords stay in the file")
	}
}

func TestWiringNamesTheArdopRigFromTheFirstRigWithControl(t *testing.T) {
	cfg := config.Config{Rigs: []config.Rig{
		{ID: "tnc", Profile: "digirig", NoCAT: true, RigPort: 4532},
		{ID: "ftdx10", Profile: "ftdx10", RigPort: 4533},
	}}
	if w := patWiring(cfg); w.ARDOPRig != "ftdx10" {
		t.Errorf("a rig with no radio control cannot be tuned: %q", w.ARDOPRig)
	}
	cfg.Rigs = []config.Rig{{ID: "hf", Profile: "ftdx10", RigPort: 4533, Modem: config.Mercury, ArqPort: 8300}}
	if w := patWiring(cfg); w.ARDOPRig != "hf" || w.HFRig != "hf" {
		t.Errorf("with an HF modem it is that rig: %+v", w)
	}
	if w := patWiring(config.Config{}); w.ARDOPRig != "" {
		t.Errorf("no rig, nothing to name: %q", w.ARDOPRig)
	}
}

func TestStateListsPatFirstAsAnAppWithItsOwnPage(t *testing.T) {
	appTest(t)
	apps := webBackend{}.State().Apps
	if len(apps) == 0 || apps[0].Name != "pat" || apps[0].Kind != "web" || apps[0].Package != "pat" {
		t.Fatalf("%+v", apps)
	}
	for _, a := range apps[1:] {
		if a.Kind != "browser" {
			t.Errorf("%s: kind %q", a.Name, a.Kind)
		}
	}
}
