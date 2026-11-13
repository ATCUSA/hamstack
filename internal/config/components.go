package config

import (
	"fmt"
	"slices"
	"strings"
)

// The kinds of service hamstack runs.
const (
	RigCtld  = "rigctld"  // rig control (hamlib): one per rig
	Mercury  = "mercury"  // HF modem: one per rig that uses it
	Direwolf = "direwolf" // VHF/UHF packet soundcard TNC: one per rig that uses it
	Pat      = "pat"      // Winlink client: one, shared by all rigs
	GPSD     = "gpsd"     // GPS daemon: one, shared by every program that wants a position
)

// Ports. Each rig gets its own so several can run at once: rig control counts up from 4532, and a
// rig's Mercury modem uses a block of ten from 8300 (ARQ control, data, broadcast and web UI are
// derived from it, see MercuryPorts).
const (
	RigPortBase = 4532
	ArqPortBase = 8300
	ArqPortStep = 10
)

// Rig is one physical radio and how it is wired up. A rig is identified by its own name, not by
// the model, so two radios of the same model are two rigs.
type Rig struct {
	ID       string     `json:"id"` // short name used on the command line and in unit names
	Name     string     `json:"name,omitempty"`
	Profile  string     `json:"profile"`             // radio model: hamlib model, baud, USB ids
	CAT      string     `json:"cat,omitempty"`       // exact CAT device (a /dev/serial/by-id path); empty = find it via the profile
	CATID    string     `json:"cat_id,omitempty"`    // USB identity (vid:pid:serial) of the CAT device
	Audio    string     `json:"audio,omitempty"`     // ALSA card id of its sound card
	AudioUSB string     `json:"audio_usb,omitempty"` // USB identity of the sound card, to find it again if card numbers change
	Modem    string     `json:"modem,omitempty"`     // "", "mercury" (HF) or "direwolf" (VHF/UHF packet)
	NoCAT    bool       `json:"no_cat,omitempty"`    // the radio has no CAT control (its profile has no hamlib model): no rigctld
	Bands    []string   `json:"bands,omitempty"`     // hf, vhf, uhf
	RigPort  int        `json:"rig_port"`            // its rigctld port
	ArqPort  int        `json:"arq_port,omitempty"`  // base of its Mercury ports
	Off      bool       `json:"off,omitempty"`       // kept in the config but not run (not at this site, or unplugged)
	Packet   PacketOpts `json:"packet,omitzero"`     // Direwolf's settings for this rig; zero values mean Direwolf's own defaults
}

// PacketOpts are the settings of a rig's Direwolf that people change. Zero means "leave it to Direwolf's default",
// so a rig that never touched them gets exactly the configuration it always had.
type PacketOpts struct {
	Baud     int    `json:"baud,omitempty"`     // modem speed: 300 (HF), 1200 (VHF packet and APRS, the default), 2400, 4800, 9600
	Call     string `json:"call,omitempty"`     // the callsign (with SSID) to use instead of the station's
	TxDelay  int    `json:"txdelay,omitempty"`  // key-up delay in 10 ms units (Direwolf's default is 30)
	TxTail   int    `json:"txtail,omitempty"`   // hold-after delay in 10 ms units (default 10)
	Persist  int    `json:"persist,omitempty"`  // CSMA persistence 0-255 (default 63)
	SlotTime int    `json:"slottime,omitempty"` // CSMA slot time in 10 ms units (default 10)
	Digipeat bool   `json:"digipeat,omitempty"` // repeat other stations' WIDEn-N packets (a transmitting service: see the notice)
}

// Bauds are the modem speeds hamstack offers.
var Bauds = []int{300, 1200, 2400, 4800, 9600}

// Validate checks the values.
func (o PacketOpts) Validate() error {
	if o.Baud != 0 {
		ok := false
		for _, b := range Bauds {
			ok = ok || b == o.Baud
		}
		if !ok {
			return fmt.Errorf("modem speed %d is not one of %v", o.Baud, Bauds)
		}
	}
	if o.Call != "" && !ValidCallsign(o.Call) {
		return fmt.Errorf("%q does not look like a callsign (with an optional -SSID)", o.Call)
	}
	for name, v := range map[string]int{"txdelay": o.TxDelay, "txtail": o.TxTail, "persist": o.Persist, "slottime": o.SlotTime} {
		if v < 0 || v > 255 {
			return fmt.Errorf("%s %d is out of range (0-255)", name, v)
		}
	}
	return nil
}

// ModemBaud is the speed to run: 1200 unless set.
func (o PacketOpts) ModemBaud() int {
	if o.Baud == 0 {
		return 1200
	}
	return o.Baud
}

var validBands = []string{"hf", "vhf", "uhf"}

// NormalizeBands lower-cases, de-duplicates and validates a band list.
func NormalizeBands(in []string) ([]string, error) {
	var out []string
	for _, b := range in {
		b = strings.ToLower(strings.TrimSpace(b))
		if b == "" {
			continue
		}
		if !slices.Contains(validBands, b) {
			return nil, fmt.Errorf("unknown band %q (hf, vhf, uhf)", b)
		}
		if !slices.Contains(out, b) {
			out = append(out, b)
		}
	}
	return out, nil
}

// MercuryPorts returns the ports of a rig's Mercury modem, all derived from ArqPort so they never
// collide between rigs: ARQ control, ARQ data, broadcast and the web UI.
func MercuryPorts(r Rig) (arq, data, broadcast, ui int) {
	n := (r.ArqPort - ArqPortBase) / ArqPortStep
	return r.ArqPort, r.ArqPort + 1, 8100 + n, 10000 + n
}

// DirewolfPorts returns the AGW and KISS ports of a rig's Direwolf, derived like Mercury's so rigs never
// collide. The first rig gets Direwolf's own defaults, 8000 and 8001.
func DirewolfPorts(r Rig) (agw, kiss int) {
	n := (r.ArqPort - ArqPortBase) / ArqPortStep
	return 8000 + 2*n, 8001 + 2*n
}

// ActiveRigs are the rigs that are not switched off.
func (c Config) ActiveRigs() []Rig {
	var out []Rig
	for _, r := range c.Rigs {
		if !r.Off {
			out = append(out, r)
		}
	}
	return out
}

// RigByID finds a rig (including a switched-off one).
func (c *Config) RigByID(id string) (*Rig, bool) {
	for i := range c.Rigs {
		if c.Rigs[i].ID == id {
			return &c.Rigs[i], true
		}
	}
	return nil, false
}

// PatEnabled reports whether Pat is part of the setup.
func (c Config) PatEnabled() bool { return !c.NoPat }

// Has reports whether a kind of service is part of the setup.
func (c Config) Has(kind string) bool {
	switch kind {
	case Pat:
		return c.PatEnabled()
	case GPSD:
		return c.RunsGPSD()
	case RigCtld:
		for _, r := range c.ActiveRigs() {
			if !r.NoCAT {
				return true
			}
		}
	case Mercury, Direwolf:
		for _, r := range c.ActiveRigs() {
			if r.Modem == kind {
				return true
			}
		}
	}
	return false
}

// Components lists the kinds of service in use, in start order.
func (c Config) Components() []string {
	var out []string
	for _, k := range []string{RigCtld, Mercury, Direwolf, Pat} {
		if c.Has(k) {
			out = append(out, k)
		}
	}
	return out
}

// NeedsRadio reports whether any rig is in use.
func (c Config) NeedsRadio() bool { return len(c.ActiveRigs()) > 0 }

// NeedsStation reports whether the setup needs a callsign (Pat, or a modem that identifies itself).
func (c Config) NeedsStation() bool { return c.Has(Pat) || c.Has(Mercury) || c.Has(Direwolf) }

// PatPacketRig is the rig Pat's packet (AX.25) connections use: the first active rig with Direwolf. Pat
// has a single AX.25 setting, so with several Direwolf rigs this one is wired to Pat.
func (c Config) PatPacketRig() (Rig, bool) {
	for _, r := range c.ActiveRigs() {
		if r.Modem == Direwolf {
			return r, true
		}
	}
	return Rig{}, false
}

// PatHFRig is the rig Pat's HF modem connection uses: the first active rig with Mercury. Pat has a
// single HF modem setting, so with several Mercury rigs this one is wired to Pat.
func (c Config) PatHFRig() (Rig, bool) {
	for _, r := range c.ActiveRigs() {
		if r.Modem == Mercury {
			return r, true
		}
	}
	return Rig{}, false
}

// AddRig validates and adds a rig, giving it free ports if it has none.
func (c *Config) AddRig(r Rig) error {
	if !ValidID(r.ID) {
		return fmt.Errorf("rig id %q must be lowercase letters, digits, - or _", r.ID)
	}
	if r.Profile == "" {
		return fmt.Errorf("rig %s needs a radio profile (see: hamstack profiles)", r.ID)
	}
	switch r.Modem {
	case "", Direwolf:
	case Mercury:
		if r.NoCAT {
			return fmt.Errorf("mercury is keyed through rigctld, and this radio has no CAT control: use direwolf, or a radio with CAT")
		}
	default:
		return fmt.Errorf("unknown modem %q (mercury, direwolf, or none)", r.Modem)
	}
	var err error
	if r.Bands, err = NormalizeBands(r.Bands); err != nil {
		return err
	}
	for _, o := range c.Rigs {
		if o.ID == r.ID {
			return fmt.Errorf("a rig named %q already exists", r.ID)
		}
	}
	if r.RigPort == 0 {
		r.RigPort = c.nextRigPort()
	}
	if r.ArqPort == 0 {
		r.ArqPort = c.nextArqPort()
	}
	c.Rigs = append(c.Rigs, r)
	return nil
}

func (c Config) nextRigPort() int {
	used := map[int]bool{}
	for _, r := range c.Rigs {
		used[r.RigPort] = true
	}
	p := RigPortBase
	for used[p] {
		p++
	}
	return p
}

func (c Config) nextArqPort() int {
	used := map[int]bool{}
	for _, r := range c.Rigs {
		used[r.ArqPort] = true
	}
	p := ArqPortBase
	for used[p] {
		p += ArqPortStep
	}
	return p
}

// RemoveRig deletes a rig from the config.
func (c *Config) RemoveRig(id string) error {
	for i, r := range c.Rigs {
		if r.ID == id {
			c.Rigs = append(c.Rigs[:i], c.Rigs[i+1:]...)
			c.forgetRig(id)
			return nil
		}
	}
	return fmt.Errorf("no rig %q (see: hamstack rigs)", id)
}

// migrate upgrades a config written before rigs existed: one radio profile plus a component list
// becomes one rig and a Pat on/off switch. It reports whether anything changed.
func (c *Config) migrate() bool {
	if c.LegacyProfile == "" && len(c.Chosen) == 0 {
		return false
	}
	chosen := func(k string) bool { return len(c.Chosen) == 0 || slices.Contains(c.Chosen, k) }
	if len(c.Rigs) == 0 && c.LegacyProfile != "" && (chosen(RigCtld) || chosen(Mercury)) {
		r := Rig{ID: c.LegacyProfile, Profile: c.LegacyProfile, RigPort: RigPortBase, ArqPort: ArqPortBase}
		if !ValidID(r.ID) {
			r.ID = "main"
		}
		if chosen(Mercury) {
			r.Modem = Mercury
		}
		c.Rigs = []Rig{r}
	}
	c.NoPat = !chosen(Pat)
	c.LegacyProfile, c.Chosen = "", nil
	return true
}

// RigByIDValue is RigByID returning a copy.
func (c Config) RigByIDValue(id string) (Rig, bool) {
	for _, r := range c.Rigs {
		if r.ID == id {
			return r, true
		}
	}
	return Rig{}, false
}
