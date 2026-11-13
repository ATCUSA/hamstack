package tui

import (
	"slices"
	"testing"
)

// These are the fix texts doctor really produces; the parser must find the command in each, and refuse
// the ones that need something filled in.
func TestFixCommand(t *testing.T) {
	ok := map[string][]string{
		"run: hamstack install":       {"install"},
		"hamstack groups fix":         {"groups", "fix"},
		"run: hamstack units install": {"units", "install"},
		"run: hamstack pat sync (sets Pat's HF modem to your Mercury; a backup of Pat's config is kept)":                                                          {"pat", "sync"},
		"update just that field (a backup is kept): hamstack pat locator":                                                                                         {"pat", "locator"},
		"for a headless or always-on machine run: hamstack linger on":                                                                                             {"linger", "on"},
		"switch with: hamstack site use eoc":                                                                                                                      {"site", "use", "eoc"},
		"turn off with: hamstack config set ui off, unless this network is trusted":                                                                               {"config", "set", "ui", "off"},
		"plug it in, or point the rig at its current port: hamstack rig identify main":                                                                            {"rig", "identify", "main"},
		"your hamlib is older than this radio: run 'hamstack install hamlib --from-source' (builds a newer one; slow on small hardware), or pick another profile": {"install", "hamlib", "--from-source"},
		"run: hamstack profiles, then hamstack config set profile ID":                                                                                             {"profiles"},
	}
	for fix, want := range ok {
		got, found := FixCommand(fix)
		if !found || !slices.Equal(got, want) {
			t.Errorf("%q -> %v %v, want %v", fix, got, found, want)
		}
	}
	for _, bad := range []string{
		"",
		"plug in and power on the radio, then run: hamstack doctor", // fine to run, but let us check below
		"run: hamstack station add ID --callsign YOURCALL (or hamstack station import-pat)",
		"hamstack rig set RIG profile ID",
		"something with no command at all",
		"hamstack config set grid ...",
		"hamstack `reboot`",
	} {
		if bad == "plug in and power on the radio, then run: hamstack doctor" {
			if got, found := FixCommand(bad); !found || !slices.Equal(got, []string{"doctor"}) {
				t.Errorf("%q -> %v %v", bad, got, found)
			}
			continue
		}
		if got, found := FixCommand(bad); found {
			t.Errorf("%q must not be runnable, got %v", bad, got)
		}
	}
}
