package patcfg

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMinimal(t *testing.T) {
	b, err := Minimal(Options{Callsign: "N0CALL", HTTPAddr: "localhost:8080", Grid: "FN31pr", Rigs: []RigRef{{"hf", 4532}, {"vhf", 4533}}, HFRig: "hf", HFArqPort: 8300})
	if err != nil {
		t.Fatal(err)
	}
	var c map[string]any
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	if c["mycall"] != "N0CALL" || c["http_addr"] != "localhost:8080" {
		t.Errorf("bad config: %s", b)
	}
	if c["locator"] != "FN31pr" {
		t.Errorf("the saved grid square belongs in Pat's locator: %s", b)
	}
	if _, has := c["secure_login_password"]; has {
		t.Error("must not set a password")
	}
	v := c["varahf"].(map[string]any)
	if v["addr"] != "localhost:8300" || v["rig"] != "hf" {
		t.Errorf("varahf: %v", v)
	}
	rigs := c["hamlib_rigs"].(map[string]any)
	if len(rigs) != 2 || rigs["vhf"].(map[string]any)["address"] != "localhost:4533" {
		t.Errorf("every rig must be available to Pat by name: %v", rigs)
	}
}

func TestMinimalWithoutModem(t *testing.T) {
	b, err := Minimal(Options{Callsign: "N0CALL", HTTPAddr: "localhost:8080"})
	if err != nil {
		t.Fatal(err)
	}
	var c map[string]any
	json.Unmarshal(b, &c)
	if _, has := c["varahf"]; has {
		t.Error("no modem selected: must not point Pat at Mercury")
	}
	if _, has := c["hamlib_rigs"]; has {
		t.Error("no modem selected: must not configure rig control")
	}
	if c["mycall"] != "N0CALL" {
		t.Errorf("mycall missing: %s", b)
	}
	if _, has := c["locator"]; has {
		t.Error("no grid known: must not invent a locator")
	}
}

func TestMinimalPacket(t *testing.T) {
	b, err := Minimal(Options{Callsign: "N0CALL", HTTPAddr: "localhost:8080", Packet: true, AGWPort: 8000})
	if err != nil {
		t.Fatal(err)
	}
	var c map[string]map[string]any
	var flat map[string]any
	json.Unmarshal(b, &flat)
	json.Unmarshal(b, &c)
	if c["ax25"]["engine"] != "agwpe" || c["agwpe"]["addr"] != "localhost:8000" {
		t.Errorf("packet config: %s", b)
	}
}

func TestHFPort(t *testing.T) {
	d := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(d, name)
		os.WriteFile(p, []byte(body), 0o600)
		return p
	}
	if n, ok := HFPort(write("a.json", `{"varahf":{"addr":"localhost:8310","rig":"vhf"}}`)); !ok || n != 8310 {
		t.Errorf("got %d %v", n, ok)
	}
	for name, body := range map[string]string{"none.json": `{"mycall":"N0CALL"}`, "empty.json": `{"varahf":{"addr":""}}`, "bad.json": `{`, "noport.json": `{"varahf":{"addr":"localhost"}}`} {
		if n, ok := HFPort(write(name, body)); ok {
			t.Errorf("%s: should have no HF modem, got %d", name, n)
		}
	}
	if _, ok := HFPort(filepath.Join(d, "missing.json")); ok {
		t.Error("a missing file has no HF modem")
	}
}

func TestScan(t *testing.T) {
	d := t.TempDir()
	w := func(name, body string) { os.WriteFile(filepath.Join(d, name), []byte(body), 0o600) }
	w("club.json", `{"mycall":"k0club","http_addr":"localhost:8082","locator":"FN31pr","secure_login_password":"SECRET"}`)
	w("me.json", `{"mycall":"N0CALL","http_addr":"localhost:8080"}`)
	w("notpat.json", `{"hello":"world"}`)
	w("broken.json", `{`)
	if err := os.Symlink(filepath.Join(d, "me.json"), filepath.Join(d, "config.json")); err != nil {
		t.Fatal(err)
	}
	got, err := Scan(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 stations, got %+v", got)
	}
	if !got[1].Default || got[0].Default {
		t.Errorf("me.json should be the default (config.json links to it): %+v", got)
	}
	if got[0].Grid != "FN31pr" || got[1].Grid != "" {
		t.Errorf("locator should be read as a grid suggestion: %+v", got)
	}
	if got[0].ID != "club" || got[0].Callsign != "K0CLUB" || got[1].ID != "me" {
		t.Errorf("unexpected: %+v", got)
	}
}

func TestPacketConfigAndPort(t *testing.T) {
	b, err := Minimal(Options{Callsign: "N0CALL", HTTPAddr: "localhost:8080", Rigs: []RigRef{{"vhf", 4533}}, Packet: true, PacketRig: "vhf", AGWPort: 8002})
	if err != nil {
		t.Fatal(err)
	}
	var c map[string]map[string]any
	json.Unmarshal(b, &c)
	if c["ax25"]["engine"] != "agwpe" || c["ax25"]["rig"] != "vhf" || c["agwpe"]["addr"] != "localhost:8002" {
		t.Errorf("packet config: %s", b)
	}
	p := filepath.Join(t.TempDir(), "pat.json")
	os.WriteFile(p, b, 0o600)
	if n, ok := PacketPort(p); !ok || n != 8002 {
		t.Errorf("PacketPort = %d %v", n, ok)
	}
	// A Digirig has no CAT, so there is no rig for Pat to tune.
	b, _ = Minimal(Options{Callsign: "N0CALL", HTTPAddr: "localhost:8080", Packet: true, AGWPort: 8000})
	json.Unmarshal(b, &c)
	if _, has := c["ax25"]["rig"]; has {
		t.Errorf("no rig to tune: %s", b)
	}
	for name, body := range map[string]string{"tcp.json": `{"ax25":{"engine":"linux"},"agwpe":{"addr":"localhost:8000"}}`, "none.json": `{}`} {
		q := filepath.Join(t.TempDir(), name)
		os.WriteFile(q, []byte(body), 0o600)
		if n, ok := PacketPort(q); ok {
			t.Errorf("%s: only the agwpe engine counts, got %d", name, n)
		}
	}
}

func TestSetLocatorChangesOnlyTheLocator(t *testing.T) {
	p := filepath.Join(t.TempDir(), "pat.json")
	orig := `{
  "mycall": "N0CALL",
  "secure_login_password": "hunter2-do-not-touch",
  "locator": "EM10dg",
  "http_addr": "localhost:8080",
  "ax25": {"engine": "agwpe", "beacon": {"every": 3600, "message": "hi \"there\""}},
  "listen": ["telnet"],
  "big": 12345678901234,
  "unicode": "café"
}`
	os.WriteFile(p, []byte(orig), 0o600)
	backup, err := SetLocator(p, "FN31pr")
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(backup); string(b) != orig {
		t.Error("the backup must be the original file, byte for byte")
	}
	var before, after map[string]json.RawMessage
	json.Unmarshal([]byte(orig), &before)
	b, _ := os.ReadFile(p)
	if err := json.Unmarshal(b, &after); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
	if string(after["locator"]) != `"FN31pr"` {
		t.Errorf("locator = %s", after["locator"])
	}
	for k, v := range before {
		if k == "locator" {
			continue
		}
		var a, c any
		json.Unmarshal(v, &a)
		json.Unmarshal(after[k], &c)
		if string(v) != string(after[k]) && !jsonEqual(a, c) {
			t.Errorf("%s changed: %s -> %s", k, v, after[k])
		}
	}
	if string(after["secure_login_password"]) != `"hunter2-do-not-touch"` || string(after["big"]) != "12345678901234" {
		t.Errorf("secrets and large numbers must survive untouched: %s %s", after["secure_login_password"], after["big"])
	}
	if len(after) != len(before) {
		t.Errorf("no key may appear or disappear: %d -> %d", len(before), len(after))
	}
	if loc, ok := Locator(p); !ok || loc != "FN31pr" {
		t.Errorf("Locator = %q %v", loc, ok)
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
		t.Errorf("a config with a password must stay private: %v", st.Mode())
	}
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func TestSetLocatorRefusesBadFiles(t *testing.T) {
	d := t.TempDir()
	bad := filepath.Join(d, "bad.json")
	os.WriteFile(bad, []byte("{not json"), 0o600)
	if _, err := SetLocator(bad, "FN31"); err == nil {
		t.Error("invalid JSON must be refused")
	}
	if b, _ := os.ReadFile(bad); string(b) != "{not json" {
		t.Error("a refused file must be left alone")
	}
	if _, err := os.Stat(bad + ".hamstack-bak"); err == nil {
		t.Error("no backup of a file that was not changed")
	}
	if _, err := SetLocator(filepath.Join(d, "missing.json"), "FN31"); err == nil {
		t.Error("a missing file is an error")
	}
	if _, ok := Locator(filepath.Join(d, "missing.json")); ok {
		t.Error("no locator in a missing file")
	}
}
