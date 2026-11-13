package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ATCUSA/hamstack/internal/hamlib"
	"github.com/ATCUSA/hamstack/internal/profile"
)

func scripted(lines ...string) (*asker, *bytes.Buffer) {
	var out bytes.Buffer
	return &asker{in: bufio.NewReader(strings.NewReader(strings.Join(lines, "\n") + "\n")), out: &out}, &out
}

func testProfiles() []profile.Profile {
	all, _ := profile.All("")
	return all
}

func TestRadioMenuShowsTheShortList(t *testing.T) {
	menu := radioMenu(testProfiles())
	var ids []string
	for _, p := range menu {
		ids = append(ids, p.ID)
	}
	want := []string{"dummy", "digirig", "ic7300", "ic7300mk2", "ic705", "ic7610", "ft991a", "ft710", "ftdx10", "flrig"}
	if !slices.Equal(ids, want) {
		t.Errorf("menu = %v, want %v (dummy, digirig, eight more)", ids, want)
	}
	// A profile the user saved is kept in the menu; a shipped one that is not popular is found by search.
	extra := append(testProfiles(), profile.Profile{ID: "mine", Name: "My radio"})
	if m := radioMenu(extra); m[len(m)-1].ID != "mine" || len(m) != len(want)+1 {
		t.Errorf("saved profile missing: %d entries", len(m))
	}
}

func TestChooseRadioByNumberIDAndFlag(t *testing.T) {
	profs := testProfiles()
	a, out := scripted("3")
	if id, err := chooseRadio(a, profs, "", "", nil); err != nil || id != "ic7300" {
		t.Errorf("number 3 = %q, %v", id, err)
	}
	if !strings.Contains(out.String(), "s) Search every radio hamlib supports") {
		t.Errorf("the menu offers the search:\n%s", out.String())
	}
	a, _ = scripted("ft710")
	if id, _ := chooseRadio(a, profs, "", "", nil); id != "ft710" {
		t.Errorf("by id = %q", id)
	}
	a, _ = scripted("")
	if id, _ := chooseRadio(a, profs, "", "", []profile.Profile{{ID: "ic7300mk2"}}); id != "ic7300mk2" {
		t.Errorf("the one detected radio is the default, got %q", id)
	}
	a, _ = scripted("")
	if _, err := chooseRadio(a, profs, "nosuch", "", nil); err == nil {
		t.Error("an unknown --radio must fail")
	}
}

func fakeSearch(saved *[]profile.Profile) radioSearch {
	var entries []hamlib.Entry
	for i := 0; i < 30; i++ {
		entries = append(entries, hamlib.Entry{ID: 9000 + i, Mfg: "Acme", Name: fmt.Sprintf("R-%02d", i), Status: "Stable"})
	}
	entries = append(entries,
		hamlib.Entry{ID: 3073, Mfg: "Icom", Name: "IC-7300", Status: "Stable"},
		hamlib.Entry{ID: 2045, Mfg: "Elecraft", Name: "KX3", Status: "Stable"},
		hamlib.Entry{ID: 9100, Mfg: "Zeta", Name: "Z-1", Status: "Alpha"},
		hamlib.Entry{ID: 4, Mfg: "FLRig", Status: "Stable"})
	return radioSearch{
		entries: func() ([]hamlib.Entry, error) { return entries, nil },
		caps: func(model int) hamlib.Caps {
			if model == 4 {
				return hamlib.Caps{Port: "Network"}
			}
			return hamlib.Caps{Port: "RS-232", MinBaud: 4800, MaxBaud: 38400}
		},
		save: func(p profile.Profile) error { *saved = append(*saved, p); return nil },
	}
}

func TestSearchFindsAndSavesAProfile(t *testing.T) {
	var saved []profile.Profile
	// "kx3": a new profile is made, asking for the speed (a bad one first, then the default).
	profs := []profile.Profile{{ID: "dummy"}}
	a, out := scripted("1", "99999", "")
	id, err := searchRadiosWith(a, profs, "kx3", fakeSearch(&saved))
	if err != nil || id != "elecraft-kx3" {
		t.Fatalf("id = %q, %v\n%s", id, err, out.String())
	}
	if len(saved) != 1 || saved[0].Rig.Model != 2045 || saved[0].Rig.Baud != 9600 || saved[0].Name != "Elecraft KX3" {
		t.Errorf("saved %+v", saved)
	}
	if !strings.Contains(out.String(), "not a speed this radio supports") || !strings.Contains(out.String(), "4800..38400") {
		t.Errorf("the speed prompt shows the supported range and rejects others:\n%s", out.String())
	}
	if !strings.Contains(saved[0].Notes, "Not tested") {
		t.Errorf("a generated profile says it is unverified: %q", saved[0].Notes)
	}
}

func TestSearchUsesAKnownProfile(t *testing.T) {
	var saved []profile.Profile
	a, out := scripted("1")
	profs := []profile.Profile{{ID: "ic7300", Name: "Icom IC-7300", Rig: struct {
		Model      int    `json:"model"`
		Baud       int    `json:"baud"`
		DeviceGlob string `json:"device_glob"`
	}{Model: 3073}}}
	id, err := searchRadiosWith(a, profs, "ic-7300", fakeSearch(&saved))
	if err != nil || id != "ic7300" || len(saved) != 0 {
		t.Errorf("id %q err %v saved %v\n%s", id, err, saved, out.String())
	}
}

func TestSearchPagesAndSearchesAgain(t *testing.T) {
	var saved []profile.Profile
	a, out := scripted("n", "n", "p", "zeta", "1", "")
	id, err := searchRadiosWith(a, nil, "", fakeSearch(&saved))
	if err != nil || id != "zeta-z-1" {
		t.Fatalf("id %q err %v\n%s", id, err, out.String())
	}
	o := out.String()
	for _, want := range []string{"All 34 radios (page 1 of 3)", "page 2 of 3", "page 3 of 3", "1 match \"zeta\"", "Acme R-00"} {
		if !strings.Contains(o, want) {
			t.Errorf("output lacks %q:\n%s", want, o)
		}
	}
	if !strings.Contains(o, `driver "Alpha"`) {
		t.Errorf("an unstable driver is flagged:\n%s", o)
	}
	// Network radios (FLRig) need no speed.
	saved = nil
	a, _ = scripted("1")
	if id, err := searchRadiosWith(a, nil, "flrig", fakeSearch(&saved)); err != nil || id != "flrig" || saved[0].Rig.Baud != 0 {
		t.Errorf("flrig: %q %v %+v", id, err, saved)
	}
	// No match, then going back.
	a, out = scripted("")
	if id, err := searchRadiosWith(a, nil, "nosuchradio", fakeSearch(&saved)); err != nil || id != "" || !strings.Contains(out.String(), "Nothing matches") {
		t.Errorf("no match: %q %v\n%s", id, err, out.String())
	}
}

func TestSaveUserProfileRoundTrips(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	p := profile.Profile{ID: "elecraft-kx3", Name: "Elecraft KX3"}
	p.Rig.Model, p.Rig.Baud = 2045, 38400
	if err := saveUserProfile(p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(userProfileDir(), "elecraft-kx3.json")); err != nil {
		t.Fatal(err)
	}
	got, err := profile.Find(userProfileDir(), "elecraft-kx3")
	if err != nil || got.Rig.Model != 2045 || got.Rig.Baud != 38400 {
		t.Errorf("%+v %v", got, err)
	}
}
