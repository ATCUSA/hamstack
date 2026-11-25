package patcfg

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

const homeConfig = `{
  "mycall": "N0CALL",
  "secure_login_password": "hunter2-do-not-touch",
  "locator": "EM10dg",
  "http_addr": "localhost:8080",
  "hamlib_rigs": {"hf": {"network": "tcp", "address": "localhost:4532"}, "mine": {"network": "tcp", "address": "localhost:9999"}},
  "varahf": {"addr": "localhost:8300", "bandwidth": 500, "rig": "hf", "ptt_ctrl": true, "custom_flag": 7},
  "listen": ["telnet"]
}`

func read(t *testing.T, p string) map[string]any {
	t.Helper()
	b, _ := os.ReadFile(p)
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, b)
	}
	return m
}

func TestWireMovesPatToTheRigsAtThisSite(t *testing.T) {
	p := filepath.Join(t.TempDir(), "pat.json")
	os.WriteFile(p, []byte(homeConfig), 0o600)
	// At the EOC: different grid, an HF-less VHF radio with its own rigctld and Direwolf, Mercury on the second rig.
	backup, changed, err := Wire(p, Wiring{
		Grid: "FN31pr", Rigs: []RigRef{{"eoc", 4533}}, HFRig: "eoc", HFArqPort: 8310,
		Packet: true, PacketRig: "eoc", AGWPort: 8004,
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"agwpe", "ax25", "hamlib_rigs", "locator", "varahf"}; !reflect.DeepEqual(changed, want) {
		t.Errorf("changed = %v, want %v", changed, want)
	}
	if b, _ := os.ReadFile(backup); string(b) != homeConfig {
		t.Error("the backup must be the original, byte for byte")
	}
	m := read(t, p)
	if m["locator"] != "FN31pr" || m["secure_login_password"] != "hunter2-do-not-touch" || m["mycall"] != "N0CALL" {
		t.Errorf("locator changed, everything else kept: %v", m)
	}
	rigs := m["hamlib_rigs"].(map[string]any)
	if rigs["eoc"].(map[string]any)["address"] != "localhost:4533" || rigs["mine"] == nil || rigs["hf"] == nil {
		t.Errorf("our rigs are set and the user's own entries are kept: %v", rigs)
	}
	vh := m["varahf"].(map[string]any)
	if vh["addr"] != "localhost:8310" || vh["rig"] != "eoc" || vh["bandwidth"] != float64(500) || vh["custom_flag"] != float64(7) {
		t.Errorf("varahf: the address and rig move, the user's bandwidth and other members stay: %v", vh)
	}
	if m["ax25"].(map[string]any)["engine"] != "agwpe" || m["agwpe"].(map[string]any)["addr"] != "localhost:8004" {
		t.Errorf("packet: %v %v", m["ax25"], m["agwpe"])
	}
	if !reflect.DeepEqual(m["listen"], []any{"telnet"}) {
		t.Errorf("unrelated settings must be untouched: %v", m["listen"])
	}
}

func TestWireWritesNothingWhenNothingDiffers(t *testing.T) {
	p := filepath.Join(t.TempDir(), "pat.json")
	os.WriteFile(p, []byte(homeConfig), 0o600)
	backup, changed, err := Wire(p, Wiring{Grid: "EM10dg", Rigs: []RigRef{{"hf", 4532}}, HFRig: "hf", HFArqPort: 8300})
	if err != nil || backup != "" || len(changed) != 0 {
		t.Errorf("already correct: backup=%q changed=%v err=%v", backup, changed, err)
	}
	if b, _ := os.ReadFile(p); string(b) != homeConfig {
		t.Error("the file must not even be rewritten")
	}
	if _, err := os.Stat(p + ".hamstack-bak"); err == nil {
		t.Error("no backup when nothing changed")
	}
}

func TestWireWithNoPacketRigRemovesTheStaleRig(t *testing.T) {
	p := filepath.Join(t.TempDir(), "pat.json")
	os.WriteFile(p, []byte(`{"ax25":{"engine":"agwpe","rig":"gone"},"agwpe":{"addr":"localhost:8000","radio_port":2}}`), 0o600)
	if _, _, err := Wire(p, Wiring{Packet: true, PacketRig: "", AGWPort: 8002}); err != nil {
		t.Fatal(err)
	}
	m := read(t, p)
	if _, has := m["ax25"].(map[string]any)["rig"]; has {
		t.Errorf("a Digirig has no CAT: Pat must not be told to tune a rig that is gone: %v", m["ax25"])
	}
	if a := m["agwpe"].(map[string]any); a["addr"] != "localhost:8002" || a["radio_port"] != float64(2) {
		t.Errorf("the address moves; the user's radio port stays: %v", a)
	}
}

func TestWireRefusesBadFiles(t *testing.T) {
	d := t.TempDir()
	bad := filepath.Join(d, "bad.json")
	os.WriteFile(bad, []byte(`{"varahf": "not an object"}`), 0o600)
	if _, _, err := Wire(bad, Wiring{HFRig: "hf", HFArqPort: 8300}); err == nil {
		t.Error("a varahf that is not an object must be refused, not overwritten")
	}
	if b, _ := os.ReadFile(bad); string(b) != `{"varahf": "not an object"}` {
		t.Error("a refused file must be left alone")
	}
	if _, _, err := Wire(filepath.Join(d, "missing.json"), Wiring{Grid: "FN31"}); err == nil {
		t.Error("a missing file is an error")
	}
}

func TestWireSetsOnlyGPSDAddress(t *testing.T) {
	p := filepath.Join(t.TempDir(), "pat.json")
	orig := `{"mycall":"N0CALL","secure_login_password":"hunter2","gpsd":{"addr":"localhost:2947","enable_http":true,"use_server_time":true}}`
	if err := os.WriteFile(p, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, changed, err := Wire(p, Wiring{}); err != nil || len(changed) != 0 {
		t.Fatalf("no GPS: changed %v, %v", changed, err)
	}
	_, changed, err := Wire(p, Wiring{GPSAddr: "localhost:2948"})
	if err != nil || len(changed) != 1 || changed[0] != "gpsd" {
		t.Fatalf("changed = %v, %v", changed, err)
	}
	b, _ := os.ReadFile(p)
	var got struct {
		Pass string `json:"secure_login_password"`
		GPSD struct {
			Addr   string `json:"addr"`
			HTTP   bool   `json:"enable_http"`
			Server bool   `json:"use_server_time"`
		} `json:"gpsd"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.GPSD.Addr != "localhost:2948" || !got.GPSD.HTTP || !got.GPSD.Server || got.Pass != "hunter2" {
		t.Errorf("got %+v: only the address may change", got)
	}
	if _, changed, _ := Wire(p, Wiring{GPSAddr: "localhost:2948"}); len(changed) != 0 {
		t.Errorf("a second sync changed %v", changed)
	}
}
