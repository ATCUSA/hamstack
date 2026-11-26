package main

import (
	"fmt"
	"io"
	"net"
	"time"

	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/svc"
	"github.com/ATCUSA/hamstack/internal/units"
)

// FLRig and rig control (rigctld) cannot both open a radio's control port: hamlib takes it alone, and flrig cannot use
// rigctld (it has only its own radio drivers). The way to share is the other one: flrig owns the port, and rigctld
// goes through it with hamlib's FLRig backend (model 4), which is the "flrig" radio profile. Researched from flrig
// 2.0.12's and hamlib's source (2026-11-26); not tried on a radio.

// flrigPort is flrig's XML-RPC server: every interface, no password, and it can key the radio.
const flrigPort = 12345

// flrigRigs are the rigs whose rig control goes through FLRig, and the other rigs with rig control of their own.
func flrigRigs(cfg config.Config) (through, own []config.Rig) {
	for _, r := range cfg.ActiveRigs() {
		switch {
		case r.NoCAT:
		case r.Profile == "flrig":
			through = append(through, r)
		default:
			own = append(own, r)
		}
	}
	return through, own
}

// flrigNote says how FLRig fits the rigs set up here, for the Apps page and the launch log.
func flrigNote(cfg config.Config) string {
	through, own := flrigRigs(cfg)
	switch {
	case len(through) > 0:
		return "Rig control for " + through[0].ID + " goes through FLRig: open FLRig and rig control connects to it"
	case len(own) > 0:
		return "Cannot share " + own[0].ID + "'s control port with rig control (hamlib holds it): to use FLRig with it, set the rig's radio model to FLRig (Settings, Rigs)"
	}
	return ""
}

// flrigTakesOver restarts rig control for each rig that goes through FLRig once FLRig answers, so it connects to the
// FLRig that just started (one started before it found nobody to talk to).
func flrigTakesOver(cfg config.Config, w io.Writer, wait time.Duration) {
	through, _ := flrigRigs(cfg)
	if len(through) == 0 {
		return
	}
	deadline := time.Now().Add(wait)
	for {
		c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", flrigPort), time.Second)
		if err == nil {
			c.Close()
			break
		}
		if time.Now().After(deadline) {
			fmt.Fprintf(w, "hamstack: FLRig did not answer on port %d: rig control for %s cannot reach the radio through it\n", flrigPort, through[0].ID)
			return
		}
		time.Sleep(time.Second)
	}
	for _, r := range through {
		u := units.Service{Kind: config.RigCtld, Rig: r.ID}.Unit()
		fmt.Fprintf(w, "hamstack: FLRig answers: restarting %s so rig control goes through it (the rig's modem restarts with it)\n", u)
		_ = svc.Run("restart", u)
	}
}
