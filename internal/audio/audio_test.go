package audio

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolve(t *testing.T) {
	cards := []string{"HDMI", "Generic", "Device"}
	if d, ok := resolve(cards, []string{"CODEC", "device"}); !ok || d != "plughw:CARD=Device,DEV=0" {
		t.Errorf("got %q %v", d, ok)
	}
	if d, ok := resolve(cards, []string{"CODEC"}); ok || d != "plughw:CARD=CODEC,DEV=0" {
		t.Errorf("fallback got %q %v", d, ok)
	}
	if d, ok := resolve(cards, nil); ok || d != "" {
		t.Errorf("no hints got %q %v", d, ok)
	}
}

func TestCardName(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cards")
	os.WriteFile(p, []byte(" 0 [NVidia         ]: HDA-Intel - HDA NVidia\n                      HDA NVidia at 0x84080000 irq 148\n 2 [Device         ]: USB-Audio - USB Audio Device\n                      C-Media USB Audio Device at usb-0000:00:14.0-2\n"), 0o644)
	if got := cardNameFrom(p, "Device"); got != "USB Audio Device" {
		t.Errorf("got %q", got)
	}
	if got := cardNameFrom(p, "device"); got != "USB Audio Device" {
		t.Errorf("ids compare ignoring case: %q", got)
	}
	if got := cardNameFrom(p, "Nope"); got != "" {
		t.Errorf("unknown card: %q", got)
	}
}
