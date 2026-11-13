package main

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/profile"
	"github.com/ATCUSA/hamstack/internal/usbdev"
)

func quiet(input string) (*asker, *bytes.Buffer) {
	var out bytes.Buffer
	return &asker{in: bufio.NewReader(strings.NewReader(input)), out: &out}, &out
}

func icom() profile.Profile {
	var p profile.Profile
	p.ID, p.Name = "ic7300mk2", "Icom IC-7300 MK2"
	p.USBIDs = []string{"0c26:0052"}
	p.Rig.DeviceGlob = "/dev/serial/by-id/usb-Icom_Inc._IC-7300MK2_*-if00"
	return p
}

func serial(tty, byID, vid, pid, sn string, iface int, hub string) usbdev.Serial {
	return usbdev.Serial{Tty: tty, ByID: byID, VID: vid, PID: pid, SerialNumber: sn, Interface: iface, Hub: hub}
}

func sound(card int, id, vid, pid, hub string) usbdev.Sound {
	return usbdev.Sound{Card: card, ID: id, VID: vid, PID: pid, Hub: hub}
}

const (
	icomA1 = "/dev/serial/by-id/usb-Icom_Inc._IC-7300MK2_IC-7300MK2_11111111-if00"
	icomB1 = "/dev/serial/by-id/usb-Icom_Inc._IC-7300MK2_IC-7300MK2_11111111-if02"
	icomA2 = "/dev/serial/by-id/usb-Icom_Inc._IC-7300MK2_IC-7300MK2_22222222-if00"
)

// An IC-7300 MK2 shows two serial ports (A and B) and a sound codec on one hub. The profile's pattern
// must pick Port A, and the codec on the same hub must come along.
func oneRadio() usbdev.Snapshot {
	return usbdev.Snapshot{
		Serials: []usbdev.Serial{
			serial("/dev/ttyACM0", icomA1, "0c26", "0052", "11111111", 0, "/sys/7-1"),
			serial("/dev/ttyACM1", icomB1, "0c26", "0052", "11111111", 2, "/sys/7-1"),
		},
		Sounds: []usbdev.Sound{sound(2, "Device", "0d8c", "0013", "/sys/7-1")},
	}
}

func TestDetectPicksCATPortAndItsSoundCard(t *testing.T) {
	a, out := quiet("")
	var cfg config.Config
	r := config.Rig{ID: "hf", Profile: "ic7300mk2"}
	assignDetected(a, cfg, &r, icom(), oneRadio())
	if r.CAT != icomA1 || r.CATID != "0c26:0052:11111111" {
		t.Errorf("must pick Port A (if00), not Port B: %+v\n%s", r, out)
	}
	if r.Audio != "Device" || r.AudioUSB != "0d8c:0013" {
		t.Errorf("the codec on the radio's hub belongs to the rig: %+v", r)
	}
}

func TestDetectNothingPluggedIn(t *testing.T) {
	a, out := quiet("")
	r := config.Rig{ID: "hf", Profile: "ic7300mk2"}
	assignDetected(a, config.Config{}, &r, icom(), usbdev.Snapshot{})
	if r.CAT != "" || !strings.Contains(out.String(), "hamstack rig identify hf") {
		t.Errorf("no radio: assign nothing and say how to proceed: %+v\n%s", r, out)
	}
}

func twinRadios() usbdev.Snapshot {
	return usbdev.Snapshot{
		Serials: []usbdev.Serial{
			serial("/dev/ttyACM0", icomA1, "0c26", "0052", "11111111", 0, "/sys/7-1"),
			serial("/dev/ttyACM2", icomA2, "0c26", "0052", "22222222", 0, "/sys/7-2"),
		},
		Sounds: []usbdev.Sound{sound(2, "Device", "0d8c", "0013", "/sys/7-1"), sound(3, "Device_1", "0d8c", "0013", "/sys/7-2")},
	}
}

func TestTwoIdenticalRadiosAreNotGuessed(t *testing.T) {
	a, out := quiet("\n") // the user declines to choose
	r := config.Rig{ID: "hf", Profile: "ic7300mk2"}
	assignDetected(a, config.Config{}, &r, icom(), twinRadios())
	if r.CAT != "" {
		t.Errorf("two identical radios must never be guessed: %+v", r)
	}
	if !strings.Contains(out.String(), "More than one device") || !strings.Contains(out.String(), "rig identify hf") {
		t.Errorf("should list the candidates and point at identify:\n%s", out)
	}
	// Choosing one assigns it, with the sound card from its own hub.
	a, _ = quiet("2\n")
	r = config.Rig{ID: "hf", Profile: "ic7300mk2"}
	assignDetected(a, config.Config{}, &r, icom(), twinRadios())
	if r.CAT != icomA2 || r.Audio != "Device_1" {
		t.Errorf("second radio and its own sound card: %+v", r)
	}
}

func TestSecondRigSkipsTheRadioTheFirstUses(t *testing.T) {
	var cfg config.Config
	cfg.AddRig(config.Rig{ID: "first", Profile: "ic7300mk2", CAT: icomA1})
	a, _ := quiet("")
	r := config.Rig{ID: "second", Profile: "ic7300mk2"}
	assignDetected(a, cfg, &r, icom(), twinRadios())
	if r.CAT != icomA2 {
		t.Errorf("an already-claimed radio must not be offered again, so the second rig gets the other one without asking: %+v", r)
	}
}

// scans returns the given snapshots in order, repeating the last.
func scans(snaps ...usbdev.Snapshot) func() (usbdev.Snapshot, error) {
	i := 0
	return func() (usbdev.Snapshot, error) {
		s := snaps[min(i, len(snaps)-1)]
		i++
		return s, nil
	}
}

func TestIdentifyByUnplugAndReplug(t *testing.T) {
	a, out := quiet("\n") // press Enter after unplugging
	var cfg config.Config
	cfg.AddRig(config.Rig{ID: "hf", Profile: "ic7300mk2", CAT: "/dev/serial/by-id/stale", Audio: "Old", AudioUSB: "dead:beef"})
	rig, _ := cfg.RigByID("hf")
	// before: nothing; then the radio appears.
	scan := scans(usbdev.Snapshot{}, usbdev.Snapshot{}, oneRadio())
	if err := identifyRig(a, &cfg, rig, icom(), scan, func(time.Duration) {}, 10*time.Second); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if rig.CAT != icomA1 || rig.CATID != "0c26:0052:11111111" || rig.Audio != "Device" {
		t.Errorf("identify should replace the old assignment with what appeared: %+v", rig)
	}
}

func TestIdentifyOnlyTakesNewDevices(t *testing.T) {
	// A second, unrelated radio stays plugged in the whole time and must not be picked.
	a, out := quiet("\n")
	other := usbdev.Snapshot{
		Serials: []usbdev.Serial{serial("/dev/ttyACM5", icomA2, "0c26", "0052", "22222222", 0, "/sys/7-9")},
		Sounds:  []usbdev.Sound{sound(5, "Device_1", "0d8c", "0013", "/sys/7-9")},
	}
	both := usbdev.Snapshot{Serials: append(append([]usbdev.Serial{}, other.Serials...), oneRadio().Serials...), Sounds: append(append([]usbdev.Sound{}, other.Sounds...), oneRadio().Sounds...)}
	var cfg config.Config
	cfg.AddRig(config.Rig{ID: "hf", Profile: "ic7300mk2"})
	rig, _ := cfg.RigByID("hf")
	if err := identifyRig(a, &cfg, rig, icom(), scans(other, other, both), func(time.Duration) {}, 10*time.Second); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if rig.CAT != icomA1 {
		t.Errorf("only what appeared after the unplug counts; the bystander radio must be ignored: %+v", rig)
	}
}

func TestIdentifyTimesOut(t *testing.T) {
	a, _ := quiet("\n")
	var cfg config.Config
	cfg.AddRig(config.Rig{ID: "hf", Profile: "ic7300mk2", CAT: icomA1})
	rig, _ := cfg.RigByID("hf")
	err := identifyRig(a, &cfg, rig, icom(), scans(usbdev.Snapshot{}), func(time.Duration) { time.Sleep(15 * time.Millisecond) }, 60*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "nothing new appeared") {
		t.Errorf("a radio that never appears must give a clear error: %v", err)
	}
	if rig.CAT != icomA1 {
		t.Errorf("a failed identify must leave the old assignment alone: %+v", rig)
	}
}

func TestIdentifyNeedsASerialPort(t *testing.T) {
	a, _ := quiet("\n")
	var cfg config.Config
	cfg.AddRig(config.Rig{ID: "hf", Profile: "ic7300mk2"})
	rig, _ := cfg.RigByID("hf")
	onlySound := usbdev.Snapshot{Sounds: []usbdev.Sound{sound(2, "Device", "0d8c", "0013", "")}}
	err := identifyRig(a, &cfg, rig, icom(), scans(usbdev.Snapshot{}, usbdev.Snapshot{}, onlySound), func(time.Duration) {}, 10*time.Second)
	if err == nil || !strings.Contains(err.Error(), "no USB serial port") {
		t.Errorf("a sound card alone cannot be a CAT port: %v", err)
	}
}

func TestSetRigField(t *testing.T) {
	r := config.Rig{ID: "a", Profile: "dummy", CAT: "/dev/x", CATID: "1:2:3", Audio: "Device", AudioUSB: "1:2"}
	for _, ok := range []struct{ k, v string }{{"name", "My HF"}, {"modem", "mercury"}, {"modem", "none"}, {"bands", "hf, vhf"}} {
		if err := setRigField(&r, ok.k, ok.v); err != nil {
			t.Errorf("%s=%s: %v", ok.k, ok.v, err)
		}
	}
	if r.Modem != "" || len(r.Bands) != 2 || r.Name != "My HF" {
		t.Errorf("%+v", r)
	}
	if err := setRigField(&r, "cat", "auto"); err != nil || r.CAT != "" || r.CATID != "" {
		t.Errorf("auto must clear the saved assignment: %+v %v", r, err)
	}
	for _, bad := range []struct{ k, v string }{{"modem", "vara"}, {"bands", "cb"}, {"nope", "x"}, {"profile", "no-such-profile"}} {
		if err := setRigField(&r, bad.k, bad.v); err == nil {
			t.Errorf("%s=%s should be rejected", bad.k, bad.v)
		}
	}
}
