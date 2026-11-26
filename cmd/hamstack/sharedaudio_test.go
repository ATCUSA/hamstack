package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ATCUSA/hamstack/internal/config"
)

func TestSharedAudioWritesTheRigsCardAndAnEnvironmentForItsPrograms(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	rig := config.Rig{ID: "ftdx10", Name: "FT-DX10", ArqPort: config.ArqPortBase + config.ArqPortStep, SharedAudio: true}
	dev, env, err := sharedAudio(rig, "plughw:CARD=CODEC,DEV=0")
	if err != nil {
		t.Fatal(err)
	}
	if dev != "hamstack_ftdx10" {
		t.Errorf("device: %s", dev)
	}
	path := strings.TrimPrefix(env, "ALSA_CONFIG_PATH=/usr/share/alsa/alsa.conf:")
	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), `pcm "hw:CARD=CODEC,DEV=0"`) {
		t.Fatalf("config %s: %v\n%s", path, err, b)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 || filepath.Dir(path) != sharedAudioDir() {
		t.Errorf("where and how: %s %v", path, fi.Mode())
	}
	if _, _, err := sharedAudio(rig, "default"); err == nil {
		t.Error("a device that is not a card cannot be shared")
	}
}

// Shared audio and ardopcf's screen concern the modem only: rig control and Pat need no restart for them.
func TestModemOnlySettingsRestartOnlyTheModem(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	before := config.Config{Rigs: []config.Rig{{ID: "ftdx10", Profile: "ftdx10", Modem: config.Ardopcf, RigPort: 4533, ArqPort: 8310}},
		Active: "me", Stations: []config.Station{{ID: "me", Callsign: "K0TEST"}}}
	after := cloneConfig(before)
	after.Rigs[0].SharedAudio, after.Rigs[0].ArdopGUI = true, config.Set(true)
	got := changedServices(before, after)
	if len(got) != 1 || got[0] != "ardopcf@ftdx10" {
		t.Fatalf("only the modem: %v", got)
	}
}

// The modem's own switches (xara's screen and monitor, its licence speeds), the radio check's choices (a profile per
// program, transmit-side fixes) and the rig's display name are read by the modem when it starts, or by nothing that
// runs: rig control and Pat must not be marked for a restart (the Pi showed "restart needed: pat, rigctld" for days
// after a profile was picked on the Radios page).
func TestModemSwitchesAndProfilesNeedNoRestartOfRigControlOrPat(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	before := config.Config{Rigs: []config.Rig{{ID: "ftdx10", Profile: "ftdx10", Modem: config.Xara, RigPort: 4533, ArqPort: 8310}},
		Active: "me", Stations: []config.Station{{ID: "me", Callsign: "K0TEST"}}}
	after := cloneConfig(before)
	r := &after.Rigs[0]
	r.Name, r.XaraGUI, r.XaraMonitor, r.XaraRegistered, r.RadioAutoTX = "Shack", config.Set(false), config.Set(true), true, true
	r.RadioProfiles = map[string]string{"fldigi": "yaesu-ftdx10/rtty-afsk"}
	got := changedServices(before, after)
	if len(got) != 1 || got[0] != "xara@ftdx10" {
		t.Fatalf("only the modem: %v", got)
	}
	after = cloneConfig(before)
	after.Rigs[0].Baud = 9600
	if got := changedServices(before, after); !slices.Contains(got, "rigctld@ftdx10") {
		t.Errorf("a new CAT speed restarts rig control: %v", got)
	}
	after = cloneConfig(before)
	after.Rigs[0].RigPort = 4540
	if got := changedServices(before, after); !slices.Contains(got, "pat") {
		t.Errorf("a new rig control port reaches Pat's wiring: %v", got)
	}
}

// Listeners share the card; the one transmitter plays on the card itself. ardopcf hung in the mixing layer with the
// radio keyed (on the Pi), so playback never goes through it.
func TestModemsListenSharedAndTransmitDirect(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("ALSA_CONFIG_PATH", "")
	rig := config.Rig{ID: "ftdx10", Audio: "CODEC", AudioUSB: "", ArqPort: config.ArqPortBase, SharedAudio: true}
	in, out, err := sharedModemDevices(rig, "plughw:CARD=CODEC,DEV=0")
	if err != nil {
		t.Fatal(err)
	}
	if in != "hamstack_ftdx10" || out != "plughw:CARD=CODEC,DEV=0" {
		t.Fatalf("capture %q, playback %q", in, out)
	}
	if !strings.Contains(os.Getenv("ALSA_CONFIG_PATH"), "ftdx10.conf") {
		t.Fatal("the modem must inherit the shared configuration")
	}
}

// Pat's page is one of the pages the dashboard relays, at 1000 above Pat's port; turning it off or on restarts Pat
// (its listen address changes) and nothing else.
func TestPatPageThroughTheDashboard(t *testing.T) {
	isolated(t)
	cfg := config.Config{Web: &config.WebSettings{}, Active: "me", Stations: []config.Station{{ID: "me", Callsign: "K0TEST", HTTPAddr: "localhost:8081"}},
		Rigs: []config.Rig{{ID: "hf", Profile: "dummy", Modem: config.Xara, RigPort: 4533, ArqPort: 8310}}}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	pages := webBackend{}.ModemPages()
	if len(pages) != 1 || pages[0].Name != "pat" || pages[0].Port != 9081 || pages[0].Target != 8081 {
		t.Fatalf("pages: %+v", pages)
	}
	after := cloneConfig(cfg)
	after.PatPage = config.Set(false)
	if got := changedServices(cfg, after); len(got) != 1 || got[0] != "pat" {
		t.Errorf("turning the page off restarts Pat only: %v", got)
	}
	var relayed bool
	for _, r := range networkRows(cfg, nil) {
		if r.Port == 9081 && strings.Contains(r.What, "through the dashboard") {
			relayed = true
		}
	}
	if !relayed {
		t.Error("the Network list shows the page's port")
	}
	for _, tg := range accessTargets(cfg) {
		if tg.ID == "pat" && tg.Mode != config.AccessLocal {
			t.Errorf("Pat's own port while the dashboard serves it: %q, want this computer only", tg.Mode)
		}
	}
}

// FLRig: with a rig whose rig control goes through it, the Apps page says so and the Network list shows its server
// (no password, can key the radio); with a rig of its own, FLRig is told it cannot share the control port.
func TestFLRigAndRigControl(t *testing.T) {
	isolated(t)
	through := config.Config{Rigs: []config.Rig{{ID: "hf", Profile: "flrig", RigPort: 4533}}}
	if n := flrigNote(through); !strings.Contains(n, "goes through FLRig") {
		t.Errorf("through FLRig: %q", n)
	}
	found := false
	for _, r := range networkRows(through, nil) {
		if r.Service == "flrig" && r.Port == flrigPort && strings.Contains(r.Note, "can key the radio") {
			found = true
		}
	}
	if !found {
		t.Error("FLRig's server is on the Network list")
	}
	own := config.Config{Rigs: []config.Rig{{ID: "hf", Profile: "ftdx10", RigPort: 4533}}}
	if n := flrigNote(own); !strings.Contains(n, "Cannot share hf's control port") {
		t.Errorf("a rig of its own: %q", n)
	}
	for _, r := range networkRows(own, nil) {
		if r.Service == "flrig" {
			t.Error("no FLRig server listed when nothing goes through it")
		}
	}
	if n := flrigNote(config.Config{Rigs: []config.Rig{{ID: "vhf", Profile: "digirig", NoCAT: true}}}); n != "" {
		t.Errorf("no rig control, nothing to say: %q", n)
	}
	var out strings.Builder
	flrigTakesOver(own, &out, time.Millisecond) // nothing goes through FLRig: nothing to wait for or restart
	if out.Len() != 0 {
		t.Errorf("%q", out.String())
	}
}
