package config

import (
	"fmt"
	"net"
	"strconv"
)

// GPS is the position source: a gpsd daemon. Either hamstack runs gpsd itself for a receiver plugged into
// this machine (Device and USB say which one), or it uses a gpsd that already runs somewhere (Addr).
type GPS struct {
	Device string `json:"device,omitempty"` // exact receiver device (a /dev/serial/by-id path)
	USB    string `json:"usb,omitempty"`    // USB identity (vid:pid:serial) of the receiver, to find it again after a replug
	Addr   string `json:"addr,omitempty"`   // "host:port" of an existing gpsd; hamstack then runs none
	Port   int    `json:"port,omitempty"`   // port of the gpsd hamstack runs (default GPSPort)
	Off    bool   `json:"off,omitempty"`    // kept in the config but not run
}

// GPSPort is the port of the gpsd hamstack runs. It is not gpsd's standard 2947, so a system-wide gpsd
// (often already running on a machine that has a GPS) cannot clash with it.
const GPSPort = 2948

// RunsGPSD reports whether hamstack runs a gpsd service: a receiver is set up, not switched off, and not
// provided by a gpsd elsewhere.
func (c Config) RunsGPSD() bool { return c.GPS != nil && c.GPS.Addr == "" && !c.GPS.Off }

// GPSAddr is where programs reach gpsd: the configured external gpsd, or hamstack's own on this machine.
// It is empty when no GPS is set up.
func (c Config) GPSAddr() string {
	switch {
	case c.GPS == nil || c.GPS.Off:
		return ""
	case c.GPS.Addr != "":
		return c.GPS.Addr
	}
	return "localhost:" + strconv.Itoa(c.GPS.ListenPort())
}

// ListenPort is the port hamstack's gpsd listens on.
func (g GPS) ListenPort() int {
	if g.Port > 0 {
		return g.Port
	}
	return GPSPort
}

// SetGPS validates and saves the GPS settings.
func (c *Config) SetGPS(g GPS) error {
	if g.Addr != "" {
		host, port, err := net.SplitHostPort(g.Addr)
		if err != nil || host == "" {
			return fmt.Errorf("gpsd address %q must look like host:port (gpsd's standard port is 2947)", g.Addr)
		}
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("gpsd address %q has no valid port", g.Addr)
		}
		g.Device, g.USB, g.Port = "", "", 0 // hamstack runs nothing for an external gpsd
	} else if g.Device == "" && g.USB == "" {
		return fmt.Errorf("a GPS needs a receiver (a device) or the address of a gpsd")
	}
	if g.Port != 0 && (g.Port < 1024 || g.Port > 65535) {
		return fmt.Errorf("gpsd port %d is not usable (use 1024-65535)", g.Port)
	}
	c.GPS = &g
	return nil
}

// RemoveGPS forgets the GPS settings.
func (c *Config) RemoveGPS() { c.GPS = nil }

// AX25 says which rig offers its Direwolf to the Linux kernel's AX.25 stack, and under which axports name.
// That Direwolf is started with -p, which makes it offer a KISS pseudo-terminal that kissattach attaches to.
type AX25 struct {
	Rig  string `json:"rig"`  // the rig whose Direwolf is attached
	Port string `json:"port"` // the name in /etc/ax25/axports
	// The port's settings; zero means the default (the station's callsign, the rig's Direwolf speed, 255, 2).
	Call   string `json:"call,omitempty"`
	Speed  int    `json:"speed,omitempty"`
	Paclen int    `json:"paclen,omitempty"`
	Window int    `json:"window,omitempty"`
}
