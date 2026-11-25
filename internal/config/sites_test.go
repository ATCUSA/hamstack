package config

import (
	"slices"
	"testing"
)

// world is a home with an HF rig and an EOC with two other rigs, two stations.
func world(t *testing.T) Config {
	t.Helper()
	var c Config
	for _, r := range []Rig{{ID: "hf", Profile: "x", Modem: Mercury}, {ID: "vhf", Profile: "x", Modem: Direwolf}, {ID: "dig", Profile: "x", Modem: Direwolf, NoCAT: true}} {
		if err := c.AddRig(r); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range []Station{{ID: "me", Callsign: "N0CALL", PatConfig: "/me.json"}, {ID: "eoc", Callsign: "K0EOC", PatConfig: "/eoc.json"}} {
		if err := c.AddStation(s); err != nil {
			t.Fatal(err)
		}
	}
	return c
}

func TestAddSiteValidation(t *testing.T) {
	c := world(t)
	for _, bad := range []Site{
		{ID: "Bad Id"}, {ID: "x", Rigs: []string{"nope"}}, {ID: "x", Station: "nope"}, {ID: "x", Grid: "zz99"},
	} {
		if err := c.AddSite(bad); err == nil {
			t.Errorf("should reject %+v", bad)
		}
	}
	if len(c.Sites) != 0 {
		t.Error("rejected sites must not be added")
	}
	if err := c.AddSite(Site{ID: "home", Rigs: []string{"hf"}, Station: "me", Grid: "em10DG"}); err != nil {
		t.Fatal(err)
	}
	if c.CurrentSite != "home" || c.Sites[0].Grid != "EM10dg" {
		t.Errorf("first site becomes current, and its grid is normalised: %+v %q", c.Sites[0], c.CurrentSite)
	}
	if err := c.AddSite(Site{ID: "home"}); err == nil {
		t.Error("duplicate id")
	}
}

func TestUseSiteAppliesRigsStationAndGrid(t *testing.T) {
	c := world(t)
	c.AddSite(Site{ID: "home", Rigs: []string{"hf"}, Station: "me", Grid: "EM10dg"})
	c.AddSite(Site{ID: "eoc", Rigs: []string{"vhf", "dig"}, Station: "eoc", Grid: "FN31pr"})
	if err := c.UseSite("eoc"); err != nil {
		t.Fatal(err)
	}
	var on []string
	for _, r := range c.ActiveRigs() {
		on = append(on, r.ID)
	}
	if !slices.Equal(on, []string{"vhf", "dig"}) {
		t.Errorf("only the EOC's rigs should be on, got %v", on)
	}
	if c.Active != "eoc" || c.CurrentSite != "eoc" || c.EffectiveGrid() != "FN31pr" {
		t.Errorf("station %q site %q grid %q", c.Active, c.CurrentSite, c.EffectiveGrid())
	}
	if err := c.UseSite("home"); err != nil {
		t.Fatal(err)
	}
	if c.Has(Direwolf) || !c.Has(Mercury) || c.Active != "me" || c.EffectiveGrid() != "EM10dg" {
		t.Errorf("back home: %v %v %q %q", c.Has(Direwolf), c.Has(Mercury), c.Active, c.EffectiveGrid())
	}
	if err := c.UseSite("nowhere"); err == nil {
		t.Error("unknown site")
	}
}

func TestUseSiteWithoutStationOrGridKeepsThem(t *testing.T) {
	c := world(t)
	c.Grid = "AA00"
	c.AddSite(Site{ID: "bare", Rigs: []string{"hf"}})
	c.Active = "eoc"
	c.UseSite("bare")
	if c.Active != "eoc" || c.EffectiveGrid() != "AA00" {
		t.Errorf("a site that names no station or grid leaves them alone: %q %q", c.Active, c.EffectiveGrid())
	}
}

func TestSiteFollowsTheLiveSetup(t *testing.T) {
	c := world(t)
	c.AddSite(Site{ID: "home", Rigs: []string{"hf"}, Station: "me"})
	c.UseSite("home")
	c.Rigs[1].Off = false // the user enables vhf while at home
	c.Active = "eoc"      // and switches station
	c.SyncSite()
	s, _ := c.Here()
	if !slices.Equal(s.Rigs, []string{"hf", "vhf", "dig"}) && !slices.Equal(s.Rigs, []string{"hf", "vhf"}) {
		t.Errorf("site rigs should follow the rigs that are on: %v", s.Rigs)
	}
	if s.Station != "eoc" {
		t.Errorf("site station should follow: %q", s.Station)
	}
	// With no current site, syncing does nothing.
	var none Config
	none.SyncSite()
}

func TestSetGridGoesToTheRightPlace(t *testing.T) {
	c := world(t)
	if where := c.SetGrid("EM10dg"); where != "default" || c.Grid != "EM10dg" {
		t.Errorf("no site: default grid, got %s %q", where, c.Grid)
	}
	c.AddSite(Site{ID: "eoc", Rigs: []string{"vhf"}})
	if where := c.SetGrid("FN31pr"); where != "site eoc" || c.Grid != "EM10dg" {
		t.Errorf("with a current site its grid changes and the default is left: %s grid=%q", where, c.Grid)
	}
	if c.EffectiveGrid() != "FN31pr" {
		t.Errorf("effective grid = %q", c.EffectiveGrid())
	}
	if err := c.Set("grid", "cn87"); err != nil {
		t.Fatal(err)
	}
	if s, _ := c.Here(); s.Grid != "CN87" {
		t.Errorf("config set grid applies to the current site: %q", s.Grid)
	}
}

func TestDeletingThingsCleansSites(t *testing.T) {
	c := world(t)
	c.AddSite(Site{ID: "eoc", Rigs: []string{"vhf", "dig"}, Station: "eoc"})
	c.RemoveRig("dig")
	c.RemoveStation("eoc")
	s, _ := c.SiteByID("eoc")
	if !slices.Equal(s.Rigs, []string{"vhf"}) || s.Station != "" {
		t.Errorf("a deleted rig or station must leave every site: %+v", s)
	}
	if err := c.UseSite("eoc"); err != nil {
		t.Errorf("a site must stay usable after that: %v", err)
	}
	c.RemoveSite("eoc")
	if c.CurrentSite != "" || len(c.Sites) != 0 {
		t.Errorf("%+v", c)
	}
}

func TestSetSiteField(t *testing.T) {
	c := world(t)
	c.AddSite(Site{ID: "eoc", Rigs: []string{"vhf"}})
	if err := c.SetSiteField("eoc", "rigs", "vhf, dig"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetSiteField("eoc", "grid", "fn31"); err != nil {
		t.Fatal(err)
	}
	s, _ := c.SiteByID("eoc")
	if !slices.Equal(s.Rigs, []string{"vhf", "dig"}) || s.Grid != "FN31" {
		t.Errorf("%+v", s)
	}
	for _, bad := range [][2]string{{"rigs", "ghost"}, {"station", "ghost"}, {"grid", "nonsense"}, {"colour", "red"}} {
		if err := c.SetSiteField("eoc", bad[0], bad[1]); err == nil {
			t.Errorf("%v should be rejected", bad)
		}
	}
	if s2, _ := c.SiteByID("eoc"); !slices.Equal(s2.Rigs, s.Rigs) || s2.Grid != s.Grid {
		t.Errorf("a rejected change must leave the site alone: %+v", s2)
	}
}

func TestScoreSites(t *testing.T) {
	c := world(t)
	c.AddSite(Site{ID: "home", Rigs: []string{"hf"}})
	c.AddSite(Site{ID: "eoc", Rigs: []string{"vhf", "dig"}})
	c.AddSite(Site{ID: "bench", Rigs: []string{}}) // no radios to recognise
	plugged := map[string]bool{"vhf": true, "dig": true}
	scores := c.ScoreSites(func(r Rig) (bool, bool) { return true, plugged[r.ID] })
	if len(scores) != 3 {
		t.Fatalf("%+v", scores)
	}
	home, eoc, bench := scores[0], scores[1], scores[2]
	if home.Complete() || !slices.Equal(home.Missing, []string{"hf"}) {
		t.Errorf("home: its radio is not here: %+v", home)
	}
	if !eoc.Complete() || eoc.Present != 2 {
		t.Errorf("eoc: both radios are here: %+v", eoc)
	}
	if bench.Complete() {
		t.Error("a site with nothing to recognise can never be called a match")
	}
	// Rigs that cannot be recognised (the dummy radio) do not count against a site.
	scores = c.ScoreSites(func(r Rig) (bool, bool) { return r.ID != "dig", plugged[r.ID] })
	if !scores[1].Complete() || scores[1].Known != 1 {
		t.Errorf("unrecognisable rigs take no part: %+v", scores[1])
	}
}
