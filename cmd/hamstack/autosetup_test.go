package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ATCUSA/hamstack/internal/apps"
	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/radiosetup"
	"github.com/ATCUSA/hamstack/internal/web"
)

// syncBuilder is a strings.Builder the band watch's goroutine can write to while the test reads it.
type syncBuilder struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuilder) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}
func (s *syncBuilder) String() string { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func wsjtx(t *testing.T) apps.App {
	t.Helper()
	a, ok := apps.Find("wsjtx")
	if !ok {
		t.Fatal("no wsjtx app")
	}
	return a
}

// By itself, at a program's start, hamstack only fills sound settings nobody set: a device someone chose, and TCI audio
// (right for a TCI radio), are left for the recommended setup's button.
func TestAutoSetupFillsOnlyEmptySound(t *testing.T) {
	chosen := "[Configuration]\nSoundInName=alsa_input.other\nTCIAudio=true\n"
	p := soundHome(t, chosen)
	lines := autoSetupApp(wsjtx(t), config.Config{})
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), "SoundInName=alsa_input.other") || !strings.Contains(string(b), "TCIAudio=true") {
		t.Errorf("a chosen device and TCI audio must stay:\n%s", b)
	}
	if !strings.Contains(string(b), "SoundOutName="+codecOut) || len(lines) != 1 || !strings.Contains(lines[0], "nothing was set") {
		t.Errorf("the empty output is filled, and says so: %q\n%s", lines, b)
	}
	if again := autoSetupApp(wsjtx(t), config.Config{}); again != nil {
		t.Errorf("nothing left to fill: %q", again)
	}
}

func TestAutoSetupStaysOutOfTheWay(t *testing.T) {
	empty := "[Configuration]\n"
	for _, tc := range []struct {
		name    string
		cfg     config.Config
		running bool
	}{
		{"turned off in Settings", config.Config{AppSetup: config.Set(false)}, false},
		{"undone for this program", config.Config{AppSetupOff: []string{"wsjtx"}}, false},
		{"the program is open: it would write its old settings back", config.Config{}, true},
	} {
		p := soundHome(t, empty)
		programRunning = func(string) bool { return tc.running }
		if got := autoSetupApp(wsjtx(t), tc.cfg); got != nil {
			t.Errorf("%s: %q", tc.name, got)
		}
		if b, _ := os.ReadFile(p); string(b) != empty {
			t.Errorf("%s: the file changed:\n%s", tc.name, b)
		}
	}
}

// Undo stays undone: the program is not set up by itself again on its next start, until Apply is pressed.
func TestUndoTurnsTheAutomaticSetupOffAndApplyBackOn(t *testing.T) {
	soundHome(t, "[Configuration]\nTCIAudio=true\n")
	if err := config.Save(config.Config{}); err != nil {
		t.Fatal(err)
	}
	apply := web.ManagementRequest{Action: "app.setup.apply", ID: "wsjtx", Confirm: true}
	if _, err := appSetupApply(apply); err != nil {
		t.Fatal(err)
	}
	msg, err := appSetupUndo(web.ManagementRequest{Action: "app.setup.undo", ID: "wsjtx", Confirm: true})
	if err != nil || !strings.Contains(msg.(string), "not set up by itself again") {
		t.Fatalf("undo: %v %v", msg, err)
	}
	if loadCfg().AppSetupOn("wsjtx") {
		t.Fatal("after Undo, WSJT-X must not be set up by itself again")
	}
	if !loadCfg().AppSetupOn("fldigi") {
		t.Error("only the program undone is left alone")
	}
	if _, err := appSetupApply(apply); err != nil {
		t.Fatal(err)
	}
	if !loadCfg().AppSetupOn("wsjtx") {
		t.Error("Apply by hand turns it back on")
	}
}

func TestHasDesktop(t *testing.T) {
	root := t.TempDir()
	if hasDesktop(root) {
		t.Error("an empty root has no desktop")
	}
	_ = os.MkdirAll(filepath.Join(root, "usr/share/wayland-sessions"), 0o755)
	if hasDesktop(root) {
		t.Error("an empty sessions folder is no desktop")
	}
	_ = os.WriteFile(filepath.Join(root, "usr/share/wayland-sessions/labwc.desktop"), []byte("[Desktop Entry]\n"), 0o644)
	if !hasDesktop(root) {
		t.Error("a Wayland session (Raspberry Pi OS's labwc) is a desktop")
	}
}

func TestSetRadioProfile(t *testing.T) {
	r := config.Rig{ID: "hf", Profile: "ftdx10"}
	if radioModel(r) == 0 {
		t.Skip("the ftdx10 profile is not found here")
	}
	for _, tc := range []struct{ use, pick, err string }{
		{"wsjtx", "yaesu-ftdx10/psk31", ""},
		{"xara", "none", ""},
		{"fldigi", "icom-ic7300/psk31", "not a profile for rig hf's radio"},
		{"direwolf", "yaesu-ftdx10/ft8", "has no radio settings profile"},
	} {
		err := setRadioProfile(&r, tc.use, tc.pick)
		if tc.err == "" && err != nil || tc.err != "" && (err == nil || !strings.Contains(err.Error(), tc.err)) {
			t.Errorf("%s=%s: %v, want %q", tc.use, tc.pick, err, tc.err)
		}
	}
	if r.RadioProfiles["wsjtx"] != "yaesu-ftdx10/psk31" || r.RadioProfiles["xara"] != "none" || len(r.RadioProfiles) != 2 {
		t.Errorf("picks: %v", r.RadioProfiles)
	}
	_ = setRadioProfile(&r, "wsjtx", "default")
	_ = setRadioProfile(&r, "xara", "default")
	if r.RadioProfiles != nil {
		t.Errorf("back to the recommended ones, nothing is stored: %v", r.RadioProfiles)
	}
}

// WSJT-X started from hamstack gets its profile's receive-side fixes (AGC OFF to AUTO on the FT-DX10) beside it, and
// again after a band change, until it closes.
func TestRadioProgramGetsItsProfilesFixes(t *testing.T) {
	cfg := config.Config{Rigs: []config.Rig{{ID: "hf", Profile: "ftdx10", Modem: config.Data2G, RigPort: 4533}}}
	if radioModel(cfg.Rigs[0]) == 0 {
		t.Skip("the ftdx10 profile is not found here")
	}
	radio := &agcRadio{freq: "14074000", agc: map[string]string{"20m": "0", "40m": "0"}, ptt: "0"}
	oldUp, oldClient, oldEvery := seedRigUp, radioClient, bandWatchEvery
	seedRigUp = func(int) bool { return true }
	radioClient = func(config.Rig) radiosetup.Radio { return radio }
	bandWatchEvery = 5 * time.Millisecond
	defer func() { seedRigUp, radioClient, bandWatchEvery = oldUp, oldClient, oldEvery }()
	var out syncBuilder
	stop := radioAppWatch(cfg, wsjtx(t), &out)
	deadline := time.Now().Add(2 * time.Second)
	for agc, _ := radio.state(); agc["20m"] != "6" && time.Now().Before(deadline); agc, _ = radio.state() {
		time.Sleep(5 * time.Millisecond)
	}
	radio.tune("7074000")
	for agc, _ := radio.state(); agc["40m"] != "6" && time.Now().Before(deadline); agc, _ = radio.state() {
		time.Sleep(5 * time.Millisecond)
	}
	stop()
	stop() // twice is harmless (a deferred stop after an early one)
	agc, _ := radio.state()
	if agc["20m"] != "6" || agc["40m"] != "6" {
		t.Fatalf("AGC: %v; said: %s", agc, out.String())
	}
	if !strings.Contains(out.String(), "FT-DX10 FT8/FT4") {
		t.Errorf("the fix names WSJT-X's profile: %s", out.String())
	}
	cfg.Rigs[0].RadioProfiles = map[string]string{"wsjtx": "none"}
	radio.mu.Lock()
	radio.agc["40m"] = "0"
	radio.mu.Unlock()
	radioAppWatch(cfg, wsjtx(t), &out)()
	time.Sleep(20 * time.Millisecond)
	if agc, _ := radio.state(); agc["40m"] != "0" {
		t.Error("with no profile picked for WSJT-X, nothing is touched")
	}
}
