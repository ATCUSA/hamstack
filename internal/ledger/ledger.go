// Package ledger records every change hamstack makes outside its own folders, so that "hamstack uninstall" can
// put the computer back the way it was: the distribution packages it added, the root-owned files it wrote, the
// groups it added the user to, and so on. A change is written down only when it really was a change: a package
// that was already installed, or a group the user was already in, is not hamstack's to take away again.
package ledger

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"time"
)

// Kinds of change.
const (
	Packages = "packages"  // distribution packages that were not installed before (Names)
	RootFile = "root-file" // a root-owned file written by hamstack (Path; Existed if one was there before)
	Sysctl   = "sysctl"    // a kernel setting changed while running (Key, and Prev: its value before)
	Group    = "group"     // the user was added to a group (Names holds the one group, User the account)
	Linger   = "linger"    // systemd lingering was switched on for User
	Modules  = "modules"   // kernel modules loaded (Names)
	Firewall = "firewall"  // the nftables helper, its watcher service and sudo rule (User: the uid)
	Files    = "files"     // files installed under the user's ~/.local for a program (Names: absolute paths)
	// AppConfig is a ham program's settings file that hamstack filled in with the radio (Path; Edits are the
	// keys it set; Sum is the hash of the file as hamstack left it, so an untouched file can simply be removed).
	AppConfig = "app-config"
)

// Edit is one setting hamstack changed in a program's settings file. Old is what it was ("" when it was not
// there or was empty), so it can be put back, but only while the setting still has the value hamstack gave it.
type Edit struct {
	Key string `json:"key"`
	Old string `json:"old"`
	New string `json:"new"`
}

// Roles say what a package or file is for, which decides whether "keep the apps" keeps it.
const (
	App     = "app"     // a ham radio program the user runs (Pat, WSJT-X, hamlib...), or its setup
	Build   = "build"   // compilers and libraries a program was built with: the program may need the libraries
	Support = "support" // something only hamstack itself needs (Xvnc, noVNC, nftables)
)

// Change is one recorded change.
type Change struct {
	Kind    string    `json:"kind"`
	Role    string    `json:"role,omitempty"`
	For     string    `json:"for,omitempty"` // the program or feature it was for: "wsjtx", "web", "ax25"
	Names   []string  `json:"names,omitempty"`
	Path    string    `json:"path,omitempty"`
	Existed bool      `json:"existed,omitempty"`
	Key     string    `json:"key,omitempty"`
	Prev    string    `json:"prev,omitempty"`
	User    string    `json:"user,omitempty"`
	Manager string    `json:"manager,omitempty"` // apt, pacman or dnf, for packages
	Edits   []Edit    `json:"edits,omitempty"`   // AppConfig: what was set in the file
	Sum     string    `json:"sum,omitempty"`     // AppConfig: sha256 of the file as hamstack wrote it
	When    time.Time `json:"when"`
}

// Path is where the ledger lives for a home directory. It is outside the config and state folders so that
// clearing those does not lose it.
func Path(home string) string {
	return filepath.Join(home, ".local", "share", "hamstack", "changes.json")
}

// Load reads the ledger; a missing file is an empty ledger.
func Load(home string) ([]Change, error) {
	b, err := os.ReadFile(Path(home))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Change
	return out, json.Unmarshal(b, &out)
}

// Add records changes, merging them into what is already there: the first record of a file or setting wins,
// because it holds the state from before hamstack touched it.
func Add(home string, changes ...Change) error {
	if len(changes) == 0 {
		return nil
	}
	have, err := Load(home)
	if err != nil {
		return err
	}
	return save(home, Merge(have, changes...))
}

// Merge is Add without the file.
func Merge(have []Change, changes ...Change) []Change {
	for _, c := range changes {
		if c.When.IsZero() {
			c.When = time.Now().UTC().Truncate(time.Second)
		}
		i := slices.IndexFunc(have, func(h Change) bool { return same(h, c) })
		if i < 0 {
			if (c.Kind == Packages || c.Kind == Files || c.Kind == Modules) && len(c.Names) == 0 {
				continue
			}
			have = append(have, c)
			continue
		}
		switch c.Kind {
		case AppConfig:
			have[i].Sum = c.Sum // the settings it first found stay on record; what the file is now is the latest
		case Packages, Files, Modules:
			for _, n := range c.Names {
				if !slices.Contains(have[i].Names, n) {
					have[i].Names = append(have[i].Names, n)
				}
			}
			sort.Strings(have[i].Names)
		}
	}
	// A package belongs to one record: if it was first pulled in as support and is asked for again as an app
	// (or the reverse), the first record keeps it.
	seen := map[string]bool{}
	for i := range have {
		if have[i].Kind != Packages {
			continue
		}
		have[i].Names = slices.DeleteFunc(have[i].Names, func(n string) bool {
			if seen[n] {
				return true
			}
			seen[n] = true
			return false
		})
	}
	return slices.DeleteFunc(have, func(c Change) bool { return c.Kind == Packages && len(c.Names) == 0 })
}

// same reports whether two changes are records of the same thing.
func same(a, b Change) bool {
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case Packages, Files:
		return a.Role == b.Role && a.For == b.For && a.Manager == b.Manager
	case RootFile, AppConfig:
		return a.Path == b.Path
	case Sysctl:
		return a.Key == b.Key
	case Group:
		return a.User == b.User && slices.Equal(a.Names, b.Names)
	case Linger, Firewall:
		return a.User == b.User
	case Modules:
		return true
	}
	return false
}

func save(home string, changes []Change) error {
	p := Path(home)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(changes, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".changes-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}

// Drop removes the records that match, after the uninstaller has undone them.
func Drop(home string, match func(Change) bool) error {
	have, err := Load(home)
	if err != nil || len(have) == 0 {
		return err
	}
	left := slices.DeleteFunc(have, match)
	if len(left) == 0 {
		err := os.Remove(Path(home))
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return save(home, left)
}
