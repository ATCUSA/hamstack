package config

import "testing"

func TestNetworkSiteOverridesAndFailClosed(t *testing.T) {
	c := Config{Network: map[string]string{"web": "local"}, Sites: []Site{{ID: "home"}, {ID: "field"}}, CurrentSite: "home"}
	if err := c.SetNetworkAccess("web", "network"); err != nil {
		t.Fatal(err)
	}
	if got := c.NetworkAccess("web", "all"); got != "network" {
		t.Fatal(got)
	}
	if c.Network["web"] != "local" {
		t.Fatal("site preference overwrote machine default")
	}
	c.CurrentSite = "field"
	if got := c.NetworkAccess("web", "all"); got != "local" {
		t.Fatal(got)
	}
	if got := BindHost("network", nil); got != "127.0.0.1" {
		t.Fatalf("missing LAN exposed listener: %s", got)
	}
	if got := BindHost("network", []string{"192.0.2.7"}); got != "192.0.2.7" {
		t.Fatal(got)
	}
	if err := c.SetNetworkAccess("web", "bogus"); err == nil {
		t.Fatal("accepted invalid access")
	}
}

func TestLegacyWebAccessPreserved(t *testing.T) {
	if (WebSettings{}).EffectiveAccess() != AccessAll {
		t.Fatal("legacy dashboard must keep network access")
	}
	if (WebSettings{Access: AccessLocal}).EffectiveAccess() != AccessLocal {
		t.Fatal("explicit local access changed")
	}
}
