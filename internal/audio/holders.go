package audio

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ATCUSA/hamstack/internal/safetext"
)

// A program that opens an ALSA device directly (ardopcf does) fails with "Device or resource busy" while anything
// else has it open: a sound server (PipeWire keeps a card a few seconds after its last client stops), a program
// listening to the radio, or another modem. These find who it is, so the failure can say.

var (
	cardKeyRE = regexp.MustCompile(`(?i)CARD=([^,]+)`)
	devKeyRE  = regexp.MustCompile(`(?i)DEV=(\d+)`)
	hwRE      = regexp.MustCompile(`^(?:plug)?hw:([^,=]+)(?:,(\d+))?$`)
)

// pcmNode is the /dev/snd name prefix of an ALSA device's PCM ("pcmC2D0"), or "" if the device is not a card's.
func pcmNode(proc, device string) string {
	card, dev := "", "0"
	if m := cardKeyRE.FindStringSubmatch(device); m != nil {
		card = m[1]
		if d := devKeyRE.FindStringSubmatch(device); d != nil {
			dev = d[1]
		}
	} else if m := hwRE.FindStringSubmatch(device); m != nil {
		card = m[1]
		if m[2] != "" {
			dev = m[2]
		}
	} else {
		return ""
	}
	index := card
	if _, err := strconv.Atoi(card); err != nil {
		index = cardIndex(filepath.Join(proc, "asound", "cards"), card)
		if index == "" {
			return ""
		}
	}
	return "pcmC" + index + "D" + dev
}

func cardIndex(cards, id string) string {
	b, err := os.ReadFile(cards)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if m := cardRE.FindStringSubmatch(line); m != nil && strings.EqualFold(strings.TrimSpace(m[2]), id) {
			return m[1]
		}
	}
	return ""
}

// Holders lists the programs that have an ALSA device ("plughw:CARD=CODEC,DEV=0", "hw:2,0") open, as
// "name (pid N)". Only this user's programs can be seen; nil if none or the device is not a card's.
func Holders(device string) []string { return holdersIn("/proc", device) }

// PlaybackHolders and CaptureHolders are Holders for one direction of the device: a card's input can be held by
// one program while its output is free.
func PlaybackHolders(device string) []string { return holdersDir("/proc", device, "p") }
func CaptureHolders(device string) []string  { return holdersDir("/proc", device, "c") }

func holdersIn(proc, device string) []string { return holdersDir(proc, device, "pc") }

func holdersDir(proc, device, dirs string) []string {
	node := pcmNode(proc, device)
	if node == "" {
		return nil
	}
	prefix := "/dev/snd/" + node
	pids, _ := os.ReadDir(proc)
	var out []string
	for _, p := range pids {
		if _, err := strconv.Atoi(p.Name()); err != nil {
			continue
		}
		fds, err := os.ReadDir(filepath.Join(proc, p.Name(), "fd"))
		if err != nil {
			continue
		}
		for _, fd := range fds {
			target, err := os.Readlink(filepath.Join(proc, p.Name(), "fd", fd.Name()))
			if err != nil || !strings.HasPrefix(target, prefix) {
				continue
			}
			rest := target[len(prefix):]
			if len(rest) != 1 || !strings.Contains(dirs, rest) {
				continue // pcmC2D0p or pcmC2D0c, not pcmC2D10p
			}
			comm, _ := os.ReadFile(filepath.Join(proc, p.Name(), "comm"))
			out = append(out, fmt.Sprintf("%s (pid %s)", safetext.Line(strings.TrimSpace(string(comm))), p.Name()))
			break
		}
	}
	return out
}

// WaitFree waits up to timeout for nothing else to have device open, and returns who still has it (nil once free).
func WaitFree(device string, timeout time.Duration) []string {
	return waitFreeIn("/proc", device, timeout, 500*time.Millisecond)
}

func waitFreeIn(proc, device string, timeout, every time.Duration) []string {
	deadline := time.Now().Add(timeout)
	for {
		h := holdersIn(proc, device)
		if h == nil || time.Now().After(deadline) {
			return h
		}
		time.Sleep(every)
	}
}
