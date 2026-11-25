package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ATCUSA/hamstack/internal/apps"
	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/ledger"
)

// Most tests assume the rig's rigctld is answering; the ones about choosing a rig say otherwise.
func init() { seedRigUp = func(int) bool { return true } }

func rigsUp(t *testing.T, up ...int) {
	t.Helper()
	old := seedRigUp
	t.Cleanup(func() { seedRigUp = old })
	seedRigUp = func(port int) bool {
		for _, p := range up {
			if p == port {
				return true
			}
		}
		return false
	}
}

func seedConfig(call, grid string) config.Config {
	return config.Config{
		Active: "me", Grid: grid,
		Stations: []config.Station{{ID: "me", Callsign: call}},
		Rigs:     []config.Rig{{ID: "ftdx10", Name: "FT-DX10", Profile: "ftdx10", RigPort: 4533}},
	}
}

func appNamed(t *testing.T, name string) apps.App {
	a, ok := apps.Find(name)
	if !ok {
		t.Fatalf("no app %s", name)
	}
	return a
}

func TestSeedWSJTXWithNoConfigFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	note, err := seedRadioApp(appNamed(t, "wsjtx"), seedConfig("N0CALL", "EM10"))
	if err != nil || !strings.Contains(note, "FT-DX10") || !strings.Contains(note, "127.0.0.1:4533") {
		t.Fatalf("%q %v", note, err)
	}
	b, _ := os.ReadFile(filepath.Join(home, ".config", "WSJT-X.ini"))
	for _, want := range []string{"[Configuration]", "Rig=Hamlib NET rigctl", "CATNetworkPort=127.0.0.1:4533", `PTT_method_CAT\0)`, "MyCall=N0CALL", "MyGrid=EM10"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("settings lack %q:\n%s", want, b)
		}
	}
	// The same bytes WSJT-X itself writes for a variant (see the live file): length byte and all.
	if !strings.Contains(string(b), `PTTMethod=@Variant(\0\0\0\x7f\0\0\0\x1eTransceiverFactory::PTTMethod\0\0\0\0\xfPTT_method_CAT\0)`) {
		t.Errorf("the PTT variant is not in Qt's form:\n%s", b)
	}
}

func TestSeedKeepsEverythingElseInAnExistingFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".config", "WSJT-X.ini")
	os.MkdirAll(filepath.Dir(path), 0o755)
	orig := "[General]\nMyVersion=2.8\n\n[Configuration]\nCATNetworkPort=\nCATSerialPort=\nMyCall=\nMyGrid=\nPTTMethod=@Variant(\\0\\0\\0\\x7f\\0\\0\\0\\x1eTransceiverFactory::PTTMethod\\0\\0\\0\\0\\xfPTT_method_VOX\\0)\nRig=None\nSoundInName=default\nUDPServer=127.0.0.1\n\n[WideGraph]\nBinsPerPixel=2\n"
	os.WriteFile(path, []byte(orig), 0o640)
	if note, err := seedRadioApp(appNamed(t, "wsjtx"), seedConfig("N0CALL", "EM10")); err != nil || note == "" {
		t.Fatalf("%q %v", note, err)
	}
	b, _ := os.ReadFile(path)
	got := string(b)
	for _, want := range []string{"[General]\nMyVersion=2.8\n", "SoundInName=default\n", "UDPServer=127.0.0.1\n", "[WideGraph]\nBinsPerPixel=2\n",
		"Rig=Hamlib NET rigctl\n", "CATNetworkPort=127.0.0.1:4533\n", "MyCall=N0CALL\n", "MyGrid=EM10\n", "CATSerialPort=\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "PTT_method_VOX") || strings.Count(got, "Rig=") != 1 || strings.Count(got, "[Configuration]") != 1 {
		t.Errorf("keys must be replaced, not repeated:\n%s", got)
	}
	if bak, _ := os.ReadFile(path + ".hamstack-bak"); string(bak) != orig {
		t.Errorf("the file as it was must be kept:\n%s", bak)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o640 {
		t.Errorf("mode %v", fi.Mode().Perm())
	}
	// Starting it again changes nothing: it has a radio now.
	if note, err := seedRadioApp(appNamed(t, "wsjtx"), seedConfig("N0CALL", "EM10")); note != "" || err != nil {
		t.Errorf("second run: %q %v", note, err)
	}
	// And the backup is still the original, not the first run's result.
	if bak, _ := os.ReadFile(path + ".hamstack-bak"); string(bak) != orig {
		t.Error("the backup was overwritten")
	}
}

func TestSeedNeverOverridesWhatSomeoneChose(t *testing.T) {
	cases := map[string]string{
		"a radio chosen":    "[Configuration]\nRig=Icom IC-7300\nCATSerialPort=/dev/ttyUSB0\nMyCall=K1ABC\n",
		"a network rig":     "[Configuration]\nRig=Hamlib NET rigctl\nCATNetworkPort=192.168.1.9:4532\n",
		"serial but no rig": "[Configuration]\nRig=None\nCATSerialPort=/dev/ttyUSB0\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			path := filepath.Join(home, ".config", "JS8Call.ini")
			os.MkdirAll(filepath.Dir(path), 0o755)
			os.WriteFile(path, []byte(content), 0o644)
			if note, err := seedRadioApp(appNamed(t, "js8call"), seedConfig("N0CALL", "EM10")); note != "" || err != nil {
				t.Errorf("%q %v", note, err)
			}
			if b, _ := os.ReadFile(path); string(b) != content {
				t.Errorf("the file changed:\n%s", b)
			}
			if fileExists(path + ".hamstack-bak") {
				t.Error("no backup is needed when nothing is written")
			}
		})
	}
}

func TestSeedKeepsAnOwnCallsign(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".config", "WSJT-X.ini")
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte("[Configuration]\nMyCall=W1AW\nMyGrid=FN31pr\nRig=None\n"), 0o644)
	if _, err := seedRadioApp(appNamed(t, "wsjtx"), seedConfig("N0CALL", "EM10")); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "MyCall=W1AW") || !strings.Contains(string(b), "MyGrid=FN31pr") || strings.Contains(string(b), "N0CALL") {
		t.Errorf("a callsign someone typed in is theirs:\n%s", b)
	}
}

func TestSeedFldigiOnlyWhenItHasNoSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".fldigi", "fldigi_def.xml")
	note, err := seedRadioApp(appNamed(t, "fldigi"), seedConfig("N0CALL", "EM10"))
	if err != nil || note == "" {
		t.Fatalf("%q %v", note, err)
	}
	b, _ := os.ReadFile(path)
	for _, want := range []string{"<FLDIGI_DEFS>", "<MYCALL>N0CALL</MYCALL>", "<MYLOC>EM10</MYLOC>", "<CHKUSEHAMLIBIS>1</CHKUSEHAMLIBIS>", "<HAMRIGMODEL>2</HAMRIGMODEL>", "<HAMRIGDEVICE>127.0.0.1:4533</HAMRIGDEVICE>", "</FLDIGI_DEFS>"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("lacks %q:\n%s", want, b)
		}
	}
	os.WriteFile(path, []byte("<FLDIGI_DEFS>\n<MYCALL>W1AW</MYCALL>\n</FLDIGI_DEFS>\n"), 0o644)
	if note, err := seedRadioApp(appNamed(t, "fldigi"), seedConfig("N0CALL", "EM10")); note != "" || err != nil {
		t.Errorf("an existing file must be left alone: %q %v", note, err)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), "W1AW") || strings.Contains(string(b), "HAMRIG") {
		t.Errorf("the file changed:\n%s", b)
	}
}

func TestSeedOnlyForRigsWithControl(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := seedConfig("N0CALL", "EM10")
	cfg.Rigs[0].NoCAT = true
	if note, err := seedRadioApp(appNamed(t, "wsjtx"), cfg); note != "" || err != nil {
		t.Errorf("%q %v", note, err)
	}
	cfg.Rigs[0].NoCAT, cfg.Rigs[0].Off = false, true
	if note, err := seedRadioApp(appNamed(t, "wsjtx"), cfg); note != "" || err != nil {
		t.Errorf("a rig that is switched off has no rigctld: %q %v", note, err)
	}
	if fileExists(filepath.Join(home, ".config", "WSJT-X.ini")) {
		t.Error("nothing should have been written")
	}
	// Programs with no radio settings of their own are not touched.
	cfg.Rigs[0].Off = false
	if note, err := seedRadioApp(appNamed(t, "flmsg"), cfg); note != "" || err != nil {
		t.Errorf("%q %v", note, err)
	}
}

// radio has two rigs and one rigctld has failed. A program given a dead port keeps it, so the rig must be one
// that answers, and with none answering nothing may be written.
func TestSeedChoosesARigThatAnswers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := seedConfig("N0CALL", "EM10")
	cfg.Rigs = []config.Rig{{ID: "ic7300mk2", Profile: "ic7300mk2", RigPort: 4532}, {ID: "ftdx10", Name: "FT-DX10", Profile: "ftdx10", RigPort: 4533}}
	rigsUp(t, 4533)
	note, err := seedRadioApp(appNamed(t, "wsjtx"), cfg)
	if err != nil || !strings.Contains(note, "FT-DX10") {
		t.Fatalf("%q %v", note, err)
	}
	if b, _ := os.ReadFile(filepath.Join(home, ".config", "WSJT-X.ini")); !strings.Contains(string(b), "CATNetworkPort=127.0.0.1:4533") || strings.Contains(string(b), "4532") {
		t.Errorf("wrong rig:\n%s", b)
	}
	home2 := t.TempDir()
	t.Setenv("HOME", home2)
	rigsUp(t)
	if note, err := seedRadioApp(appNamed(t, "wsjtx"), cfg); note != "" || err != nil || fileExists(filepath.Join(home2, ".config", "WSJT-X.ini")) {
		t.Errorf("with no rigctld answering nothing is written: %q %v", note, err)
	}
}

func readLedger(t *testing.T, home string) []ledger.Change {
	t.Helper()
	c, err := ledger.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestSeedingIsRecordedAndCanBeUndone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".config", "WSJT-X.ini")
	os.MkdirAll(filepath.Dir(path), 0o755)
	orig := "[Configuration]\nRig=None\nPTTMethod=@Variant(\\0\\0\\0\\x7f\\0\\0\\0\\x1eTransceiverFactory::PTTMethod\\0\\0\\0\\0\\xfPTT_method_VOX\\0)\nSoundInName=default\n"
	os.WriteFile(path, []byte(orig), 0o644)
	if _, err := seedRadioApp(appNamed(t, "wsjtx"), seedConfig("N0CALL", "EM10")); err != nil {
		t.Fatal(err)
	}
	ch := readLedger(t, home)
	if len(ch) != 1 || ch[0].Kind != ledger.AppConfig || ch[0].Path != path || ch[0].For != "wsjtx" || len(ch[0].Edits) != 3 {
		t.Fatalf("ledger: %+v", ch)
	}

	t.Run("untouched file goes back whole", func(t *testing.T) {
		msg, err := revertAppConfig(ch[0])
		if err != nil || !strings.Contains(msg, "restored") {
			t.Fatalf("%q %v", msg, err)
		}
		if b, _ := os.ReadFile(path); string(b) != orig {
			t.Errorf("not as it was:\n%s", b)
		}
		if fileExists(path + ".hamstack-bak") {
			t.Error("the backup must go once it is back in place")
		}
	})

	t.Run("a file the program has saved since loses only the radio", func(t *testing.T) {
		os.WriteFile(path, []byte(orig), 0o644)
		os.Remove(path + ".hamstack-bak")
		os.Remove(filepath.Join(home, ".local", "share", "hamstack", "changes.json"))
		if _, err := seedRadioApp(appNamed(t, "wsjtx"), seedConfig("N0CALL", "EM10")); err != nil {
			t.Fatal(err)
		}
		ch = readLedger(t, home)
		b, _ := os.ReadFile(path)
		os.WriteFile(path, []byte(string(b)+"[WideGraph]\nBinsPerPixel=2\n"), 0o644) // WSJT-X saves its own things on exit
		msg, err := revertAppConfig(ch[0])
		if err != nil || !strings.Contains(msg, "took the radio out") {
			t.Fatalf("%q %v", msg, err)
		}
		b, _ = os.ReadFile(path)
		got := string(b)
		if !strings.Contains(got, "Rig=None") || !strings.Contains(got, "PTT_method_VOX") || strings.Contains(got, "CATNetworkPort") ||
			!strings.Contains(got, "BinsPerPixel=2") || !strings.Contains(got, "SoundInName=default") {
			t.Errorf("the radio keys go back, everything else stays:\n%s", got)
		}
	})

	t.Run("a radio the person chose since is left", func(t *testing.T) {
		os.WriteFile(path, []byte("[Configuration]\nRig=Icom IC-7300\nCATSerialPort=/dev/ttyUSB0\nPTTMethod=other\n"), 0o644)
		before, _ := os.ReadFile(path)
		if msg, err := revertAppConfig(ch[0]); msg != "" || err != nil {
			t.Errorf("%q %v", msg, err)
		}
		if after, _ := os.ReadFile(path); string(after) != string(before) {
			t.Errorf("changed:\n%s", after)
		}
	})
}

func TestSeededFldigiFileIsRemovedOrKeptWithItsOwnSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if _, err := seedRadioApp(appNamed(t, "fldigi"), seedConfig("N0CALL", "EM10")); err != nil {
		t.Fatal(err)
	}
	ch := readLedger(t, home)
	if len(ch) != 1 || ch[0].Kind != ledger.AppConfig {
		t.Fatalf("%+v", ch)
	}
	path := ch[0].Path
	// fldigi rewrites the file with its other settings when it closes.
	b, _ := os.ReadFile(path)
	os.WriteFile(path, []byte(strings.Replace(string(b), "</FLDIGI_DEFS>", "<SQUELCHLEVEL>-3</SQUELCHLEVEL>\n</FLDIGI_DEFS>", 1)), 0o644)
	if msg, err := revertAppConfig(ch[0]); err != nil || !strings.Contains(msg, "took the radio out") {
		t.Fatalf("%q %v", msg, err)
	}
	b, _ = os.ReadFile(path)
	if !strings.Contains(string(b), "<CHKUSEHAMLIBIS>0</CHKUSEHAMLIBIS>") || !strings.Contains(string(b), "<HAMRIGDEVICE></HAMRIGDEVICE>") || !strings.Contains(string(b), "SQUELCHLEVEL") {
		t.Errorf("%s", b)
	}
	// A file nobody has changed since is simply removed, with the folder fldigi had not made yet.
	os.Remove(path)
	if _, err := seedRadioApp(appNamed(t, "fldigi"), seedConfig("N0CALL", "EM10")); err != nil {
		t.Fatal(err)
	}
	ch = readLedger(t, home)
	if msg, err := revertAppConfig(ch[0]); err != nil || !strings.Contains(msg, "removed") {
		t.Fatalf("%q %v", msg, err)
	}
	if fileExists(path) || fileExists(filepath.Dir(path)) {
		t.Error("the untouched file and its empty folder must be gone")
	}
}

func TestUninstallUndoesSeededRadioSettingsEvenWhenKeepingApps(t *testing.T) {
	f := uninstallTestFacts(t)
	f.changes = []ledger.Change{{Kind: ledger.AppConfig, Role: ledger.Support, For: "wsjtx", Path: filepath.Join(f.home, ".config", "WSJT-X.ini"),
		Edits: []ledger.Edit{{Key: "Rig", Old: "None", New: "Hamlib NET rigctl"}}}}
	for _, keep := range []bool{true, false} {
		steps, notes, err := uninstallPlan(f, keep)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(planText(steps), "Take hamstack's radio setup out of wsjtx's settings") {
			t.Errorf("keep=%v: plan lacks the settings step:\n%s", keep, planText(steps))
		}
		if !strings.Contains(strings.Join(notes, "\n"), "callsign and grid") {
			t.Errorf("keep=%v: %v", keep, notes)
		}
	}
}

func TestIniSetCreatesAndReplaces(t *testing.T) {
	if got := iniSet("", "Configuration", [][2]string{{"Rig", "X"}}); got != "[Configuration]\nRig=X\n" {
		t.Errorf("%q", got)
	}
	in := "[A]\nk=1\n[Configuration]\nRig=None\nz=2\n[B]\nk=3\n"
	want := "[A]\nk=1\n[Configuration]\nRig=Y\nz=2\nNew=1\n[B]\nk=3\n"
	if got := iniSet(in, "Configuration", [][2]string{{"Rig", "Y"}, {"New", "1"}}); got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
	if v, ok := iniGet(in, "B", "k"); !ok || v != "3" {
		t.Errorf("iniGet: %q %v", v, ok)
	}
	if _, ok := iniGet(in, "A", "Rig"); ok {
		t.Error("keys of other sections must not be found")
	}
}
