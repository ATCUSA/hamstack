package doctor

import (
	"errors"
	"strings"
	"testing"

	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/profile"
)

func fakeSys() System {
	return System{
		LookPath:      func(string) (string, error) { return "/usr/bin/x", nil },
		Glob:          func(string) ([]string, error) { return []string{"/dev/ttyACM0"}, nil },
		Cards:         func() []string { return []string{"HDMI", "Device"} },
		Access:        func(string) error { return nil },
		Group:         func(string) string { return "uucp" },
		Listening:     func(int) bool { return false },
		ServiceActive: func(string) bool { return false },
		FileExists:    func(string) bool { return true },
		UnitsState:    func() (bool, bool) { return true, true },
		GroupAdvice:   func(string) (bool, string, string) { return true, "", "" },
		SerialGroup:   func() string { return "uucp" },
		RigModels:     func() (map[int]string, error) { return map[int]string{1: "Stable", 2: "Stable", 3094: "Beta"}, nil },
		HamlibVersion: func() string { return "4.7.2" },
	}
}

// inputs is one real-looking rig with Mercury, plus Pat and a station.
func inputs() Inputs {
	var p profile.Profile
	p.ID, p.Name = "radio", "Test Radio"
	p.Rig.Model, p.Rig.DeviceGlob = 1, "/dev/serial/by-id/x*"
	p.Audio.CardHints = []string{"Device"}
	var cfg config.Config
	cfg.AddRig(config.Rig{ID: "main", Profile: "radio", Modem: config.Mercury})
	cfg.AddStation(config.Station{ID: "me", Callsign: "N0CALL", PatConfig: "/p.json"})
	return Inputs{Cfg: cfg, Profiles: []profile.Profile{p}, Sys: fakeSys()}
}

func find(rs []Result, name string) Result {
	for _, r := range rs {
		if r.Name == name {
			return r
		}
	}
	return Result{Name: "MISSING " + name, Level: Fail}
}

func missing(rs []Result, name string) bool { return strings.HasPrefix(find(rs, name).Name, "MISSING") }

func TestAllGood(t *testing.T) {
	rs := Run(inputs())
	if w := Worst(rs); w != OK {
		t.Errorf("worst = %v: %+v", w, rs)
	}
	if !missing(rs, "main: radio profile") {
		t.Error("with a single rig the checks are not prefixed with its name")
	}
}

func TestProblems(t *testing.T) {
	in := inputs()
	in.Sys.SystemdErr = errors.New("no session")
	in.Sys.LookPath = func(b string) (string, error) {
		if b == "mercury" {
			return "", errors.New("nf")
		}
		return "/x", nil
	}
	in.Sys.Access = func(string) error { return errors.New("permission denied") }
	in.Sys.Cards = func() []string { return []string{"HDMI"} }
	in.Sys.Listening = func(p int) bool { return p == 8300 } // VARA-style clash
	in.Sys.UnitsState = func() (bool, bool) { return false, false }
	in.Cfg.UI = true
	rs := Run(in)
	for name, want := range map[string]Level{
		"systemd user session": Fail, "programs": Fail, "radio (CAT port)": Fail,
		"radio sound card": Warn, "modem port": Warn, "rig control port": OK,
		"service units": Warn, "Mercury web UI": Warn,
	} {
		if got := find(rs, name); got.Level != want {
			t.Errorf("%s: %v want %v (%s)", name, got.Level, want, got.Detail)
		}
	}
	if r := find(rs, "modem port"); !strings.Contains(r.Fix, "VARA") {
		t.Errorf("port 8300 hint should mention VARA: %q", r.Fix)
	}
	if find(rs, "programs").Detail != "not found: mercury" {
		t.Errorf("programs: %q", find(rs, "programs").Detail)
	}
}

func TestOwnServiceMayUsePort(t *testing.T) {
	in := inputs()
	in.Sys.Listening = func(int) bool { return true }
	in.Sys.ServiceActive = func(s string) bool { return s == "rigctld@main" || s == "mercury@main" }
	rs := Run(in)
	if find(rs, "modem port").Level != OK || find(rs, "rig control port").Level != OK {
		t.Errorf("our own services on their ports are fine: %+v", rs)
	}
}

func TestNothingConfigured(t *testing.T) {
	in := inputs()
	in.Cfg = config.Config{} // no rigs, Pat on, no station
	rs := Run(in)
	if find(rs, "station").Level != Fail {
		t.Errorf("Pat needs a station: %+v", find(rs, "station"))
	}
	for _, name := range []string{"radio profile", "rig control port", "modem port", "radio (CAT port)"} {
		if !missing(rs, name) {
			t.Errorf("no rigs, so no %q check: %+v", name, find(rs, name))
		}
	}
}

func TestDummyProfileSkipsHardware(t *testing.T) {
	in := inputs()
	in.Profiles[0].Rig.DeviceGlob = ""
	in.Profiles[0].Audio.Null = true
	rs := Run(in)
	for _, r := range rs {
		if strings.HasPrefix(r.Name, "radio (") || r.Name == "radio sound card" {
			t.Errorf("hardware check ran for a no-radio profile: %+v", r)
		}
	}
}

func TestUnknownProfileOnARig(t *testing.T) {
	in := inputs()
	in.Cfg.Rigs[0].Profile = "gone"
	r := find(Run(in), "radio profile")
	if r.Level != Fail || !strings.Contains(r.Fix, "hamstack rig set main profile") {
		t.Errorf("%+v", r)
	}
}

func TestModelSupport(t *testing.T) {
	in := inputs()
	in.Profiles[0].Rig.Model = 3094
	in.Sys.RigModels = func() (map[int]string, error) { return map[int]string{1: "Stable"}, nil }
	in.Sys.HamlibVersion = func() string { return "4.6.2" }
	r := find(Run(in), "radio supported by hamlib")
	if r.Level != Fail || !strings.Contains(r.Detail, "4.6.2") || !strings.Contains(r.Detail, "3094") || !strings.Contains(r.Fix, "--from-source") {
		t.Errorf("a model missing from an old hamlib must FAIL with the version and a fix: %+v", r)
	}
	in.Sys.RigModels = func() (map[int]string, error) { return map[int]string{3094: "Beta"}, nil }
	if r := find(Run(in), "radio supported by hamlib"); r.Level != OK || !strings.Contains(r.Detail, "Beta") {
		t.Errorf("supported beta driver should pass and mention Beta: %+v", r)
	}
	in.Sys.RigModels = func() (map[int]string, error) { return nil, errors.New("exec failed") }
	if r := find(Run(in), "radio supported by hamlib"); r.Level != Warn {
		t.Errorf("an unanswerable question is a warning, not a failure: %+v", r)
	}
}

func TestAssignedDevice(t *testing.T) {
	in := inputs()
	in.Cfg.Rigs[0].CAT = "/dev/serial/by-id/usb-Icom-if00"
	in.Sys.FileExists = func(p string) bool { return p == "/p.json" }
	r := find(Run(in), "radio (CAT port)")
	if r.Level != Warn || !strings.Contains(r.Detail, "usb-Icom-if00") || !strings.Contains(r.Fix, "hamstack rig identify main") {
		t.Errorf("an assigned radio that is not there must name the device and the way to reassign it: %+v", r)
	}
	in.Sys.FileExists = func(string) bool { return true }
	if r := find(Run(in), "radio (CAT port)"); r.Level != OK || r.Detail != "/dev/serial/by-id/usb-Icom-if00" {
		t.Errorf("assigned and present: %+v", r)
	}
}

func TestGroupChecks(t *testing.T) {
	in := inputs()
	in.Sys.GroupAdvice = func(g string) (bool, string, string) {
		return true, "you are not in the " + g + " group", "hamstack groups fix"
	}
	r := find(Run(in), "serial port permission")
	if r.Level != Warn || !strings.Contains(r.Detail, "uucp") || r.Fix != "hamstack groups fix" {
		t.Errorf("a missing serial group must warn with the group name and the fix: %+v", r)
	}
	in.Sys.GroupAdvice = func(string) (bool, string, string) { return false, "", "" }
	if !missing(Run(in), "serial port permission") {
		t.Error("a system without that group has nothing to check")
	}
	// Sound-card permission only matters with linger (no login session to grant it).
	in = inputs()
	in.Sys.GroupAdvice = func(g string) (bool, string, string) { return true, "not in " + g, "hamstack groups fix" }
	in.Sys.Linger = func() (bool, error) { return false, nil }
	if !missing(Run(in), "sound card permission") {
		t.Error("without linger a desktop login grants sound access")
	}
	in.Sys.Linger = func() (bool, error) { return true, nil }
	if r := find(Run(in), "sound card permission"); r.Level != Warn {
		t.Errorf("with linger the audio group is needed: %+v", r)
	}
}

func TestStartAtBoot(t *testing.T) {
	in := inputs()
	in.Sys.AnyEnabled = func() bool { return true }
	in.Sys.Linger = func() (bool, error) { return false, nil }
	if r := find(Run(in), "start at boot"); r.Level != Warn || !strings.Contains(r.Fix, "hamstack linger on") {
		t.Errorf("enabled services without linger must warn with the fix: %+v", r)
	}
	in.Sys.Linger = func() (bool, error) { return true, nil }
	if r := find(Run(in), "start at boot"); r.Level != OK {
		t.Errorf("linger on is fine: %+v", r)
	}
	in.Sys.AnyEnabled = func() bool { return false }
	if !missing(Run(in), "start at boot") {
		t.Error("nothing is enabled at login, so boot behaviour is irrelevant")
	}
}

func TestPatOnly(t *testing.T) {
	in := inputs()
	in.Cfg.Rigs = nil
	in.Sys.LookPath = func(b string) (string, error) {
		if b == "pat" {
			return "/x", nil
		}
		return "", errors.New("not installed")
	}
	in.Sys.Listening = func(int) bool { return true } // would warn if ports were checked
	rs := Run(in)
	if r := find(rs, "programs"); r.Level != OK || r.Detail != "pat installed" {
		t.Errorf("pat only: rigctld and mercury must not be required: %+v", r)
	}
	if Worst(rs) == Fail {
		t.Errorf("a valid pat-only setup must not fail: %+v", rs)
	}
}

func TestMercuryNeedsStationButNotPatConfig(t *testing.T) {
	in := inputs()
	in.Cfg.NoPat = true
	in.Sys.FileExists = func(string) bool { return false }
	rs := Run(in)
	if find(rs, "station").Level != OK || !missing(rs, "station Pat config") {
		t.Errorf("Mercury needs a callsign, but the Pat config file matters to Pat alone: %+v", rs)
	}
}

// ---- several rigs ----

func twoRigs() Inputs {
	in := inputs()
	in.Cfg.Rigs = nil
	in.Cfg.AddRig(config.Rig{ID: "hf", Profile: "radio", Modem: config.Mercury, CAT: "/dev/serial/by-id/hf-if00", AudioUSB: "0d8c:0013"})
	in.Cfg.AddRig(config.Rig{ID: "vhf", Profile: "radio", Modem: config.Mercury, CAT: "/dev/serial/by-id/vhf-if00", AudioUSB: "10c4:ea60:DR1"})
	return in
}

func TestPatHFModemMustReachAMercury(t *testing.T) {
	in := twoRigs()
	in.Sys.PatHFPort = func() (int, bool) { return 8310, true }
	if r := find(Run(in), "Pat HF modem"); r.Level != OK || !strings.Contains(r.Detail, "rig vhf") {
		t.Errorf("port 8310 is rig vhf's Mercury: %+v", r)
	}
	in.Sys.PatHFPort = func() (int, bool) { return 8300, true }
	if r := find(Run(in), "Pat HF modem"); r.Level != OK || !strings.Contains(r.Detail, "rig hf") {
		t.Errorf("port 8300 is rig hf's Mercury: %+v", r)
	}
	in.Cfg.Rigs[0].Modem = "" // hf lost its modem, but Pat still points at it
	r := find(Run(in), "Pat HF modem")
	if r.Level != Warn || !strings.Contains(r.Detail, "8300") || !strings.Contains(r.Detail, "8310 (rig vhf)") {
		t.Errorf("Pat pointing at a Mercury that no longer exists must warn and list the real ones: %+v", r)
	}
	in.Sys.PatHFPort = func() (int, bool) { return 0, false }
	if r := find(Run(in), "Pat HF modem"); r.Level != Warn {
		t.Errorf("Pat with no HF modem cannot use Mercury: %+v", r)
	}
	in.Cfg.NoPat = true
	if !missing(Run(in), "Pat HF modem") {
		t.Error("without Pat there is nothing to check")
	}
}

func TestDirewolfRig(t *testing.T) {
	in := inputs()
	in.Cfg.Rigs = nil
	in.Cfg.AddRig(config.Rig{ID: "pkt", Profile: "radio", Modem: config.Direwolf})
	in.Sys.Listening = func(p int) bool { return p == 8000 }
	in.Sys.ServiceActive = func(s string) bool { return s == "direwolf@pkt" }
	rs := Run(in)
	if r := find(rs, "packet TNC port"); r.Level != OK || !strings.Contains(r.Detail, "direwolf") {
		t.Errorf("direwolf on its own AGW port: %+v", r)
	}
	if r := find(rs, "radio sound card"); r.Level != OK {
		t.Errorf("a Direwolf rig needs its sound card checked like a Mercury one: %+v", r)
	}
	in.Sys.ServiceActive = func(string) bool { return false }
	if r := find(Run(in), "packet TNC port"); r.Level != Warn {
		t.Errorf("another program on 8000 must be flagged: %+v", r)
	}
	if !missing(rs, "modem port") {
		t.Error("Mercury's port is not checked on a Direwolf rig")
	}
}

func TestInterfaceWithNoCAT(t *testing.T) {
	in := inputs()
	in.Profiles[0].Rig.Model = 0 // like a Digirig: audio and a PTT serial line, no CAT
	in.Cfg.Rigs = nil
	in.Cfg.AddRig(config.Rig{ID: "dig", Profile: "radio", Modem: config.Direwolf, NoCAT: true})
	in.Sys.RigModels = func() (map[int]string, error) { return nil, errors.New("must not be asked") }
	rs := Run(in)
	if !missing(rs, "radio supported by hamlib") || !missing(rs, "rig control port") {
		t.Errorf("a radio with no CAT has no hamlib model and no rigctld: %+v", rs)
	}
	if r := find(rs, "PTT serial port"); r.Level != OK {
		t.Errorf("the serial port is a PTT line, labelled as such: %+v", r)
	}
	if !missing(rs, "radio (CAT port)") {
		t.Error("it has no CAT port")
	}
	in.Cfg.Rigs[0].Modem = config.Direwolf
	if r := find(Run(in), "programs"); !strings.Contains(r.Detail, "direwolf") || strings.Contains(r.Detail, "rigctld") {
		t.Errorf("only direwolf (and pat) must be required: %+v", r)
	}
}

func TestPatPacketTNC(t *testing.T) {
	in := inputs()
	in.Cfg.Rigs = nil
	in.Cfg.AddRig(config.Rig{ID: "a", Profile: "radio", Modem: config.Direwolf})
	in.Cfg.AddRig(config.Rig{ID: "b", Profile: "radio", Modem: config.Direwolf})
	in.Sys.PatPacketPort = func() (int, bool) { return 8002, true }
	if r := find(Run(in), "Pat packet TNC"); r.Level != OK || !strings.Contains(r.Detail, "rig b") {
		t.Errorf("8002 is rig b's Direwolf: %+v", r)
	}
	in.Sys.PatPacketPort = func() (int, bool) { return 8100, true }
	if r := find(Run(in), "Pat packet TNC"); r.Level != Warn || !strings.Contains(r.Detail, "8000 (rig a)") {
		t.Errorf("a stale port must list the real ones: %+v", r)
	}
	in.Sys.PatPacketPort = func() (int, bool) { return 0, false }
	if r := find(Run(in), "Pat packet TNC"); r.Level != Warn || !strings.Contains(r.Fix, "hamstack pat sync") {
		t.Errorf("Pat not set up for AGWPE: %+v", r)
	}
	in.Cfg.Rigs[0].Modem, in.Cfg.Rigs[1].Modem = "", ""
	if !missing(Run(in), "Pat packet TNC") {
		t.Error("no Direwolf rig, nothing to check")
	}
}

func TestSeveralRigsAreChecked(t *testing.T) {
	rs := Run(twoRigs())
	for _, name := range []string{"hf: radio profile", "vhf: radio profile", "hf: rig control port", "vhf: modem port", "hf: radio (CAT port)", "vhf: radio (CAT port)"} {
		if missing(rs, name) {
			t.Errorf("missing check %q", name)
		}
	}
	if r := find(rs, "rig hardware"); r.Level != OK {
		t.Errorf("distinct hardware should pass: %+v", r)
	}
}

func TestEachRigUsesItsOwnPorts(t *testing.T) {
	in := twoRigs()
	in.Sys.Listening = func(p int) bool { return p == 4533 || p == 8310 } // only the second rig's ports are busy
	in.Sys.ServiceActive = func(s string) bool { return s == "rigctld@vhf" || s == "mercury@vhf" }
	rs := Run(in)
	if find(rs, "vhf: rig control port").Level != OK || find(rs, "vhf: modem port").Level != OK {
		t.Errorf("vhf's own services on vhf's ports: %+v", rs)
	}
	if !strings.Contains(find(rs, "hf: rig control port").Detail, "4532 is free") {
		t.Errorf("hf checks port 4532: %+v", find(rs, "hf: rig control port"))
	}
	// An unrelated program on the second rig's port is flagged against that rig, not the first.
	in.Sys.ServiceActive = func(string) bool { return false }
	if r := find(Run(in), "vhf: rig control port"); r.Level != Warn || !strings.Contains(r.Detail, "4533") {
		t.Errorf("%+v", r)
	}
}

func TestTwoRigsOnOnePort(t *testing.T) {
	in := twoRigs()
	in.Cfg.Rigs[1].CAT = in.Cfg.Rigs[0].CAT
	r := find(Run(in), "shared radio port")
	if r.Level != Fail || !strings.Contains(r.Detail, "hf and vhf") || !strings.Contains(r.Fix, "hamstack rig identify vhf") {
		t.Errorf("two rigs on one serial port must fail and say how to fix it: %+v", r)
	}
	in = twoRigs()
	in.Cfg.Rigs[1].AudioUSB = in.Cfg.Rigs[0].AudioUSB
	if r := find(Run(in), "shared sound card"); r.Level != Fail {
		t.Errorf("two modems on one sound card must fail: %+v", r)
	}
}

func TestOffRigsAreNotChecked(t *testing.T) {
	in := twoRigs()
	in.Cfg.Rigs[1].Off = true
	rs := Run(in)
	if !missing(rs, "vhf: radio profile") || !missing(rs, "hf: radio profile") {
		t.Error("with one active rig the checks are not prefixed, and the switched-off rig is skipped")
	}
	if missing(rs, "radio profile") {
		t.Error("the active rig must still be checked")
	}
}

func TestPatLocatorMatchesTheGridHere(t *testing.T) {
	in := inputs()
	in.Cfg.Grid = "EM10dg"
	in.Sys.PatLocator = func() (string, bool) { return "EM10dg", true }
	if r := find(Run(in), "Pat locator"); r.Level != OK {
		t.Errorf("same locator: %+v", r)
	}
	in.Sys.PatLocator = func() (string, bool) { return "em10", true } // a shorter one in the same square
	if r := find(Run(in), "Pat locator"); r.Level != OK {
		t.Errorf("4 characters of the same square is not a mismatch: %+v", r)
	}
	in.Sys.PatLocator = func() (string, bool) { return "FN31pr", true }
	r := find(Run(in), "Pat locator")
	if r.Level != Warn || !strings.Contains(r.Detail, "FN31pr") || !strings.Contains(r.Detail, "EM10dg") || r.Fix != "update just that field (a backup is kept): hamstack pat locator" {
		t.Errorf("a different square must warn with both values and the fix: %+v", r)
	}
	// At a site, the site's grid is the one that counts.
	in.Cfg.AddSite(config.Site{ID: "eoc", Grid: "FN31pr"})
	if r := find(Run(in), "Pat locator"); r.Level != OK {
		t.Errorf("at a site whose grid matches Pat's: %+v", r)
	}
	in.Cfg.Grid, in.Cfg.Sites[0].Grid = "", ""
	if !missing(Run(in), "Pat locator") {
		t.Error("no grid known, nothing to compare")
	}
	in.Cfg.Grid = "EM10dg"
	in.Sys.PatLocator = func() (string, bool) { return "", false }
	if !missing(Run(in), "Pat locator") {
		t.Error("Pat has no locator set: nothing to compare")
	}
	in.Cfg.NoPat = true
	in.Sys.PatLocator = func() (string, bool) { return "FN31pr", true }
	if !missing(Run(in), "Pat locator") {
		t.Error("without Pat there is nothing to check")
	}
}

func TestSiteHint(t *testing.T) {
	in := inputs()
	if !missing(Run(in), "site") {
		t.Error("no sites defined: no site check")
	}
	in.Cfg.AddSite(config.Site{ID: "home", Rigs: []string{"main"}})
	in.Sys.SiteHint = func() (string, string, bool) { return "", "", false }
	if r := find(Run(in), "site"); r.Level != OK || !strings.Contains(r.Detail, "home") {
		t.Errorf("at a site with nothing better: %+v", r)
	}
	in.Sys.SiteHint = func() (string, string, bool) { return "eoc", `the radios of site "eoc" are plugged in`, true }
	r := find(Run(in), "site")
	if r.Level != Warn || r.Fix != "switch with: hamstack site use eoc" {
		t.Errorf("a better-matching site must be suggested, never applied: %+v", r)
	}
}

func gpsInputs() Inputs {
	in := inputs()
	in.Cfg.SetGPS(config.GPS{Device: "/dev/ttyACM9"})
	in.Sys.GPSDPath = func() (string, error) { return "/usr/sbin/gpsd", nil }
	in.Sys.GPSDevice = func() (string, error) { return "/dev/ttyACM9", nil }
	in.Sys.SystemGPSD = func() string { return "" }
	in.Sys.ServiceActive = func(n string) bool { return n == "gpsd" }
	in.Sys.GPSFix = func() (bool, string, error) { return true, "3D fix, grid FN31pr", nil }
	return in
}

func TestGPSChecks(t *testing.T) {
	if rs := Run(inputs()); !missing(rs, "gpsd") || !missing(rs, "GPS fix") {
		t.Error("without a GPS there is nothing to check")
	}
	rs := Run(gpsInputs())
	if Worst(rs) != OK {
		t.Fatalf("all good: %+v", rs)
	}
	for _, n := range []string{"gpsd", "GPS receiver", "GPS fix"} {
		if find(rs, n).Level != OK {
			t.Errorf("%s: %+v", n, find(rs, n))
		}
	}

	cases := map[string]struct {
		change func(*Inputs)
		check  string
		level  Level
		fix    string
	}{
		"gpsd missing": {func(in *Inputs) { in.Sys.GPSDPath = func() (string, error) { return "", errors.New("no") } }, "gpsd", Fail, "hamstack install gpsd"},
		"unplugged": {func(in *Inputs) {
			in.Sys.GPSDevice = func() (string, error) { return "", errors.New("GPS receiver not found") }
		}, "GPS receiver", Fail, "hamstack gps use"},
		"permissions": {func(in *Inputs) { in.Sys.Access = func(string) error { return errors.New("denied") } }, "GPS receiver", Fail, "hamstack groups fix"},
		"system gpsd": {func(in *Inputs) { in.Sys.SystemGPSD = func() string { return "gpsd.socket" } }, "system gpsd", Warn, "disable --now gpsd.socket"},
		"stopped":     {func(in *Inputs) { in.Sys.ServiceActive = func(string) bool { return false } }, "gpsd service", Warn, "hamstack start gpsd"},
		"no fix": {func(in *Inputs) {
			in.Sys.GPSFix = func() (bool, string, error) { return false, "no fix, 0 of 9 satellites in use", nil }
		}, "GPS fix", Warn, "sky"},
		"no answer": {func(in *Inputs) {
			in.Sys.GPSFix = func() (bool, string, error) { return false, "", errors.New("cannot reach gpsd") }
		}, "GPS fix", Warn, "hamstack gps"},
	}
	for name, c := range cases {
		in := gpsInputs()
		c.change(&in)
		r := find(Run(in), c.check)
		if r.Level != c.level || !strings.Contains(r.Fix, c.fix) {
			t.Errorf("%s: %+v", name, r)
		}
	}

	// A gpsd elsewhere: nothing to install or find here, only whether it answers.
	in := gpsInputs()
	in.Cfg.SetGPS(config.GPS{Addr: "pi:2947"})
	in.Sys.GPSDPath = func() (string, error) { return "", errors.New("must not be asked") }
	in.Sys.GPSDevice = in.Sys.GPSDPath
	rs = Run(in)
	if !missing(rs, "gpsd") || !missing(rs, "GPS receiver") || find(rs, "GPS fix").Level != OK {
		t.Errorf("external gpsd: %+v", rs)
	}
	// Switched off: silent.
	in = gpsInputs()
	in.Cfg.GPS.Off = true
	if rs := Run(in); !missing(rs, "gpsd") || !missing(rs, "GPS fix") {
		t.Error("a switched-off GPS is not checked")
	}
}

func TestSerialRadioWithoutAPortIsReported(t *testing.T) {
	in := inputs()
	in.Profiles[0].Rig.DeviceGlob = "" // a radio picked from hamlib's list: no pattern to find its port by
	in.Profiles[0].Rig.Baud = 19200
	r := find(Run(in), "radio (CAT port)")
	if r.Level != Warn || !strings.Contains(r.Detail, "no serial port is assigned") || !strings.Contains(r.Fix, "hamstack rig identify main") {
		t.Errorf("%+v", r)
	}
	in.Profiles[0].Rig.Baud = 0 // the dummy rig has no port and needs none
	if r := find(Run(in), "radio (CAT port)"); !strings.HasPrefix(r.Name, "MISSING") {
		t.Errorf("the dummy radio must not be asked for a port: %+v", r)
	}
}
