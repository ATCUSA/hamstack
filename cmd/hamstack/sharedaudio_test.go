package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	after.Rigs[0].SharedAudio, after.Rigs[0].ArdopGUI = true, true
	got := changedServices(before, after)
	if len(got) != 1 || got[0] != "ardopcf@ftdx10" {
		t.Fatalf("only the modem: %v", got)
	}
}
