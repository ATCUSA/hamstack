package profile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetect(t *testing.T) {
	all, err := All("")
	if err != nil {
		t.Fatal(err)
	}
	glob := func(pattern string) ([]string, error) {
		if pattern == "/dev/serial/by-id/usb-Icom_Inc._IC-7300MK2_*-if00" {
			return []string{"/dev/serial/by-id/usb-Icom_Inc._IC-7300MK2_IC-7300MK2_1234-if00"}, nil
		}
		return nil, nil
	}
	got := Detect(all, glob)
	if len(got) != 1 || got[0].ID != "ic7300mk2" {
		t.Errorf("detected %+v, want only ic7300mk2 (the dummy profile has no device and must never match)", got)
	}
}

func TestGuess(t *testing.T) {
	all, _ := All("")
	if g := Guess(all, "0C26", "0052"); len(g) != 1 || g[0].ID != "ic7300mk2" {
		t.Errorf("0c26:0052 is the IC-7300 MK2 (case must not matter): %+v", g)
	}
	if g := Guess(all, "dead", "beef"); len(g) != 0 {
		t.Errorf("unknown ids must not guess: %+v", g)
	}
}

func TestDigirigAndPacketPTT(t *testing.T) {
	all, err := All("")
	if err != nil {
		t.Fatal(err)
	}
	var dig, icom, dummy *Profile
	for i := range all {
		switch all[i].ID {
		case "digirig":
			dig = &all[i]
		case "ic7300mk2":
			icom = &all[i]
		case "dummy":
			dummy = &all[i]
		}
	}
	if dig == nil || icom == nil || dummy == nil {
		t.Fatal("built-in profiles missing")
	}
	if dig.HasCAT() || dig.PacketPTT() != "rts" {
		t.Errorf("a Digirig has no CAT and is keyed by RTS: %+v", dig.Packet)
	}
	if !icom.HasCAT() || icom.PacketPTT() != "rig" {
		t.Errorf("a CAT radio is keyed through rigctld by default: %q", icom.PacketPTT())
	}
	if !dummy.HasCAT() { // hamlib's dummy rig is model 1
		t.Error("the dummy rig is a hamlib model")
	}
	var bare Profile
	if bare.PacketPTT() != "none" {
		t.Errorf("no CAT and no setting means VOX: %q", bare.PacketPTT())
	}
	if g := Guess(all, "10c4", "ea60"); len(g) != 1 || g[0].ID != "digirig" {
		t.Errorf("guess for the CP2102N chip: %+v", g)
	}
}

// Every service start reads the profiles. One broken file in the user's folder used to make All fail, which
// stopped every radio, including ones that use a built-in profile.
func TestABrokenUserProfileDoesNotBreakTheOthers(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("mine.json", `{"id":"mine","name":"My radio","rig":{"model":3073,"baud":19200}}`)
	write("broken.json", `{"id":"broken", "name": `)
	write("noid.json", `{"name":"No id"}`)
	write("dummy.json", `{"id":"dummy","name":"My own dummy","rig":{"model":1}}`) // a user file wins over the built-in

	all, err := All(dir)
	if err != nil {
		t.Fatalf("a broken user file must not be fatal: %v", err)
	}
	names := map[string]string{}
	for _, p := range all {
		names[p.ID] = p.Name
	}
	if names["mine"] != "My radio" || names["dummy"] != "My own dummy" || names["ic7300mk2"] == "" || names["broken"] != "" {
		t.Errorf("profiles: %v", names)
	}
	if p, err := Find(dir, "ic7300mk2"); err != nil || p.ID != "ic7300mk2" {
		t.Errorf("a built-in profile must still be found: %v", err)
	}
	bad := Problems(dir)
	if len(bad) != 2 || !strings.Contains(bad[0].Error(), "broken.json") || !strings.Contains(bad[1].Error(), "id is required") {
		t.Errorf("problems: %v", bad)
	}
	if _, err := Find(dir, "broken"); err == nil || !strings.Contains(err.Error(), "broken.json") {
		t.Errorf("looking for the broken one should say why it is missing: %v", err)
	}
	if len(Problems("")) != 0 || len(Problems(t.TempDir())) != 0 {
		t.Error("no user files, no problems")
	}
}
