package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/profile"
	"github.com/ATCUSA/hamstack/internal/rigctl"
	"github.com/ATCUSA/hamstack/internal/versions"
)

// ardopcf is the ARDOP HF modem. Pat connects to it over TCP (command port, data port one higher); hamstack
// runs it as one service per rig, like Mercury, so it appears under Services and takes part in sound-card
// sharing with the other HF modems.
//
// ardopcf has no rigctld support of its own (its options are a serial port, an RTS line, a CM108 or a GPIO pin,
// and none of those may be used on the radio's serial port, which rigctld owns). Pat keys the radio through
// rigctld when its ARDOP settings name the rig and turn ptt_ctrl on, which `hamstack pat sync` does.

func runArdopcf(cfg config.Config, rig config.Rig, prof profile.Profile) error {
	snap, _ := scanDevices()
	dev := resolveAudio(rig, prof, snap)
	if dev == "" {
		return fmt.Errorf("rig %s has no sound card for ardopcf: set the radio's sound card (hamstack rig set %s audio CARD)", rig.ID, rig.ID)
	}
	if err := waitPort(rig.RigPort, 15*time.Second); err != nil {
		return fmt.Errorf("rig %s: rig control is not running: %w", rig.ID, err)
	}
	path, err := versions.Which("ardopcf")
	if err != nil {
		return fmt.Errorf("ardopcf is not installed (run: hamstack install ardopcf)")
	}
	// Its log files go to hamstack's state folder, not wherever the service happens to start.
	logs := filepath.Join(stateDir(), "ardopcf-"+rig.ID)
	if err := os.MkdirAll(logs, 0o755); err != nil {
		return err
	}
	command, _ := config.ArdopPorts(rig)
	argv := []string{"ardopcf", strconv.Itoa(command), dev, dev, "--logdir", logs}
	fmt.Fprintf(os.Stderr, "hamstack: rig %s: ardopcf on port %d (audio %s)\n", rig.ID, command, dev)
	return syscall.Exec(path, argv, os.Environ())
}

// runUnkey makes sure a rig is not transmitting. The ardopcf unit runs it when it stops: Pat holds the PTT, so if
// ardopcf or Pat dies mid-transmission nothing else would let go of it. Failure to reach rigctld is not an error
// (it may be the reason the unit is stopping).
func runUnkey(rigID string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	rig, _, err := findRig(cfg, rigID)
	if err != nil {
		return err
	}
	c := rigctl.New("127.0.0.1:" + strconv.Itoa(rig.RigPort))
	defer c.Close()
	if _, err := c.Do("T 0"); err != nil {
		fmt.Fprintf(os.Stderr, "hamstack: rig %s: could not confirm the radio is unkeyed: %v\n", rig.ID, err)
	}
	return nil
}
