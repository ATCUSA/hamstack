package main

import (
	"errors"
	"strings"
	"testing"
	"time"
)

const (
	pwInputOnly = `[{"type":"PipeWire:Interface:Device","info":{"props":{"media.class":"Audio/Device","api.alsa.card":2}}},
 {"type":"PipeWire:Interface:Node","info":{"props":{"media.class":"Audio/Source","api.alsa.pcm.card":2}}}]`
	pwBoth = `[{"type":"PipeWire:Interface:Device","info":{"props":{"media.class":"Audio/Device","api.alsa.card":2}}},
 {"type":"PipeWire:Interface:Node","info":{"props":{"media.class":"Audio/Source","api.alsa.pcm.card":2}}},
 {"type":"PipeWire:Interface:Node","info":{"props":{"media.class":"Audio/Sink","api.alsa.pcm.card":2}}}]`
)

// fakePW is a PipeWire that offers only the input of card 2 until WirePlumber is restarted (if fixes is true).
func fakePW(fixes bool, playbackHeldFor int) (*pwProbe, *int) {
	restarts := 0
	held := playbackHeldFor
	p := &pwProbe{
		dump: func() ([]byte, error) {
			if restarts > 0 && fixes {
				return []byte(pwBoth), nil
			}
			return []byte(pwInputOnly), nil
		},
		held: func(dev string, playback bool) []string {
			if !playback {
				return []string{"pipewire (pid 1)"} // PipeWire itself holding the input it offers is no reason to wait
			}
			if held != 0 {
				held--
				return []string{"ardopcf (pid 7)"}
			}
			return nil
		},
		restart: func() error { restarts++; return nil },
		sleep:   func(time.Duration) {},
		settle:  time.Second,
		wait:    2 * time.Second,
	}
	return p, &restarts
}

var codecCard = []pwCardRef{{Rig: "ftdx10", ID: "CODEC", Index: 2}}

func TestPipeWireGetsTheCardsOutputBack(t *testing.T) {
	p, restarts := fakePW(true, 2) // the modem just stopped lets go after two looks
	var out strings.Builder
	p.fix(codecCard, &out)
	if *restarts != 1 {
		t.Fatalf("WirePlumber restarted %d times, want 1:\n%s", *restarts, out.String())
	}
	if s := out.String(); !strings.Contains(s, "not offering the output of ftdx10's sound card") || !strings.Contains(s, "both ways again") {
		t.Errorf("messages:\n%s", s)
	}
}

func TestPipeWireIsLeftAloneWhileTheOutputIsHeld(t *testing.T) {
	p, restarts := fakePW(true, -1) // held for good
	var out strings.Builder
	p.fix(codecCard, &out)
	if *restarts != 0 {
		t.Fatal("restarting WirePlumber while a program holds the output cannot help")
	}
	if !strings.Contains(out.String(), "ardopcf (pid 7) has it open") {
		t.Errorf("must say who holds it:\n%s", out.String())
	}
}

func TestPipeWireThatStaysBrokenIsReported(t *testing.T) {
	p, restarts := fakePW(false, 0)
	var out strings.Builder
	p.fix(codecCard, &out)
	if *restarts != 1 || !strings.Contains(out.String(), "still has no output") {
		t.Errorf("restarts %d:\n%s", *restarts, out.String())
	}
}

func TestNoPipeWireNothingToDo(t *testing.T) {
	p, restarts := fakePW(true, 0)
	p.dump = func() ([]byte, error) { return nil, errors.New("pw-dump: not found") }
	var out strings.Builder
	p.fix(codecCard, &out)
	if *restarts != 0 || out.Len() != 0 {
		t.Errorf("restarts %d, said %q", *restarts, out.String())
	}
	p, restarts = fakePW(true, 0)
	p.dump = func() ([]byte, error) { return []byte(pwBoth), nil }
	p.fix(codecCard, &out)
	if *restarts != 0 || out.Len() != 0 {
		t.Errorf("a card offered both ways: restarts %d, said %q", *restarts, out.String())
	}
}

// A modem takes a moment to let go of the card after it is stopped; the program must not start before (its stream
// through PipeWire fails for good if the card is busy). PipeWire itself holding the card is no reason to wait.
func TestRadioAppWaitsForTheCardToBeLetGo(t *testing.T) {
	p, _ := fakePW(true, 0)
	p.dump = func() ([]byte, error) { return []byte(pwBoth), nil }
	old, oldHolders := pipeWire, cardHolders
	defer func() { pipeWire, cardHolders = old, oldHolders }()
	pipeWire = *p
	looks := 0
	cardHolders = func(string) []string {
		looks++
		if looks <= 3 {
			return []string{"xara (pid 7)", "pipewire (pid 1)"}
		}
		return []string{"pipewire (pid 1)"}
	}
	var out strings.Builder
	readyCards([]pwCardRef{{Rig: "ftdx10", ID: "CODEC", Index: 2}}, &out)
	if looks != 4 || strings.Contains(out.String(), "still in use") {
		t.Errorf("looked %d times; said %q", looks, out.String())
	}
	looks, out = -100, strings.Builder{}
	readyCards([]pwCardRef{{Rig: "ftdx10", ID: "CODEC", Index: 2}}, &out)
	if !strings.Contains(out.String(), "still in use by xara (pid 7)") {
		t.Errorf("a card that stays busy must be named: %q", out.String())
	}
}
