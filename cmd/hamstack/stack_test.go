package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/units"
)

// units remove must stop every kind of service, and stop each rig's clients before its rigctld: rigctld
// does not exit promptly while a client is connected, so the wrong order cost a 90 second timeout.
func TestEveryServiceCoversAllKindsAndStopsClientsFirst(t *testing.T) {
	var cfg config.Config
	cfg.AddRig(config.Rig{ID: "hf", Profile: "x", Modem: config.Mercury})
	cfg.AddRig(config.Rig{ID: "pkt", Profile: "x", Modem: config.Direwolf})
	cfg.AddRig(config.Rig{ID: "off", Profile: "x", Modem: config.Direwolf, Off: true})
	all := everyService(cfg)
	var names []string
	for _, s := range all {
		names = append(names, s.Name())
	}
	for _, want := range []string{"rigctld@hf", "mercury@hf", "direwolf@hf", "rigctld@pkt", "direwolf@pkt", "mercury@pkt", "direwolf@off", "pat"} {
		if !slices.Contains(names, want) {
			t.Errorf("%s is missing, so 'units remove' would leave it running: %v", want, names)
		}
	}
	// stopServices stops in reverse, so in this list a rig's modems must come after its rigctld.
	idx := func(n string) int { return slices.Index(names, n) }
	for _, rig := range []string{"hf", "pkt", "off"} {
		for _, modem := range []string{"mercury", "direwolf"} {
			if idx(modem+"@"+rig) < idx("rigctld@"+rig) {
				t.Errorf("%s@%s must be listed after rigctld@%s so that it is stopped first", modem, rig, rig)
			}
		}
	}
	if idx("pat") != len(names)-1 {
		t.Error("Pat is listed last so that it is stopped first")
	}
}

func TestStopTimeoutIsBoundedForEveryUnitThatTalksToARig(t *testing.T) {
	var cfg config.Config
	cfg.AddRig(config.Rig{ID: "a", Profile: "dummy", Modem: config.Mercury})
	cfg.AddRig(config.Rig{ID: "b", Profile: "dummy", Modem: config.Direwolf})
	cfg.AddStation(config.Station{ID: "me", Callsign: "N0CALL", PatConfig: "/x.json"})
	p := units.Plan{Config: cfg, Hamstack: "/usr/local/bin/hamstack", Pat: "/usr/bin/pat", Station: cfg.Stations[0]}
	for _, r := range cfg.ActiveRigs() {
		p.Rigs = append(p.Rigs, units.RigPlan{Rig: r})
	}
	for name, text := range units.Generate(p) {
		if name == "hamstack-pat.service" {
			continue
		}
		if !strings.Contains(text, "TimeoutStopSec=10") {
			t.Errorf("%s must bound its stop time, or a service with a client attached costs 90 seconds:\n%s", name, text)
		}
	}
}
