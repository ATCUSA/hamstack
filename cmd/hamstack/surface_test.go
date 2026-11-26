package main

import (
	"testing"

	"github.com/ATCUSA/hamstack/internal/apps"
	"github.com/ATCUSA/hamstack/internal/catalog"
	"github.com/ATCUSA/hamstack/internal/config"
)

// Whatever is added to the catalog must show up somewhere a person can find it once installed: as an app on the
// Apps page, as a service on the Services page (or, for an installed modem no rig runs yet, as "installed, not in
// use"), or be infrastructure that has its own place. A new entry fails this test until someone decides which.
func TestEveryCatalogProgramHasAPlaceOnceInstalled(t *testing.T) {
	// not apps and not modems, with the place they show
	elsewhere := map[string]string{
		"hamlib": "Services: rigctld, one per rig",
		"gpsd":   "Services: GPS daemon",
		"wsjtx":  "Apps: the WSJT-X card (wsjtx and wsjtx-improved provide the same program)",
		"ax25":   "command-line tools with nothing to run; listed under Updates, Programs",
		"vnc":    "Settings, Remote access (and the Apps page's readiness list)",
		"novnc":  "Settings, Remote access (and the Apps page's readiness list)",
		"wayvnc": "Settings, Remote access (and the Apps page's readiness list)",
	}
	modems := map[string]bool{config.Direwolf: true}
	for _, m := range config.HFModemKinds {
		modems[m] = true
	}
	for _, p := range catalog.All {
		_, inElsewhere := elsewhere[p.Name]
		isApp := p.Name == "pat"
		for _, a := range apps.All {
			if a.Package == p.Name {
				isApp = true
			}
		}
		if !inElsewhere && !isApp && !modems[p.Name] {
			t.Errorf("catalog program %q would be installed and then show nowhere: make it an app (internal/apps), a service kind, an unusedPrograms entry, or list where it shows in this test", p.Name)
		}
	}
	// every modem kind is also a service kind units can generate and name
	for m := range modems {
		if _, ok := catalog.ByName(m); !ok {
			t.Errorf("modem %q is not in the catalog", m)
		}
	}
}
