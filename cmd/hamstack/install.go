package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/ATCUSA/hamstack/internal/catalog"
	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/distro"
	"github.com/ATCUSA/hamstack/internal/install"
	"github.com/ATCUSA/hamstack/internal/versions"
)

// splitArgs separates flags from positional arguments so flags can appear anywhere.
func splitArgs(args []string) (flags, pos []string) {
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
		} else {
			pos = append(pos, a)
		}
	}
	return
}

func catalogPkg(name string) (catalog.Package, bool) {
	for _, p := range catalog.All {
		if p.Name == name {
			return p, true
		}
	}
	return catalog.Package{}, false
}

// installable is what "install all" means: the Winlink and packet stack. Everything else in the catalog is
// installed by name, or chosen from the list that "hamstack install" shows.
var installable = []string{install.Hamlib, install.Pat, install.Mercury, install.Direwolf}

// custom are the programs with a plan of their own in the install package; the rest use the catalog's recipe.
func customPlan(name string) bool { return slices.Contains(installable, name) }

// canBuildFromSource reports whether --from-source can build a program: hamlib and Direwolf have their own
// build plans, and any program with a recipe in the catalog.
func canBuildFromSource(name string) bool {
	if name == install.Hamlib || name == install.Direwolf {
		return true
	}
	p, ok := catalog.ByName(name)
	return ok && p.Source != nil
}

func cmdInstall(args []string) error {
	flags, pos := splitArgs(args)
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	dry := fs.Bool("dry-run", false, "show the plan, change nothing")
	yes := fs.Bool("yes", false, "do not ask for confirmation")
	force := fs.Bool("force", false, "reinstall even if already installed")
	fromSource := fs.Bool("from-source", false, "build from source instead of using the distribution's package (newest release; installs compilers)")
	list := fs.Bool("list", false, "list every program, how it would be installed, and what it needs")
	if err := fs.Parse(flags); err != nil {
		return err
	}
	if *list {
		return cmdInstallList()
	}
	interactive := !*yes && !*dry && isTerminal() && stdinIsTerminal()
	switch {
	case len(pos) > 0 && pos[0] == "all":
		pos = slices.Clone(installable)
	case len(pos) == 0:
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		needed := packagesFor(cfg.Components()) // what your setup needs (everything if you have not chosen)
		if interactive {
			var missing []string
			for _, n := range needed {
				if p, ok := catalog.ByName(n); ok && versions.FindInstalled(p) == nil {
					missing = append(missing, n)
				}
			}
			a := &asker{in: bufio.NewReader(os.Stdin), out: os.Stdout}
			if pos, err = pickPrograms(a, missing); err != nil {
				return err
			}
			if len(pos) == 0 {
				fmt.Println("Nothing chosen.")
				return nil
			}
		} else {
			pos = needed
		}
	}
	for _, n := range pos {
		if _, ok := catalog.ByName(n); !ok {
			return fmt.Errorf("unknown program %q (see: hamstack install --list)", n)
		}
	}
	requested := pos
	pos, err := expandSelection(os.Stdin, os.Stdout, pos, interactive)
	if err != nil {
		return err
	}
	for _, n := range pos {
		if c := installedConflict(n); c != "" && !*force {
			return fmt.Errorf("%s conflicts with %s, which is installed (they provide the same program): remove %s first, or install anyway with --force", n, c, c)
		}
	}
	src := map[string]bool{}
	if *fromSource {
		for _, n := range requested { // only what was asked for: what it needs comes however it comes
			if !canBuildFromSource(n) {
				return fmt.Errorf("%s cannot be built from source by hamstack (it is already built from source, or has no recipe yet)", n)
			}
			src[n] = true
		}
	}
	steps, notice, err := buildInstallStepsSrc(pos, *force, src)
	if err != nil {
		return err
	}
	if err := install.Run(steps, install.Options{DryRun: *dry, Yes: *yes, Notice: notice}); err != nil {
		return err
	}
	if !*dry {
		pathHint()
	}
	return nil
}

// packagesFor maps components to the packages that provide them.
func packagesFor(components []string) []string {
	var out []string
	for _, c := range components {
		if c == config.RigCtld {
			out = append(out, install.Hamlib) // rigctld is part of hamlib
		} else {
			out = append(out, c)
		}
	}
	return out
}

// joinNotices combines the disclaimers of several source builds, skipping empty ones.
func joinNotices(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + "\n" + b
}

// buildInstallSteps plans the installation of names, skipping those already present unless force.
func buildInstallSteps(names []string, force bool) ([]install.Step, error) {
	steps, _, err := buildInstallStepsSrc(names, force, nil)
	return steps, err
}

// buildInstallStepsSrc is buildInstallSteps with a set of packages to build from source. It also
// returns a notice (disclaimer) to show before the plan.
func buildInstallStepsSrc(names []string, force bool, fromSource map[string]bool) ([]install.Step, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	env := install.HostEnv()
	var steps []install.Step
	var notice string
	for _, n := range names {
		pkg, _ := catalogPkg(n)
		if in := versions.FindInstalled(pkg); in != nil && !force && !fromSource[n] { // a source build replaces what is installed
			fmt.Printf("%s: already installed (%s %s), skipping (use --force to reinstall)\n", n, in.Path, in.Version)
			continue
		}
		var s []install.Step
		var err error
		switch n {
		case install.Hamlib:
			if fromSource[n] {
				var ver string
				if s, ver, err = install.HamlibSourcePlan(ctx, env); err == nil {
					notice = joinNotices(notice, install.HamlibSourceNotice(env, ver))
				}
			} else {
				s, err = install.HamlibPlan(env)
			}
		case install.Direwolf:
			var nt string
			s, nt, err = install.DirewolfPlan(ctx, env, fromSource[n])
			notice = joinNotices(notice, nt)
		case install.Pat:
			s, err = install.PatPlan(ctx, env)
		default:
			var latest string
			if len(pkg.Distro[string(env.Distro.Family)]) == 0 || fromSource[n] {
				latest, _ = versions.Latest(ctx, pkg) // "" when it cannot be looked up: AppPlan says so if a build needs it
			}
			var nt string
			s, nt, err = install.AppPlan(ctx, env, pkg, latest, fromSource[n])
			notice = joinNotices(notice, nt)
			if n == install.GPSD {
				notice = joinNotices(notice, install.GPSDNotice(env))
			}
		case install.Mercury:
			var latest string
			if latest, err = versions.Latest(ctx, pkg); err != nil {
				return nil, "", fmt.Errorf("looking up the latest Mercury release: %w", err)
			}
			if s, err = install.MercuryPlan(ctx, env, latest); err == nil {
				notice = joinNotices(notice, install.MercurySourceNotice(env, latest))
			}
		}
		if err != nil {
			return nil, "", fmt.Errorf("%s: %w", n, err)
		}
		steps = append(steps, s...)
	}
	return steps, notice, nil
}

// pathHint warns if ~/.local/bin is not on PATH, where hamstack installs what it builds or downloads.
func pathHint() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	local := filepath.Join(home, ".local", "bin")
	for _, d := range filepath.SplitList(os.Getenv("PATH")) {
		if filepath.Clean(d) == local {
			return
		}
	}
	fmt.Fprintf(os.Stderr, "\nnote: %s is not on your PATH, and that is where hamstack installs what it builds or downloads (Pat, Mercury, anything built from source).\n"+
		"Add it to PATH (for example in ~/.profile) and open a new shell.\n", local)
}

func cmdUpdate(args []string) error {
	flags, _ := splitArgs(args)
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	dry := fs.Bool("dry-run", false, "show the plan, change nothing")
	yes := fs.Bool("yes", false, "do not ask for confirmation")
	if err := fs.Parse(flags); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var mine []string
	var system []string
	srcSet := map[string]bool{}
	for _, p := range catalog.All {
		n, pkg := p.Name, p
		in := versions.FindInstalled(pkg)
		if in == nil || in.Version == "" {
			continue
		}
		latest, err := versions.Latest(ctx, pkg)
		if err != nil {
			if !errors.Is(err, versions.ErrNoUpstream) {
				fmt.Fprintf(os.Stderr, "note: %s: could not check upstream: %v\n", n, err)
			}
			continue
		}
		if versions.Compare(in.Version, latest) >= 0 {
			fmt.Printf("%s %s: up to date\n", n, in.Version)
			continue
		}
		if in.Origin == "system package" {
			system = append(system, fmt.Sprintf("%s %s -> %s", n, in.Version, latest))
		} else {
			fmt.Printf("%s %s -> %s: will update\n", n, in.Version, latest)
			mine = append(mine, n)
			if canBuildFromSource(n) {
				srcSet[n] = true // one in ~/.local was built from source: rebuild it the same way
			}
		}
	}
	if len(system) > 0 {
		fmt.Printf("\nInstalled by your package manager (update them with it; the distro package may lag upstream):\n")
		for _, s := range system {
			fmt.Println("  " + s)
		}
		fmt.Println("  " + systemUpdateHint(distro.Detect()))
	}
	if len(mine) == 0 {
		return nil
	}
	fmt.Println()
	steps, notice, err := buildInstallStepsSrc(mine, true, srcSet)
	if err != nil {
		return err
	}
	return install.Run(steps, install.Options{DryRun: *dry, Yes: *yes, Notice: notice})
}

func systemUpdateHint(d distro.Info) string {
	switch d.Family {
	case distro.Arch:
		return "sudo pacman -Syu   (or your AUR helper for AUR packages)"
	case distro.Debian:
		return "sudo apt-get update && sudo apt-get upgrade"
	case distro.Fedora:
		return "sudo dnf upgrade"
	}
	return "use your distribution's package manager"
}

// installedConflict returns an installed program that cannot be installed together with name, or "".
func installedConflict(name string) string {
	p, ok := catalog.ByName(name)
	if !ok {
		return ""
	}
	for _, c := range p.Conflicts {
		if q, ok := catalog.ByName(c); ok && versions.FindInstalled(q) != nil {
			return c
		}
	}
	return ""
}
