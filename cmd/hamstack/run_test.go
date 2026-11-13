package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/profile"
	"github.com/ATCUSA/hamstack/internal/usbdev"
)

func TestResolveCATUsesTheSavedPortIfItExists(t *testing.T) {
	dir := t.TempDir()
	dev := filepath.Join(dir, "usb-Icom-if00")
	os.WriteFile(dev, nil, 0o644)
	got, err := resolveCAT(config.Rig{CAT: dev, CATID: "0c26:0052:1"}, icom(), usbdev.Snapshot{})
	if err != nil || got != dev {
		t.Errorf("got %q, %v", got, err)
	}
}

// The point of saving a USB identity: the by-id name changed (or the radio came back on a different
// path) but the same physical radio is plugged in, so it is found again.
func TestResolveCATRefindsTheRadioByUSBIdentity(t *testing.T) {
	snap := usbdev.Snapshot{Serials: []usbdev.Serial{
		serial("/dev/ttyACM7", "", "0c26", "0052", "22222222", 0, ""), // another radio of the same model
		serial("/dev/ttyACM3", "/dev/serial/by-id/new-name-if02", "0c26", "0052", "11111111", 2, ""),
		serial("/dev/ttyACM2", "/dev/serial/by-id/new-name-if00", "0c26", "0052", "11111111", 0, ""),
	}}
	r := config.Rig{CAT: "/nonexistent/old-name-if00", CATID: "0c26:0052:11111111"}
	got, err := resolveCAT(r, icom(), snap)
	if err != nil || got != "/dev/serial/by-id/new-name-if00" {
		t.Errorf("must match the saved serial number and the same interface (if00), got %q, %v", got, err)
	}
	r.CAT = "/nonexistent/old-name-if02"
	if got, _ := resolveCAT(r, icom(), snap); got != "/dev/serial/by-id/new-name-if02" {
		t.Errorf("the interface in the old name (if02) is kept, got %q", got)
	}
	// With no by-id name the plain tty is used.
	snap.Serials[0].SerialNumber = "11111111"
	snap.Serials = snap.Serials[:1]
	if got, _ := resolveCAT(config.Rig{CAT: "/nonexistent/x", CATID: "0c26:0052:11111111"}, icom(), snap); got != "/dev/ttyACM7" {
		t.Errorf("fallback to the tty name, got %q", got)
	}
}

func TestResolveCATDoesNotConfuseTwinRadios(t *testing.T) {
	snap := usbdev.Snapshot{Serials: []usbdev.Serial{
		serial("/dev/ttyACM1", "/dev/serial/by-id/b-if00", "0c26", "0052", "22222222", 0, ""),
	}}
	// The saved radio (serial 1111...) is not plugged in; only a different radio of the same model is.
	_, err := resolveCAT(config.Rig{CAT: "/nonexistent/a-if00", CATID: "0c26:0052:11111111"}, icom(), snap)
	if err == nil || !strings.Contains(err.Error(), "radio not found") {
		t.Errorf("must not fall back to a different radio of the same model: %v", err)
	}
}

func TestResolveCATFromProfilePatternAndNoDevice(t *testing.T) {
	dir := t.TempDir()
	dev := filepath.Join(dir, "usb-Icom_Inc._IC-7300MK2_x_999-if00")
	os.WriteFile(dev, nil, 0o644)
	prof := icom()
	prof.Rig.DeviceGlob = filepath.Join(dir, "usb-Icom_Inc._IC-7300MK2_*-if00")
	if got, err := resolveCAT(config.Rig{}, prof, usbdev.Snapshot{}); err != nil || got != dev {
		t.Errorf("profile pattern: %q %v", got, err)
	}
	prof.Rig.DeviceGlob = filepath.Join(dir, "nothing-*")
	if _, err := resolveCAT(config.Rig{}, prof, usbdev.Snapshot{}); err == nil {
		t.Error("no match must be an error (exit status 3: radio not there)")
	}
	var dummy profile.Profile // no device pattern at all
	if got, err := resolveCAT(config.Rig{}, dummy, usbdev.Snapshot{}); err != nil || got != "" {
		t.Errorf("a radio with no device needs none: %q %v", got, err)
	}
}

func TestResolveAudio(t *testing.T) {
	var null profile.Profile
	null.Audio.Null = true
	if got := resolveAudio(config.Rig{Audio: "Device"}, null, usbdev.Snapshot{}); got != "" {
		t.Errorf("null audio profile: %q", got)
	}
	var p profile.Profile
	p.Audio.CardHints = []string{"Nothing"}
	twins := usbdev.Snapshot{Sounds: []usbdev.Sound{
		{Card: 1, ID: "Device", VID: "0d8c", PID: "0013", SerialNumber: "AAA"},
		{Card: 2, ID: "Device_1", VID: "0d8c", PID: "0013", SerialNumber: "BBB"},
	}}
	// Card ids are handed out in plug-in order, so they can swap between boots: the USB serial wins.
	if got := resolveAudio(config.Rig{Audio: "Device", AudioUSB: "0d8c:0013:BBB"}, p, twins); got != "plughw:CARD=Device_1,DEV=0" {
		t.Errorf("must follow the serial number, not the remembered card id: %q", got)
	}
	// Saved without a serial: fine if it is the only card of its kind...
	one := usbdev.Snapshot{Sounds: twins.Sounds[:1]}
	if got := resolveAudio(config.Rig{AudioUSB: "0d8c:0013"}, p, one); got != "plughw:CARD=Device,DEV=0" {
		t.Errorf("sole card of that kind: %q", got)
	}
	// ...but with identical cards it must not guess; it falls through to the hints.
	if got := resolveAudio(config.Rig{AudioUSB: "0d8c:0013"}, p, twins); strings.Contains(got, "Device") {
		t.Errorf("two identical cards and no serial: must not pick one: %q", got)
	}
}
