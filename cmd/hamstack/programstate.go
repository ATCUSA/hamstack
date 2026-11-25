package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/ATCUSA/hamstack/internal/catalog"
	"github.com/ATCUSA/hamstack/internal/distro"
	"github.com/ATCUSA/hamstack/internal/install"
	"github.com/ATCUSA/hamstack/internal/ledger"
	"github.com/ATCUSA/hamstack/internal/tui"
	"github.com/ATCUSA/hamstack/internal/updates"
	"github.com/ATCUSA/hamstack/internal/versions"
)

// What an "update available" row needs to say: who owns the program, what the distribution offers, whether the
// package is held back, and how to go back.

var distroFacts struct {
	sync.Mutex
	at        time.Time
	installed map[string]bool
	held      map[string]bool
	candidate map[string]string
}

// distroSnapshot reads, at most every couple of minutes, which packages are installed and which are held.
func distroSnapshot(fam distro.Family) (installed, held map[string]bool) {
	distroFacts.Lock()
	defer distroFacts.Unlock()
	if distroFacts.installed != nil && time.Since(distroFacts.at) < 2*time.Minute {
		return distroFacts.installed, distroFacts.held
	}
	distroFacts.installed, _ = install.InstalledPackages(fam)
	if distroFacts.installed == nil {
		distroFacts.installed = map[string]bool{}
	}
	distroFacts.held = heldPackages(fam)
	distroFacts.candidate = map[string]string{}
	distroFacts.at = time.Now()
	return distroFacts.installed, distroFacts.held
}

// heldPackages are the packages the person has told the package manager not to update.
func heldPackages(fam distro.Family) map[string]bool {
	held := map[string]bool{}
	switch fam {
	case distro.Debian:
		if out, err := exec.Command("apt-mark", "showhold").Output(); err == nil {
			for _, f := range strings.Fields(string(out)) {
				held[f] = true
			}
		}
	case distro.Arch:
		if f, err := os.Open("/etc/pacman.conf"); err == nil {
			defer f.Close()
			sc := bufio.NewScanner(f)
			for sc.Scan() {
				if rest, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "IgnorePkg"); ok {
					_, list, _ := strings.Cut(rest, "=")
					for _, p := range strings.Fields(list) {
						held[p] = true
					}
				}
			}
		}
	}
	return held
}

var candidateLine = regexp.MustCompile(`(?m)^\s*Candidate:\s*(\S+)`)

// distroCandidate is the version the distribution would install for a package now.
func distroCandidate(fam distro.Family, name string) string {
	distroFacts.Lock()
	if v, ok := distroFacts.candidate[name]; ok {
		distroFacts.Unlock()
		return v
	}
	distroFacts.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	v := ""
	switch fam {
	case distro.Debian:
		if out, err := exec.CommandContext(ctx, "apt-cache", "policy", name).Output(); err == nil {
			if m := candidateLine.FindStringSubmatch(string(out)); m != nil && m[1] != "(none)" {
				v = versions.UpstreamVersion(m[1])
			}
		}
	case distro.Arch:
		if out, err := exec.CommandContext(ctx, "pacman", "-Si", name).Output(); err == nil {
			for _, l := range strings.Split(string(out), "\n") {
				if k, val, ok := strings.Cut(l, ":"); ok && strings.TrimSpace(k) == "Version" {
					v = versions.UpstreamVersion(strings.TrimSpace(val))
				}
			}
		}
	}
	distroFacts.Lock()
	if distroFacts.candidate != nil {
		distroFacts.candidate[name] = v
	}
	distroFacts.Unlock()
	return v
}

// builtByHamstack reports whether the ledger records files hamstack installed under ~/.local for a program.
func builtByHamstack(name string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	changes, _ := ledger.Load(home)
	for _, c := range changes {
		if c.Kind == ledger.Files && c.For == name && len(c.Names) > 0 {
			return true
		}
	}
	return false
}

// programFacts adds to a program row what the Updates page needs: the distribution's package, its candidate
// version, whether it is held, and what rolling back would do.
func programFacts(row *tui.Program, p catalog.Package) {
	fam := distro.Detect().Family
	installed, held := distroSnapshot(fam)
	sys := ""
	for _, n := range p.Distro[string(fam)] {
		if installed[n] {
			sys = n
			break
		}
	}
	if row.Origin == "system package" && sys != "" {
		row.Package, row.Held = sys, held[sys]
		row.Candidate = distroCandidate(fam, sys)
	}
	if builtByHamstack(p.Name) {
		row.Rollback = "remove"
		if sys != "" {
			row.Rollback = "distro"
		}
	}
}

// rollbackProgram takes out the files hamstack installed for a program under ~/.local. If the distribution's
// package is installed too, that is what is used again; if not, the program is gone.
func rollbackProgram(name string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	changes, err := ledger.Load(home)
	if err != nil {
		return "", err
	}
	var rec *ledger.Change
	for i := range changes {
		if changes[i].Kind == ledger.Files && changes[i].For == name {
			rec = &changes[i]
		}
	}
	if rec == nil {
		return "", fmt.Errorf("hamstack has no record of installing %s, so it will not guess which files to remove", name)
	}
	n, skipped := install.RemoveFiles(install.HostEnv().Prefix(), rec.Names)
	_ = os.Remove(filepath.Join(home, ".local", "share", "hamstack", "installed", name))
	if err := ledger.Drop(home, func(c ledger.Change) bool { return c.Kind == ledger.Files && c.For == name }); err != nil {
		return "", err
	}
	msg := fmt.Sprintf("Removed hamstack's copy of %s (%d files).", name, n)
	if len(skipped) > 0 {
		msg += fmt.Sprintf(" %d paths were left alone.", len(skipped))
	}
	return msg + " Restart anything that was using it.", nil
}

// rollbackSelf puts the previous hamstack program back, and keeps the one being replaced as the new "previous", so
// the same step goes forward again. Running processes keep the program they started with.
func rollbackSelf() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return err
	}
	return swapPrevious(exe)
}

func swapPrevious(exe string) error {
	prev, swap := exe+".previous", exe+".swap"
	if !fileExists(prev) {
		return fmt.Errorf("there is no previous hamstack to go back to (%s)", prev)
	}
	_ = os.Remove(swap)
	if err := os.Link(exe, swap); err != nil {
		return err
	}
	if err := os.Rename(prev, exe); err != nil {
		_ = os.Remove(swap)
		return err
	}
	return os.Rename(swap, prev)
}

// previousSelfVersion asks the kept previous program for its version, or "".
func previousSelfVersion() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return ""
	}
	prev := exe + ".previous"
	if !fileExists(prev) {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, prev, "version").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(out)), "hamstack"))
}

// The update list asks the package manager what it would install, so that a newer upstream release the
// distribution does not package yet is not shown as an update (updates.NotPackaged).
func init() {
	updates.DistroCandidate = func(p catalog.Package) string {
		fam := distro.Detect().Family
		installed, _ := distroSnapshot(fam)
		for _, n := range p.Distro[string(fam)] {
			if installed[n] {
				return distroCandidate(fam, n)
			}
		}
		return ""
	}
}
