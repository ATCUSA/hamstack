package main

import (
	"flag"
	"github.com/ATCUSA/hamstack/internal/netinfo"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/usbdev"
)

func TestGPSDArgs(t *testing.T) {
	got := gpsdArgs(2948, "/run/user/1000/hamstack-gpsd.sock", "/dev/serial/by-id/x")
	want := []string{"gpsd", "-N", "-n", "-b", "-S", "2948", "-F", "/run/user/1000/hamstack-gpsd.sock", "/dev/serial/by-id/x"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
	// Never gpsd's own port or socket, and never anything that writes to the receiver.
	if slices.Contains(got, "2947") {
		t.Error("must not use gpsd's standard port")
	}
	if !slices.Contains(got, "-b") {
		t.Error("gpsd must run read-only, so a radio chosen by mistake is not probed")
	}
	if got := gpsdArgs(2948, "", "/dev/ttyACM0"); slices.Contains(got, "-F") {
		t.Errorf("no control socket without a runtime dir: %v", got)
	}
}

func TestResolveGPS(t *testing.T) {
	dir := t.TempDir()
	dev := filepath.Join(dir, "gps")
	if err := os.WriteFile(dev, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	snap := usbdev.Snapshot{Serials: []usbdev.Serial{{Tty: "/dev/ttyACM3", ByID: "/dev/serial/by-id/new", VID: "1546", PID: "01a8"}}}

	if got, err := resolveGPS(config.GPS{Device: dev, USB: "1546:01a8"}, snap); err != nil || got != dev {
		t.Errorf("saved device present: %q, %v", got, err)
	}
	// The saved path is gone (a different cable or kernel naming): found again by USB identity.
	if got, err := resolveGPS(config.GPS{Device: filepath.Join(dir, "gone"), USB: "1546:01a8"}, snap); err != nil || got != "/dev/serial/by-id/new" {
		t.Errorf("by identity: %q, %v", got, err)
	}
	snap.Serials[0].ByID = ""
	if got, _ := resolveGPS(config.GPS{USB: "1546:01a8"}, snap); got != "/dev/ttyACM3" {
		t.Errorf("without a by-id path: %q", got)
	}
	// Unplugged, or a different receiver: an error, so the service exits "not there" rather than guessing.
	if _, err := resolveGPS(config.GPS{Device: filepath.Join(dir, "gone"), USB: "1546:01a7"}, snap); err == nil {
		t.Error("a different USB device must not be taken for the receiver")
	}
	if _, err := resolveGPS(config.GPS{Device: filepath.Join(dir, "gone")}, usbdev.Snapshot{}); err == nil {
		t.Error("an unplugged receiver must be reported")
	}
}

func TestGPSCandidatesAndRigPorts(t *testing.T) {
	snap := usbdev.Snapshot{Serials: []usbdev.Serial{
		{Tty: "/dev/ttyACM0", VID: "1546", PID: "01a8", Interface: 0},
		{Tty: "/dev/ttyUSB0", VID: "0c26", PID: "0052", Product: "IC-7300MK2"},
		{Tty: "/dev/ttyACM1", VID: "1546", PID: "01a8", Interface: 1}, // a second port of the same receiver
	}}
	c := gpsCandidates(snap)
	if len(c) != 1 || c[0].Tty != "/dev/ttyACM0" {
		t.Errorf("candidates = %+v", c)
	}
	var cfg config.Config
	cfg.AddRig(config.Rig{ID: "hf", Profile: "ic7300mk2", CATID: "0c26:0052", CAT: "/dev/serial/by-id/radio"})
	if !usedByRig(cfg, snap.Serials[1]) || usedByRig(cfg, snap.Serials[0]) {
		t.Error("a radio's port must be recognised so it is never offered to gpsd")
	}
}

// A device named on the command line that is not a USB port hamstack can see is still checked against the
// rigs, by its path and by what a by-id link points at.
func TestRigUsingPath(t *testing.T) {
	dir := t.TempDir()
	tty := filepath.Join(dir, "ttyS0")
	link := filepath.Join(dir, "radio-link")
	other := filepath.Join(dir, "ttyS1")
	for _, f := range []string{tty, other} {
		if err := os.WriteFile(f, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(tty, link); err != nil {
		t.Fatal(err)
	}
	var cfg config.Config
	cfg.AddRig(config.Rig{ID: "dummy", Profile: "dummy"})
	cfg.AddRig(config.Rig{ID: "hf", Profile: "ic7300mk2", CAT: link})
	for path, want := range map[string]string{link: "hf", tty: "hf", other: "", filepath.Join(dir, "gone"): ""} {
		if got := rigUsingPath(cfg, path); got != want {
			t.Errorf("rigUsingPath(%s) = %q, want %q", filepath.Base(path), got, want)
		}
	}
}

func TestParseInterspersed(t *testing.T) {
	for _, c := range []struct {
		args []string
		pos  []string
		addr string
		port int
	}{
		{[]string{"--addr", "pi:2947"}, nil, "pi:2947", 0},
		{[]string{"--addr=pi:2947"}, nil, "pi:2947", 0},
		{[]string{"/dev/ttyACM0", "--port", "2950"}, []string{"/dev/ttyACM0"}, "", 2950},
		{[]string{"--port", "2950", "/dev/ttyACM0"}, []string{"/dev/ttyACM0"}, "", 2950},
		{nil, nil, "", 0},
	} {
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		addr := fs.String("addr", "", "")
		port := fs.Int("port", 0, "")
		pos, err := parseInterspersed(fs, c.args)
		if err != nil || !slices.Equal(pos, c.pos) || *addr != c.addr || *port != c.port {
			t.Errorf("%v: pos %v addr %q port %d, %v", c.args, pos, *addr, *port, err)
		}
	}
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.String("addr", "", "")
	if _, err := parseInterspersed(fs, []string{"--addr"}); err == nil {
		t.Error("a flag without its value must be an error")
	}
}

func TestNetworkRowsAndAgwTarget(t *testing.T) {
	var cfg config.Config
	cfg.UI = true
	cfg.AddRig(config.Rig{ID: "hf", Profile: "dummy", Modem: config.Mercury})
	cfg.AddRig(config.Rig{ID: "pkt", Profile: "dummy", Modem: config.Direwolf})
	cfg.AddStation(config.Station{ID: "me", Callsign: "N0CALL", HTTPAddr: "localhost:8080", PatConfig: "/p.json"})
	cfg.Active = "me"
	cfg.SetGPS(config.GPS{Device: "/dev/ttyACM0"})
	rows := networkRows(cfg, []netinfo.Listener{
		{IP: net.ParseIP("127.0.0.1"), Port: 4532},
		{IP: net.IPv4zero, Port: 8002}, // the second rig's Direwolf: AGW is 8000+2n with n = 1
	})
	byPort := map[int]netRow{}
	for _, r := range rows {
		byPort[r.Port] = r
	}
	if r := byPort[4532]; !r.Open || r.Scope != netinfo.Local || scopeText(r) != "this computer only" {
		t.Errorf("rigctld: %+v", r)
	}
	if r := byPort[8002]; r.Service != "direwolf@pkt" || !r.Open || r.Scope != netinfo.Network || scopeText(r) != "ANY computer on your network" {
		t.Errorf("direwolf AGW: %+v", r)
	}
	if r := byPort[8300]; r.Service != "mercury@hf" || r.Open {
		t.Errorf("mercury ARQ: %+v", r)
	}
	if r := byPort[2948]; r.Service != "gpsd" || r.Open || scopeText(r) != "not running" {
		t.Errorf("gpsd: %+v", r)
	}
	if r := byPort[8080]; r.Service != "pat" {
		t.Errorf("pat: %+v", r)
	}
	ui := 0
	for _, r := range rows {
		if strings.Contains(r.What, "web UI") && r.Service != "pat" {
			ui++
			if r.Note != "no password" {
				t.Errorf("the Mercury UI must be flagged: %+v", r)
			}
		}
	}
	if ui != 1 {
		t.Errorf("expected the Mercury UI row once, got %d", ui)
	}
	cfg.UI = false
	for _, r := range networkRows(cfg, nil) {
		if strings.Contains(r.What, "web UI") && r.Service != "pat" {
			t.Errorf("UI off but listed: %+v", r)
		}
	}

	addr, rig, err := agwTarget(cfg, "", "")
	if err != nil || rig.ID != "pkt" || addr != "localhost:8002" {
		t.Errorf("agwTarget = %q %v %v", addr, rig.ID, err)
	}
	if _, _, err := agwTarget(cfg, "hf", ""); err == nil {
		t.Error("an HF rig has no Direwolf")
	}
	if a, r, err := agwTarget(cfg, "", "pi:8000"); err != nil || a != "pi:8000" || r.ID != "" {
		t.Errorf("override: %q %v %v", a, r, err)
	}
	var none config.Config
	if _, _, err := agwTarget(none, "", ""); err == nil || !strings.Contains(err.Error(), "no rig uses Direwolf") {
		t.Errorf("no rigs: %v", err)
	}
}
