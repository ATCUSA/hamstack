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
