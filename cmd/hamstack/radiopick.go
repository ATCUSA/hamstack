package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/ATCUSA/hamstack/internal/hamlib"
	"github.com/ATCUSA/hamstack/internal/install"
	"github.com/ATCUSA/hamstack/internal/profile"
	"github.com/ATCUSA/hamstack/internal/versions"
)

// popularRadios are shown first when choosing a radio: the dummy rig (testing without hardware), a Digirig (an
// interface rather than a radio), a few of the most common HF rigs, and FLRig (a method: control through flrig).
// Any other radio hamlib knows is one search away.
var popularRadios = []string{"dummy", "digirig", "ic7300", "ic7300mk2", "ic705", "ic7610", "ft991a", "ft710", "ftdx10", "flrig"}

// radioMenu lists the profiles to show: the popular ones that exist, then any others (saved by the user),
// so nothing a person has set up disappears from the menu.
func radioMenu(profs []profile.Profile) []profile.Profile {
	byID := map[string]profile.Profile{}
	for _, p := range profs {
		byID[p.ID] = p
	}
	var out []profile.Profile
	seen := map[string]bool{}
	for _, id := range popularRadios {
		if p, ok := byID[id]; ok {
			out = append(out, p)
			seen[id] = true
		}
	}
	builtin, _ := profile.All("") // the profiles shipped with hamstack: search finds those that are not on the short list
	shipped := map[string]bool{}
	for _, p := range builtin {
		shipped[p.ID] = true
	}
	for _, p := range profs {
		if !seen[p.ID] && !shipped[p.ID] {
			out = append(out, p) // one the user saved
		}
	}
	return out
}

// chooseRadio asks which radio to use: the short menu, or a search of everything hamlib supports. A search
// can make a new profile, so the caller should reload the profiles afterwards.
func chooseRadio(a *asker, profs []profile.Profile, flagID, current string, detected []profile.Profile) (string, error) {
	find := func(id string) bool {
		for _, p := range profs {
			if p.ID == id {
				return true
			}
		}
		return false
	}
	if flagID != "" {
		if !find(flagID) {
			return "", fmt.Errorf("unknown radio %q (see: hamstack profiles; or choose one by searching: leave --radio out)", flagID)
		}
		fmt.Fprintf(a.out, "radio: %s\n", flagID)
		return flagID, nil
	}
	isDetected := func(id string) bool {
		for _, d := range detected {
			if d.ID == id {
				return true
			}
		}
		return false
	}
	suggest := ""
	if len(detected) == 1 {
		suggest = detected[0].ID // exactly one known radio is plugged in: offer it
	}
	menu := radioMenu(profs)
	for {
		for i, p := range menu {
			mark := ""
			if p.ID == current {
				mark = "  (current)"
			}
			if isDetected(p.ID) {
				mark += "  (detected: plugged in now)"
			}
			fmt.Fprintf(a.out, "  %2d) %-10s %s%s\n", i+1, p.ID, p.Name, mark)
		}
		fmt.Fprintln(a.out, "   s) Search every radio hamlib supports (several hundred): type part of a name, like ic-7100 or kx3")
		prompt := "Choose a radio (number, id, or s to search): "
		if suggest != "" {
			prompt = fmt.Sprintf("Choose a radio (number, id, or s to search) [%s]: ", suggest)
		}
		ans, ok := a.line(prompt)
		if !ok {
			return "", fmt.Errorf("no radio chosen")
		}
		switch {
		case ans == "" && suggest != "":
			return suggest, nil
		case ans == "" && current != "" && find(current):
			return current, nil
		case ans == "":
			fmt.Fprintln(a.out, "not a valid choice")
			continue
		}
		if n, err := strconv.Atoi(ans); err == nil && n >= 1 && n <= len(menu) {
			return menu[n-1].ID, nil
		}
		if find(ans) {
			return ans, nil
		}
		query := ans
		if strings.EqualFold(ans, "s") || strings.EqualFold(ans, "search") {
			query = ""
		}
		id, err := searchRadios(a, profs, query)
		if err != nil {
			fmt.Fprintln(a.out, err)
			continue
		}
		if id != "" {
			return id, nil
		}
	}
}

// radioSearch is what searchRadios needs from the system, so tests can supply it.
type radioSearch struct {
	entries func() ([]hamlib.Entry, error)
	caps    func(model int) hamlib.Caps
	save    func(profile.Profile) error
}

func systemRadioSearch(a *asker) radioSearch {
	return radioSearch{
		entries: func() ([]hamlib.Entry, error) {
			rigctld, err := ensureHamlib(a)
			if err != nil {
				return nil, err
			}
			return hamlib.InstalledList(rigctld)
		},
		caps: func(model int) hamlib.Caps {
			if rigctld, err := versions.Which("rigctld"); err == nil {
				if c, err := hamlib.ModelCaps(filepath.Join(filepath.Dir(rigctld), "rigctl"), model); err == nil {
					return c
				}
			}
			return hamlib.Caps{}
		},
		save: saveUserProfile,
	}
}

// ensureHamlib returns the path of rigctld, offering to install hamlib first if it is missing: the list of
// radios comes from the installed hamlib, so it is always the list this system can actually drive.
func ensureHamlib(a *asker) (string, error) {
	if p, err := versions.Which("rigctld"); err == nil {
		return p, nil
	}
	fmt.Fprintln(a.out, "The list of radios comes from hamlib, which is not installed yet.")
	if !a.confirm("Install hamlib now?", true) {
		return "", fmt.Errorf("hamlib is needed to search the radio list")
	}
	steps, notice, err := buildInstallStepsSrc([]string{install.Hamlib}, false, nil)
	if err != nil {
		return "", err
	}
	if err := install.Run(steps, install.Options{In: a.in, Out: a.out, Notice: notice}); err != nil {
		return "", err
	}
	if p, err := versions.Which("rigctld"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("hamlib did not install")
}

const searchPage = 12

func searchRadios(a *asker, profs []profile.Profile, query string) (string, error) {
	return searchRadiosWith(a, profs, query, systemRadioSearch(a))
}

// searchRadiosWith lets the user find any radio in hamlib's list by typing part of its name, page through
// the matches, and pick one. It returns the id of a profile for it (made on the spot if there is none), or
// "" to go back to the menu.
func searchRadiosWith(a *asker, profs []profile.Profile, query string, rs radioSearch) (string, error) {
	entries, err := rs.entries()
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "", fmt.Errorf("hamlib lists no radios")
	}
	page := 0
	for {
		hits := hamlib.Filter(entries, query)
		if len(hits) == 0 {
			fmt.Fprintf(a.out, "Nothing matches %q among %d radios. Try fewer letters, or just the maker.\n", query, len(entries))
		} else {
			pages := (len(hits) + searchPage - 1) / searchPage
			page = max(0, min(page, pages-1))
			lo, hi := page*searchPage, min(len(hits), (page+1)*searchPage)
			if query == "" {
				fmt.Fprintf(a.out, "All %d radios (page %d of %d):\n", len(hits), page+1, pages)
			} else {
				fmt.Fprintf(a.out, "%d match %q (page %d of %d):\n", len(hits), query, page+1, pages)
			}
			for i, e := range hits[lo:hi] {
				fmt.Fprintf(a.out, "  %2d) %-28s hamlib %d, %s\n", i+1, e.Label(), e.ID, e.Status)
			}
		}
		ans, ok := a.line("Number to choose, n/p for the next or previous page, text to search again, Enter to go back: ")
		if !ok || ans == "" {
			return "", nil
		}
		switch strings.ToLower(ans) {
		case "n":
			page++
			continue
		case "p":
			page--
			continue
		}
		if n, err := strconv.Atoi(ans); err == nil {
			lo := page * searchPage
			if n >= 1 && lo+n-1 < len(hits) {
				return profileForEntry(a, profs, hits[lo+n-1], rs)
			}
			fmt.Fprintln(a.out, "not on this page")
			continue
		}
		query, page = ans, 0
	}
}

var nonID = regexp.MustCompile(`[^a-z0-9]+`)

// profileForEntry returns the profile for a hamlib model: an existing one if hamstack already knows the radio,
// otherwise a new one built from what hamlib says about it and saved for later.
func profileForEntry(a *asker, profs []profile.Profile, e hamlib.Entry, rs radioSearch) (string, error) {
	for _, p := range profs {
		if p.Rig.Model == e.ID {
			fmt.Fprintf(a.out, "Using the %s profile (%s).\n", p.ID, p.Name)
			return p.ID, nil
		}
	}
	c := rs.caps(e.ID)
	baud := 0
	if c.Serial() {
		def := c.DefaultBaud()
		for {
			ans, ok := a.line(fmt.Sprintf("Serial speed: the radio's CAT rate (it supports %d..%d) [%d]: ", c.MinBaud, c.MaxBaud, def))
			if !ok {
				return "", fmt.Errorf("cancelled")
			}
			if ans == "" {
				baud = def
				break
			}
			if n, err := strconv.Atoi(ans); err == nil && n >= c.MinBaud && n <= c.MaxBaud {
				baud = n
				break
			}
			fmt.Fprintln(a.out, "not a speed this radio supports")
		}
	}
	p := newProfileFor(e, baud, profs)
	if e.Status != "Stable" && e.Status != "" {
		fmt.Fprintf(a.out, "Note: hamlib marks this radio's driver %q, so some features may not work.\n", e.Status)
	}
	if err := rs.save(p); err != nil {
		return "", err
	}
	fmt.Fprintf(a.out, "Saved as the profile %q.\n", p.ID)
	return p.ID, nil
}

// newProfileFor builds a profile for a hamlib model that hamstack has no profile for.
// Its id is made from the name and has to serve as a rig id too (setup names the rig after it): never empty,
// at most 32 characters, and not the id of a profile for a different model, which saving would overwrite.
func newProfileFor(e hamlib.Entry, baud int, existing []profile.Profile) profile.Profile {
	id := strings.Trim(nonID.ReplaceAllString(strings.ToLower(e.Label()), "-"), "-")
	if len(id) > 32 {
		id = strings.Trim(id[:32], "-")
	}
	if id == "" || findProfile(existing, id) != nil {
		suffix := fmt.Sprintf("-%d", e.ID)
		id = strings.Trim(id[:min(len(id), 32-len(suffix))], "-") + suffix
		id = strings.TrimLeft(id, "-")
		if id == strconv.Itoa(e.ID) {
			id = "hamlib-" + id
		}
	}
	p := profile.Profile{ID: id, Name: e.Label()}
	p.Rig.Model, p.Rig.Baud = e.ID, baud
	p.Audio.CardHints = []string{"CODEC", "Device"}
	p.Notes = fmt.Sprintf("Added from hamlib's list (model %d, driver %s). Not tested by the author: set the radio's CAT rate to %d, "+
		"and assign its serial port with hamstack rig identify.", e.ID, e.Status, baud)
	return p
}

func saveUserProfile(p profile.Profile) error {
	dir := userProfileDir()
	if dir == "" {
		return fmt.Errorf("cannot find the config directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(p, "", "  ")
	return os.WriteFile(filepath.Join(dir, p.ID+".json"), append(b, '\n'), 0o644)
}
