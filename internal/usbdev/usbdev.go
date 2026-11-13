// Package usbdev finds the USB serial ports and sound cards on the machine and groups the ones
// that belong to the same physical device (a radio's CAT port and its built-in sound card hang off
// the same USB hub). It reads sysfs and /dev only, so it needs no privileges.
package usbdev

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/ATCUSA/hamstack/internal/safetext"
)

// Serial is a USB serial port.
type Serial struct {
	Tty          string // /dev/ttyACM0
	ByID         string // /dev/serial/by-id/... (stable across ports and reboots), if present
	VID, PID     string
	SerialNumber string
	Manufacturer string
	Product      string
	Interface    int
	Hub          string // sysfs path of the hub it hangs off ("" when plugged straight into the computer)
	path         string // sysfs path of its USB device
}

// Sound is a USB sound card.
type Sound struct {
	Card         int
	ID           string // ALSA id, the bracketed name in /proc/asound/cards (use it in plughw:CARD=<id>)
	VID, PID     string
	SerialNumber string
	Product      string
	Hub          string
	path         string
}

// Identity names a device in a way that survives replugging: vendor:product plus the USB serial
// number. When the device has no serial number it is empty and the device can only be told apart
// from an identical one by the port it is plugged into.
func (s Serial) Identity() string { return identity(s.VID, s.PID, s.SerialNumber) }

// Identity: see Serial.Identity.
func (s Sound) Identity() string { return identity(s.VID, s.PID, s.SerialNumber) }

// USBID is vendor:product, plus :serial when the device has a serial number. It is what a rig
// stores to find a device again; without a serial, an identical device would match too.
func (s Serial) USBID() string { return usbKey(s.VID, s.PID, s.SerialNumber) }

// USBID: see Serial.USBID.
func (s Sound) USBID() string { return usbKey(s.VID, s.PID, s.SerialNumber) }

func usbKey(vid, pid, serial string) string {
	if serial == "" {
		return vid + ":" + pid
	}
	return vid + ":" + pid + ":" + serial
}

func identity(vid, pid, serial string) string {
	if serial == "" {
		return ""
	}
	return vid + ":" + pid + ":" + serial
}

// Snapshot is what is plugged in at one moment.
type Snapshot struct {
	Serials []Serial
	Sounds  []Sound
}

var hubName = regexp.MustCompile(`^[0-9]+-[0-9.]+$`)

func attr(dir, name string) string {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return ""
	}
	// A USB device names itself (manufacturer, product, serial number), and those names are printed: keep
	// only what is printable, so a device cannot send escape sequences to the terminal.
	return strings.TrimSpace(safetext.Line(string(b)))
}

// usbDevice walks up from a sysfs path to the USB device directory (the one with idVendor).
func usbDevice(p string) string {
	for d := p; d != "/" && d != "." && d != ""; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "idVendor")); err == nil {
			return d
		}
	}
	return ""
}

// hubOf returns the hub a USB device hangs off, or "" if it is on a root port.
func hubOf(usbDir string) string {
	parent := filepath.Dir(usbDir)
	if hubName.MatchString(filepath.Base(parent)) {
		return parent
	}
	return ""
}

// Scan reads sysfsRoot (normally /sys) and devRoot (normally /dev).
func Scan(sysfsRoot, devRoot string) (Snapshot, error) {
	var snap Snapshot

	byID := map[string]string{} // tty name -> by-id path
	ids, _ := filepath.Glob(filepath.Join(devRoot, "serial", "by-id", "*"))
	for _, id := range ids {
		if t, err := filepath.EvalSymlinks(id); err == nil {
			byID[filepath.Base(t)] = id
		}
	}

	ttys, _ := filepath.Glob(filepath.Join(sysfsRoot, "class", "tty", "*"))
	for _, t := range ttys {
		iface, err := filepath.EvalSymlinks(filepath.Join(t, "device"))
		if err != nil {
			continue // not backed by a device (virtual terminals)
		}
		usb := usbDevice(iface)
		if usb == "" {
			continue // a built-in serial port, not USB
		}
		name := filepath.Base(t)
		ifn, _ := strconv.Atoi(attr(iface, "bInterfaceNumber"))
		snap.Serials = append(snap.Serials, Serial{
			Tty: filepath.Join(devRoot, name), ByID: byID[name],
			VID: attr(usb, "idVendor"), PID: attr(usb, "idProduct"), SerialNumber: attr(usb, "serial"),
			Manufacturer: attr(usb, "manufacturer"), Product: attr(usb, "product"),
			Interface: ifn, Hub: hubOf(usb), path: usb,
		})
	}

	cards, _ := filepath.Glob(filepath.Join(sysfsRoot, "class", "sound", "card[0-9]*"))
	for _, c := range cards {
		iface, err := filepath.EvalSymlinks(filepath.Join(c, "device"))
		if err != nil {
			continue
		}
		usb := usbDevice(iface)
		if usb == "" {
			continue // built-in (PCI) sound card
		}
		n, _ := strconv.Atoi(strings.TrimPrefix(filepath.Base(c), "card"))
		snap.Sounds = append(snap.Sounds, Sound{
			Card: n, ID: attr(c, "id"),
			VID: attr(usb, "idVendor"), PID: attr(usb, "idProduct"), SerialNumber: attr(usb, "serial"),
			Product: attr(usb, "product"), Hub: hubOf(usb), path: usb,
		})
	}
	sort.Slice(snap.Serials, func(i, j int) bool { return snap.Serials[i].Tty < snap.Serials[j].Tty })
	sort.Slice(snap.Sounds, func(i, j int) bool { return snap.Sounds[i].Card < snap.Sounds[j].Card })
	return snap, nil
}

// Group is a set of devices that share a hub and so probably belong to one physical device.
// A hub inside a radio looks the same as a hub on your desk, so a group is a hint: when it holds
// several serial ports or sound cards, the user picks.
type Group struct {
	Hub     string
	Serials []Serial
	Sounds  []Sound
}

// Groups collects devices by the hub they share. Devices plugged straight into the computer each
// form a group of their own.
func (s Snapshot) Groups() []Group {
	idx := map[string]int{}
	var out []Group
	at := func(hub, own string) *Group {
		key := hub
		if key == "" {
			key = "own:" + own
		}
		if i, ok := idx[key]; ok {
			return &out[i]
		}
		idx[key] = len(out)
		out = append(out, Group{Hub: hub})
		return &out[len(out)-1]
	}
	for _, d := range s.Serials {
		g := at(d.Hub, d.path)
		g.Serials = append(g.Serials, d)
	}
	for _, d := range s.Sounds {
		g := at(d.Hub, d.path)
		g.Sounds = append(g.Sounds, d)
	}
	return out
}

// Event is a device appearing or disappearing.
type Event struct {
	Added  bool
	Kind   string // "serial" or "sound"
	Detail string
}

// Diff lists what changed between two snapshots.
func Diff(old, cur Snapshot) []Event {
	var ev []Event
	oldS, curS := map[string]Serial{}, map[string]Serial{}
	for _, d := range old.Serials {
		oldS[d.Tty+"|"+d.path] = d
	}
	for _, d := range cur.Serials {
		curS[d.Tty+"|"+d.path] = d
	}
	for k, d := range curS {
		if _, ok := oldS[k]; !ok {
			ev = append(ev, Event{true, "serial", d.Describe()})
		}
	}
	for k, d := range oldS {
		if _, ok := curS[k]; !ok {
			ev = append(ev, Event{false, "serial", d.Describe()})
		}
	}
	oldC, curC := map[string]Sound{}, map[string]Sound{}
	for _, d := range old.Sounds {
		oldC[strconv.Itoa(d.Card)+"|"+d.path] = d
	}
	for _, d := range cur.Sounds {
		curC[strconv.Itoa(d.Card)+"|"+d.path] = d
	}
	for k, d := range curC {
		if _, ok := oldC[k]; !ok {
			ev = append(ev, Event{true, "sound", d.Describe()})
		}
	}
	for k, d := range oldC {
		if _, ok := curC[k]; !ok {
			ev = append(ev, Event{false, "sound", d.Describe()})
		}
	}
	sort.Slice(ev, func(i, j int) bool { return ev[i].Detail < ev[j].Detail })
	return ev
}

// Describe is a one-line description of a serial port.
func (s Serial) Describe() string {
	return s.Tty + "  " + label(s.Manufacturer, s.Product) + "  " + usbID(s.VID, s.PID, s.SerialNumber)
}

// Describe is a one-line description of a sound card.
func (s Sound) Describe() string {
	return "card " + strconv.Itoa(s.Card) + " [" + s.ID + "]  " + s.Product + "  " + usbID(s.VID, s.PID, s.SerialNumber)
}

func label(mfr, product string) string {
	switch {
	case mfr != "" && product != "":
		return mfr + " " + product
	case product != "":
		return product
	}
	return mfr
}

func usbID(vid, pid, serial string) string {
	s := vid + ":" + pid
	if serial != "" {
		s += " serial " + serial
	}
	return s
}

// gpsVendors are USB vendors whose serial devices are GPS receivers: u-blox (the chips in most GPS
// dongles, VK-162 and similar) and Garmin. Many receivers use a generic serial chip instead (Prolific,
// FTDI, CP210x, which radios and cables use too); those cannot be told apart by their IDs, so they are
// recognised only by a product name that mentions GPS or GNSS.
var gpsVendors = map[string]bool{"1546": true, "091e": true}

// LooksLikeGPS reports whether a serial port is probably a GPS receiver.
func (s Serial) LooksLikeGPS() bool {
	if gpsVendors[strings.ToLower(s.VID)] {
		return true
	}
	name := strings.ToLower(s.Manufacturer + " " + s.Product)
	return strings.Contains(name, "gps") || strings.Contains(name, "gnss") || strings.Contains(name, "u-blox")
}
