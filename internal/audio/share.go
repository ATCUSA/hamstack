package audio

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// A shared sound card. A modem that opens a card directly ("plughw:CARD=CODEC,DEV=0") holds it alone: a second
// program (another modem, a monitor, a gateway listening on several modes) gets "Device or resource busy". ALSA can
// share a card itself, with no sound server and no root: dsnoop lets many programs read the same capture, dmix mixes
// what they play. hamstack writes such a configuration per rig and gives it only to the programs it starts, through
// ALSA_CONFIG_PATH (the system's own configuration first, then this file): nothing system-wide changes, and the
// desktop's sound and ~/.asoundrc are left alone.
//
// What it cannot do: share a card a sound server (PipeWire, PulseAudio) has open at the same time, which holds the
// hardware the same way a modem does. And sharing the transmit side is mixing, not taking turns: only one program may
// key the radio, which is for hamstack to arbitrate.

// CardCaps are what a card can do, as the shared configuration needs it: channels each way and a sample rate.
type CardCaps struct {
	Capture, Playback int
	Rate              int
}

var (
	channelsRE = regexp.MustCompile(`^\s*Channels:\s*(\d+)`)
	ratesRE    = regexp.MustCompile(`^\s*Rates:\s*(.*)$`)
	numberRE   = regexp.MustCompile(`\d+`)
)

// Caps reads a card's capabilities from /proc/asound/<card>/stream0 (USB cards have one); stereo at 48 kHz if not.
func Caps(cardID string) CardCaps {
	b, _ := os.ReadFile(filepath.Join("/proc/asound", cardID, "stream0"))
	return capsFromStream(string(b))
}

func capsFromStream(text string) CardCaps {
	c := CardCaps{}
	section := ""
	rates := map[string][]int{}
	for _, line := range strings.Split(text, "\n") {
		switch strings.TrimSpace(line) {
		case "Playback:", "Capture:":
			section = strings.TrimSuffix(strings.TrimSpace(line), ":")
			continue
		}
		if m := channelsRE.FindStringSubmatch(line); m != nil {
			n, _ := strconv.Atoi(m[1])
			if section == "Capture" && c.Capture == 0 {
				c.Capture = n
			}
			if section == "Playback" && c.Playback == 0 {
				c.Playback = n
			}
		}
		if m := ratesRE.FindStringSubmatch(line); m != nil && section != "" && rates[section] == nil {
			for _, s := range numberRE.FindAllString(m[1], -1) {
				n, _ := strconv.Atoi(s)
				rates[section] = append(rates[section], n)
			}
			if strings.Contains(m[1], "continuous") && len(rates[section]) == 2 {
				lo, hi := rates[section][0], rates[section][1]
				rates[section] = nil
				for _, r := range []int{48000, 44100} {
					if r >= lo && r <= hi {
						rates[section] = append(rates[section], r)
					}
				}
			}
		}
	}
	if c.Capture == 0 {
		c.Capture = 2
	}
	if c.Playback == 0 {
		c.Playback = 2
	}
	// One rate both ways (dsnoop and dmix run the card at a fixed rate): 48 kHz if both sides have it.
	c.Rate = 48000
	if rates["Capture"] != nil || rates["Playback"] != nil {
		for _, want := range []int{48000, 44100} {
			if has(rates["Capture"], want) && has(rates["Playback"], want) {
				c.Rate = want
				break
			}
		}
	}
	return c
}

func has(list []int, n int) bool {
	if list == nil {
		return true // that side said nothing: assume it takes what the other does
	}
	for _, v := range list {
		if v == n {
			return true
		}
	}
	return false
}

// SharedDevice is the ALSA name of a rig's shared card.
func SharedDevice(rigID string) string { return "hamstack_" + strings.ReplaceAll(rigID, "-", "_") }

// ipcBase keeps hamstack's shared-memory keys apart from other programs'; each rig gets two (capture, playback).
const ipcBase = 0x48530000

// SharedConfig is the ALSA configuration of a rig's shared card. index is the rig's number (it keeps the IPC keys
// of two rigs apart); cardID is its ALSA card id.
func SharedConfig(rigID, rigName, cardID string, index int, c CardCaps) string {
	dev := SharedDevice(rigID)
	hw := fmt.Sprintf("hw:CARD=%s,DEV=0", cardID)
	name := strings.NewReplacer(`"`, "", `\`, "").Replace(rigName)
	if name == "" {
		name = rigID
	}
	// Periods that come out whole at any program's rate once converted, chosen for ardopcf, which is the strict one
	// (seen on the Pi): playback 10 ms (it insists on exactly 120 frames at its 12 kHz), capture 50 ms (it asks for
	// about 600 frames before it sets the rate, and 600 must then mean 12 kHz). Both suit Mercury and Direwolf.
	slave := func(ch, period int) string {
		return fmt.Sprintf("  slave {\n    pcm %q\n    rate %d\n    channels %d\n    format S16_LE\n    period_size %d\n    buffer_size %d\n  }\n", hw, c.Rate, ch, period, 8*period)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# hamstack: the shared sound card of rig %s. Written each time one of its services starts; do not edit.\n", rigID)
	fmt.Fprintf(&b, "pcm.%s_rx {\n  type dsnoop\n  ipc_key %d\n  ipc_perm 0600\n%s}\n", dev, ipcBase+2*index, slave(c.Capture, c.Rate/20))
	fmt.Fprintf(&b, "pcm.%s_tx {\n  type dmix\n  ipc_key %d\n  ipc_perm 0600\n%s}\n", dev, ipcBase+2*index+1, slave(c.Playback, c.Rate/100))
	fmt.Fprintf(&b, "pcm.%s {\n  type plug\n  slave.pcm {\n    type asym\n    capture.pcm \"%s_rx\"\n    playback.pcm \"%s_tx\"\n  }\n  hint.description \"hamstack: %s, shared\"\n}\n", dev, dev, dev, name)
	return b.String()
}

// systemALSAConfig is the system's own top-level ALSA configuration: it must come first, or nothing else is defined.
var systemALSAConfig = "/usr/share/alsa/alsa.conf"

// WriteShared writes a rig's shared-card configuration into dir and returns the environment entry that gives it
// to a program (and only to that program).
func WriteShared(dir, rigID, conf string) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, rigID+".conf")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(conf), 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", err
	}
	return "ALSA_CONFIG_PATH=" + systemALSAConfig + ":" + path, nil
}
