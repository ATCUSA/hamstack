package radiosetup

import "slices"

// Use is a program hamstack runs on the radio, and the kind of profile it needs. Several programs have no profile of
// their own in openRMS Rigs; they get the closest one, and say so.
type Use struct {
	Name      string // the modem kind or app name: "ardopcf", "wsjtx"
	Title     string // for people: "WSJT-X"
	Operation string // the profile operation it needs
	Closest   string // why a profile written for something else is used, when it is
}

// Uses are the programs with a recommended profile, modems first.
var Uses = []Use{
	{Name: "ardopcf", Title: "ardopcf", Operation: "winlink-ardop"},
	{Name: "xara", Title: "xara", Operation: "winlink-vara"},
	{Name: "mercury", Title: "Mercury", Operation: "winlink-vara", Closest: "the profile written for VARA HF, the closest there is for Mercury"},
	{Name: "data2g", Title: "Data2G", Operation: "winlink-vara", Closest: "the profile written for VARA HF, the closest there is for Data2G"},
	{Name: "wsjtx", Title: "WSJT-X", Operation: "ft8"},
	{Name: "js8call", Title: "JS8Call", Operation: "ft8", Closest: "the profile written for FT8, the closest there is for JS8Call"},
	{Name: "fldigi", Title: "fldigi", Operation: "psk", Closest: "the profile written for PSK31; the RTTY one is there too"},
}

// UseFor is the Use of a program.
func UseFor(name string) (Use, bool) {
	for _, u := range Uses {
		if u.Name == name {
			return u, true
		}
	}
	return Use{}, false
}

// ByID is a bundled profile by its id ("yaesu-ftdx10/ft8").
func ByID(id string) (Profile, bool) {
	for _, p := range All() {
		if p.ID == id {
			return p, true
		}
	}
	return Profile{}, false
}

// ForModel are the bundled profiles for a radio (its hamlib model), in the bundle's order.
func ForModel(model int) []Profile {
	var out []Profile
	for _, p := range All() {
		if p.HamlibModel == model {
			out = append(out, p)
		}
	}
	return out
}

// Recommended is the profile a program gets on a radio unless its owner picks another. Of several for the same
// operation, one for a radio on a USB cable comes before one for remote operation (the IC-7300MK2 has both for FT8,
// the LAN one first in the bundle): hamstack drives the radio over USB.
func Recommended(model int, use string) (Profile, bool) {
	u, ok := UseFor(use)
	if !ok {
		return Profile{}, false
	}
	var found []Profile
	for _, p := range ForModel(model) {
		if p.Operation == u.Operation {
			found = append(found, p)
		}
	}
	for _, p := range found {
		if !slices.Contains(p.Tags, "remote") {
			return p, true
		}
	}
	if len(found) > 0 {
		return found[0], true
	}
	return Profile{}, false
}

// RecommendedFor are the programs a profile is the recommended one for ("WSJT-X", "JS8Call"), for telling people
// which one to pick.
func RecommendedFor(p Profile) []string {
	var out []string
	for _, u := range Uses {
		if r, ok := Recommended(p.HamlibModel, u.Name); ok && r.ID == p.ID {
			out = append(out, u.Title)
		}
	}
	return out
}
