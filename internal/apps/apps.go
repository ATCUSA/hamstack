// Package apps lists the desktop programs the dashboard can open in the browser. Each runs alone on its own
// virtual X display (TigerVNC's Xvnc, on localhost only), which the dashboard relays to noVNC in the browser:
// the way DigiPi does it, one app per display.
package apps

import (
	"fmt"
	"strings"

	"github.com/ATCUSA/hamstack/internal/catalog"
	"github.com/ATCUSA/hamstack/internal/versions"
)

// App is one desktop program.
type App struct {
	Name    string // used in URLs and unit names: vnc@NAME
	Title   string
	Package string // the catalog program that provides it
	Radio   bool   // uses the radio's sound card: the HF modem is stopped while it is open
}

// All are the programs the dashboard knows, in the order it lists them. The display numbers follow this order,
// so new programs go at the end.
var All = []App{
	{"wsjtx", "WSJT-X", "wsjtx-improved", true},
	{"js8call", "JS8Call", "js8call", true},
	{"fldigi", "Fldigi", "fldigi", true},
	{"flmsg", "FLMsg", "flmsg", false},
	{"flamp", "FLAmp", "flamp", false},
	{"xastir", "Xastir", "xastir", false},
	{"flrig", "FLRig", "flrig", false},
	{"console", "Hamstack console", "xterm", false},
}

// FirstDisplay is the X display number of the first program; each gets its own, and its VNC port is
// 5900 plus that number.
const FirstDisplay = 20

// DesktopPort is where hamstack's own wayvnc shares the computer's desktop to the dashboard (localhost only).
const DesktopPort = 5899

// Find returns the program with that name.
func Find(name string) (App, bool) {
	for _, a := range All {
		if a.Name == name {
			return a, true
		}
	}
	return App{}, false
}

// Display is the program's X display number.
func (a App) Display() int {
	for i, x := range All {
		if x.Name == a.Name {
			return FirstDisplay + i
		}
	}
	return 0
}

// Port is the TCP port of the program's VNC server, on localhost.
func (a App) Port() int { return 5900 + a.Display() }

// Binary is the executable that starts it.
func (a App) Binary() string {
	if a.Name == "console" {
		return "xterm"
	}
	if p, ok := catalog.ByName(a.Package); ok {
		return p.Binary
	}
	return a.Name
}

// Path finds the program, or returns "" when it is not installed.
func (a App) Path() string {
	p, err := versions.Which(a.Binary())
	if err != nil {
		return ""
	}
	return p
}

// ValidName reports whether s could be a program name (letters, digits and dashes), so a URL cannot smuggle
// anything else into a unit name.
func ValidName(s string) bool {
	if s == "" || len(s) > 32 {
		return false
	}
	return strings.Trim(s, "abcdefghijklmnopqrstuvwxyz0123456789-") == ""
}

// VNCServer resolves the executable names used by TigerVNC distributions.
func VNCServer() (string, error) {
	for _, name := range []string{"Xvnc", "Xtigervnc"} {
		if path, err := versions.Which(name); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("TigerVNC server is not installed")
}
