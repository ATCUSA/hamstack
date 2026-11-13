package units

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/profile"
)

func cfgWith(t *testing.T, rigs ...config.Rig) config.Config {
	t.Helper()
	var c config.Config
	for _, r := range rigs {
		if err := c.AddRig(r); err != nil {
			t.Fatal(err)
		}
	}
	return c
}

func planFor(t *testing.T, c config.Config) Plan {
	t.Helper()
	pr, err := profile.Find("", "dummy")
	if err != nil {
		t.Fatal(err)
	}
	p := Plan{Config: c, StateDir: "/home/u/.local/state/hamstack", Pat: "/usr/bin/pat", Hamstack: "/home/u/.local/bin/hamstack",
		Station: config.Station{ID: "club", Callsign: "K0CLUB", PatConfig: "/home/u/my pat/club.json", HTTPAddr: "localhost:8081"}}
	for _, r := range c.ActiveRigs() {
		p.Rigs = append(p.Rigs, RigPlan{Rig: r, Profile: pr})
	}
	return p
}

func names(ss []Service) []string {
	var out []string
	for _, s := range ss {
		out = append(out, s.Name())
	}
	return out
}

func TestServicesInStartOrder(t *testing.T) {
	c := cfgWith(t, config.Rig{ID: "hf", Profile: "dummy", Modem: config.Mercury}, config.Rig{ID: "vhf", Profile: "dummy"})
	want := []string{"rigctld@hf", "mercury@hf", "rigctld@vhf", "pat"}
	if got := names(ServicesFor(c)); !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	c.Rigs[1].Off = true
	c.NoPat = true
	if got := names(ServicesFor(c)); !slices.Equal(got, []string{"rigctld@hf", "mercury@hf"}) {
		t.Errorf("off rigs and Pat must not appear: %v", got)
	}
}

func TestUnitNamesAndParse(t *testing.T) {
	s := Service{config.Mercury, "hf"}
	if s.Unit() != "hamstack-mercury@hf.service" || s.Name() != "mercury@hf" {
		t.Errorf("%+v", s)
	}
	if u := (Service{Kind: config.Pat}).Unit(); u != "hamstack-pat.service" {
		t.Errorf("pat unit = %s", u)
	}
	for _, n := range []string{"mercury@hf", "rigctld@vhf-1", "pat"} {
		if got, ok := ParseName(n); !ok || got.Name() != n {
			t.Errorf("ParseName(%q) = %+v %v", n, got, ok)
		}
	}
	for _, bad := range []string{"mercury", "pat@hf", "rigctld@", "rigctld@Bad Id", "bogus@hf", ""} {
		if _, ok := ParseName(bad); ok {
			t.Errorf("ParseName(%q) should fail", bad)
		}
	}
}

func TestResolveArgs(t *testing.T) {
	c := cfgWith(t, config.Rig{ID: "hf", Profile: "dummy", Modem: config.Mercury}, config.Rig{ID: "vhf", Profile: "dummy", Modem: config.Mercury})
	cases := map[string][]string{
		"all":         {"rigctld@hf", "mercury@hf", "rigctld@vhf", "mercury@vhf", "pat"},
		"mercury":     {"mercury@hf", "mercury@vhf"},  // a kind: every instance
		"vhf":         {"rigctld@vhf", "mercury@vhf"}, // a rig: everything of it
		"pat":         {"pat"},
		"rigctld@hf":  {"rigctld@hf"},
		"mercury@vhf": {"mercury@vhf"},
	}
	for arg, want := range cases {
		got, err := Resolve(c, arg)
		if err != nil || !slices.Equal(names(got), want) {
			t.Errorf("Resolve(%q) = %v, %v; want %v", arg, names(got), err, want)
		}
	}
	if _, err := Resolve(c, "uhf"); err == nil || !strings.Contains(err.Error(), "rigctld@hf") {
		t.Errorf("an unknown name should list what exists: %v", err)
	}
}

func TestOnlyNeededUnitsGenerated(t *testing.T) {
	both := cfgWith(t, config.Rig{ID: "hf", Profile: "dummy", Modem: config.Mercury})
	if got := Generate(planFor(t, both)); len(got) != 3 || got[rigctldTemplate] == "" || got[mercuryTemplate] == "" || got[patUnitFile] == "" {
		t.Errorf("full setup: %v", keys(got))
	}
	rigOnly := cfgWith(t, config.Rig{ID: "hf", Profile: "dummy"})
	rigOnly.NoPat = true
	if got := Generate(planFor(t, rigOnly)); len(got) != 1 || got[rigctldTemplate] == "" {
		t.Errorf("rig control only: %v", keys(got))
	}
	patOnly := config.Config{}
	if got := Generate(planFor(t, patOnly)); len(got) != 1 || got[patUnitFile] == "" {
		t.Errorf("pat only: %v", keys(got))
	}
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func TestTemplatesHoldNoRigSpecifics(t *testing.T) {
	// Adding a rig must never need a new or changed unit file: the instance name is all they use.
	one := Generate(planFor(t, cfgWith(t, config.Rig{ID: "hf", Profile: "dummy", Modem: config.Mercury})))
	two := Generate(planFor(t, cfgWith(t, config.Rig{ID: "hf", Profile: "dummy", Modem: config.Mercury}, config.Rig{ID: "vhf", Profile: "dummy", Modem: config.Mercury})))
	for _, f := range []string{rigctldTemplate, mercuryTemplate} {
		if one[f] != two[f] {
			t.Errorf("%s changed when a rig was added", f)
		}
	}
	r := one[rigctldTemplate]
	for _, want := range []string{"run rigctld %i", "RestartPreventExitStatus=3", "Description=hamstack rig control (rigctld, %i)"} {
		if !strings.Contains(r, want) {
			t.Errorf("rigctld template missing %q:\n%s", want, r)
		}
	}
	m := one[mercuryTemplate]
	for _, want := range []string{"run mercury %i", "BindsTo=hamstack-rigctld@%i.service", "After=hamstack-rigctld@%i.service", "StartLimitBurst=3"} {
		if !strings.Contains(m, want) {
			t.Errorf("mercury template missing %q:\n%s", want, m)
		}
	}
	if strings.Index(m, "StartLimitBurst") > strings.Index(m, "[Service]") {
		t.Error("StartLimit* belongs in [Unit]")
	}
	if strings.Contains(r+m, "%%i") {
		t.Error("the instance specifier %i must not be escaped")
	}
}

func TestPatUnit(t *testing.T) {
	c := cfgWith(t, config.Rig{ID: "hf", Profile: "dummy", Modem: config.Mercury}, config.Rig{ID: "vhf", Profile: "dummy", Modem: config.Mercury})
	u := Generate(planFor(t, c))[patUnitFile]
	for _, want := range []string{
		`--config "/home/u/my pat/club.json"`, // path with a space is quoted
		"--log /home/u/.local/state/hamstack/pat-club.log",
		"--event-log /home/u/.local/state/hamstack/pat-club-events.json",
		"http --addr localhost:8081",
		"Wants=hamstack-mercury@hf.service", // Pat's HF modem is the first Mercury rig
		"After=hamstack-mercury@hf.service",
	} {
		if !strings.Contains(u, want) {
			t.Errorf("pat unit missing %q:\n%s", want, u)
		}
	}
	if strings.Contains(u, "mercury@vhf") {
		t.Errorf("Pat waits only for its own HF modem:\n%s", u)
	}
	noModem := cfgWith(t, config.Rig{ID: "hf", Profile: "dummy"})
	if u := Generate(planFor(t, noModem))[patUnitFile]; strings.Contains(u, "mercury") {
		t.Errorf("no Mercury rig: Pat must not reference it (systemd would try to start it):\n%s", u)
	}
}

func TestMercuryConfigPerRig(t *testing.T) {
	c := cfgWith(t, config.Rig{ID: "hf", Profile: "dummy", Modem: config.Mercury}, config.Rig{ID: "second", Profile: "dummy", Modem: config.Mercury})
	pr, _ := profile.Find("", "dummy")
	st := config.Station{ID: "me", Callsign: "N0CALL"}
	first := MercuryConfig(c, c.Rigs[0], pr, st, "", "/state")
	second := MercuryConfig(c, c.Rigs[1], pr, st, "", "/state")
	for _, want := range []string{"arq_tcp_base_port = 8300", "broadcast_tcp_port = 8100", "ui_port = 10000", "device = localhost:4532", "callsign = N0CALL", "sound_system = null", "path = /state/mercury-hf.jsonl", "ui_enabled = false"} {
		if !strings.Contains(first, want) {
			t.Errorf("first rig missing %q:\n%s", want, first)
		}
	}
	for _, want := range []string{"arq_tcp_base_port = 8310", "broadcast_tcp_port = 8101", "ui_port = 10001", "device = localhost:4533", "path = /state/mercury-second.jsonl"} {
		if !strings.Contains(second, want) {
			t.Errorf("second rig missing %q (it must not share ports or its message history with the first):\n%s", want, second)
		}
	}
	prReal, _ := profile.Find("", "ic7300mk2")
	if got := MercuryConfig(c, c.Rigs[0], prReal, st, "plughw:CARD=Device,DEV=0", "/state"); !strings.Contains(got, "input_device = plughw:CARD=Device,DEV=0") || !strings.Contains(got, "sound_system = alsa") {
		t.Errorf("real audio:\n%s", got)
	}
}

func TestEscape(t *testing.T) {
	if got := escape("a%b$c"); got != "a%%b$$c" {
		t.Errorf("escape = %q", got)
	}
}

// Install writes the units; Stale finds the ones a plan no longer needs, including the fixed units
// of the single-radio versions.
func TestInstallAndStale(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgDir)
	dir, err := Dir()
	if err != nil || !strings.HasPrefix(dir, cfgDir) {
		t.Fatalf("unit dir %q should follow XDG_CONFIG_HOME: %v", dir, err)
	}
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "hamstack-rigctld.service"), []byte("old"), 0o644) // single-radio era
	os.WriteFile(filepath.Join(dir, "hamstack-mercury.service"), []byte("old"), 0o644)
	os.WriteFile(filepath.Join(dir, "unrelated.service"), []byte("x"), 0o644)

	p := planFor(t, cfgWith(t, config.Rig{ID: "hf", Profile: "dummy"}))
	p.Config.NoPat = true
	p.StateDir = filepath.Join(cfgDir, "state")
	written, err := Install(p)
	if err != nil || len(written) != 1 || filepath.Base(written[0]) != rigctldTemplate {
		t.Fatalf("Install = %v, %v", written, err)
	}
	stale, err := Stale(p)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(stale)
	if !slices.Equal(stale, []string{"hamstack-mercury.service", "hamstack-rigctld.service"}) {
		t.Errorf("stale = %v (the template in use and unrelated files must not be listed)", stale)
	}
	if _, err := RemoveFiles(stale); err != nil {
		t.Fatal(err)
	}
	if left, _ := Installed(); !slices.Equal(left, []string{rigctldTemplate}) {
		t.Errorf("left = %v", left)
	}
	if _, err := os.Stat(filepath.Join(dir, "unrelated.service")); err != nil {
		t.Error("files that are not ours must never be touched")
	}
}

func TestDirewolfServicesAndTemplate(t *testing.T) {
	c := cfgWith(t,
		config.Rig{ID: "hf", Profile: "dummy", Modem: config.Mercury},
		config.Rig{ID: "pkt", Profile: "dummy", Modem: config.Direwolf},
		config.Rig{ID: "dig", Profile: "digirig", Modem: config.Direwolf, NoCAT: true})
	want := []string{"rigctld@hf", "mercury@hf", "rigctld@pkt", "direwolf@pkt", "direwolf@dig", "pat"}
	if got := names(ServicesFor(c)); !slices.Equal(got, want) {
		t.Errorf("got %v, want %v (a rig with no CAT, like a Digirig, has no rigctld)", got, want)
	}
	if s, ok := ParseName("direwolf@pkt"); !ok || s.Unit() != "hamstack-direwolf@pkt.service" {
		t.Errorf("direwolf@pkt: %+v %v", s, ok)
	}
	gen := Generate(planFor(t, c))
	d := gen[direwolfTemplate]
	for _, want := range []string{"run direwolf %i", "After=hamstack-rigctld@%i.service", "StartLimitBurst=3", "RestartPreventExitStatus=3"} {
		if !strings.Contains(d, want) {
			t.Errorf("direwolf template missing %q:\n%s", want, d)
		}
	}
	if strings.Contains(d, "BindsTo") || strings.Contains(d, "Wants=hamstack-rigctld") {
		t.Errorf("a Digirig rig has no rigctld, so the template must not require it:\n%s", d)
	}
	// Only Digirig-style rigs: no rig control template at all.
	only := cfgWith(t, config.Rig{ID: "dig", Profile: "digirig", Modem: config.Direwolf, NoCAT: true})
	if got := Generate(planFor(t, only)); got[rigctldTemplate] != "" || got[direwolfTemplate] == "" {
		t.Errorf("no CAT rig: no rigctld template, but a direwolf one: %v", keys(got))
	}
}

func TestDirewolfPTT(t *testing.T) {
	cases := []struct {
		mode, dev string
		port      int
		want      string
		bad       bool
	}{
		{"rig", "", 4533, "PTT RIG 2 localhost:4533", false},
		{"rts", "/dev/serial/by-id/usb-Silicon_Labs-if00-port0", 0, "PTT /dev/serial/by-id/usb-Silicon_Labs-if00-port0 RTS", false},
		{"dtr", "/dev/ttyUSB0", 0, "PTT /dev/ttyUSB0 DTR", false},
		{"cm108", "", 0, "PTT CM108", false},
		{"none", "", 0, "", false},
		{"", "", 0, "", false},
		{"rig", "", 0, "", true},  // keying through rigctld with no rigctld
		{"rts", "", 0, "", true},  // a serial line with no serial port
		{"vox2", "", 0, "", true}, // unknown
	}
	for _, c := range cases {
		got, err := DirewolfPTT(c.mode, c.dev, c.port)
		if (err != nil) != c.bad || got != c.want {
			t.Errorf("DirewolfPTT(%q,%q,%d) = %q, %v; want %q (bad=%v)", c.mode, c.dev, c.port, got, err, c.want, c.bad)
		}
	}
}

func TestDirewolfConfig(t *testing.T) {
	var c config.Config
	c.AddRig(config.Rig{ID: "a", Profile: "dummy", Modem: config.Direwolf})
	c.AddRig(config.Rig{ID: "b", Profile: "dummy", Modem: config.Direwolf})
	pr, _ := profile.Find("", "dummy")
	st := config.Station{ID: "me", Callsign: "N0CALL"}
	first := DirewolfConfig(c.Rigs[0], pr, st, "", "")
	for _, want := range []string{"ADEVICE null null", "MYCALL N0CALL", "MODEM 1200", "AGWPORT 8000", "KISSPORT 8001"} {
		if !strings.Contains(first, want) {
			t.Errorf("first rig missing %q:\n%s", want, first)
		}
	}
	if strings.Contains(first, "PTT") {
		t.Errorf("no PTT method must write no PTT line:\n%s", first)
	}
	if strings.Index(first, "KISSPORT 0") < 0 || strings.Index(first, "KISSPORT 0") > strings.Index(first, "KISSPORT 8001") {
		t.Errorf("the default KISS port must be removed before the rig's own is added:\n%s", first)
	}
	second := DirewolfConfig(c.Rigs[1], pr, st, "", "PTT RIG 2 localhost:4533")
	if !strings.Contains(second, "AGWPORT 8002") || !strings.Contains(second, "KISSPORT 8003") || !strings.Contains(second, "PTT RIG 2 localhost:4533") {
		t.Errorf("second rig must not share the first's ports:\n%s", second)
	}
	real, _ := profile.Find("", "ic7300mk2")
	got := DirewolfConfig(c.Rigs[0], real, st, "plughw:CARD=Device,DEV=0", "")
	if !strings.Contains(got, "ADEVICE plughw:CARD=Device,DEV=0 plughw:CARD=Device,DEV=0") {
		t.Errorf("real sound card:\n%s", got)
	}
	if got := DirewolfConfig(c.Rigs[0], real, config.Station{}, "plughw:CARD=Device,DEV=0", ""); !strings.Contains(got, "MYCALL N0CALL") {
		t.Errorf("with no station Direwolf still needs a MYCALL:\n%s", got)
	}
}

func TestGPSDService(t *testing.T) {
	c := cfgWith(t, config.Rig{ID: "hf", Profile: "dummy", Modem: config.Mercury})
	if slices.Contains(names(ServicesFor(c)), "gpsd") {
		t.Fatal("gpsd is a service only when a GPS is set up")
	}
	if err := c.SetGPS(config.GPS{Device: "/dev/ttyACM0"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"gpsd", "rigctld@hf", "mercury@hf", "pat"}
	if got := names(ServicesFor(c)); !slices.Equal(got, want) {
		t.Errorf("services = %v, want %v", got, want)
	}
	if s, ok := ParseName("gpsd"); !ok || s.Unit() != "hamstack-gpsd.service" {
		t.Errorf("ParseName(gpsd) = %+v, %v", s, ok)
	}
	if _, ok := ParseName("gpsd@hf"); ok {
		t.Error("gpsd has no instances")
	}
	got, err := Resolve(c, "gpsd")
	if err != nil || len(got) != 1 || got[0].Kind != config.GPSD {
		t.Errorf("Resolve(gpsd) = %v, %v", got, err)
	}

	gen := Generate(planFor(t, c))
	u := gen["hamstack-gpsd.service"]
	for _, want := range []string{"ExecStart=/home/u/.local/bin/hamstack run gpsd\n", "RestartPreventExitStatus=3", "TimeoutStopSec=10", "WantedBy=default.target"} {
		if !strings.Contains(u, want) {
			t.Errorf("gpsd unit lacks %q:\n%s", want, u)
		}
	}
	if strings.Contains(u, "/dev/") || strings.Contains(u, "2948") {
		t.Errorf("the unit must not hold the receiver or the port:\n%s", u)
	}
	pat := gen["hamstack-pat.service"]
	if !strings.Contains(pat, "After=hamstack-gpsd.service") || !strings.Contains(pat, "Wants=hamstack-gpsd.service") {
		t.Errorf("Pat should start after gpsd:\n%s", pat)
	}
	c.GPS.Addr = "pi:2947"
	if _, ok := Generate(planFor(t, c))["hamstack-gpsd.service"]; ok {
		t.Error("no unit for a gpsd that runs elsewhere")
	}
}
