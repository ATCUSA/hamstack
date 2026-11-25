package main

import (
	"fmt"
	"strconv"

	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/rigctl"
	"github.com/ATCUSA/hamstack/internal/svc"
	"github.com/ATCUSA/hamstack/internal/units"
)

// A rig can have more than one HF modem set up (ARDOP and a VARA-style one), but they cannot run together: each
// opens the radio's sound card for itself, and the radio has one transmitter. Switching stops and disables the one
// it ran (applyUnits does that for a service that is no longer wanted) and enables and starts the other.

// startModemUnit enables and starts a modem's service. A variable so that a test does not touch systemd.
var startModemUnit = func(unit string) error { return svc.Run("enable", "--now", unit) }

// switchRigModem makes `to` the modem rig `id` runs.
func switchRigModem(id, to string) (string, error) {
	cfg := loadCfg()
	rig, ok := cfg.RigByID(id)
	if !ok {
		return "", fmt.Errorf("no rig %q", id)
	}
	if rig.Modem == to {
		return "rig " + id + " already runs " + to, nil
	}
	if !rig.NoCAT {
		// Stopping a modem in the middle of an over would leave the session, and maybe the transmitter, hanging.
		c := rigctl.New("127.0.0.1:" + strconv.Itoa(rig.RigPort))
		ptt, err := c.PTT()
		c.Close()
		if err == nil && ptt {
			return "", fmt.Errorf("the radio is transmitting: switch modems when it has finished")
		}
	}
	msg, err := mutate(func(c *config.Config) (string, error) {
		r, ok := c.RigByID(id)
		if !ok {
			return "", fmt.Errorf("no rig %q", id)
		}
		from := r.Modem
		if err := r.SwitchModem(to); err != nil {
			return "", err
		}
		return fmt.Sprintf("rig %s: %s stopped, %s started", id, orDash(from), to), nil
	})
	if err != nil {
		return "", err
	}
	if err := startModemUnit(units.Service{Kind: to, Rig: id}.Unit()); err != nil {
		return msg, fmt.Errorf("rig %s now uses %s, but it did not start: %w (see: hamstack logs %s@%s)", id, to, err, to, id)
	}
	if note := patFollowUp(loadCfg()); note != "" {
		msg += ". " + note
	}
	return msg, nil
}
