package config

import (
	"fmt"
	"slices"
	"strings"
)

// Site is a named place you operate from (home, the EOC, the club). It records which of your rigs are
// there, which station is active, and the grid square, so moving between places is one command.
type Site struct {
	ID      string   `json:"id"`
	Name    string   `json:"name,omitempty"`
	Rigs    []string `json:"rigs"`              // ids of the rigs that are on at this site; the rest are switched off
	Station string   `json:"station,omitempty"` // the active station here ("" leaves it as it is)
	Grid    string   `json:"grid,omitempty"`    // the grid square here ("" uses the default grid)
}

// SiteByID finds a site.
func (c Config) SiteByID(id string) (Site, bool) {
	for _, s := range c.Sites {
		if s.ID == id {
			return s, true
		}
	}
	return Site{}, false
}

func (c *Config) siteRef(id string) *Site {
	for i := range c.Sites {
		if c.Sites[i].ID == id {
			return &c.Sites[i]
		}
	}
	return nil
}

// Here is the current site, if one is selected.
func (c Config) Here() (Site, bool) { return c.SiteByID(c.CurrentSite) }

// EffectiveGrid is the grid square that applies now: the current site's, else the default.
func (c Config) EffectiveGrid() string {
	if s, ok := c.Here(); ok && s.Grid != "" {
		return s.Grid
	}
	return c.Grid
}

// SetGrid sets the grid square for where you are: the current site's, or the default when there is no
// current site. It says which it changed.
func (c *Config) SetGrid(g string) (where string) {
	if s := c.siteRef(c.CurrentSite); s != nil {
		s.Grid = g
		return "site " + s.ID
	}
	c.Grid = g
	return "default"
}

func (c Config) validateSite(s Site) error {
	if !ValidID(s.ID) {
		return fmt.Errorf("site id %q must be lowercase letters, digits, - or _", s.ID)
	}
	for _, r := range s.Rigs {
		if _, ok := c.RigByIDValue(r); !ok {
			return fmt.Errorf("site %s: there is no rig %q (see: hamstack rigs)", s.ID, r)
		}
	}
	if s.Station != "" {
		if _, ok := c.Find(s.Station); !ok {
			return fmt.Errorf("site %s: there is no station %q (see: hamstack stations)", s.ID, s.Station)
		}
	}
	if s.Grid != "" && !ValidGrid(s.Grid) {
		return fmt.Errorf("site %s: %q is not a grid square", s.ID, s.Grid)
	}
	return nil
}

// AddSite validates and adds a site. The first one added becomes the current site.
func (c *Config) AddSite(s Site) error {
	if s.Grid != "" {
		s.Grid = NormalizeGrid(s.Grid)
	}
	if err := c.validateSite(s); err != nil {
		return err
	}
	if _, dup := c.SiteByID(s.ID); dup {
		return fmt.Errorf("a site named %q already exists", s.ID)
	}
	s.Rigs = slices.Clone(s.Rigs)
	c.Sites = append(c.Sites, s)
	if c.CurrentSite == "" {
		c.CurrentSite = s.ID
	}
	return nil
}

// RemoveSite deletes a site. Rigs, stations and the grid are left as they are.
func (c *Config) RemoveSite(id string) error {
	for i, s := range c.Sites {
		if s.ID == id {
			c.Sites = append(c.Sites[:i], c.Sites[i+1:]...)
			if c.CurrentSite == id {
				c.CurrentSite = ""
			}
			return nil
		}
	}
	return fmt.Errorf("no site %q (see: hamstack sites)", id)
}

// SetSiteField changes one field of a site: name, rigs, station or grid.
func (c *Config) SetSiteField(id, key, value string) error {
	s := c.siteRef(id)
	if s == nil {
		return fmt.Errorf("no site %q (see: hamstack sites)", id)
	}
	next := *s
	switch key {
	case "name":
		next.Name = value
	case "rigs":
		next.Rigs = nil
		for _, r := range strings.Split(value, ",") {
			if r = strings.TrimSpace(r); r != "" {
				next.Rigs = append(next.Rigs, r)
			}
		}
	case "station":
		next.Station = value
	case "grid":
		if value != "" {
			if !ValidGrid(value) {
				return fmt.Errorf("%q is not a grid square (examples: EM10, EM10dg)", value)
			}
			value = NormalizeGrid(value)
		}
		next.Grid = value
	default:
		return fmt.Errorf("unknown key %q (name, rigs, station, grid)", key)
	}
	if err := c.validateSite(next); err != nil {
		return err
	}
	*s = next
	return nil
}

// UseSite makes a site current and applies it: its rigs are switched on and every other rig off, and
// its station (if it names one) becomes the active station.
func (c *Config) UseSite(id string) error {
	s, ok := c.SiteByID(id)
	if !ok {
		return fmt.Errorf("no site %q (see: hamstack sites)", id)
	}
	if err := c.validateSite(s); err != nil {
		return err // a rig or station it names was deleted since
	}
	for i := range c.Rigs {
		c.Rigs[i].Off = !slices.Contains(s.Rigs, c.Rigs[i].ID)
	}
	if s.Station != "" {
		c.Active = s.Station
	}
	c.CurrentSite = id
	return nil
}

// SyncSite records the current setup in the current site, so the site always describes what is running
// now: after you enable, disable or add a rig, or switch station, the site follows.
func (c *Config) SyncSite() {
	s := c.siteRef(c.CurrentSite)
	if s == nil {
		return
	}
	s.Rigs = nil
	for _, r := range c.ActiveRigs() {
		s.Rigs = append(s.Rigs, r.ID)
	}
	if c.Active != "" {
		s.Station = c.Active
	}
}

// forgetRig removes a deleted rig from every site, and the kernel AX.25 setup if it pointed at that rig.
func (c *Config) forgetRig(id string) {
	for i := range c.Sites {
		c.Sites[i].Rigs = slices.DeleteFunc(c.Sites[i].Rigs, func(r string) bool { return r == id })
	}
	if c.AX25 != nil && c.AX25.Rig == id {
		c.AX25 = nil // its Direwolf was the one offered to the kernel's AX.25 stack
	}
}

// forgetStation clears a deleted station from every site.
func (c *Config) forgetStation(id string) {
	for i := range c.Sites {
		if c.Sites[i].Station == id {
			c.Sites[i].Station = ""
		}
	}
}

// SiteScore says how many of a site's identifiable radios are plugged in.
type SiteScore struct {
	Site    Site
	Known   int      // rigs whose hardware can be recognised (a saved USB device, or a profile with a pattern)
	Present int      // of those, how many are plugged in now
	Missing []string // ids of the ones that are not
}

// Complete reports whether the site has radios to recognise and all of them are present.
func (s SiteScore) Complete() bool { return s.Known > 0 && s.Present == s.Known }

// ScoreSites compares each site's rigs with what is plugged in. identify reports, for a rig, whether its
// hardware can be recognised at all and whether it is present; rigs that cannot be recognised (the dummy
// rig) take no part, so they never make a site look absent.
func (c Config) ScoreSites(identify func(Rig) (known, present bool)) []SiteScore {
	var out []SiteScore
	for _, s := range c.Sites {
		sc := SiteScore{Site: s}
		for _, id := range s.Rigs {
			r, ok := c.RigByIDValue(id)
			if !ok {
				continue
			}
			known, here := identify(r)
			if !known {
				continue
			}
			sc.Known++
			if here {
				sc.Present++
			} else {
				sc.Missing = append(sc.Missing, id)
			}
		}
		out = append(out, sc)
	}
	return out
}
