package audio

import "testing"

// pwDump is the shape of pw-dump's output, trimmed to the properties read, as a PipeWire 1.2 on a Raspberry Pi
// wrote it for a USB codec (card 2) next to an HDMI output (card 0). Card numbers come as numbers; the "alsa.card"
// of an older version's node as a string.
const pwDumpBoth = `[
 {"id": 66, "type": "PipeWire:Interface:Device", "info": {"props": {"media.class": "Audio/Device", "api.alsa.card": 2}}},
 {"id": 65, "type": "PipeWire:Interface:Device", "info": {"props": {"media.class": "Audio/Device", "api.alsa.card": 0}}},
 {"id": 38, "type": "PipeWire:Interface:Node", "info": {"props": {"media.class": "Audio/Sink", "api.alsa.pcm.card": 2}}},
 {"id": 37, "type": "PipeWire:Interface:Node", "info": {"props": {"media.class": "Audio/Source", "alsa.card": "2"}}},
 {"id": 40, "type": "PipeWire:Interface:Node", "info": {"props": {"media.class": "Audio/Sink", "api.alsa.pcm.card": 0}}},
 {"id": 81, "type": "PipeWire:Interface:Node", "info": {"props": {"media.class": "Stream/Input/Audio", "node.name": "pw-record"}}},
 {"id": 3, "type": "PipeWire:Interface:Module", "info": {"props": null}}
]`

// What the same card looked like when a modem had its playback open as WirePlumber started: input only.
const pwDumpInputOnly = `[
 {"id": 48, "type": "PipeWire:Interface:Device", "info": {"props": {"media.class": "Audio/Device", "api.alsa.card": 2}}},
 {"id": 57, "type": "PipeWire:Interface:Node", "info": {"props": {"media.class": "Audio/Source", "api.alsa.pcm.card": 2}}},
 {"id": 35, "type": "PipeWire:Interface:Node", "info": {"props": {"media.class": "Audio/Sink", "node.name": "auto_null"}}}
]`

func TestPipeWireCardSaysWhichDirectionsItOffers(t *testing.T) {
	for _, tc := range []struct {
		name, dump string
		card       int
		want       PWCard
		missing    string
	}{
		{"both", pwDumpBoth, 2, PWCard{Managed: true, Sink: true, Source: true}, ""},
		{"input only", pwDumpInputOnly, 2, PWCard{Managed: true, Source: true}, "output"},
		{"output only (HDMI)", pwDumpBoth, 0, PWCard{Managed: true, Sink: true}, "input"},
		{"not managed", pwDumpBoth, 5, PWCard{}, ""},
		{"no PipeWire objects", `[]`, 2, PWCard{}, ""},
	} {
		got, err := PipeWireCard([]byte(tc.dump), tc.card)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got != tc.want || got.Missing() != tc.missing {
			t.Errorf("%s: %+v missing %q, want %+v missing %q", tc.name, got, got.Missing(), tc.want, tc.missing)
		}
	}
	if _, err := PipeWireCard([]byte("not json"), 2); err == nil {
		t.Error("garbage must be an error, not \"nothing missing\"")
	}
}

func TestHoldersOfOneDirection(t *testing.T) {
	proc := fakeProc(t) // card 2's capture is held, its playback is free
	if got := holdersDir(proc, "hw:CARD=CODEC,DEV=0", "p"); got != nil {
		t.Errorf("playback is free: %q", got)
	}
	if got := holdersDir(proc, "hw:CARD=CODEC,DEV=0", "c"); len(got) != 1 {
		t.Errorf("capture is held: %q", got)
	}
}
