package tui

import (
	"regexp"
	"strings"
)

var safeArg = regexp.MustCompile(`^[A-Za-z0-9@._:/=+-]+$`)

// placeholder reports whether an argument is a placeholder for something the user must supply
// (ID, RIG, PROFILE, YOURCALL, KEY, VALUE): such a command cannot be run as it stands.
func placeholder(a string) bool {
	if len(a) < 2 {
		return false
	}
	for _, r := range a {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

// FixCommand finds the hamstack command inside a doctor fix text such as "run: hamstack pat sync (sets
// ...)" and returns its arguments. It returns false when the fix names no command, or one that needs
// something filled in first.
func FixCommand(fix string) ([]string, bool) {
	i := strings.Index(fix, "hamstack ")
	if i < 0 {
		return nil, false
	}
	rest := fix[i+len("hamstack "):]
	for _, stop := range []string{" (", ",", ";", "'", " or ", " then ", " unless "} {
		if j := strings.Index(rest, stop); j >= 0 {
			rest = rest[:j]
		}
	}
	args := strings.Fields(rest)
	if len(args) == 0 {
		return nil, false
	}
	for _, a := range args {
		if !safeArg.MatchString(a) || placeholder(a) || strings.Contains(a, "...") {
			return nil, false
		}
	}
	return args, true
}
