package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/ATCUSA/hamstack/internal/config"
)

func TestXaraArgv(t *testing.T) {
	rig := config.Rig{ID: "hf", RigPort: 4533, ArqPort: 8310, XaraGUI: config.Set(false), XaraMonitor: config.Set(false)}
	got := xaraArgv(rig, "K0TEST", "hamstack_hf", "plughw:CARD=CODEC,DEV=0")
	want := []string{"xara", "--call", "K0TEST", "--hf", "2300", "--audio", "alsa", "--capture", "hamstack_hf", "--playback", "plughw:CARD=CODEC,DEV=0",
		"--radio", "--ptt", "rigctld:127.0.0.1:4533", "--addr", "127.0.0.1", "--port", "8310", "--kiss-port", "off"}
	if !slices.Equal(got, want) {
		t.Fatalf("\n got %q\nwant %q", got, want)
	}
	// Speeds that need a registered copy of VARA are never asked for, and the web page (which can key the radio
	// with --ui-control) stays off.
	for _, bad := range []string{"--registered", "--ui-port", "--ui-control", "0.0.0.0"} {
		if slices.Contains(got, bad) || strings.Contains(strings.Join(got, " "), bad) {
			t.Errorf("%s must not be passed", bad)
		}
	}
}

func TestXaraArgvScreenAndRegisteredOnlyWhenTurnedOn(t *testing.T) {
	rig := config.Rig{ID: "hf", RigPort: 4533, ArqPort: 8310, XaraMonitor: config.Set(false)} // the screen left to its default: on
	got := strings.Join(xaraArgv(rig, "K0TEST", "in", "out"), " ")
	if !strings.Contains(got, "--ui-port 8317") || strings.Contains(got, "--ui-addr") || strings.Contains(got, "--ui-control") || strings.Contains(got, "--registered") {
		t.Errorf("screen on: view-only, on localhost, in the rig's block: %s", got)
	}
	rig.XaraRegistered = true
	if got := xaraArgv(rig, "K0TEST", "in", "out"); got[len(got)-1] != "--registered" {
		t.Errorf("registered on: %q", got)
	}
	rig.XaraMonitor = nil // left to its default: on
	if got := xaraArgv(rig, "K0TEST", "in", "out"); got[len(got)-1] != "--monitor" {
		t.Errorf("monitor on: %q", got)
	}
}

// xara takes the VARA ports of the rig's block, like Mercury and Data2G, so Pat's VARA HF address is the same
// whichever of them the rig runs; and it makes ardopcf move to the middle of the block when both are set up.
func TestXaraSharesTheVARAPorts(t *testing.T) {
	rig := config.Rig{ID: "hf", ArqPort: 8320, Modem: config.Xara, Alt: []string{config.Ardopcf}}
	if c, d := config.XaraPorts(rig); c != 8320 || d != 8321 {
		t.Errorf("xara ports %d %d", c, d)
	}
	m, _, _, _ := config.MercuryPorts(rig)
	if c, _ := config.XaraPorts(rig); c != m {
		t.Error("xara and Mercury must share the command port")
	}
	if c, _ := config.ArdopPorts(rig); c != 8324 {
		t.Errorf("ardopcf beside xara must move to +4, got %d", c)
	}
	if !config.IsVARAModem(config.Xara) || !config.IsHFModem(config.Xara) {
		t.Error("xara is an HF modem with the VARA interface")
	}
}
