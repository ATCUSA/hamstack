// Package config holds hamstack's own settings and paths.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Config is the user's hamstack configuration.
type Config struct {
	Network    map[string]string `json:"network_access,omitempty"`
	Rigs       []Rig             `json:"rigs,omitempty"`        // the radios; see Rig
	NoPat      bool              `json:"no_pat,omitempty"`      // Pat is part of the setup unless this is set
	UI         bool              `json:"ui"`                    // Mercury's UI port for mercury-qt (listens on all interfaces, no auth)
	Data2GPage *bool             `json:"data2g_page,omitempty"` // Data2G's page through the dashboard; unset: on
	// PatPage serves Pat's web page through the dashboard (HTTPS, dashboard logins only) while the dashboard runs, and
	// Pat itself then listens on this computer only: Pat's page has no login of its own and can send mail as the
	// station. Unset: on (best practice, owner's decision 2026-11-26); off leaves Pat on the station's own access.
	PatPage *bool `json:"pat_page,omitempty"`
	// AppSetup has hamstack set up a radio program the first time it starts from a hamstack menu entry or the dashboard:
	// its radio (rigctld), and for WSJT-X its sound card, only where nothing is set yet. Unset: on. AppSetupOff are the
	// programs whose setup was undone: those are left alone until the recommended setup is applied again.
	AppSetup    *bool    `json:"app_setup,omitempty"`
	AppSetupOff []string `json:"app_setup_off,omitempty"`
	// Launchers keeps hamstack's menu entries in the desktop's application menu on a computer with a desktop, refreshed
	// as programs come and go. Unset: on; "hamstack launcher remove" turns it off.
	Launchers   *bool        `json:"launchers,omitempty"`
	Active      string       `json:"active"` // id of the active station
	Grid        string       `json:"grid"`   // operator's Maidenhead grid square, reused by every program that wants a locator
	Stations    []Station    `json:"stations"`
	Sites       []Site       `json:"sites,omitempty"` // named places; see Site
	GPS         *GPS         `json:"gps,omitempty"`   // the position source (gpsd); see GPS
	AX25        *AX25        `json:"ax25,omitempty"`  // Direwolf offered to the kernel's AX.25 stack; see AX25
	Web         *WebSettings `json:"web,omitempty"`   // the browser dashboard; see WebSettings
	CurrentSite string       `json:"site,omitempty"`  // the site in use
	// Read only to upgrade configs from before rigs existed (see migrate); never written back.
	LegacyProfile string   `json:"profile,omitempty"`
	Chosen        []string `json:"components,omitempty"`
}

// Dir is hamstack's config directory (~/.config/hamstack).
func Dir() (string, error) {
	d, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "hamstack"), nil
}

func path() (string, error) {
	d, err := Dir()
	return filepath.Join(d, "config.json"), err
}

// Load reads the config; a missing file gives an empty Config.
func Load() (Config, error) {
	var c Config
	p, err := path()
	if err != nil {
		return c, err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, err
	}
	c.migrate()
	return c, nil
}

// Save writes the config.
func Save(c Config) error {
	p, err := path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	// Write to a temporary file and rename it into place, so something reading the config at the same
	// moment (the TUI refreshes while it changes things) never sees a half-written file.
	tmp, err := os.CreateTemp(filepath.Dir(p), ".config-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}

var callsignRE = regexp.MustCompile(`^[A-Z0-9]{1,3}[0-9][A-Z0-9]{0,3}[A-Z](-[0-9]{1,2})?$`)

// ValidCallsign reports whether s looks like an amateur callsign (optionally with -SSID).
func ValidCallsign(s string) bool { return callsignRE.MatchString(s) }

// Set changes one key from the command line.
func (c *Config) Set(key, value string) error {
	switch key {
	case "profile":
		// Shorthand from the single-radio days: changes the model of the only rig.
		switch len(c.Rigs) {
		case 0:
			return fmt.Errorf("no rig yet: add one with 'hamstack rig add ID --profile %s'", value)
		case 1:
			c.Rigs[0].Profile = value // existence is checked by the caller against known profiles
		default:
			return fmt.Errorf("you have %d rigs: change one with 'hamstack rig set ID profile %s'", len(c.Rigs), value)
		}
	case "grid":
		g, err := parseGrid(value)
		if err != nil {
			return err
		}
		c.SetGrid(g) // the current site's grid if there is one, else the default
	case "components":
		return fmt.Errorf("components are now set per radio: 'hamstack rig set ID modem mercury|none', 'hamstack rig disable ID', 'hamstack pat on|off'")
	case "data2g-page", "data2g_page":
		switch strings.ToLower(value) {
		case "on", "true", "yes", "1":
			c.Data2GPage = Set(true)
		case "off", "false", "no", "0":
			c.Data2GPage = Set(false)
		default:
			return fmt.Errorf("data2g-page is on or off")
		}
	case "app-setup", "app_setup", "launchers":
		on, err := onOff(value)
		if err != nil {
			return fmt.Errorf("%s is on or off", key)
		}
		if key == "launchers" {
			c.Launchers = Set(on)
		} else {
			c.AppSetup = Set(on)
		}
	case "ui":
		switch strings.ToLower(value) {
		case "on", "true", "yes", "1":
			c.UI = true
		case "off", "false", "no", "0":
			c.UI = false
		default:
			return fmt.Errorf("ui must be on or off")
		}
	default:
		return fmt.Errorf("unknown key %q (profile, ui, grid, data2g-page, app-setup, launchers)", key)
	}
	return nil
}
