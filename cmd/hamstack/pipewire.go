package main

import (
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/ATCUSA/hamstack/internal/audio"
	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/svc"
)

// pwCardRef is a rig's sound card as PipeWire and ALSA know it.
type pwCardRef struct {
	Rig   string // the rig's label, for messages
	ID    string // ALSA card id ("CODEC")
	Index int    // ALSA card index
}

// pwProbe gives PipeWire a radio's sound card back in both directions before a program that reaches the radio
// through PipeWire (WSJT-X, JS8Call) starts. See audio.PipeWireCard for why it can lose one. Its system access is
// in fields so a test can fake it.
type pwProbe struct {
	dump    func() ([]byte, error)                      // pw-dump's output; an error means no PipeWire to fix
	held    func(device string, playback bool) []string // who has one direction of an ALSA device open
	restart func() error                                // start WirePlumber again, so it looks at the cards afresh
	sleep   func(time.Duration)
	settle  time.Duration // how long to wait for a just-stopped modem to let go of the card
	wait    time.Duration // how long to wait for PipeWire to offer the card again
}

var pipeWire = pwProbe{
	dump: func() ([]byte, error) { return exec.Command("pw-dump").Output() },
	held: func(device string, playback bool) []string {
		if playback {
			return audio.PlaybackHolders(device)
		}
		return audio.CaptureHolders(device)
	},
	restart: func() error { return svc.Run("restart", "wireplumber.service") },
	sleep:   time.Sleep,
	settle:  3 * time.Second,
	wait:    8 * time.Second,
}

// lost is what PipeWire is missing of each card now, keyed by card index; nil when nothing (or no PipeWire).
func (p pwProbe) lost(cards []pwCardRef) map[int]audio.PWCard {
	b, err := p.dump()
	if err != nil {
		return nil
	}
	out := map[int]audio.PWCard{}
	for _, c := range cards {
		if pc, err := audio.PipeWireCard(b, c.Index); err == nil && pc.Missing() != "" {
			out[c.Index] = pc
		}
	}
	return out
}

// fix starts WirePlumber again when PipeWire has lost a direction of one of cards and that direction is free now
// (WirePlumber can only offer what it can open). It tells w what it did and why.
func (p pwProbe) fix(cards []pwCardRef, w io.Writer) {
	lost := p.lost(cards)
	if len(lost) == 0 {
		return
	}
	var redo []string
	for _, c := range cards {
		pc, ok := lost[c.Index]
		if !ok {
			continue
		}
		dev := "hw:CARD=" + c.ID + ",DEV=0"
		busy := func() []string {
			var h []string
			if !pc.Sink {
				h = append(h, p.held(dev, true)...)
			}
			if !pc.Source {
				h = append(h, p.held(dev, false)...)
			}
			return h
		}
		h := busy()
		for waited := time.Duration(0); len(h) > 0 && waited < p.settle; waited += 250 * time.Millisecond {
			p.sleep(250 * time.Millisecond)
			h = busy()
		}
		if len(h) > 0 {
			fmt.Fprintf(w, "hamstack: PipeWire has no %s for %s's sound card, and cannot look again while %s has it open\n", pc.Missing(), c.Rig, strings.Join(h, ", "))
			continue
		}
		redo = append(redo, pc.Missing()+" of "+c.Rig+"'s sound card")
	}
	if len(redo) == 0 {
		return
	}
	fmt.Fprintf(w, "hamstack: PipeWire had lost the %s (a modem had it open when PipeWire looked at it): restarting WirePlumber so it looks again\n", strings.Join(redo, " and the "))
	if err := p.restart(); err != nil {
		fmt.Fprintf(w, "hamstack: %v\n", err)
		return
	}
	for waited := time.Duration(0); ; waited += 500 * time.Millisecond {
		left := p.lost(cards)
		if len(left) == 0 {
			fmt.Fprintln(w, "hamstack: PipeWire offers the radio's sound card both ways again")
			return
		}
		if waited >= p.wait {
			for _, c := range cards {
				if pc, ok := left[c.Index]; ok {
					fmt.Fprintf(w, "hamstack: PipeWire still has no %s for %s's sound card: choose it again in the program's audio settings once it appears\n", pc.Missing(), c.Rig)
				}
			}
			return
		}
		p.sleep(500 * time.Millisecond)
	}
}

// radioCards are the sound cards of the active rigs.
func radioCards(cfg config.Config) []pwCardRef {
	snap, _ := scanDevices()
	var out []pwCardRef
	for _, r := range cfg.ActiveRigs() {
		rig, prof, err := findRig(cfg, r.ID)
		if err != nil {
			continue
		}
		id := cardOf(resolveAudio(rig, prof, snap))
		if id == "" {
			continue
		}
		n, err := strconv.Atoi(audio.CardIndex(id))
		if err != nil {
			continue
		}
		out = append(out, pwCardRef{Rig: rigLabel(rig), ID: id, Index: n})
	}
	return out
}
