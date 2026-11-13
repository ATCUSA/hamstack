package main

import (
	"github.com/ATCUSA/hamstack/internal/catalog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ATCUSA/hamstack/internal/config"
)

// isolated points hamstack's config, state and home at an empty temp tree, so a test can change the
// configuration freely without touching the real one or any systemd units.
func isolated(t *testing.T) (home string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	t.Setenv("HOME", filepath.Join(root, "home"))
	os.MkdirAll(filepath.Join(root, "home"), 0o755)
	return filepath.Join(root, "home")
}

func TestBackendRigLifecycle(t *testing.T) {
	isolated(t)
	b := backend{}
	if !b.NeedsSetup() {
		t.Error("an empty config is a fresh install")
	}
	if msg, err := b.RigAdd("dummy", "hf", "mercury"); err != nil || !strings.Contains(msg, "added rig hf") {
		t.Fatalf("RigAdd: %q %v", msg, err)
	}
	if msg, err := b.RigAdd("digirig", "dig", "direwolf"); err != nil {
		t.Fatalf("RigAdd digirig: %q %v", msg, err)
	}
	if _, err := b.RigAdd("digirig", "bad", "mercury"); err == nil || !strings.Contains(err.Error(), "no CAT") {
		t.Errorf("Mercury on a radio with no CAT must be refused with the reason: %v", err)
	}
	if b.NeedsSetup() {
		t.Error("with rigs it is no longer a fresh install")
	}
	rigs := b.Rigs()
	if len(rigs) != 2 || rigs[0].ID != "hf" || !rigs[0].On || rigs[0].Modem != "mercury" || !strings.Contains(rigs[0].Ports, "rig 4532") || !strings.Contains(rigs[0].Ports, "modem 8300") {
		t.Errorf("rigs = %+v", rigs)
	}
	if !rigs[1].NoCAT || !strings.Contains(rigs[1].Ports, "packet 8002") || strings.Contains(rigs[1].Ports, "rig ") {
		t.Errorf("a no-CAT rig has a packet port and no rigctld: %+v", rigs[1])
	}
	if h := b.Header(); h.Rigs != "hf, dig" {
		t.Errorf("header rigs = %q", h.Rigs)
	}

	if msg, err := b.RigToggle("hf"); err != nil || !strings.Contains(msg, "off") {
		t.Errorf("toggle: %q %v", msg, err)
	}
	if got := b.Rigs()[0]; got.On || got.Hardware != "off" {
		t.Errorf("a switched-off rig says so: %+v", got)
	}
	if h := b.Header(); h.Rigs != "dig" {
		t.Errorf("switched-off rigs leave the header: %q", h.Rigs)
	}
	b.RigToggle("hf")
	if _, err := b.RigModem("hf", "direwolf"); err != nil {
		t.Error(err)
	}
	if _, err := b.RigModem("dig", "mercury"); err == nil {
		t.Error("a rig with no CAT cannot take Mercury")
	}
	if got := b.Rigs()[0].Modem; got != "direwolf" {
		t.Errorf("modem = %q", got)
	}
	if _, err := b.RigModem("hf", ""); err != nil || b.Rigs()[0].Modem != "" {
		t.Errorf("an empty modem means none: %v", err)
	}
	for _, id := range []string{"nope"} {
		if _, err := b.RigToggle(id); err == nil {
			t.Error("unknown rig")
		}
		if _, err := b.RigRemove(id); err == nil {
			t.Error("unknown rig")
		}
	}
	if _, err := b.RigRemove("dig"); err != nil || len(b.Rigs()) != 1 {
		t.Errorf("remove: %v %v", err, b.Rigs())
	}
}

func TestBackendProfiles(t *testing.T) {
	isolated(t)
	byID := map[string]bool{}
	for _, p := range (backend{}).Profiles() {
		byID[p.ID] = p.HasCAT
	}
	if !byID["ic7300mk2"] || byID["digirig"] || !byID["dummy"] {
		t.Errorf("profiles and whether each has CAT: %v", byID)
	}
}

func TestBackendStationsAndImport(t *testing.T) {
	home := isolated(t)
	b := backend{}
	if msg, err := b.StationAdd("me", "k0test"); err != nil || !strings.Contains(msg, "K0TEST") || !strings.Contains(msg, "Winlink password") {
		t.Fatalf("StationAdd: %q %v", msg, err)
	}
	if _, err := b.StationAdd("me", "K0TEST"); err == nil {
		t.Error("duplicate station id")
	}
	if _, err := b.StationAdd("x", "nonsense"); err == nil {
		t.Error("bad callsign")
	}
	st := b.Stations()
	if len(st) != 1 || !st[0].Active || st[0].Callsign != "K0TEST" {
		t.Errorf("stations = %+v", st)
	}
	patDir := filepath.Join(home, ".config", "pat")
	os.MkdirAll(patDir, 0o755)
	os.WriteFile(filepath.Join(patDir, "club.json"), []byte(`{"mycall":"K0CLUB","http_addr":"localhost:8082","secure_login_password":"secret"}`), 0o600)
	msg, err := b.StationImport()
	if err != nil || !strings.Contains(msg, "imported 1") {
		t.Fatalf("StationImport: %q %v", msg, err)
	}
	if got := b.Stations(); len(got) != 2 {
		t.Errorf("after import: %+v", got)
	}
	if msg, _ := b.StationImport(); !strings.Contains(msg, "nothing new") {
		t.Errorf("importing again adds nothing: %q", msg)
	}
	if b, _ := os.ReadFile(filepath.Join(patDir, "club.json")); !strings.Contains(string(b), "secret") {
		t.Error("import must not touch the Pat file")
	}
	if _, err := b.Use("club"); err != nil {
		t.Fatal(err)
	}
	if h := b.Header(); !strings.Contains(h.Station, "K0CLUB") {
		t.Errorf("header station = %q", h.Station)
	}
	if _, err := b.StationRemove("club"); err != nil {
		t.Fatal(err)
	}
	if len(b.Stations()) != 1 {
		t.Error("remove")
	}
}

func TestBackendStationImportWithNoPatConfigs(t *testing.T) {
	isolated(t)
	if _, err := (backend{}).StationImport(); err == nil || !strings.Contains(err.Error(), "no Pat configs") {
		t.Errorf("nothing to import is an error that says so: %v", err)
	}
}

func TestBackendSites(t *testing.T) {
	isolated(t)
	b := backend{}
	b.RigAdd("dummy", "hf", "mercury")
	b.RigAdd("dummy", "vhf", "")
	b.StationAdd("me", "K0TEST")
	b.StationAdd("eoc", "K0EOC")
	b.SetSetting("grid", "EM10dg")
	if msg, err := b.SiteAdd("home"); err != nil || !strings.Contains(msg, "rigs hf,vhf") || !strings.Contains(msg, "grid EM10dg") {
		t.Fatalf("SiteAdd captures what is set up now: %q %v", msg, err)
	}
	if _, err := b.SiteAdd("home"); err == nil {
		t.Error("duplicate site")
	}
	if _, err := b.SiteGrid("home", "em10dg"); err != nil {
		t.Fatal(err)
	}
	cfg := loadCfg()
	cfg.SetSiteField("home", "rigs", "hf")
	if err := cfg.AddSite(config.Site{ID: "eoc", Rigs: []string{"vhf"}, Station: "eoc", Grid: "FN31pr"}); err != nil {
		t.Fatal(err)
	}
	config.Save(cfg)
	if msg, err := b.SiteUse("eoc"); err != nil || !strings.Contains(msg, "at eoc") {
		t.Fatalf("SiteUse: %q %v", msg, err)
	}
	h := b.Header()
	if h.Site != "eoc" || h.Rigs != "vhf" || h.Grid != "FN31pr" || !strings.Contains(h.Station, "K0EOC") {
		t.Errorf("switching applies the site's rigs, station and grid: %+v", h)
	}
	sites := b.Sites()
	if len(sites) != 2 || !sites[1].Current || sites[0].Current {
		t.Errorf("sites = %+v", sites)
	}
	if _, err := b.SiteGrid("eoc", "not a grid"); err == nil {
		t.Error("bad grid")
	}
	if _, err := b.SiteUse("nowhere"); err == nil {
		t.Error("unknown site")
	}
	if _, err := b.SiteRemove("home"); err != nil || len(b.Sites()) != 1 {
		t.Errorf("remove: %v", err)
	}
	if _, _, ok := b.SiteHint(); ok {
		t.Error("dummy rigs cannot be recognised, so there is never a better site")
	}
}

func TestBackendSettings(t *testing.T) {
	isolated(t)
	b := backend{}
	get := func(key string) (v, kind string) {
		for _, s := range b.Settings() {
			if s.Key == key {
				return s.Value, s.Kind
			}
		}
		return "", "missing"
	}
	if v, k := get("pat"); v != "on" || k != "toggle" {
		t.Errorf("Pat starts on: %q %q", v, k)
	}
	if _, err := b.SetSetting("pat", ""); err != nil {
		t.Fatal(err)
	}
	if v, _ := get("pat"); v != "off" {
		t.Errorf("a toggle flips: %q", v)
	}
	b.SetSetting("pat", "")
	if v, _ := get("pat"); v != "on" {
		t.Errorf("and flips back: %q", v)
	}
	if v, _ := get("ui"); v != "off" {
		t.Errorf("the web UI starts off: %q", v)
	}
	b.SetSetting("ui", "")
	if v, _ := get("ui"); v != "on" {
		t.Error("ui toggle")
	}
	if _, err := b.SetSetting("grid", "fn31pr"); err != nil {
		t.Fatal(err)
	}
	if v, k := get("grid"); v != "FN31pr" || k != "text" {
		t.Errorf("grid is normalised: %q %q", v, k)
	}
	if _, err := b.SetSetting("grid", "xx"); err == nil {
		t.Error("a bad grid is refused")
	}
	if v, _ := get("grid"); v != "FN31pr" {
		t.Error("a refused value leaves the old one")
	}
	if _, err := b.SetSetting("nonsense", ""); err == nil {
		t.Error("unknown setting")
	}
	if _, kind := get("groups"); kind != "exec" {
		t.Error("the groups row runs a command in the terminal")
	}
}

func TestBackendCommandPassesArgumentsUnmangled(t *testing.T) {
	cmd := (backend{}).Command("rig", "identify", "a b; rm -rf /")
	if cmd.Args[0] != "sh" || cmd.Args[1] != "-c" {
		t.Fatalf("args = %v", cmd.Args)
	}
	if !strings.Contains(cmd.Args[2], "press Enter to return") || !strings.Contains(cmd.Args[2], `"$0" "$@"`) {
		t.Errorf("the command must wait for Enter, and pass its arguments as arguments, not text: %s", cmd.Args[2])
	}
	// Everything after the script is $0 (hamstack) and the arguments, each kept as one word.
	if tail := cmd.Args[4:]; !slices.Equal(tail, []string{"rig", "identify", "a b; rm -rf /"}) {
		t.Errorf("arguments = %v", tail)
	}
	if strings.Contains(cmd.Args[2], "rm -rf") {
		t.Error("user-supplied text must never be part of the shell script")
	}
}

func TestBackendDevicesAndProgramsDoNotPanic(t *testing.T) {
	isolated(t)
	b := backend{}
	keys := map[string]bool{}
	for _, d := range b.Devices() {
		if d.Key == "" || keys[d.Key] {
			t.Errorf("every device needs its own key: %+v", d)
		}
		keys[d.Key] = true
	}
	progs := b.Programs()
	if len(progs) != len(catalog.All) {
		t.Errorf("every program in the catalog is listed: %d of %d", len(progs), len(catalog.All))
	}
	for _, p := range progs {
		if p.Name == "hamlib" && !p.CanSource || p.Name == "pat" && p.CanSource || p.Name == "fldigi" && !p.CanSource {
			t.Errorf("hamlib, direwolf and programs with a recipe can be built from source; pat and mercury cannot: %+v", p)
		}
	}
	if _, err := b.AssignDevice("serial|/dev/nonexistent", "x"); err == nil {
		t.Error("assigning to a rig that does not exist")
	}
}
