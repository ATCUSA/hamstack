package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ATCUSA/hamstack/internal/apps"
	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/ledger"
)

// Programs that talk to a radio ask for its settings the first time they run. hamstack already runs rigctld
// for each rig, so it fills in the answer: "Hamlib NET rigctl" at that rig's rigctld port. It only does so
// for a program that has no radio set up yet, and never changes one that has: what a person chose in the
// program's own settings is theirs.
//
// What was checked, by starting each program on a spare display against a dummy rigctld: WSJT-X Improved 2.8,
// JS8Call 2.2 and fldigi 4.2 read the radio's frequency and mode from the settings written here. What was not
// exercised is keying: the PTT method is written in the form WSJT-X uses for its other values, and fldigi's
// PTT flag is a guess at the tag's meaning, so the README tells people to look at it once.

// pttCAT is how WSJT-X and JS8Call store "PTT method: CAT": a Qt variant of an enum, with the length of the
// name (and its NUL) before it.
const pttCAT = `@Variant(\0\0\0\x7f\0\0\0\x1eTransceiverFactory::PTTMethod\0\0\0\0\xfPTT_method_CAT\0)`

// seedRigUp says whether a rig's rigctld is answering. A variable so a test need not run one.
var seedRigUp = portOpen

// seedRig is the rig a program is pointed at: the first whose rigctld answers now. A rig that is switched off,
// unplugged or whose rigctld has failed is not chosen, because a program that is given a dead port keeps it:
// it never looks like it has no radio again. With none answering, nothing is written, and the next start tries
// again.
func seedRig(cfg config.Config) (config.Rig, bool) {
	for _, r := range panelRigs(cfg) {
		if seedRigUp(r.RigPort) {
			return r, true
		}
	}
	return config.Rig{}, false
}

// seedRadioApp sets up the radio of a program about to start, if it has none yet, and writes down what it
// changed so 'hamstack uninstall' can put it back. It returns a line to tell the person what it did, or ""
// when it left everything alone.
func seedRadioApp(a apps.App, cfg config.Config) (string, error) {
	switch a.Name {
	case "wsjtx", "js8call", "fldigi":
	default:
		return "", nil
	}
	rig, ok := seedRig(cfg)
	if !ok {
		return "", nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	st, _ := cfg.ActiveStation()
	var chg *ledger.Change
	switch a.Name {
	case "wsjtx":
		chg, err = seedQtIni(filepath.Join(home, ".config", "WSJT-X.ini"), rig, st.Callsign, cfg.Grid)
	case "js8call":
		chg, err = seedQtIni(filepath.Join(home, ".config", "JS8Call.ini"), rig, st.Callsign, cfg.Grid)
	case "fldigi":
		chg, err = seedFldigi(filepath.Join(home, ".fldigi", "fldigi_def.xml"), rig, st.Callsign, cfg.Grid)
	}
	if err != nil || chg == nil {
		return "", err
	}
	chg.For = a.Name
	note := fmt.Sprintf("%s: radio set up for %s (rigctld at 127.0.0.1:%d); change it in the program's own settings if you like", a.Title, rigLabel(rig), rig.RigPort)
	if err := ledger.Add(home, *chg); err != nil {
		note += fmt.Sprintf(" (could not record this for 'hamstack uninstall': %v)", err)
	}
	return note, nil
}

func sumOf(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// seedQtIni gives a WSJT-X-style program (WSJT-X, WSJT-X Improved, JS8Call) its radio, when the settings file has
// none. It writes the radio's keys, and the callsign and grid if they are empty. nil means it did nothing.
func seedQtIni(path string, rig config.Rig, call, grid string) (*ledger.Change, error) {
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	text := string(old)
	if r, _ := iniGet(text, "Configuration", "Rig"); r != "" && r != "None" {
		return nil, nil // a radio is chosen
	}
	for _, k := range []string{"CATNetworkPort", "CATSerialPort"} {
		if v, _ := iniGet(text, "Configuration", k); v != "" {
			return nil, nil // set up some other way
		}
	}
	radio := [][2]string{
		{"Rig", "Hamlib NET rigctl"},
		{"CATNetworkPort", fmt.Sprintf("127.0.0.1:%d", rig.RigPort)},
		{"PTTMethod", pttCAT},
	}
	var edits []ledger.Edit // the radio's keys only: a callsign someone may want to keep is not undone
	for _, p := range radio {
		was, _ := iniGet(text, "Configuration", p[0])
		edits = append(edits, ledger.Edit{Key: p[0], Old: was, New: p[1]})
	}
	set := radio
	if v, _ := iniGet(text, "Configuration", "MyCall"); v == "" && call != "" {
		set = append(set, [2]string{"MyCall", call})
	}
	if v, _ := iniGet(text, "Configuration", "MyGrid"); v == "" && grid != "" {
		set = append(set, [2]string{"MyGrid", grid})
	}
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
		if bak := path + ".hamstack-bak"; !fileExists(bak) { // the file as it was before hamstack touched it
			if err := os.WriteFile(bak, old, mode); err != nil {
				return nil, err
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	out := []byte(iniSet(text, "Configuration", set))
	if err := writeFileAtomic(path, out, mode); err != nil {
		return nil, err
	}
	return &ledger.Change{Kind: ledger.AppConfig, Role: ledger.Support, Path: path, Edits: edits, Sum: sumOf(out)}, nil
}

// iniGet reads a key of a section from the text of a Qt settings file.
func iniGet(text, section, key string) (string, bool) {
	in := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "[") {
			in = line == "["+section+"]"
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok && in && k == key {
			return v, true
		}
	}
	return "", false
}

// iniSet sets keys in a section, keeping every other line as it is: a key that is there has its value
// replaced, one that is not is added at the end of the section, and a missing section is added at the end.
func iniSet(text, section string, kv [][2]string) string {
	var lines []string
	if text != "" {
		lines = strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	}
	start, end := -1, len(lines)
	for i, l := range lines {
		l = strings.TrimRight(l, "\r")
		if strings.HasPrefix(l, "[") {
			if start >= 0 {
				end = i
				break
			}
			if l == "["+section+"]" {
				start = i
			}
		}
	}
	if start < 0 {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, "["+section+"]")
		start, end = len(lines)-1, len(lines)
	}
	for _, p := range kv {
		found := false
		for i := start + 1; i < end; i++ {
			if k, _, ok := strings.Cut(lines[i], "="); ok && k == p[0] {
				lines[i], found = p[0]+"="+p[1], true
				break
			}
		}
		if !found {
			lines = append(lines[:end], append([]string{p[0] + "=" + p[1]}, lines[end:]...)...)
			end++
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

// iniDel removes a key from a section.
func iniDel(text, section, key string) string {
	var out []string
	in := false
	for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		if strings.HasPrefix(line, "[") {
			in = strings.TrimRight(line, "\r") == "["+section+"]"
		}
		if k, _, ok := strings.Cut(line, "="); ok && in && k == key {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n") + "\n"
}

// seedFldigi writes fldigi's first settings file, with its radio. fldigi keeps everything in one XML file and
// starts a setup wizard when there is none, so an existing file is never touched. nil means it did nothing.
func seedFldigi(path string, rig config.Rig, call, grid string) (*ledger.Change, error) {
	if fileExists(path) {
		return nil, nil
	}
	device := fmt.Sprintf("127.0.0.1:%d", rig.RigPort)
	var b bytes.Buffer
	b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<FLDIGI_DEFS>\n")
	tag := func(name, value string) {
		b.WriteString("<" + name + ">")
		_ = xml.EscapeText(&b, []byte(value))
		b.WriteString("</" + name + ">\n")
	}
	if call != "" {
		tag("MYCALL", call)
	}
	if grid != "" {
		tag("MYLOC", grid)
	}
	// 2 is hamlib's own number for "NET rigctl", which fldigi passes straight to hamlib.
	edits := []ledger.Edit{{Key: "CHKUSEHAMLIBIS", Old: "0", New: "1"}, {Key: "HAMRIGMODEL", Old: "0", New: "2"},
		{Key: "HAMRIGDEVICE", New: device}, {Key: "HAMLIBCMDPTT", Old: "0", New: "1"}}
	for _, e := range edits {
		tag(e.Key, e.New)
	}
	b.WriteString("</FLDIGI_DEFS>\n")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if err := writeFileAtomic(path, b.Bytes(), 0o644); err != nil {
		return nil, err
	}
	return &ledger.Change{Kind: ledger.AppConfig, Role: ledger.Support, Path: path, Edits: edits, Sum: sumOf(b.Bytes())}, nil
}

// revertAppConfig undoes what seeding did to a program's settings, for 'hamstack uninstall': the radio's keys go
// back to what they were, but only the ones still holding what hamstack gave them. A file nobody has changed
// since is put back whole (the backup) or removed. It returns a line saying what it did.
func revertAppConfig(c ledger.Change) (string, error) {
	b, err := os.ReadFile(c.Path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	bak := c.Path + ".hamstack-bak"
	if c.Sum != "" && sumOf(b) == c.Sum { // as hamstack left it: the program has not saved anything since
		if old, err := os.ReadFile(bak); err == nil {
			if err := writeFileAtomic(c.Path, old, 0o644); err != nil {
				return "", err
			}
			_ = os.Remove(bak)
			return "restored " + c.Path, nil
		}
		if err := os.Remove(c.Path); err != nil {
			return "", err
		}
		_ = os.Remove(filepath.Dir(c.Path)) // only goes if it is now empty
		return "removed " + c.Path, nil
	}
	text, n := string(b), 0
	for _, e := range c.Edits {
		if strings.HasSuffix(c.Path, ".xml") {
			from, to := "<"+e.Key+">"+e.New+"</"+e.Key+">", "<"+e.Key+">"+e.Old+"</"+e.Key+">"
			if strings.Contains(text, from) {
				text, n = strings.Replace(text, from, to, 1), n+1
			}
			continue
		}
		if v, ok := iniGet(text, "Configuration", e.Key); ok && v == e.New {
			if e.Old == "" {
				text = iniDel(text, "Configuration", e.Key)
			} else {
				text = iniSet(text, "Configuration", [][2]string{{e.Key, e.Old}})
			}
			n++
		}
	}
	if n == 0 {
		return "", nil // the radio was changed in the program since: it is the person's now
	}
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(c.Path); err == nil {
		mode = fi.Mode().Perm()
	}
	if err := writeFileAtomic(c.Path, []byte(text), mode); err != nil {
		return "", err
	}
	_ = os.Remove(bak)
	return fmt.Sprintf("took the radio out of %s (%d settings)", c.Path, n), nil
}

// writeFileAtomic replaces a file in one step, so a program starting at the same moment never reads half of it.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".hamstack-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
