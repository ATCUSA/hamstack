package audio

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A USB sound card's /proc/asound/<card>/stream0, as the kernel's usb-audio driver writes it (representative of a
// stereo USB CODEC like the one in many HF radios, and of a CM108 that captures in mono).
const streamStereo = `USB AUDIO  CODEC at usb-xhci-hcd.0-1.2, full speed : USB Audio

Playback:
  Status: Stop
  Interface 1
    Altset 1
    Format: S16_LE
    Channels: 2
    Endpoint: 0x02 (2 OUT) (ADAPTIVE)
    Rates: 8000, 11025, 16000, 22050, 32000, 44100, 48000
    Bits: 16

Capture:
  Status: Stop
  Interface 2
    Altset 1
    Format: S16_LE
    Channels: 2
    Endpoint: 0x82 (2 IN) (ASYNC)
    Rates: 8000, 11025, 16000, 22050, 32000, 44100, 48000
    Bits: 16
`

const streamCM108 = `C-Media Electronics Inc. USB PnP Sound Device at usb-0000:00:14.0-2, full speed : USB Audio

Playback:
  Status: Stop
  Interface 1
    Altset 1
    Format: S16_LE
    Channels: 2
    Endpoint: 0x01 (1 OUT) (ADAPTIVE)
    Rates: 44100, 48000

Capture:
  Status: Stop
  Interface 2
    Altset 1
    Format: S16_LE
    Channels: 1
    Endpoint: 0x82 (2 IN) (ADAPTIVE)
    Rates: 44100 - 48000 (continuous)
`

func TestCardCapsFromStream(t *testing.T) {
	c := capsFromStream(streamStereo)
	if c.Capture != 2 || c.Playback != 2 || c.Rate != 48000 {
		t.Errorf("stereo CODEC: %+v", c)
	}
	c = capsFromStream(streamCM108)
	if c.Capture != 1 || c.Playback != 2 || c.Rate != 48000 {
		t.Errorf("CM108 captures in mono: %+v", c)
	}
	c = capsFromStream("")
	if c.Capture != 2 || c.Playback != 2 || c.Rate != 48000 {
		t.Errorf("no stream file: stereo at 48 kHz: %+v", c)
	}
	c = capsFromStream(strings.ReplaceAll(streamStereo, ", 48000", ""))
	if c.Rate != 44100 {
		t.Errorf("no 48 kHz: the next best: %+v", c)
	}
}

func TestSharedConfigListensToManyAndKeepsCardsApart(t *testing.T) {
	conf := SharedConfig("ftdx10", "FT-DX10", "CODEC", 3, CardCaps{Capture: 1, Playback: 2, Rate: 48000})
	for _, want := range []string{
		"pcm.hamstack_ftdx10 {", "type dsnoop", "type dmix", "type asym", "type plug",
		`pcm "hw:CARD=CODEC,DEV=0"`, "ipc_perm 0600", "rate 48000",
		`hint.description "hamstack: FT-DX10, shared"`,
	} {
		if !strings.Contains(conf, want) {
			t.Errorf("missing %q:\n%s", want, conf)
		}
	}
	// capture at the card's own channel count, playback at its own
	if !strings.Contains(conf, "channels 1") || !strings.Contains(conf, "channels 2") {
		t.Errorf("channels:\n%s", conf)
	}
	other := SharedConfig("vhf", "Digirig", "Device", 4, CardCaps{Capture: 1, Playback: 2, Rate: 48000})
	if keys(conf) == keys(other) {
		t.Error("two rigs must not share an IPC key: their cards would be mixed up")
	}
	if SharedDevice("my-rig") != "hamstack_my_rig" {
		t.Errorf("device name: %s", SharedDevice("my-rig"))
	}
}

func keys(conf string) string {
	var out []string
	for _, l := range strings.Split(conf, "\n") {
		if strings.Contains(l, "ipc_key") {
			out = append(out, strings.TrimSpace(l))
		}
	}
	return strings.Join(out, ",")
}

func TestWriteSharedConfigGivesAnEnvironmentOnlyThatProcessUses(t *testing.T) {
	dir := t.TempDir()
	env, err := WriteShared(dir, "ftdx10", "SHARED CONF")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(env, "ALSA_CONFIG_PATH=") || !strings.Contains(env, "alsa.conf:") || !strings.HasSuffix(env, filepath.Join(dir, "ftdx10.conf")) {
		t.Fatalf("env: %s", env)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "ftdx10.conf"))
	if string(b) != "SHARED CONF" {
		t.Fatalf("file: %q", b)
	}
}

// ardopcf asks for a playback period of exactly rate/100 frames (10 ms: 120 at its 12 kHz) and stops if it cannot have
// it ("cannot set playback period size", seen on the Pi). For capture it asks for about 600 frames before it sets the
// rate, so the capture period must come out at 600 exactly at 12 kHz (50 ms), or the rate is then refused ("cannot
// set capture sample rate", also seen on the Pi).
func TestSharedCardPeriodsSuitArdopcf(t *testing.T) {
	for rate, want := range map[int][2]string{48000: {"period_size 2400", "period_size 480"}, 44100: {"period_size 2205", "period_size 441"}} {
		conf := SharedConfig("hf", "", "CODEC", 0, CardCaps{Capture: 2, Playback: 2, Rate: rate})
		rx, tx, _ := strings.Cut(conf, "type dmix")
		if !strings.Contains(rx, want[0]) || !strings.Contains(tx, want[1]) {
			t.Errorf("%d Hz: capture %q, playback %q:\n%s", rate, want[0], want[1], conf)
		}
	}
}

// dmix uses more than its own key (a second shared-memory segment for its mixing buffer). With the capture and
// playback keys next to each other, playback failed while capture was open ("unable to initialize sum ring
// buffer", seen on the Pi when ardopcf keyed up). Keys must be well apart, also between rigs.
func TestSharedCardKeysAreWellApart(t *testing.T) {
	var all []int
	for i := 0; i < 4; i++ {
		for _, l := range strings.Split(SharedConfig("r", "", "C", i, CardCaps{2, 2, 48000}), "\n") {
			if f := strings.Fields(l); len(f) == 2 && f[0] == "ipc_key" {
				var k int
				_, _ = fmt.Sscan(f[1], &k)
				all = append(all, k)
			}
		}
	}
	for i := range all {
		for j := range all {
			if i != j && all[i]-all[j] < 4 && all[j]-all[i] < 4 {
				t.Fatalf("keys %d and %d are too close: %v", all[i], all[j], all)
			}
		}
	}
}
