// Package hamlib asks the installed hamlib which radio models it supports.
package hamlib

import (
	"bufio"
	"context"
	"io"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Models maps a hamlib model number to its driver status (Stable, Beta, Alpha...).
type Models map[int]string

// Parse reads the table printed by `rigctld -l`. Columns are: model number, manufacturer,
// model name, driver version, status, macro. Manufacturer and name may contain spaces, so
// only the first field and the last two are relied on.
func Parse(r io.Reader) Models {
	out := Models{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 5 {
			continue
		}
		id, err := strconv.Atoi(f[0])
		if err != nil {
			continue // header or banner line
		}
		out[id] = f[len(f)-2]
	}
	return out
}

// Installed runs `<rigctld> -l` and parses its output. rigctld is the path of the program to ask.
func Installed(rigctld string) (Models, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	b, err := exec.CommandContext(ctx, rigctld, "-l").Output()
	if err != nil && len(b) == 0 {
		return nil, err
	}
	return Parse(strings.NewReader(string(b))), nil
}

// Entry is one radio model in hamlib's table.
type Entry struct {
	ID        int
	Mfg, Name string
	Status    string // Stable, Beta, Alpha...
	Macro     string
}

// Label is "Icom IC-7300" (or just the maker when the model has no name, as FLRig).
func (e Entry) Label() string { return strings.TrimSpace(e.Mfg + " " + e.Name) }

var columnGap = regexp.MustCompile(`\s{2,}`)

// List reads the table printed by `rigctld -l` with the maker and model name of each line. The columns
// are separated by runs of spaces; the maker or model may themselves contain single spaces ("NET rigctl").
func List(r io.Reader) []Entry {
	var out []Entry
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := columnGap.Split(strings.TrimSpace(sc.Text()), -1)
		if len(f) < 5 {
			continue
		}
		id, err := strconv.Atoi(f[0])
		if err != nil {
			continue
		}
		e := Entry{ID: id, Mfg: f[1], Status: f[len(f)-2], Macro: f[len(f)-1]}
		if len(f) >= 6 {
			e.Name = f[2]
		}
		out = append(out, e)
	}
	return out
}

// Installed lists every model the installed rigctld supports, for choosing one.
func InstalledList(rigctld string) ([]Entry, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	b, err := exec.CommandContext(ctx, rigctld, "-l").Output()
	if err != nil && len(b) == 0 {
		return nil, err
	}
	return List(strings.NewReader(string(b))), nil
}

func norm(s string) string {
	var b strings.Builder
	for _, c := range strings.ToLower(s) {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			b.WriteRune(c)
		}
	}
	return b.String()
}

// subsequence reports whether the letters of needle appear in order in hay.
func subsequence(needle, hay string) bool {
	i := 0
	for j := 0; j < len(hay) && i < len(needle); j++ {
		if hay[j] == needle[i] {
			i++
		}
	}
	return i == len(needle)
}

// Filter returns the entries that match a search, best first. Every word of the query must match, ignoring
// case, spaces and dashes ("ic 7300", "ic7300" and "IC-7300" are the same), either as part of the
// maker, name or macro, or as letters in order ("ft991" finds "FT-991A", "k3" finds "Elecraft K3"). Matches that
// are whole parts rank above scattered ones, and a short query on its own matches the start of a name best.
func Filter(entries []Entry, query string) []Entry {
	words := strings.Fields(query)
	if len(words) == 0 {
		return entries
	}
	type scored struct {
		e     Entry
		score int
	}
	var hits []scored
	for _, e := range entries {
		hay := norm(e.Mfg + e.Name)
		full := hay + norm(e.Macro) + strconv.Itoa(e.ID)
		total, ok := 0, true
		for _, w := range words {
			n := norm(w)
			if n == "" {
				continue
			}
			switch {
			case strings.HasPrefix(norm(e.Name), n):
				total += 0
			case strings.Contains(hay, n):
				total += 2
			case strings.Contains(full, n):
				total += 3
			case subsequence(n, hay):
				total += 6
			default:
				ok = false
			}
			if !ok {
				break
			}
		}
		if ok {
			hits = append(hits, scored{e, total})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score < hits[j].score
		}
		return hits[i].e.Label() < hits[j].e.Label()
	})
	out := make([]Entry, len(hits))
	for i, h := range hits {
		out[i] = h.e
	}
	return out
}

// Caps is what `rigctl -u` says about how a model is connected.
type Caps struct {
	Port             string // "RS-232", "Network", "USB", "None", ...
	MinBaud, MaxBaud int    // 0 when the model has no serial speed
}

var speedRE = regexp.MustCompile(`Serial speed:\s*(\d+)\.\.(\d+) baud`)

// ParseCaps reads the text of `rigctl -m N -u`.
func ParseCaps(text string) Caps {
	var c Caps
	for _, l := range strings.Split(text, "\n") {
		if v, ok := strings.CutPrefix(l, "Port type:"); ok {
			c.Port = strings.TrimSpace(v)
		}
		if m := speedRE.FindStringSubmatch(l); m != nil && c.MaxBaud == 0 {
			c.MinBaud, _ = strconv.Atoi(m[1])
			c.MaxBaud, _ = strconv.Atoi(m[2])
		}
	}
	return c
}

// ModelCaps asks the installed rigctl (next to rigctld) about a model.
func ModelCaps(rigctl string, model int) (Caps, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	b, err := exec.CommandContext(ctx, rigctl, "-m", strconv.Itoa(model), "-u").Output()
	if err != nil && len(b) == 0 {
		return Caps{}, err
	}
	return ParseCaps(string(b)), nil
}

// Serial reports whether the model is reached over a serial port.
func (c Caps) Serial() bool { return c.MaxBaud > 0 }

// DefaultBaud proposes a speed within the model's range: 9600 if it supports it (the usual factory
// setting), else the nearest it does.
func (c Caps) DefaultBaud() int {
	switch {
	case c.MaxBaud == 0:
		return 0
	case 9600 < c.MinBaud:
		return c.MinBaud
	case 9600 > c.MaxBaud:
		return c.MaxBaud
	}
	return 9600
}
