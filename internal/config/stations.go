package config

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
)

// Station is one operating identity: a callsign with its own Pat config and web UI address.
// Examples: a personal call, a club call, an emergency-communications call.
type Station struct {
	ID        string `json:"id"`         // short name used on the command line
	Name      string `json:"name"`       // optional label, e.g. "Club"
	Callsign  string `json:"callsign"`   // may include an -SSID
	PatConfig string `json:"pat_config"` // path of this station's Pat config file
	HTTPAddr  string `json:"http_addr"`  // Pat web UI listen address, e.g. localhost:8081
	// Access is who may reach the web UI: AccessLocal, AccessNetwork or AccessAll. Empty means the
	// host in HTTPAddr is used as it is (stations from before this setting, or a custom address).
	Access string `json:"access,omitempty"`
}

// Who can reach a web UI.
const (
	AccessLocal   = "local"   // this computer only (127.0.0.1)
	AccessNetwork = "network" // this computer's LAN address: reachable from the network, not through loopback
	AccessAll     = "all"     // every interface (0.0.0.0): the LAN address and loopback
)

// ParseAccess reads an access word. It accepts the three names and a few obvious synonyms.
func ParseAccess(s string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "local", "localhost", "127.0.0.1", "loopback":
		return AccessLocal, nil
	case "network", "lan":
		return AccessNetwork, nil
	case "all", "0.0.0.0", "any":
		return AccessAll, nil
	}
	return "", fmt.Errorf("access %q must be local, network or all", s)
}

// EffectiveAccess is the station's access, worked out from HTTPAddr when Access is not set. It is empty
// for a custom address that is none of the three.
func (s Station) EffectiveAccess() string {
	if s.Access != "" {
		return s.Access
	}
	host, _, err := net.SplitHostPort(s.HTTPAddr)
	if err != nil {
		return ""
	}
	switch host {
	case "localhost", "127.0.0.1", "::1":
		return AccessLocal
	case "", "0.0.0.0", "::":
		return AccessAll
	}
	return ""
}

// ListenAddr is the address Pat's web UI should listen on. lan holds this computer's LAN addresses,
// which "network" needs; with none, it falls back to localhost and ok is false so the caller can warn.
func (s Station) ListenAddr(lan []string) (addr string, ok bool) {
	p := strconv.Itoa(port(s.HTTPAddr))
	switch s.Access {
	case AccessLocal:
		return "localhost:" + p, true
	case AccessAll:
		return "0.0.0.0:" + p, true
	case AccessNetwork:
		if len(lan) == 0 {
			return "localhost:" + p, false
		}
		return net.JoinHostPort(lan[0], p), true
	}
	return s.HTTPAddr, true
}

// WebURL is the address to type into a browser for the station's web UI, given this computer's LAN
// addresses. "all" shows the LAN address, since 0.0.0.0 is not something to browse to.
func (s Station) WebURL(lan []string) string {
	addr, _ := s.ListenAddr(lan)
	host, p, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://" + addr
	}
	if host == "0.0.0.0" || host == "" || host == "::" {
		host = "localhost"
		if len(lan) > 0 {
			host = lan[0]
		}
	}
	return "http://" + net.JoinHostPort(host, p)
}

// SetAccess changes who can reach a station's web UI.
func (c *Config) SetAccess(id, access string) error {
	a, err := ParseAccess(access)
	if err != nil {
		return err
	}
	for i := range c.Stations {
		if c.Stations[i].ID == id {
			c.Stations[i].Access = a
			return nil
		}
	}
	return fmt.Errorf("no station %q (see: hamstack stations)", id)
}

var idRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// ValidID reports whether s is usable as a station id.
func ValidID(s string) bool { return idRE.MatchString(s) }

// NormalizeCallsign upper-cases and trims s.
func NormalizeCallsign(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }

func validAddr(a string) error {
	_, port, err := net.SplitHostPort(a)
	if err != nil {
		return fmt.Errorf("address %q must look like localhost:8080", a)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("address %q has a bad port", a)
	}
	return nil
}

func port(a string) int {
	_, p, _ := net.SplitHostPort(a)
	n, _ := strconv.Atoi(p)
	return n
}

// Find returns the station with the given id.
func (c Config) Find(id string) (Station, bool) {
	for _, s := range c.Stations {
		if s.ID == id {
			return s, true
		}
	}
	return Station{}, false
}

// ActiveStation returns the active station, if any.
func (c Config) ActiveStation() (Station, bool) { return c.Find(c.Active) }

// NextAddr returns a localhost address on the lowest port from 8080 that no station uses.
func (c Config) NextAddr() string {
	used := map[int]bool{}
	for _, s := range c.Stations {
		used[port(s.HTTPAddr)] = true
	}
	p := 8080
	for used[p] {
		p++
	}
	return "localhost:" + strconv.Itoa(p)
}

// AddStation validates and adds a station. The first one added becomes active.
func (c *Config) AddStation(s Station) error {
	s.Callsign = NormalizeCallsign(s.Callsign)
	switch {
	case !ValidID(s.ID):
		return fmt.Errorf("station id %q must be lowercase letters, digits, - or _", s.ID)
	case !ValidCallsign(s.Callsign):
		return fmt.Errorf("%q does not look like a callsign", s.Callsign)
	case s.PatConfig == "":
		return fmt.Errorf("station %s needs a Pat config path", s.ID)
	}
	if s.HTTPAddr == "" {
		s.HTTPAddr = c.NextAddr()
	}
	if err := validAddr(s.HTTPAddr); err != nil {
		return err
	}
	if s.Access != "" {
		a, err := ParseAccess(s.Access)
		if err != nil {
			return err
		}
		s.Access = a
	}
	for _, o := range c.Stations {
		switch {
		case o.ID == s.ID:
			return fmt.Errorf("station %q already exists", s.ID)
		case port(o.HTTPAddr) == port(s.HTTPAddr):
			return fmt.Errorf("web UI port %d is already used by station %q", port(s.HTTPAddr), o.ID)
		}
	}
	c.Stations = append(c.Stations, s)
	if c.Active == "" {
		c.Active = s.ID
	}
	return nil
}

// RemoveStation removes a station; if it was active, the first remaining one becomes active.
func (c *Config) RemoveStation(id string) error {
	for i, s := range c.Stations {
		if s.ID == id {
			c.Stations = append(c.Stations[:i], c.Stations[i+1:]...)
			c.forgetStation(id)
			if c.Active == id {
				c.Active = ""
				if len(c.Stations) > 0 {
					c.Active = c.Stations[0].ID
				}
			}
			return nil
		}
	}
	return fmt.Errorf("no station %q", id)
}

// Use makes id the active station.
func (c *Config) Use(id string) error {
	if _, ok := c.Find(id); !ok {
		return fmt.Errorf("no station %q (see: hamstack stations)", id)
	}
	c.Active = id
	return nil
}
