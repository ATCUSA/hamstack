package audio

import "testing"

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
