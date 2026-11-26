package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/ATCUSA/hamstack/internal/apps"
	"github.com/ATCUSA/hamstack/internal/ledger"
)

// A radio program's own menu entry starts it directly: the HF modem keeps the sound card, the program's audio
// through PipeWire fails to open ("Device or resource busy" in PipeWire's log), and it has no sound until it is
// started again. Seen on a Raspberry Pi with WSJT-X Improved started from its own entry while xara ran. So when
// hamstack writes its own entries ("WSJT-X (hamstack)", which stops the modem first), it hides the programs' own ones:
// an entry in a system folder by a user entry of the same name that says Hidden, an entry already in the user's
// folder (a source build puts it there) by marking it NoDisplay, with a copy kept so it can be put back.

// ownMenuEntries are the menu entries the radio programs install for themselves.
var ownMenuEntries = map[string][]string{
	"wsjtx":   {"wsjtx.desktop", "ws.desktop"}, // WSJT-X, and WSJT-X Improved's own build
	"js8call": {"js8call.desktop"},
	"fldigi":  {"fldigi.desktop"},
}

const hiddenMarker = "X-Hamstack-Hidden=true"

// systemAppDirs are the folders a desktop finds system-wide menu entries in.
func systemAppDirs() []string {
	dirs := os.Getenv("XDG_DATA_DIRS")
	if dirs == "" {
		dirs = "/usr/local/share:/usr/share"
	}
	var out []string
	for _, d := range strings.Split(dirs, ":") {
		if d != "" {
			out = append(out, filepath.Join(d, "applications"))
		}
	}
	return out
}

// hideOwnEntries hides the own entries of the radio programs that have a hamstack entry (installed). It returns the
// override files it wrote (removed by 'hamstack uninstall' like the launchers) and the entries it marked in place
// (put back from their copies).
func hideOwnEntries(userDir string, sysDirs []string, installed func(apps.App) bool) (created []string, edited []ledger.Change) {
	for _, a := range apps.All {
		if !a.Radio || !installed(a) {
			continue
		}
		for _, id := range ownMenuEntries[a.Name] {
			path := filepath.Join(userDir, id)
			if b, err := os.ReadFile(path); err == nil {
				text := string(b)
				if strings.Contains(text, hiddenMarker) && strings.Contains(text, "\nHidden=true\n") && !strings.Contains(text, "\nExec=") {
					created = append(created, path) // an override written before: still ours to remove on uninstall
					continue
				}
				if strings.Contains(text, hiddenMarker) || strings.Contains(text, "\nNoDisplay=true") || strings.Contains(text, "\nHidden=true") {
					continue // hidden already, by hamstack or by the person
				}
				eol := "\n"
				if strings.Contains(text, "\r\n") { // WSJT-X Improved's entry has Windows line endings
					eol = "\r\n"
				}
				i := strings.Index(text, "[Desktop Entry]"+eol)
				if i < 0 {
					continue
				}
				i += len("[Desktop Entry]" + eol)
				out := text[:i] + "NoDisplay=true" + eol + hiddenMarker + eol + text[i:]
				if bak := path + ".hamstack-bak"; !fileExists(bak) {
					if err := os.WriteFile(bak, b, 0o644); err != nil {
						continue
					}
				}
				if err := writeFileAtomic(path, []byte(out), 0o644); err != nil {
					continue
				}
				edited = append(edited, ledger.Change{Kind: ledger.AppConfig, Role: ledger.Support, For: "launcher", Path: path, Sum: sumOf([]byte(out))})
				continue
			}
			for _, d := range sysDirs {
				if !fileExists(filepath.Join(d, id)) {
					continue
				}
				body := "[Desktop Entry]\nType=Application\nName=" + a.Title + "\nHidden=true\n" + hiddenMarker + "\n"
				if err := os.MkdirAll(userDir, 0o755); err == nil && writeFileAtomic(path, []byte(body), 0o644) == nil {
					created = append(created, path)
				}
				break
			}
		}
	}
	return created, edited
}

// unhideOwnEntries undoes hideOwnEntries in userDir: it removes the override files it wrote and puts back the entries it
// marked (from their copies, or by taking its two lines out if the copy is gone). It returns how many it undid.
func unhideOwnEntries(userDir string) int {
	n := 0
	for _, ids := range ownMenuEntries {
		for _, id := range ids {
			path := filepath.Join(userDir, id)
			b, err := os.ReadFile(path)
			if err != nil || !strings.Contains(string(b), hiddenMarker) {
				continue
			}
			text := string(b)
			switch {
			case strings.Contains(text, "\nHidden=true\n") && !strings.Contains(text, "\nExec="):
				if os.Remove(path) == nil {
					n++
				}
			default:
				if old, err := os.ReadFile(path + ".hamstack-bak"); err == nil {
					if writeFileAtomic(path, old, 0o644) == nil {
						_ = os.Remove(path + ".hamstack-bak")
						n++
					}
					continue
				}
				text = strings.Replace(strings.Replace(text, "NoDisplay=true\r\n"+hiddenMarker+"\r\n", "", 1), "NoDisplay=true\n"+hiddenMarker+"\n", "", 1)
				if writeFileAtomic(path, []byte(text), 0o644) == nil {
					n++
				}
			}
		}
	}
	return n
}
