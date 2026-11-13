package config

import "testing"

func TestStations(t *testing.T) {
	var c Config
	add := func(id, call, addr string) error {
		return c.AddStation(Station{ID: id, Callsign: call, PatConfig: "/x/" + id + ".json", HTTPAddr: addr})
	}
	if err := add("me", "n0call", ""); err != nil {
		t.Fatal(err)
	}
	if c.Active != "me" || c.Stations[0].Callsign != "N0CALL" || c.Stations[0].HTTPAddr != "localhost:8080" {
		t.Fatalf("first station: %+v active=%s", c.Stations[0], c.Active)
	}
	if err := add("club", "K0CLUB", ""); err != nil || c.Stations[1].HTTPAddr != "localhost:8081" {
		t.Fatalf("second: %v %+v", err, c.Stations)
	}
	for _, bad := range []struct{ id, call, addr string }{
		{"me", "K0ABC", ""},               // duplicate id
		{"Bad Id", "K0ABC", ""},           // invalid id
		{"x", "nonsense", ""},             // invalid callsign
		{"y", "K0ABC", "localhost:8080"},  // port clash
		{"z", "K0ABC", "8080"},            // malformed address
		{"w", "K0ABC", "localhost:99999"}, // bad port
	} {
		if err := add(bad.id, bad.call, bad.addr); err == nil {
			t.Errorf("expected error for %+v", bad)
		}
	}
	if err := c.Use("club"); err != nil || c.Active != "club" {
		t.Fatalf("use: %v", err)
	}
	if err := c.Use("nope"); err == nil {
		t.Error("use unknown should fail")
	}
	if err := c.RemoveStation("club"); err != nil || c.Active != "me" || len(c.Stations) != 1 {
		t.Fatalf("remove active: %v active=%s", err, c.Active)
	}
	if c.NextAddr() != "localhost:8081" {
		t.Errorf("NextAddr = %s", c.NextAddr())
	}
}
