package patcfg

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `{
  "mycall": "N0CALL",
  "secure_login_password": "hunter2",
  "locator": "EM10",
  "ardop": {"addr": "", "rig": "", "ptt_ctrl": false, "connect_requests": 10},
  "varahf": {"addr": "localhost:8310", "rig": "", "ptt_ctrl": false},
  "telnet": {"password": "also-secret"}
}
`

func writeSample(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "pat.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func byPath(cs []Change) map[string]Change {
	m := map[string]Change{}
	for _, c := range cs {
		m[c.Path] = c
	}
	return m
}

func TestPlanNamesEachMemberAndWritesNothing(t *testing.T) {
	p := writeSample(t, sample)
	before, _ := os.ReadFile(p)
	changes, err := Plan(p, Wiring{Grid: "EM10mn", Rigs: []RigRef{{"ftdx10", 4533}}, ARDOPRig: "ftdx10"})
	if err != nil {
		t.Fatal(err)
	}
	m := byPath(changes)
	for path, want := range map[string]string{"locator": `"EM10mn"`, "hamlib_rigs.ftdx10.address": `"localhost:4533"`, "hamlib_rigs.ftdx10.network": `"tcp"`, "ardop.rig": `"ftdx10"`} {
		if m[path].New != want {
			t.Errorf("%s: %+v, want new %s (all: %+v)", path, m[path], want, changes)
		}
	}
	if c := m["ardop.rig"]; !c.WasSet || c.Old != `""` {
		t.Errorf("ardop.rig was set to an empty string: %+v", c)
	}
	if c := m["hamlib_rigs.ftdx10.address"]; c.WasSet {
		t.Errorf("a rig that was not there was not set: %+v", c)
	}
	if after, _ := os.ReadFile(p); string(after) != string(before) {
		t.Error("a plan must change nothing")
	}
	if exists(p + ".hamstack-bak") {
		t.Error("a plan makes no backup")
	}
}

func TestArdopRigIsFilledOnlyWhenEmptyAndNothingElseOfArdopIsTouched(t *testing.T) {
	p := writeSample(t, strings.Replace(sample, `"rig": "", "ptt_ctrl": false, "connect_requests"`, `"rig": "kx3", "ptt_ctrl": false, "connect_requests"`, 1))
	changes, _ := Plan(p, Wiring{ARDOPRig: "ftdx10"})
	if len(changes) != 0 {
		t.Errorf("a rig that is set is the person's choice: %+v", changes)
	}
	p = writeSample(t, sample)
	changes, _ = Plan(p, Wiring{ARDOPRig: "ftdx10"})
	for _, c := range changes {
		if strings.HasPrefix(c.Path, "ardop.") && c.Path != "ardop.rig" {
			t.Errorf("only ardop.rig, not %s: who keys the radio and where the TNC listens are the person's to say", c.Path)
		}
	}
	// No ardop section at all is filled in too.
	p = writeSample(t, `{"mycall":"N0CALL"}`)
	changes, _ = Plan(p, Wiring{ARDOPRig: "ftdx10"})
	if len(changes) != 1 || changes[0].Path != "ardop.rig" || changes[0].WasSet {
		t.Errorf("%+v", changes)
	}
}

func TestApplyKeepsTheFirstBackupAndRevertPutsBackOnlyWhatIsStillTheirs(t *testing.T) {
	p := writeSample(t, sample)
	w := Wiring{Grid: "EM10mn", Rigs: []RigRef{{"ftdx10", 4533}}, ARDOPRig: "ftdx10"}
	bak, changes, err := Apply(p, w)
	if err != nil || len(changes) == 0 {
		t.Fatalf("%v %+v", err, changes)
	}
	if b, _ := os.ReadFile(bak); string(b) != sample {
		t.Errorf("the backup must be the file as it was:\n%s", b)
	}
	// Something changes the file later (Pat's own settings page, say), and hamstack changes it again.
	if err := SetField(p, "mycall", "W1AW"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Apply(p, Wiring{Grid: "DN17aa"}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(bak); string(b) != sample {
		t.Error("the first backup must not be replaced by a later one")
	}

	// The person changed ardop.rig since; the locator is still what Apply set.
	if err := SetField(p, "ardop.rig", "kx3"); err != nil {
		t.Fatal(err)
	}
	n, err := Revert(p, changes)
	if err != nil {
		t.Fatal(err)
	}
	var c map[string]any
	b, _ := os.ReadFile(p)
	if json.Unmarshal(b, &c) != nil {
		t.Fatalf("not JSON: %s", b)
	}
	ard := c["ardop"].(map[string]any)
	if ard["rig"] != "kx3" {
		t.Errorf("a setting the person changed since must be left: %v", ard)
	}
	if c["locator"] == "EM10mn" || c["locator"] == "EM10" && n == 0 {
		t.Logf("locator %v (it was changed again by the second Apply, so it is theirs)", c["locator"])
	}
	if _, there := c["hamlib_rigs"]; there {
		t.Errorf("rigs hamstack added go again, and the empty object with them: %v", c["hamlib_rigs"])
	}
	if c["mycall"] != "W1AW" || c["secure_login_password"] != "hunter2" || c["telnet"].(map[string]any)["password"] != "also-secret" {
		t.Errorf("everything else, passwords included, stays: %v", c)
	}
	if n < 1 {
		t.Errorf("reverted %d", n)
	}
}

func TestRevertToTheLetter(t *testing.T) {
	p := writeSample(t, sample)
	_, changes, _ := Apply(p, Wiring{Rigs: []RigRef{{"ftdx10", 4533}}, ARDOPRig: "ftdx10"})
	if _, err := Revert(p, changes); err != nil {
		t.Fatal(err)
	}
	var got, want map[string]any
	b, _ := os.ReadFile(p)
	json.Unmarshal(b, &got)
	json.Unmarshal([]byte(sample), &want)
	g, _ := json.Marshal(got)
	w, _ := json.Marshal(want)
	if string(g) != string(w) {
		t.Errorf("after apply and revert the settings are as they were:\n got %s\nwant %s", g, w)
	}
}

func TestFieldsReturnOnlyWhatWasAskedFor(t *testing.T) {
	p := writeSample(t, sample)
	got, err := Fields(p, []string{"ardop.rig", "ardop.ptt_ctrl", "varahf.addr", "nothing.here", "ardop.connect_requests"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"ardop.rig": "", "ardop.ptt_ctrl": "false", "varahf.addr": "localhost:8310", "nothing.here": "", "ardop.connect_requests": "10"}
	if len(got) != len(want) {
		t.Errorf("%v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	for _, v := range got {
		if strings.Contains(v, "hunter2") || strings.Contains(v, "also-secret") {
			t.Error("a password must never come back")
		}
	}
	if err := SetField(p, "ardop.ptt_ctrl", true); err != nil {
		t.Fatal(err)
	}
	if err := SetField(p, "ardop.addr", "localhost:8515"); err != nil {
		t.Fatal(err)
	}
	got, _ = Fields(p, []string{"ardop.ptt_ctrl", "ardop.addr", "ardop.connect_requests"})
	if got["ardop.ptt_ctrl"] != "true" || got["ardop.addr"] != "localhost:8515" || got["ardop.connect_requests"] != "10" {
		t.Errorf("%v", got)
	}
	if err := SetField(p, "new.deep.value", "x"); err != nil {
		t.Fatal(err)
	}
	if got, _ := Fields(p, []string{"new.deep.value"}); got["new.deep.value"] != "x" {
		t.Errorf("%v", got)
	}
	if err := SetField(p, "mycall.sub", "x"); err == nil {
		t.Error("a setting that is not an object cannot have members")
	}
}

func TestSetLocatorKeepsTheFirstBackupToo(t *testing.T) {
	p := writeSample(t, sample)
	if _, err := SetLocator(p, "EM10mn"); err != nil {
		t.Fatal(err)
	}
	if _, err := SetLocator(p, "DN17aa"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p + ".hamstack-bak"); string(b) != sample {
		t.Errorf("the backup is the original, not the file before the last change:\n%s", b)
	}
}

func TestArdopcfPointsPatAtOurModemAndKeysThroughRigctld(t *testing.T) {
	p := writeSample(t, sample)
	changes, _ := Plan(p, Wiring{ARDOPRig: "ftdx10", ARDOPPort: 8300})
	by := byPath(changes)
	if by["ardop.addr"].New != `"localhost:8300"` || by["ardop.ptt_ctrl"].New != "true" || by["ardop.rig"].New != `"ftdx10"` {
		t.Fatalf("%+v", changes)
	}
	// An address the person chose (another computer, another program) is left alone, keying included.
	own := strings.Replace(sample, `"ardop": {"addr": ""`, `"ardop": {"addr": "other:8515"`, 1)
	changes, _ = Plan(writeSample(t, own), Wiring{ARDOPRig: "ftdx10", ARDOPPort: 8300})
	if c := byPath(changes); c["ardop.addr"].Path != "" || c["ardop.ptt_ctrl"].Path != "" {
		t.Fatalf("their address was touched: %+v", changes)
	}
	// And the VARA connection is not pointed at it.
	if _, ok := byPath(changes)["varahf.addr"]; ok {
		t.Fatal("varahf must not be wired by an ARDOP-only setup")
	}
}
