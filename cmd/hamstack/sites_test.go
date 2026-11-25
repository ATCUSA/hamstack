package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/profile"
	"github.com/ATCUSA/hamstack/internal/usbdev"
)

// A rig is recognisable when it has a saved CAT device or its profile has a device pattern; it is present
// when that device exists. The dummy radio has neither, so it can never make a site look absent.
func TestRigIdentifier(t *testing.T) {
	dir := t.TempDir()
	here := filepath.Join(dir, "radio-here")
	os.WriteFile(here, nil, 0o644)
	gone := filepath.Join(dir, "radio-gone")
	var dummy profile.Profile
	dummy.ID = "dummy"
	real := icom()
	real.Rig.DeviceGlob = "" // identity comes from the saved device
	profs := []profile.Profile{dummy, real}
	id := rigIdentifier(profs, usbdev.Snapshot{})
	cases := []struct {
		rig            config.Rig
		known, present bool
	}{
		{config.Rig{ID: "d", Profile: "dummy"}, false, false},
		{config.Rig{ID: "a", Profile: "ic7300mk2", CAT: here}, true, true},
		{config.Rig{ID: "b", Profile: "ic7300mk2", CAT: gone}, true, false},
		{config.Rig{ID: "c", Profile: "nonexistent"}, false, false},
	}
	for _, c := range cases {
		if k, p := id(c.rig); k != c.known || p != c.present {
			t.Errorf("%s: known=%v present=%v, want %v %v", c.rig.ID, k, p, c.known, c.present)
		}
	}
}

func TestBetterSiteIsOnlyEverASuggestion(t *testing.T) {
	var cfg config.Config
	cfg.AddRig(config.Rig{ID: "hf", Profile: "x"})
	cfg.AddRig(config.Rig{ID: "vhf", Profile: "x"})
	cfg.AddSite(config.Site{ID: "home", Rigs: []string{"hf"}})
	cfg.AddSite(config.Site{ID: "eoc", Rigs: []string{"vhf"}})
	cfg.CurrentSite = "home"
	site := func(id string, known, present int, missing ...string) config.SiteScore {
		s, _ := cfg.SiteByID(id)
		return config.SiteScore{Site: s, Known: known, Present: present, Missing: missing}
	}
	// At home, home's radio is missing and the EOC's is plugged in.
	if b, ok := betterSite(cfg, []config.SiteScore{site("home", 1, 0, "hf"), site("eoc", 1, 1)}); !ok || b.Site.ID != "eoc" {
		t.Errorf("the EOC's radio is the one plugged in: %+v %v", b, ok)
	}
	// Home's radios are here: no suggestion, even though the EOC's are too.
	if _, ok := betterSite(cfg, []config.SiteScore{site("home", 1, 1), site("eoc", 1, 1)}); ok {
		t.Error("the current site is fine: do not suggest leaving it")
	}
	// Neither site's radios are plugged in: nothing to suggest.
	if _, ok := betterSite(cfg, []config.SiteScore{site("home", 1, 0, "hf"), site("eoc", 1, 0, "vhf")}); ok {
		t.Error("no site matches")
	}
	// The current site has nothing recognisable (dummy rigs): it cannot be judged wrong.
	if _, ok := betterSite(cfg, []config.SiteScore{site("home", 0, 0), site("eoc", 1, 1)}); ok {
		t.Error("a site that cannot be judged must not be called mismatched")
	}
}

func TestCloneConfigIsIndependent(t *testing.T) {
	var cfg config.Config
	cfg.AddRig(config.Rig{ID: "hf", Profile: "x"})
	cfg.AddSite(config.Site{ID: "home", Rigs: []string{"hf"}})
	before := cloneConfig(cfg)
	cfg.Rigs[0].Off = true
	cfg.Sites[0].Grid = "EM10"
	cfg.Stations = append(cfg.Stations, config.Station{ID: "x"})
	if before.Rigs[0].Off || before.Sites[0].Grid != "" || len(before.Stations) != 0 {
		t.Errorf("changing the config must not change the saved 'before' used to work out what to stop: %+v", before)
	}
}
