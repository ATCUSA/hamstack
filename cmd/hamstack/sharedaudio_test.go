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
