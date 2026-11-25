package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ATCUSA/hamstack/internal/config"
)

const d2gListing = `input:
  0   2 ch  Ryzen HD Audio Controller Digital Microphone
  1   2 ch  Monitor of USB Audio Device Analog Stereo
  2   2 ch  USB Audio Device Mono
output:
  0   2 ch  Ryzen HD Audio Controller Speaker
  1   2 ch  USB Audio Device Analog Stereo
`

func TestData2GDevicePicksTheCardNotItsMonitor(t *testing.T) {
	if n, ok := data2gDevice(d2gListing, "input", "USB Audio Device"); !ok || n != 2 {
		t.Errorf("input: %d %v (the Monitor entry is an output's, not a microphone)", n, ok)
	}
	if n, ok := data2gDevice(d2gListing, "output", "usb audio device"); !ok || n != 1 {
		t.Errorf("output: %d %v", n, ok)
	}
	if _, ok := data2gDevice(d2gListing, "input", "Digirig"); ok {
		t.Error("a card that is not listed must not match")
	}
	if _, ok := data2gDevice(d2gListing, "input", ""); ok {
		t.Error("no name, no match")
	}
}

func TestData2GArgs(t *testing.T) {
	r := config.Rig{ID: "hf", RigPort: 4533, ArqPort: 8310}
	got := strings.Join(data2gArgs(config.Config{}, r, "N0CALL", "/c/data2g.conf", 2, 1, false), " ")
	for _, want := range []string{"host --config /c/data2g.conf", "--command-port 8310", "--kiss-port 8101",
		"--rig-model 2 --rig-device localhost:4533 --ptt-method cat", "--mycall N0CALL", "--input-device 2", "--output-device 1"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	if strings.Contains(got, "--web") {
		t.Errorf("the headless host has no web port: %s", got)
	}
	ui := strings.Join(data2gArgs(config.Config{}, r, "", "/c/d.conf", -1, -1, true), " ")
	if !strings.HasPrefix(ui, "web --web-listen 0.0.0.0 --web-port 8401 ") || strings.Contains(ui, "--mycall") || strings.Contains(ui, "-device 2") {
		t.Errorf("web front end: %s", ui)
	}
}

func TestHasData2GPassword(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(body), 0o600)
		return p
	}
	if !hasData2GPassword(write("a", "[audio]\nx=1\n\n[web]\npassword=\"pbkdf2-sha256$100000$s$k\"\n")) {
		t.Error("a password under [web] is a password")
	}
	for name, body := range map[string]string{
		"b": "[web]\nport=8400\n",
		"c": "[other]\npassword=\"x\"\n", // another section's key is not the web password
		"d": "[web]\npassword=\n",
		"e": "",
	} {
		if hasData2GPassword(write(name, body)) {
			t.Errorf("%s: not a web password", name)
		}
	}
	if hasData2GPassword(filepath.Join(dir, "missing")) {
		t.Error("no file, no password")
	}
}

func TestData2GRigsAndPasswordErrors(t *testing.T) {
	var cfg config.Config
	cfg.AddRig(config.Rig{ID: "a", Profile: "dummy", Modem: config.Data2G})
	cfg.AddRig(config.Rig{ID: "m", Profile: "dummy", Modem: config.Mercury})
	cfg.AddRig(config.Rig{ID: "b", Profile: "dummy", Modem: config.Data2G, Off: true})
	if got := data2gRigs(cfg); len(got) != 2 || got[0].ID != "a" || got[1].ID != "b" {
		t.Errorf("rigs with Data2G, on or off: %+v", got)
	}
	if err := cmdData2G(nil); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Errorf("usage: %v", err)
	}
	if err := cmdData2G([]string{"password", "a", "extra"}); err == nil {
		t.Error("too many arguments")
	}
}

func TestPatArgs(t *testing.T) {
	st := config.Station{ID: "club", PatConfig: "/c/club.json"}
	got := strings.Join(patArgs(st, "192.0.2.10:8081", "/s"), " ")
	want := "pat --config /c/club.json --log /s/pat-club.log --event-log /s/pat-club-events.json http --addr 192.0.2.10:8081"
	if got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
}
