package config

import (
	"strings"
	"testing"
)

func TestGPSSetup(t *testing.T) {
	var c Config
	if c.RunsGPSD() || c.GPSAddr() != "" || c.Has(GPSD) {
		t.Fatal("a config with no GPS must not run gpsd")
	}
	if err := c.SetGPS(GPS{Device: "/dev/serial/by-id/usb-u-blox-if00", USB: "1546:01a8"}); err != nil {
		t.Fatal(err)
	}
	if !c.RunsGPSD() || !c.Has(GPSD) || c.GPSAddr() != "localhost:2948" {
		t.Errorf("own gpsd: runs=%v addr=%q", c.RunsGPSD(), c.GPSAddr())
	}
	c.GPS.Port = 2950
	if c.GPSAddr() != "localhost:2950" {
		t.Errorf("addr = %q", c.GPSAddr())
	}
	c.GPS.Off = true
	if c.RunsGPSD() || c.GPSAddr() != "" {
		t.Error("a switched-off GPS must not run or be offered to programs")
	}
	c.RemoveGPS()
	if c.GPS != nil {
		t.Error("RemoveGPS left the setting")
	}
}

func TestGPSExternalDaemon(t *testing.T) {
	var c Config
	if err := c.SetGPS(GPS{Addr: "pi.local:2947", Device: "/dev/ttyACM0", USB: "1:2", Port: 3000}); err != nil {
		t.Fatal(err)
	}
	if c.RunsGPSD() {
		t.Error("hamstack must not run a gpsd when it uses one elsewhere")
	}
	if c.GPSAddr() != "pi.local:2947" || c.GPS.Device != "" || c.GPS.USB != "" || c.GPS.Port != 0 {
		t.Errorf("gps = %+v", *c.GPS)
	}
}

func TestGPSValidation(t *testing.T) {
	for name, g := range map[string]GPS{
		"nothing":        {},
		"no port":        {Addr: "pi"},
		"no host":        {Addr: ":2947"},
		"bad port":       {Addr: "pi:x"},
		"port too large": {Addr: "pi:70000"},
		"privileged":     {Device: "/dev/ttyACM0", Port: 80},
	} {
		var c Config
		if err := c.SetGPS(g); err == nil {
			t.Errorf("%s: accepted %+v", name, g)
		} else if c.GPS != nil {
			t.Errorf("%s: kept a rejected setting", name)
		}
	}
}

func TestGPSSurvivesSaveAndLoad(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var c Config
	if err := c.SetGPS(GPS{Device: "/dev/ttyACM0", USB: "1546:01a8"}); err != nil {
		t.Fatal(err)
	}
	if err := Save(c); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil || got.GPS == nil || got.GPS.USB != "1546:01a8" {
		t.Fatalf("loaded %+v, %v", got.GPS, err)
	}
	// An old config has no gps key and must stay without one.
	var old Config
	if err := Save(old); err != nil {
		t.Fatal(err)
	}
	if got, _ := Load(); got.GPS != nil {
		t.Error("a config without GPS gained one")
	}
	_ = strings.TrimSpace
}
