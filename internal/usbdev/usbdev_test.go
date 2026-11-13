package usbdev

import (
	"os"
	"path/filepath"
	"testing"
)

// fixture builds a fake sysfs and /dev modelled on how an IC-7300 MK2 appears (see the project
// wiki): one hub inside the radio, with a composite serial device (two CDC-ACM interfaces,
// Port A and Port B) and a C-Media sound codec side by side. A Digirig is plugged straight into
// the computer, and a built-in PCI sound card must be ignored.
type fixture struct{ sys, dev string }

func newFixture(t *testing.T) fixture {
	t.Helper()
	root := t.TempDir()
	f := fixture{filepath.Join(root, "sys"), filepath.Join(root, "dev")}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	w := func(p, body string) {
		must(os.MkdirAll(filepath.Dir(p), 0o755))
		must(os.WriteFile(p, []byte(body), 0o644))
	}
	usbdev := func(dir, vid, pid, serial, mfr, prod string) {
		w(filepath.Join(dir, "idVendor"), vid+"\n")
		w(filepath.Join(dir, "idProduct"), pid+"\n")
		if serial != "" {
			w(filepath.Join(dir, "serial"), serial+"\n")
		}
		w(filepath.Join(dir, "manufacturer"), mfr+"\n")
		w(filepath.Join(dir, "product"), prod+"\n")
	}
	link := func(target, name string) {
		must(os.MkdirAll(filepath.Dir(name), 0o755))
		must(os.Symlink(target, name))
	}
	base := filepath.Join(f.sys, "devices", "pci0000:00", "0000:00:14.0")
	hub := filepath.Join(base, "usb7", "7-1")
	radio := filepath.Join(hub, "7-1.1")
	codec := filepath.Join(hub, "7-1.2")
	digirig := filepath.Join(base, "usb7", "7-3")
	w(filepath.Join(base, "usb7", "idVendor"), "1d6b\n") // the root hub
	usbdev(hub, "1a40", "0101", "", "Terminus", "USB 2.0 Hub")
	usbdev(radio, "0c26", "0052", "00000001", "Icom Inc.", "IC-7300MK2")
	usbdev(codec, "0d8c", "0013", "", "C-Media Electronics Inc.", "USB Audio Device")
	usbdev(digirig, "10c4", "ea60", "DR0001", "Silicon Labs", "CP2102N USB to UART Bridge Controller")

	ttyA := filepath.Join(radio, "7-1.1:1.0", "tty", "ttyACM0")
	ttyB := filepath.Join(radio, "7-1.1:1.2", "tty", "ttyACM1")
	ttyD := filepath.Join(digirig, "7-3:1.0", "ttyUSB0", "tty", "ttyUSB0")
	w(filepath.Join(radio, "7-1.1:1.0", "bInterfaceNumber"), "00\n")
	w(filepath.Join(radio, "7-1.1:1.2", "bInterfaceNumber"), "02\n")
	w(filepath.Join(digirig, "7-3:1.0", "bInterfaceNumber"), "00\n")
	for name, iface := range map[string]string{"ttyACM0": filepath.Join(radio, "7-1.1:1.0"), "ttyACM1": filepath.Join(radio, "7-1.1:1.2"), "ttyUSB0": filepath.Join(digirig, "7-3:1.0")} {
		must(os.MkdirAll(filepath.Join(f.sys, "class", "tty", name), 0o755))
		link(iface, filepath.Join(f.sys, "class", "tty", name, "device"))
	}
	_, _, _ = ttyA, ttyB, ttyD

	// USB sound card on the radio's hub, and a PCI one that is not USB.
	sndIface := filepath.Join(codec, "7-1.2:1.0")
	must(os.MkdirAll(sndIface, 0o755))
	w(filepath.Join(f.sys, "class", "sound", "card2", "id"), "Device\n")
	link(sndIface, filepath.Join(f.sys, "class", "sound", "card2", "device"))
	pci := filepath.Join(f.sys, "devices", "pci0000:00", "0000:00:1f.3")
	must(os.MkdirAll(pci, 0o755))
	w(filepath.Join(f.sys, "class", "sound", "card0", "id"), "Generic\n")
	link(pci, filepath.Join(f.sys, "class", "sound", "card0", "device"))

	// /dev with stable by-id names.
	for _, n := range []string{"ttyACM0", "ttyACM1", "ttyUSB0"} {
		w(filepath.Join(f.dev, n), "")
	}
	link(filepath.Join(f.dev, "ttyACM0"), filepath.Join(f.dev, "serial", "by-id", "usb-Icom_Inc._IC-7300MK2_IC-7300MK2_00000001-if00"))
	link(filepath.Join(f.dev, "ttyACM1"), filepath.Join(f.dev, "serial", "by-id", "usb-Icom_Inc._IC-7300MK2_IC-7300MK2_00000001-if02"))
	link(filepath.Join(f.dev, "ttyUSB0"), filepath.Join(f.dev, "serial", "by-id", "usb-Silicon_Labs_CP2102N_USB_to_UART_Bridge_Controller_DR0001-if00-port0"))
	return f
}

func TestScan(t *testing.T) {
	f := newFixture(t)
	snap, err := Scan(f.sys, f.dev)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Serials) != 3 || len(snap.Sounds) != 1 {
		t.Fatalf("want 3 serial ports and 1 USB sound card (the PCI card must be ignored), got %d and %d", len(snap.Serials), len(snap.Sounds))
	}
	a := snap.Serials[0]
	if a.Tty != filepath.Join(f.dev, "ttyACM0") || a.VID != "0c26" || a.PID != "0052" || a.SerialNumber != "00000001" || a.Interface != 0 {
		t.Errorf("Port A: %+v", a)
	}
	if want := filepath.Join(f.dev, "serial", "by-id", "usb-Icom_Inc._IC-7300MK2_IC-7300MK2_00000001-if00"); a.ByID != want {
		t.Errorf("by-id = %q, want %q", a.ByID, want)
	}
	if snap.Serials[1].Interface != 2 {
		t.Errorf("Port B should be interface 2: %+v", snap.Serials[1])
	}
	if s := snap.Sounds[0]; s.ID != "Device" || s.Card != 2 || s.VID != "0d8c" {
		t.Errorf("sound card: %+v", s)
	}
	if a.Identity() != "0c26:0052:00000001" {
		t.Errorf("identity = %q", a.Identity())
	}
	if snap.Sounds[0].Identity() != "" {
		t.Error("a device without a USB serial number has no stable identity")
	}
}

func TestGroupsPairRadioPortsWithItsSoundCard(t *testing.T) {
	f := newFixture(t)
	snap, _ := Scan(f.sys, f.dev)
	groups := snap.Groups()
	if len(groups) != 2 {
		t.Fatalf("want 2 groups (the radio's hub, and the Digirig on its own), got %d: %+v", len(groups), groups)
	}
	var radio, dig *Group
	for i := range groups {
		if len(groups[i].Sounds) == 1 {
			radio = &groups[i]
		} else {
			dig = &groups[i]
		}
	}
	if radio == nil || len(radio.Serials) != 2 || radio.Sounds[0].ID != "Device" {
		t.Errorf("the IC-7300MK2's two serial ports and its codec belong together: %+v", radio)
	}
	if dig == nil || len(dig.Serials) != 1 || dig.Hub != "" {
		t.Errorf("the Digirig is plugged in directly: %+v", dig)
	}
}

func TestDiffSeesPlugAndUnplug(t *testing.T) {
	f := newFixture(t)
	full, _ := Scan(f.sys, f.dev)
	without := Snapshot{Serials: full.Serials[2:], Sounds: nil} // radio unplugged
	ev := Diff(full, without)
	if len(ev) != 3 {
		t.Fatalf("unplugging the radio removes 2 serial ports and 1 sound card: %+v", ev)
	}
	for _, e := range ev {
		if e.Added {
			t.Errorf("nothing was added: %+v", e)
		}
	}
	if ev := Diff(without, full); len(ev) != 3 || !ev[0].Added {
		t.Errorf("plugging it back in: %+v", ev)
	}
	if len(Diff(full, full)) != 0 {
		t.Error("no change must be no events")
	}
}

func TestEmptyMachine(t *testing.T) {
	snap, err := Scan(t.TempDir(), t.TempDir())
	if err != nil || len(snap.Serials) != 0 || len(snap.Sounds) != 0 || len(snap.Groups()) != 0 {
		t.Errorf("an empty machine is not an error: %v %+v", err, snap)
	}
}

func TestUSBID(t *testing.T) {
	if got := (Serial{VID: "0c26", PID: "0052", SerialNumber: "00000001"}).USBID(); got != "0c26:0052:00000001" {
		t.Errorf("with serial: %q", got)
	}
	if got := (Sound{VID: "0d8c", PID: "0013"}).USBID(); got != "0d8c:0013" {
		t.Errorf("without serial it is still usable, just not unique: %q", got)
	}
}

func TestLooksLikeGPS(t *testing.T) {
	for _, c := range []struct {
		s    Serial
		want bool
	}{
		{Serial{VID: "1546", PID: "01a8"}, true},                                    // u-blox 8
		{Serial{VID: "1546", PID: "01A7"}, true},                                    // u-blox 7
		{Serial{VID: "091e", PID: "0003"}, true},                                    // Garmin
		{Serial{VID: "067b", PID: "2303", Product: "USB-Serial Controller"}, false}, // generic: cannot tell
		{Serial{VID: "0403", PID: "6001", Product: "FT232R USB UART"}, false},
		{Serial{VID: "0c26", PID: "0052", Manufacturer: "Icom Inc.", Product: "IC-7300MK2"}, false},
		{Serial{VID: "10c4", PID: "ea60", Product: "CP2102 GPS Module"}, true},
		{Serial{VID: "1a86", PID: "7523", Product: "USB GNSS Receiver"}, true},
	} {
		if got := c.s.LooksLikeGPS(); got != c.want {
			t.Errorf("%+v: LooksLikeGPS = %v, want %v", c.s, got, c.want)
		}
	}
}

// A USB device chooses its own name and serial number; they are printed, so they must not carry escape sequences.
func TestDeviceNamesAreSafeToPrint(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "product"), []byte("Evil\x1b]0;owned\x07 GPS\x1b[2J\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := attr(dir, "product"); got != "Evil]0;owned GPS[2J" {
		t.Errorf("got %q", got)
	}
	if got := attr(dir, "missing"); got != "" {
		t.Errorf("a missing attribute: %q", got)
	}
}
