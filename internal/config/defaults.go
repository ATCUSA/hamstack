package config

import "slices"

// SettingDefault is one of hamstack's own settings with its best-practice value: whether the config has it, and how
// to put it back. Settings shows "Back to default" for any that differ, and the full reset puts every one back. The
// keys are the ones Settings uses ("app-setup", "rig:hf:xara-monitor"); a few (a rig's radio profiles) have no row
// there, and are only put back by the full reset.
//
// Not here, on purpose: what describes the station rather than how hamstack runs it (rigs, stations, sites, the grid,
// devices, the dashboard password and certificate), and the system's own state (lingering, groups).
type SettingDefault struct {
	Key       string
	Label     string // for the list a full reset shows
	Default   string // the default, for people: "on", "off", "recommended"
	IsDefault func(Config) bool
	Reset     func(*Config)
}

// SettingDefaults are hamstack's own settings in this config, each rig's included.
func (c Config) SettingDefaults() []SettingDefault {
	out := []SettingDefault{
		{Key: "pat", Label: "Pat (Winlink client)", Default: "on",
			IsDefault: func(c Config) bool { return !c.NoPat }, Reset: func(c *Config) { c.NoPat = false }},
		{Key: "ui", Label: "Mercury's web page on the network", Default: "off",
			IsDefault: func(c Config) bool { return !c.UI }, Reset: func(c *Config) { c.UI = false }},
		{Key: "data2g-page", Label: "Data2G's page through the dashboard", Default: "on",
			IsDefault: func(c Config) bool { return c.Data2GPage == nil }, Reset: func(c *Config) { c.Data2GPage = nil }},
		{Key: "pat-page", Label: "Pat's page through the dashboard", Default: "on",
			IsDefault: func(c Config) bool { return c.PatPage == nil }, Reset: func(c *Config) { c.PatPage = nil }},
		{Key: "app-setup", Label: "Set up radio programs when they start", Default: "on (every program)",
			IsDefault: func(c Config) bool { return c.AppSetup == nil && len(c.AppSetupOff) == 0 },
			Reset:     func(c *Config) { c.AppSetup, c.AppSetupOff = nil, nil }},
		{Key: "launchers", Label: "Menu entries on this computer's desktop", Default: "on",
			IsDefault: func(c Config) bool { return c.Launchers == nil }, Reset: func(c *Config) { c.Launchers = nil }},
		{Key: "expert", Label: "Expert mode", Default: "off",
			IsDefault: func(c Config) bool { return c.Web == nil || !c.Web.Expert },
			Reset: func(c *Config) {
				if c.Web != nil {
					c.Web.Expert = false
				}
			}},
		{Key: "rig-tune", Label: "Radio tuning from the dashboard", Default: "off",
			IsDefault: func(c Config) bool { return c.Web == nil || !c.Web.RigTune },
			Reset: func(c *Config) {
				if c.Web != nil {
					c.Web.RigTune = false
				}
			}},
		{Key: "console", Label: "Terminal in the dashboard", Default: "off",
			IsDefault: func(c Config) bool { return c.Web == nil || !c.Web.Console },
			Reset: func(c *Config) {
				if c.Web != nil {
					c.Web.Console = false
				}
			}},
	}
	for _, r := range c.Rigs {
		id, name := r.ID, r.ID
		if r.Name != "" {
			name = r.Name
		}
		rig := func(key, label, def string, is func(Rig) bool, reset func(*Rig)) SettingDefault {
			return SettingDefault{Key: "rig:" + id + ":" + key, Label: name + ": " + label, Default: def,
				IsDefault: func(c Config) bool {
					r, ok := c.RigByID(id)
					return !ok || is(*r)
				},
				Reset: func(c *Config) {
					if r, ok := c.RigByID(id); ok {
						reset(r)
					}
				}}
		}
		// A rig with CAT control listens through the shared view of its sound card, as a new one does; without CAT
		// it has no modem that could share it.
		shared := !r.NoCAT
		out = append(out,
			rig("xara-gui", "xara's screen", "on", func(r Rig) bool { return r.XaraGUI == nil }, func(r *Rig) { r.XaraGUI = nil }),
			rig("xara-monitor", "xara's monitor", "off", func(r Rig) bool { return r.XaraMonitor == nil }, func(r *Rig) { r.XaraMonitor = nil }),
			rig("xara-registered", "xara's registered speeds", "off", func(r Rig) bool { return !r.XaraRegistered }, func(r *Rig) { r.XaraRegistered = false }),
			rig("ardop-gui", "ardopcf's screen", "on while the firewall holds it to this computer", func(r Rig) bool { return r.ArdopGUI == nil }, func(r *Rig) { r.ArdopGUI = nil }),
			rig("shared-audio", "shared listening on the sound card", map[bool]string{true: "on", false: "off"}[shared],
				func(r Rig) bool { return r.SharedAudio == shared }, func(r *Rig) { r.SharedAudio = shared }),
			rig("radio-auto-tx", "fix transmit-side radio settings by itself", "off", func(r Rig) bool { return !r.RadioAutoTX }, func(r *Rig) { r.RadioAutoTX = false }),
			rig("radio-profiles", "radio settings profile per program", "recommended", func(r Rig) bool { return len(r.RadioProfiles) == 0 }, func(r *Rig) { r.RadioProfiles = nil }),
		)
	}
	return out
}

// SettingDefaultFor is the setting with this key.
func (c Config) SettingDefaultFor(key string) (SettingDefault, bool) {
	for _, d := range c.SettingDefaults() {
		if d.Key == key {
			return d, true
		}
	}
	return SettingDefault{}, false
}

// ChangedSettings are the settings that differ from their default.
func (c Config) ChangedSettings() []SettingDefault {
	var out []SettingDefault
	for _, d := range c.SettingDefaults() {
		if !d.IsDefault(c) {
			out = append(out, d)
		}
	}
	return out
}

// ResetChanges are what a full reset would change, for people: each setting that differs from its default, and each
// access rule that lets more than this computer in. Empty when there is nothing to reset.
func (c Config) ResetChanges() []string {
	var out []string
	for _, d := range c.ChangedSettings() {
		out = append(out, d.Label+": back to "+d.Default)
	}
	rule := func(where, key, mode string) {
		if mode != "" && mode != AccessLocal {
			out = append(out, "who can reach "+key+where+": "+mode+" -> this computer only")
		}
	}
	for _, k := range sortedKeys(c.Network) {
		rule("", k, c.Network[k])
	}
	for _, s := range c.Sites {
		for _, k := range sortedKeys(s.Network) {
			rule(" (site "+s.ID+")", k, s.Network[k])
		}
	}
	for _, st := range c.Stations {
		rule("", "Pat's web UI for station "+st.ID, st.Access)
	}
	return out
}

// ResetToDefaults puts every setting back to its default and every access rule back to this computer only: a rule is
// only ever narrowed, never removed (a port without one is left to its program's own address, which can be wider).
// What describes the station stays (rigs, stations, sites, the grid, the dashboard's password and certificate), and so
// does the dashboard's own access, so the browser that asked is not locked out.
func (c *Config) ResetToDefaults() {
	for _, d := range c.SettingDefaults() {
		d.Reset(c)
	}
	narrow := func(m map[string]string) {
		for k, v := range m {
			if v != AccessLocal {
				m[k] = AccessLocal
			}
		}
	}
	narrow(c.Network)
	for i := range c.Sites {
		narrow(c.Sites[i].Network)
	}
	for i := range c.Stations {
		if c.Stations[i].Access != "" && c.Stations[i].Access != AccessLocal {
			c.Stations[i].Access = AccessLocal
		}
	}
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
