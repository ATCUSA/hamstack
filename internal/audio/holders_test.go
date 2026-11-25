package audio

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeProc builds a /proc with two sound cards and one process that has card 2's capture device open.
func fakeProc(t *testing.T) string {
	t.Helper()
	proc := t.TempDir()
	_ = os.MkdirAll(filepath.Join(proc, "asound"), 0o755)
	_ = os.WriteFile(filepath.Join(proc, "asound", "cards"), []byte(
		" 0 [vc4hdmi0       ]: vc4-hdmi - vc4-hdmi-0\n 2 [CODEC          ]: USB-Audio - USB AUDIO  CODEC\n"), 0o644)
	holder := filepath.Join(proc, "4242")
	_ = os.MkdirAll(filepath.Join(holder, "fd"), 0o755)
	_ = os.WriteFile(filepath.Join(holder, "comm"), []byte("wireplumber\n"), 0o644)
	_ = os.Symlink("/dev/snd/pcmC2D0c", filepath.Join(holder, "fd", "7"))
	other := filepath.Join(proc, "99")
	_ = os.MkdirAll(filepath.Join(other, "fd"), 0o755)
	_ = os.WriteFile(filepath.Join(other, "comm"), []byte("bash\n"), 0o644)
	_ = os.Symlink("/dev/snd/pcmC0D0p", filepath.Join(other, "fd", "3")) // another card: not a holder
	return proc
}

func TestHoldersNamesWhoHasTheCardOpen(t *testing.T) {
	proc := fakeProc(t)
	for _, dev := range []string{"plughw:CARD=CODEC,DEV=0", "hw:CODEC,0", "plughw:2,0", "hw:2"} {
		got := holdersIn(proc, dev)
		if len(got) != 1 || got[0] != "wireplumber (pid 4242)" {
			t.Errorf("%s: %q", dev, got)
		}
	}
	if got := holdersIn(proc, "plughw:CARD=vc4hdmi0,DEV=0"); len(got) != 1 || got[0] != "bash (pid 99)" {
		t.Errorf("card 0: %q", got)
	}
	if got := holdersIn(proc, "plughw:CARD=Nope,DEV=0"); got != nil {
		t.Errorf("unknown card: %q", got)
	}
}

func TestWaitFreeReturnsAsSoonAsTheCardIsFree(t *testing.T) {
	proc := fakeProc(t)
	go func() {
		time.Sleep(150 * time.Millisecond)
		_ = os.Remove(filepath.Join(proc, "4242", "fd", "7")) // the sound server lets go
	}()
	start := time.Now()
	if got := waitFreeIn(proc, "plughw:CARD=CODEC,DEV=0", 3*time.Second, 50*time.Millisecond); got != nil {
		t.Fatalf("still held: %q", got)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("waited past the release")
	}
	_ = os.Symlink("/dev/snd/pcmC2D0p", filepath.Join(proc, "4242", "fd", "8"))
	if got := waitFreeIn(proc, "plughw:CARD=CODEC,DEV=0", 200*time.Millisecond, 50*time.Millisecond); len(got) != 1 {
		t.Fatalf("a card that stays busy must name its holder: %q", got)
	}
}
