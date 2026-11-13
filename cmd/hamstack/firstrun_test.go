package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/versions"
)

func TestFirstRunOffer(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var out bytes.Buffer

	// Not a terminal, or already set up: say nothing and never read input.
	if err := offerFirstRunSetup(strings.NewReader(""), &out, true, false); err != nil || out.Len() != 0 {
		t.Errorf("non-interactive: %v %q", err, out.String())
	}
	if err := offerFirstRunSetup(strings.NewReader(""), &out, false, true); err != nil || out.Len() != 0 {
		t.Errorf("set up already: %v %q", err, out.String())
	}

	// Declined: told how to start it later, and not asked again.
	if err := offerFirstRunSetup(strings.NewReader("n\n"), &out, true, true); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Welcome to hamstack", "Run the guided setup now?", "hamstack setup winlink"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	if _, err := os.Stat(firstRunMarker()); err != nil {
		t.Errorf("a declined offer must be remembered: %v", err)
	}
	out.Reset()
	if err := offerFirstRunSetup(strings.NewReader("y\n"), &out, true, true); err != nil || out.Len() != 0 {
		t.Errorf("asked a second time: %v %q", err, out.String())
	}
}

func TestNextStepsFitTheSetup(t *testing.T) {
	var cfg config.Config
	cfg.AddRig(config.Rig{ID: "pkt", Profile: "dummy", Modem: config.Direwolf})
	var b bytes.Buffer
	printNextSteps(&b, cfg)
	for _, want := range []string{"packet listen", "hamstack ax25", "hamstack gps use", "hamstack network", "site add"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("a Direwolf setup should suggest %q:\n%s", want, b.String())
		}
	}
	var hf config.Config
	hf.AddRig(config.Rig{ID: "hf", Profile: "dummy", Modem: config.Mercury})
	hf.SetGPS(config.GPS{Device: "/dev/ttyACM0"})
	hf.Sites = []config.Site{{ID: "home"}}
	b.Reset()
	printNextSteps(&b, hf)
	for _, not := range []string{"packet listen", "ax25", "gps use", "site add"} {
		if strings.Contains(b.String(), not) {
			t.Errorf("an HF setup with a GPS and a site should not suggest %q:\n%s", not, b.String())
		}
	}
}

func TestFindStale(t *testing.T) {
	var cfg config.Config
	cfg.AddRig(config.Rig{ID: "pkt", Profile: "dummy", Modem: config.Direwolf})
	inst := map[string]*versions.Installed{
		"hamlib":   {Version: "4.6.2", Origin: "system package"},
		"direwolf": {Version: "1.7", Origin: "system package"},
	}
	latest := map[string]string{"hamlib": "4.7.2", "direwolf": "1.8.1"}
	find := func() []stalePackage {
		return findStale(cfg, nil, func(n string) *versions.Installed { return inst[n] }, func(n string) string { return latest[n] })
	}
	got := find()
	if len(got) != 2 || got[0].Name != "hamlib" || got[1].Name != "direwolf" || got[1].Installed != "1.7" || got[1].Latest != "1.8.1" || got[0].MustHave {
		t.Errorf("both are old: %+v", got)
	}
	// hamstack's own build in ~/.local is not the distribution's, and a current one is not stale.
	inst["direwolf"].Origin = "user install"
	inst["hamlib"].Version = "4.7.2"
	if got := find(); len(got) != 0 {
		t.Errorf("nothing to offer: %+v", got)
	}
	// Upstream unreachable, or not installed: no offer.
	inst["hamlib"].Version = "4.0"
	latest["hamlib"] = ""
	delete(inst, "direwolf")
	if got := find(); len(got) != 0 {
		t.Errorf("unknown latest: %+v", got)
	}
	// Direwolf is only checked when the setup uses it.
	latest["hamlib"] = "4.7.2"
	inst["direwolf"] = &versions.Installed{Version: "1.7", Origin: "system package"}
	var hf config.Config
	hf.AddRig(config.Rig{ID: "hf", Profile: "dummy", Modem: config.Mercury})
	cfg = hf
	if got := find(); len(got) != 1 || got[0].Name != "hamlib" {
		t.Errorf("an HF setup does not use Direwolf: %+v", got)
	}
}

// /dev/null is a character device, as a terminal is; only a terminal answers for its settings.
func TestDevNullIsNotATerminal(t *testing.T) {
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Skip(err)
	}
	defer null.Close()
	if st, _ := null.Stat(); st.Mode()&os.ModeCharDevice == 0 {
		t.Skip("/dev/null is not a character device here")
	}
	if isTTY(null) {
		t.Error("/dev/null was taken for a terminal")
	}
	f, err := os.CreateTemp(t.TempDir(), "plain")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if isTTY(f) {
		t.Error("a plain file was taken for a terminal")
	}
}
