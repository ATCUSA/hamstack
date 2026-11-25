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
		{"me", "K1ABC", ""},               // duplicate id
		{"Bad Id", "K1ABC", ""},           // invalid id
		{"x", "nonsense", ""},             // invalid callsign
		{"y", "K1ABC", "localhost:8080"},  // port clash
		{"z", "K1ABC", "8080"},            // malformed address
		{"w", "K1ABC", "localhost:99999"}, // bad port
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

func TestStationAccess(t *testing.T) {
	lan := []string{"192.168.1.20"}
	for _, c := range []struct {
		access, addr string
		ok           bool
		wantEff      string
	}{
		{"local", "localhost:8080", true, "local"},
		{"all", "0.0.0.0:8080", true, "all"},
		{"network", "192.168.1.20:8080", true, "network"},
		{"", "localhost:8080", true, "local"}, // before the setting existed: the address decides
	} {
		s := Station{HTTPAddr: "localhost:8080", Access: c.access}
		got, ok := s.ListenAddr(lan)
		if got != c.addr || ok != c.ok || s.EffectiveAccess() != c.wantEff {
			t.Errorf("%q: %q %v %q", c.access, got, ok, s.EffectiveAccess())
		}
	}
	s := Station{HTTPAddr: "10.0.0.5:8081"}
	if a, _ := s.ListenAddr(lan); a != "10.0.0.5:8081" || s.EffectiveAccess() != "" {
		t.Errorf("custom address kept as is: %q %q", a, s.EffectiveAccess())
	}
	if a, ok := (Station{HTTPAddr: "localhost:8080", Access: "network"}).ListenAddr(nil); a != "localhost:8080" || ok {
		t.Errorf("no LAN address falls back to localhost and says so: %q %v", a, ok)
	}
	if u := (Station{HTTPAddr: "localhost:8080", Access: "all"}).WebURL(lan); u != "http://192.168.1.20:8080" {
		t.Errorf("url %q", u)
	}
	for _, w := range []string{"LAN", "0.0.0.0", "localhost"} {
		if _, err := ParseAccess(w); err != nil {
			t.Errorf("%s: %v", w, err)
		}
	}
	if _, err := ParseAccess("everyone"); err == nil {
		t.Error("bad word accepted")
	}
	var c Config
	if err := c.AddStation(Station{ID: "me", Callsign: "N0CALL", PatConfig: "/x", Access: "lan"}); err != nil || c.Stations[0].Access != "network" {
		t.Errorf("add with access: %v %+v", err, c.Stations)
	}
	if err := c.SetAccess("me", "all"); err != nil || c.Stations[0].Access != "all" {
		t.Errorf("SetAccess: %v", err)
	}
	if c.SetAccess("nope", "all") == nil || c.SetAccess("me", "x") == nil {
		t.Error("bad id or word accepted")
	}
}
