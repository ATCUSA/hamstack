package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ATCUSA/hamstack/internal/audio"
	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/profile"
	"github.com/ATCUSA/hamstack/internal/versions"
)

// xara is a VARA-compatible HF and FM modem for Linux (MIT licence). Its original author stopped distributing it;
// a maintained fork is github.com/NR5P/xara. hamstack runs the xara it finds (on PATH or in ~/.local/bin) and does not
// download or build it.
const xaraWhere = "xara is not installed. hamstack does not download it: build it from the maintained fork, " +
	"https://github.com/NR5P/xara (cargo build --release -p xara), and put the xara program on your PATH or in ~/.local/bin"

// xaraArgv is the command line of one rig's xara. It keys the radio through this rig's rigctld, listens for Pat on
// localhost only, and starts at 2300 Hz, which Pat's VARA connection changes with BW500 or BW2300 as it wants. KISS
// is left off (Pat does not use it). Its web page and the speeds of a registered copy of VARA are only turned on by
// the rig's settings (xara-gui, xara-registered).
func xaraArgv(rig config.Rig, callsign, in, out string) []string {
	command, _ := config.XaraPorts(rig)
	argv := []string{"xara"}
	if callsign != "" {
		argv = append(argv, "--call", callsign)
	}
	argv = append(argv,
		"--hf", "2300",
		"--audio", "alsa", "--capture", in, "--playback", out,
		"--radio", "--ptt", "rigctld:127.0.0.1:"+strconv.Itoa(rig.RigPort),
		"--addr", "127.0.0.1", "--port", strconv.Itoa(command),
		"--kiss-port", "off")
	if rig.XaraGUI {
		// localhost (xara's default address) and view-only (no --ui-control): the dashboard shows it behind its login.
		argv = append(argv, "--ui-port", strconv.Itoa(config.XaraGUIPort(rig)))
	}
	if rig.XaraRegistered {
		argv = append(argv, "--registered")
	}
	if rig.XaraMonitor {
		argv = append(argv, "--monitor")
	}
	return argv
}

func runXara(cfg config.Config, rig config.Rig, prof profile.Profile) error {
	path, err := versions.Which("xara")
	if err != nil {
		fmt.Fprintln(os.Stderr, "hamstack: "+xaraWhere)
		os.Exit(exitRadioMissing)
	}
	st, _ := cfg.ActiveStation()
	if st.Callsign == "" {
		return fmt.Errorf("xara needs your callsign: set up a station first (hamstack station add)")
	}
	snap, _ := scanDevices()
	in, out := modemAudio(rig, prof, snap)
	if in == "" {
		return fmt.Errorf("rig %s has no sound card for xara: set the radio's sound card (hamstack rig set %s audio CARD)", rig.ID, rig.ID)
	}
	if err := waitPort(rig.RigPort, 15*time.Second); err != nil {
		return fmt.Errorf("rig %s: rig control is not running: %w", rig.ID, err)
	}
	if holders := audio.WaitFree(out, 8*time.Second); len(holders) > 0 {
		fmt.Fprintf(os.Stderr, "hamstack: rig %s: the sound card %s is in use by %s: xara may not be able to open it\n", rig.ID, out, strings.Join(holders, ", "))
	}
	argv := xaraArgv(rig, st.Callsign, in, out)
	command, _ := config.XaraPorts(rig)
	fmt.Fprintf(os.Stderr, "hamstack: rig %s: xara on port %d (audio in %s, out %s)\n", rig.ID, command, in, out)
	return superviseModem(rig, "xara", path, argv)
}
