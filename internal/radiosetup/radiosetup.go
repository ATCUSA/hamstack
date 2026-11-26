// Package radiosetup checks a radio's settings against the openRMS Rigs profile for what it is used for (Winlink over
// ARDOP, over a VARA-style modem, FT8), through rigctld, and sets the ones it may.
//
// The profiles are openRMS Rigs data (CC-BY-SA-4.0, "openRMS Rigs contributors"; see NOTICE.md), bundled as JSON by
// scripts/bundle-radio-profiles.py. Nothing is fetched at run time, and a profile's raw CAT commands are only ever its
// own settings-menu reads and writes (a test checks every bundled one).
package radiosetup

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

//go:embed profiles.json
var bundle []byte

// Profile is one radio's settings for one use.
type Profile struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Operation   string   `json:"operation"`
	HamlibModel int      `json:"hamlib_model"`
	Rig         string   `json:"rig"`
	Steps       []Step   `json:"steps"`
	Sources     []Source `json:"sources"`
	Tags        []string `json:"tags"`
}

// Step is one setting.
type Step struct {
	ID         string   `json:"id"`
	Kind       string   `json:"kind"` // mode, level, func, cat, manual
	Name       string   `json:"name"`
	Value      any      `json:"value"`
	Expect     *Expect  `json:"expect"`
	Display    string   `json:"display"`
	Unit       string   `json:"unit"`
	Safety     string   `json:"safety"` // "tx": it changes what goes on the air
	CheckOnly  bool     `json:"check_only"`
	Cat        *Cat     `json:"cat"`
	Manual     *Manual  `json:"manual"`
	Note       string   `json:"note"`
	Confidence string   `json:"confidence"`
	Sources    []string `json:"sources"`
	Passband   int      `json:"passband_hz"`
}

// Expect is what a setting must (not) be: outside it, the setting is wrong, not just different from the suggestion.
type Expect struct {
	Not   []any    `json:"not"`
	Min   *float64 `json:"min"`
	Max   *float64 `json:"max"`
	OneOf []any    `json:"one_of"`
}

// Cat is a raw CAT step: a read, the reply's pattern (its first group is the value), a set with {value} or {value:03}.
type Cat struct {
	Protocol string            `json:"protocol"`
	Read     string            `json:"read"`
	Reply    string            `json:"reply"`
	Set      string            `json:"set"`
	Values   map[string]string `json:"values"`
}

// Manual is a setting made on the radio itself.
type Manual struct {
	Menu  string `json:"menu"`
	Value string `json:"value"`
}

// Source is what a step rests on.
type Source struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	URL   string `json:"url"`
	Date  string `json:"date"`
}

type bundleFile struct {
	Commit   string    `json:"commit"`
	Licence  string    `json:"licence"`
	Credit   string    `json:"credit"`
	Profiles []Profile `json:"profiles"`
}

// All are the bundled profiles.
func All() []Profile {
	var b bundleFile
	if err := json.Unmarshal(bundle, &b); err != nil {
		return nil
	}
	return b.Profiles
}

// Find is the profile for a radio (its hamlib model) and a use ("winlink-ardop", "winlink-vara", "ft8").
func Find(model int, operation string) (Profile, bool) {
	for _, p := range All() {
		if p.HamlibModel == model && p.Operation == operation {
			return p, true
		}
	}
	return Profile{}, false
}

// Radio is a rigctld connection: one command, its reply lines.
type Radio interface {
	Do(cmd string) ([]string, error)
}

// The states of a Result.
const (
	OK         = "ok"         // as the profile has it
	Outside    = "outside"    // outside what it must be: wrong
	Differs    = "differs"    // within what it must be, but not the suggestion
	Unreadable = "unreadable" // the radio or hamlib cannot read it
	ManualStep = "manual"     // set on the radio itself
)

// Result is one step checked.
type Result struct {
	Step   Step   `json:"step"`
	Now    string `json:"now"`    // as read ("6", "PKTUSB", "050")
	NowFor string `json:"nowFor"` // for people ("AUTO", "REAR")
	Want   string `json:"want"`
	State  string `json:"state"`
	CanFix bool   `json:"canFix"` // it can be set (not check-only, not manual, readable, and an ASCII CAT step when raw)
	Auto   bool   `json:"auto"`   // it is fixed by itself: outside what it must be, and receive-side (or transmit-side when allowed)
}

// ascii is a CAT step hamstack can send through rigctld's "w" (text commands ending in ";", as Yaesu's are).
func (s Step) ascii() bool { return s.Cat != nil && s.Cat.Protocol == "ascii" && s.Cat.Read != "" }

func clean(lines []string) string {
	return strings.TrimSpace(strings.ReplaceAll(strings.Join(lines, " "), "\x00", ""))
}

// read gets a step's value as rigctld reports it.
func read(r Radio, s Step) (string, error) {
	switch s.Kind {
	case "mode":
		l, err := r.Do("m")
		if err != nil || len(l) == 0 {
			return "", fmt.Errorf("cannot read the mode: %v", err)
		}
		return strings.TrimSpace(l[0]), nil
	case "level":
		l, err := r.Do("l " + s.Name)
		if err != nil {
			return "", err
		}
		return clean(l), nil
	case "func":
		l, err := r.Do("u " + s.Name)
		if err != nil {
			return "", err
		}
		return clean(l), nil
	case "cat":
		if !s.ascii() {
			return "", fmt.Errorf("not a text CAT command")
		}
		l, err := r.Do("w " + s.Cat.Read)
		if err != nil {
			return "", err
		}
		m := regexp.MustCompile(s.Cat.Reply).FindStringSubmatch(clean(l))
		if m == nil || len(m) < 2 {
			return "", fmt.Errorf("unexpected reply %q", clean(l))
		}
		return m[1], nil
	}
	return "", fmt.Errorf("cannot read a %s step", s.Kind)
}

func num(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, err == nil
	}
	return 0, false
}

// same compares a value as read with one from the profile: numbers as numbers (050 is 50; true is 1), words without case.
func same(read string, want any) bool {
	if a, ok := num(read); ok {
		if b, ok := num(want); ok {
			return math.Abs(a-b) < 1e-6
		}
	}
	return strings.EqualFold(strings.TrimSpace(read), strings.TrimSpace(fmt.Sprint(want)))
}

// inside says whether a value meets the step's expect (true when it has none).
func inside(read string, e *Expect) bool {
	if e == nil {
		return true
	}
	for _, n := range e.Not {
		if same(read, n) {
			return false
		}
	}
	if len(e.OneOf) > 0 {
		ok := false
		for _, o := range e.OneOf {
			ok = ok || same(read, o)
		}
		if !ok {
			return false
		}
	}
	v, isNum := num(read)
	if e.Min != nil && (!isNum || v < *e.Min) || e.Max != nil && (!isNum || v > *e.Max) {
		return false
	}
	return true
}

// agcNames are hamlib's AGC values (RIG_AGC_*), as "l AGC" reports them.
var agcNames = map[int]string{0: "OFF", 1: "SUPERFAST", 2: "FAST", 3: "SLOW", 4: "USER", 5: "MEDIUM", 6: "AUTO"}

func label(s Step, v string) string {
	if s.Kind == "level" && s.Name == "AGC" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			if w, ok := agcNames[n]; ok {
				return w
			}
		}
	}
	if s.Cat != nil && s.Cat.Values != nil {
		if n, err := strconv.Atoi(v); err == nil {
			if w, ok := s.Cat.Values[strconv.Itoa(n)]; ok {
				return w
			}
		}
	}
	if s.Kind == "func" {
		if same(v, 1) {
			return "on"
		}
		if same(v, 0) {
			return "off"
		}
	}
	return v
}

func wantText(s Step) string {
	if s.Manual != nil {
		return s.Manual.Value
	}
	if s.Display != "" {
		return s.Display
	}
	if s.Value == nil {
		return ""
	}
	v := fmt.Sprint(s.Value)
	if s.Kind == "func" {
		if b, ok := s.Value.(bool); ok {
			if b {
				return "on"
			}
			return "off"
		}
	}
	return label(s, v)
}

// Check reads every step. allowTX says whether transmit-side settings outside their range are fixed by themselves too.
func Check(r Radio, p Profile, allowTX bool) []Result {
	var out []Result
	for _, s := range p.Steps {
		res := Result{Step: s, Want: wantText(s)}
		if s.Kind == "manual" || s.Kind == "cat" && !s.ascii() {
			res.State = ManualStep
			out = append(out, res)
			continue
		}
		v, err := read(r, s)
		if err != nil {
			res.State = Unreadable
			out = append(out, res)
			continue
		}
		res.Now, res.NowFor = v, label(s, v)
		switch {
		case !inside(v, s.Expect):
			res.State = Outside
		case s.Value == nil || same(v, s.Value):
			res.State = OK
		default:
			res.State = Differs
		}
		res.CanFix = !s.CheckOnly && s.Value != nil && (s.Kind != "cat" || s.Cat.Set != "")
		// By itself only what is wrong, never what merely differs from the suggestion (a power setting, a noise blanker
		// someone uses for voice): and transmit-side only when the owner allows it.
		res.Auto = res.CanFix && res.State == Outside && (s.Safety != "tx" || allowTX)
		out = append(out, res)
	}
	return out
}

var setFieldRE = regexp.MustCompile(`\{value(?::0?(\d+))?\}`)

// setCommand is the rigctld command that gives a step the profile's value.
func setCommand(s Step) (string, error) {
	switch s.Kind {
	case "mode":
		pb := s.Passband
		if pb == 0 {
			pb = -1 // the radio's own passband for the mode
		}
		return fmt.Sprintf("M %v %d", s.Value, pb), nil
	case "level":
		return fmt.Sprintf("L %s %v", s.Name, s.Value), nil
	case "func":
		v := 0
		if n, ok := num(s.Value); ok && n != 0 {
			v = 1
		}
		return fmt.Sprintf("U %s %d", s.Name, v), nil
	case "cat":
		if !s.ascii() || s.Cat.Set == "" {
			return "", fmt.Errorf("%s cannot be set from here", s.Name)
		}
		n, ok := num(s.Value)
		if !ok {
			return "", fmt.Errorf("%s has no number to set", s.Name)
		}
		cmd := setFieldRE.ReplaceAllStringFunc(s.Cat.Set, func(f string) string {
			if m := setFieldRE.FindStringSubmatch(f); m[1] != "" {
				w, _ := strconv.Atoi(m[1])
				return fmt.Sprintf("%0*d", w, int(n))
			}
			return strconv.Itoa(int(n))
		})
		return "w " + cmd, nil
	}
	return "", fmt.Errorf("%s is set on the radio itself", s.Name)
}

// Fix gives a step the profile's value, and reads it back.
func Fix(r Radio, s Step) error {
	if s.CheckOnly {
		return fmt.Errorf("%s is only checked, never set", s.Name)
	}
	cmd, err := setCommand(s)
	if err != nil {
		return err
	}
	if _, err := r.Do(cmd); err != nil {
		return fmt.Errorf("setting %s: %w", s.Name, err)
	}
	v, err := read(r, s)
	if err != nil {
		return fmt.Errorf("%s was set, but could not be read back: %w", s.Name, err)
	}
	if !same(v, s.Value) {
		return fmt.Errorf("%s reads %s after setting it to %v", s.Name, label(s, v), s.Value)
	}
	return nil
}
