package install

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/ATCUSA/hamstack/internal/distro"
	"github.com/ATCUSA/hamstack/internal/ledger"
)

// track looks at the system just before a step runs and returns what to call once the step has succeeded,
// which writes the changes the step made into the ledger.
func track(s Step) func() error {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return func() error { return nil }
	}
	var after []func() []ledger.Change
	if len(s.Pkgs) > 0 {
		fam := distro.Detect().Family
		if before, err := InstalledPackages(fam); err == nil {
			role := s.Role
			if role == "" {
				role = ledger.Support
			}
			after = append(after, func() []ledger.Change {
				now, err := InstalledPackages(fam)
				if err != nil {
					return nil
				}
				var added []string
				for p := range now {
					if !before[p] {
						added = append(added, p)
					}
				}
				sort.Strings(added)
				return []ledger.Change{{Kind: ledger.Packages, Role: role, For: s.For, Names: added, Manager: manager(fam)}}
			})
		}
	}
	if s.Track != nil {
		if f := s.Track(); f != nil {
			after = append(after, f)
		}
	}
	return func() error {
		var all []ledger.Change
		for _, f := range after {
			all = append(all, f()...)
		}
		return ledger.Add(home, all...)
	}
}

func manager(fam distro.Family) string {
	return map[distro.Family]string{distro.Debian: "apt", distro.Arch: "pacman", distro.Fedora: "dnf"}[fam]
}

// InstalledPackages is the set of distribution packages installed now. On Debian a name carries its
// architecture when the package can be installed for several (libfoo:arm64), as apt wants it back.
func InstalledPackages(fam distro.Family) (map[string]bool, error) {
	var out []byte
	var err error
	switch fam {
	case distro.Debian:
		out, err = exec.Command("dpkg-query", "-W", "-f", "${db:Status-Abbrev}${binary:Package}\n").Output()
	case distro.Arch:
		out, err = exec.Command("pacman", "-Qq").Output()
	case distro.Fedora:
		out, err = exec.Command("rpm", "-qa", "--qf", "%{NAME}\n").Output()
	default:
		return nil, fmt.Errorf("unsupported distribution")
	}
	if err != nil {
		return nil, err
	}
	return parsePackages(fam, string(out)), nil
}

func parsePackages(fam distro.Family, out string) map[string]bool {
	set := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		if fam == distro.Debian {
			rest, ok := strings.CutPrefix(line, "ii ") // installed, and fully so
			if !ok {
				continue
			}
			line = rest
		}
		if line = strings.TrimSpace(line); line != "" {
			set[line] = true
		}
	}
	return set
}

// shareDirs are the folders under ~/.local/share that programs commonly install into. Other folders there are
// looked into only when they are new or named after the program, because that tree also holds whatever else
// the user keeps (games, containers) and can be enormous.
var shareDirs = []string{"applications", "icons", "man", "doc", "info", "pixmaps", "metainfo", "appdata", "locale", "aclocal", "pkgconfig", "mime", "sounds"}

type fileMark struct {
	size  int64
	mtime int64
}

// snapshot notes the files under the install prefix. descend says which folders of share to go into.
func snapshot(prefix string, descend func(shareSub string) bool) map[string]fileMark {
	out := map[string]fileMark{}
	for _, top := range []string{"bin", "lib", "lib64", "libexec", "include", "etc", "share"} {
		root := filepath.Join(prefix, top)
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if d != nil && d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				if top == "share" && filepath.Dir(p) == root && !descend(d.Name()) {
					return filepath.SkipDir
				}
				return nil
			}
			if info, err := d.Info(); err == nil {
				out[p] = fileMark{info.Size(), info.ModTime().UnixNano()}
			}
			return nil
		})
	}
	return out
}

// TrackTree records the files an install step puts under the user's ~/.local for a program: everything there
// that is new or changed once the step is done.
func TrackTree(e Env, name string, alsoNamed ...string) func() func() []ledger.Change {
	return func() func() []ledger.Change {
		prefix := e.prefix()
		share := filepath.Join(prefix, "share")
		had := map[string]bool{}
		if entries, err := os.ReadDir(share); err == nil {
			for _, d := range entries {
				had[d.Name()] = true
			}
		}
		names := append([]string{name}, alsoNamed...)
		named := func(sub string) bool {
			l := strings.ToLower(sub)
			return slices.ContainsFunc(names, func(n string) bool { return n != "" && strings.Contains(l, strings.ToLower(n)) })
		}
		// hamstack's own folder there holds this ledger, not the program.
		before := snapshot(prefix, func(sub string) bool {
			return sub != "hamstack" && (slices.Contains(shareDirs, sub) || named(sub))
		})
		return func() []ledger.Change {
			after := snapshot(prefix, func(sub string) bool {
				return sub != "hamstack" && (slices.Contains(shareDirs, sub) || named(sub) || !had[sub])
			})
			var paths []string
			for p, m := range after {
				if old, ok := before[p]; !ok || old != m {
					paths = append(paths, p)
				}
			}
			sort.Strings(paths)
			return []ledger.Change{{Kind: ledger.Files, Role: ledger.App, For: name, Names: paths}}
		}
	}
}

// TrackPaths records fixed paths (a single program, or a folder that is the program's alone).
func TrackPaths(name string, paths ...string) func() func() []ledger.Change {
	return func() func() []ledger.Change {
		return func() []ledger.Change {
			return []ledger.Change{{Kind: ledger.Files, Role: ledger.App, For: name, Names: paths}}
		}
	}
}

// Removal is what can be taken off again of the packages hamstack added.
type Removal struct {
	Remove []string            // still installed, and nothing else needs them
	Gone   []string            // no longer installed: nothing to do
	Needed map[string][]string // still installed but other software now depends on them: left alone
	Cmds   [][]string          // the commands that remove Remove
}

// PlanRemoval works out which of pkgs can be removed without taking anything else with them. Packages that
// something installed since now depends on are left in place: the computer has come to need them.
func PlanRemoval(e Env, pkgs []string) (Removal, error) {
	var r Removal
	now, err := InstalledPackages(e.Distro.Family)
	if err != nil {
		return r, err
	}
	var have []string
	for _, p := range pkgs {
		if now[p] {
			have = append(have, p)
		} else {
			r.Gone = append(r.Gone, p)
		}
	}
	if len(have) == 0 {
		return r, nil
	}
	r.Needed = map[string][]string{}
	keep := have
	// The usual case is that nothing else has come to depend on them: one question to the package manager.
	if len(alsoRemovedSet(e.Distro.Family, keep)) > 0 {
		// Otherwise find which ones are needed, and leave those.
		keep = slices.DeleteFunc(slices.Clone(keep), func(p string) bool {
			others := alsoRemoved(e.Distro.Family, p, have)
			if len(others) > 0 {
				r.Needed[p] = others
			}
			return len(others) > 0
		})
		if len(keep) > 0 {
			if others := alsoRemovedSet(e.Distro.Family, keep); len(others) > 0 {
				for _, p := range keep { // could not separate them: leave them all
					r.Needed[p] = others
				}
				keep = nil
			}
		}
	}
	r.Remove = keep
	if len(keep) == 0 {
		return r, nil
	}
	var cmd []string
	switch e.Distro.Family {
	case distro.Debian:
		cmd = append([]string{"apt-get", "remove", "--purge", "-y"}, keep...)
	case distro.Arch:
		cmd = append([]string{"pacman", "-Rn", "--noconfirm"}, keep...)
	case distro.Fedora:
		cmd = append([]string{"dnf", "remove", "-y", "--noautoremove"}, keep...)
	}
	p, err := e.priv(cmd...)
	if err != nil {
		return r, err
	}
	r.Cmds = [][]string{p}
	return r, nil
}

// alsoRemoved lists what removing one package would take with it that is not in the set being removed.
func alsoRemoved(fam distro.Family, pkg string, set []string) []string {
	return outside(dependents(fam, []string{pkg}), set)
}

func alsoRemovedSet(fam distro.Family, set []string) []string {
	return outside(dependents(fam, set), set)
}

func outside(removed, set []string) []string {
	bare := func(s string) string { n, _, _ := strings.Cut(s, ":"); return n }
	in := map[string]bool{}
	for _, s := range set {
		in[bare(s)] = true
	}
	var out []string
	for _, p := range removed {
		if !in[bare(p)] && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

// dependents asks the package manager, without changing anything, which packages would go if pkgs were
// removed (including pkgs themselves where the tool lists them).
func dependents(fam distro.Family, pkgs []string) []string {
	switch fam {
	case distro.Debian:
		cmd := exec.Command("apt-get", append([]string{"-s", "remove", "--purge"}, pkgs...)...)
		cmd.Env = append(os.Environ(), "LC_ALL=C")
		out, _ := cmd.Output()
		return parseAptSimulation(string(out))
	case distro.Arch:
		// pacman refuses to remove a package that another needs, and names the one that needs it.
		cmd := exec.Command("pacman", append([]string{"-Rp", "--print-format", "%n"}, pkgs...)...)
		cmd.Env = append(os.Environ(), "LC_ALL=C")
		out, err := cmd.CombinedOutput()
		if err == nil {
			return nil
		}
		return parsePacmanBreaks(string(out))
	case distro.Fedora:
		cmd := exec.Command("rpm", append([]string{"-e", "--test"}, pkgs...)...)
		cmd.Env = append(os.Environ(), "LC_ALL=C")
		out, err := cmd.CombinedOutput()
		if err == nil {
			return nil
		}
		return parseRpmNeeded(string(out))
	}
	return nil
}

// parseAptSimulation reads the "Remv name [version]" and "Purg name [version]" lines of apt-get -s.
func parseAptSimulation(out string) []string {
	var names []string
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && (f[0] == "Remv" || f[0] == "Purg") {
			names = append(names, f[1])
		}
	}
	return names
}

// parsePacmanBreaks reads "removing X breaks dependency 'Y' required by Z" and gives the Zs.
func parsePacmanBreaks(out string) []string {
	var names []string
	for _, line := range strings.Split(out, "\n") {
		if _, by, ok := strings.Cut(line, " required by "); ok {
			names = append(names, strings.TrimSpace(by))
		}
	}
	if len(names) == 0 {
		names = []string{"(pacman could not check)"}
	}
	return names
}

// parseRpmNeeded reads "X is needed by (installed) Y-1.2-3.fc45.x86_64" and gives the Ys.
func parseRpmNeeded(out string) []string {
	var names []string
	for _, line := range strings.Split(out, "\n") {
		if _, by, ok := strings.Cut(line, " is needed by (installed) "); ok {
			names = append(names, strings.TrimSpace(by))
		}
	}
	if len(names) == 0 {
		names = []string{"(rpm could not check)"}
	}
	return names
}

// RemoveFiles deletes recorded files under prefix, and the folders they leave empty. A recorded folder is
// removed whole only when it is a program's own folder under share or lib. Anything outside prefix is ignored:
// the ledger is a file in the user's home, and must not be able to point a deletion somewhere else.
func RemoveFiles(prefix string, paths []string) (removed int, skipped []string) {
	prefix = filepath.Clean(prefix)
	dirs := map[string]bool{}
	for _, p := range paths {
		p = filepath.Clean(p)
		rel, err := filepath.Rel(prefix, p)
		if err != nil || rel == "." || strings.HasPrefix(rel, "..") || !filepath.IsAbs(p) {
			skipped = append(skipped, p)
			continue
		}
		parts := strings.Split(rel, string(filepath.Separator))
		info, err := os.Lstat(p)
		if err != nil {
			continue // already gone
		}
		if info.IsDir() {
			if len(parts) < 2 || (parts[0] != "share" && parts[0] != "lib") || parts[1] == "hamstack" {
				skipped = append(skipped, p)
				continue
			}
			if err := os.RemoveAll(p); err != nil {
				skipped = append(skipped, p)
				continue
			}
			removed++
			continue
		}
		if err := os.Remove(p); err != nil {
			skipped = append(skipped, p)
			continue
		}
		removed++
		for d := filepath.Dir(p); ; d = filepath.Dir(d) {
			r, err := filepath.Rel(prefix, d)
			if err != nil || strings.Count(r, string(filepath.Separator)) < 1 || strings.HasPrefix(r, "..") {
				break // leave ~/.local/bin, ~/.local/share and the like, even when empty
			}
			dirs[d] = true
		}
	}
	// Deepest first, so a folder emptied by removing its children can go too.
	list := make([]string, 0, len(dirs))
	for d := range dirs {
		list = append(list, d)
	}
	sort.Slice(list, func(i, j int) bool { return len(list[i]) > len(list[j]) })
	for _, d := range list {
		_ = os.Remove(d) // fails, rightly, unless empty
	}
	return removed, skipped
}

// Prefix is where hamstack installs what it builds or downloads (~/.local).
func (e Env) Prefix() string { return e.prefix() }
