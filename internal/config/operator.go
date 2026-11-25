package config

import (
	"fmt"
	"regexp"
	"strings"
)

// The operator's own details are saved once in hamstack's config and reused when configuring each
// program, so setup does not ask for the same thing repeatedly. Today that is the callsigns (see
// Station), the radio (Config.Profile) and the grid square below.

var gridRE = regexp.MustCompile(`^[A-Ra-r]{2}[0-9]{2}([A-Xa-x]{2}([0-9]{2})?)?$`)

// ValidGrid reports whether s is a 4, 6 or 8 character Maidenhead grid locator.
func ValidGrid(s string) bool { return gridRE.MatchString(strings.TrimSpace(s)) }

// NormalizeGrid writes a locator in the conventional case: the first pair upper case and the
// sub-square lower case (EM10dg).
func NormalizeGrid(s string) string {
	s = strings.TrimSpace(s)
	if len(s) < 4 {
		return s
	}
	out := strings.ToUpper(s[:2]) + s[2:4]
	if len(s) >= 6 {
		out += strings.ToLower(s[4:6])
	}
	if len(s) >= 8 {
		out += s[6:8]
	}
	return out
}

func parseGrid(value string) (string, error) {
	if !ValidGrid(value) {
		return "", fmt.Errorf("%q is not a grid square (examples: EM10, EM10dg)", value)
	}
	return NormalizeGrid(value), nil
}
