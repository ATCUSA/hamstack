package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ATCUSA/hamstack/internal/apps"
	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/web"
)

const (
	codecIn  = "alsa_input.usb-codec.analog-stereo"
	codecOut = "alsa_output.usb-codec.analog-stereo"
)

func TestSoundEdits(t *testing.T) {
	// As found on the Pi: the right devices, but TCI audio on, so nothing reached the radio.
	tci := "[Configuration]\nSoundInName=" + codecIn + "\nSoundOutName=" + codecOut + "\nTCIAudio=true\n"
	if got := soundEdits(tci, codecIn, codecOut); len(got) != 1 || got[0] != (soundEdit{"TCIAudio", "true", "false"}) {
		t.Errorf("TCI on: %+v", got)
	}
	empty := soundEdits("[Configuration]\nRig=None\n", codecIn, codecOut)
	if len(empty) != 2 || empty[0].Key != "SoundInName" || empty[1].New != codecOut {
		t.Errorf("no devices: %+v", empty)
	}
	if got := soundEdits("[Configuration]\nSoundInName="+codecIn+"\nSoundOutName="+codecOut+"\nTCIAudio=false\n", codecIn, codecOut); len(got) != 0 {
		t.Errorf("already right: %+v", got)
	}
}

func soundHome(t *testing.T, ini string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	_ = os.MkdirAll(filepath.Join(home, ".config"), 0o755)
	p := filepath.Join(home, ".config", "WSJT-X.ini")
	if err := os.WriteFile(p, []byte(ini), 0o644); err != nil {
		t.Fatal(err)
	}
	oldNames, oldRunning, oldUp := rigSoundNames, programRunning, seedRigUp
	rigSoundNames = func(config.Config) (string, string, string, bool) { return "ftdx10", codecIn, codecOut, true }
	programRunning = func(string) bool { return false }
	seedRigUp = func(int) bool { return false } // no rig control: only the sound part applies
	t.Cleanup(func() { rigSoundNames, programRunning, seedRigUp = oldNames, oldRunning, oldUp })
	return p
}

func TestRecommendedSetupFixesWSJTXSoundAndUndoPutsItBack(t *testing.T) {
	orig := "[Configuration]\nRig=Hamlib NET rigctl\nCATNetworkPort=127.0.0.1:4533\nSoundInName=" + codecIn + "\nTCIAudio=true\n"
	p := soundHome(t, orig)
	req := web.ManagementRequest{Action: "app.setup.apply", ID: "wsjtx", Confirm: true}

	programRunning = func(c string) bool { return true }
	if _, err := appSetupApply(req); err == nil || !strings.Contains(err.Error(), "close WSJT-X first") {
		t.Fatalf("while it runs, its own save would undo the change: %v", err)
	}
	programRunning = func(string) bool { return false }
	msg, err := appSetupApply(req)
	if err != nil || !strings.Contains(msg.(string), "sound set to ftdx10's card") {
		t.Fatalf("apply: %v %v", msg, err)
	}
	b, _ := os.ReadFile(p)
	for _, want := range []string{"TCIAudio=false", "SoundOutName=" + codecOut, "Rig=Hamlib NET rigctl"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("after apply, missing %q:\n%s", want, b)
		}
	}
	if _, err := appSetupApply(req); err == nil {
		t.Error("a second apply has nothing to do and says so")
	}
	if _, err := appSetupUndo(web.ManagementRequest{Action: "app.setup.undo", ID: "wsjtx", Confirm: true}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != orig {
		t.Errorf("undo must put the file back:\n%s", b)
	}
}

// The app page shows the sound changes even when the radio is already set up (it is left alone).
func TestRecommendedSetupPreviewShowsSoundChanges(t *testing.T) {
	soundHome(t, "[Configuration]\nRig=Hamlib NET rigctl\nSoundInName="+codecIn+"\nSoundOutName="+codecOut+"\nTCIAudio=true\n")
	seedRigUp = func(int) bool { return true }
	cfg := config.Config{Rigs: []config.Rig{{ID: "ftdx10", Profile: "dummy", RigPort: 4533}}}
	a, _ := apps.Find("wsjtx")
	s := radioSetupPreview(cfg, a, os.Getenv("HOME"))
	if !s.CanDo || len(s.Changes) != 1 || s.Changes[0].Path != "TCIAudio" || s.Changes[0].New != "false" {
		t.Fatalf("%+v", s)
	}
	if !strings.Contains(s.Summary, "leaves it as it is") || !strings.Contains(s.Summary, "TCI audio off") || !strings.Contains(s.Title, "sound") {
		t.Errorf("summary: %q, title %q", s.Summary, s.Title)
	}
}
