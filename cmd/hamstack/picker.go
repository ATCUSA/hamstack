package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/ATCUSA/hamstack/internal/catalog"
	"github.com/ATCUSA/hamstack/internal/distro"
	"github.com/ATCUSA/hamstack/internal/versions"
)

// installMethod says how a program would be installed on this system.
func installMethod(p catalog.Package, fam distro.Family) string {
	f := string(fam)
	if p.Source != nil && p.Source.NotOn[f] != "" {
		p.Source = nil // a recipe that cannot build here is no recipe
	}
	switch {
	case customPlan(p.Name):
		switch p.Name {
		case "hamlib":
			return "distribution package, or built from source"
		case "direwolf":
			if f == "arch" {
				return "built from source"
			}
			return "distribution package, or built from source"
		}
		return "downloaded or built from source"
	case len(p.Distro[f]) > 0 && p.Source != nil:
		return "distribution package, or built from source"
	case len(p.Distro[f]) > 0:
		return "distribution package"
	case p.Source != nil:
		return "built from source"
	case f == "arch" && len(p.AUR) > 0:
		return "AUR only (you install it)"
	}
	return "not available here"
}

// cmdInstallList prints every program with what it is, whether it is installed, and how it would be installed.
func cmdInstallList() error {
	fam := distro.Detect().Family
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PROGRAM\tWHAT\tINSTALLED\tHOW\tNEEDS\tSUGGESTS")
	for _, c := range catalog.Categories {
		for _, p := range catalog.All {
			if p.Category != c.ID {
				continue
			}
			inst := "-"
			if in := versions.FindInstalled(p); in != nil {
				inst = orDash(in.Version)
				if in.Origin == "user install" {
					inst += " (built by you)"
				}
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", p.Name, p.Desc, inst, installMethod(p, fam), orDash(strings.Join(p.Requires, ",")), orDash(strings.Join(p.Suggests, ",")))
		}
	}
	return tw.Flush()
}

// pickPrograms shows the catalog by category and lets the user choose what to install, by number or name.
// Programs the configured setup needs and that are missing are pre-selected.
func pickPrograms(a *asker, needed []string) ([]string, error) {
	fam := distro.Detect().Family
	var order []catalog.Package
	fmt.Fprintln(a.out, "Programs hamstack can install (and keep up to date):")
	n := 0
	for _, c := range catalog.Categories {
		first := true
		for _, p := range catalog.All {
			if p.Category != c.ID {
				continue
			}
			if first {
				fmt.Fprintf(a.out, "\n%s\n", c.Title)
				first = false
			}
			n++
			order = append(order, p)
			state := "not installed"
			if in := versions.FindInstalled(p); in != nil {
				state = "installed " + in.Version
				if in.Version == "" {
					state = "installed"
				}
			}
			mark := ""
			if slices.Contains(needed, p.Name) {
				mark = "  <- your setup needs it"
			}
			fmt.Fprintf(a.out, "  %2d) %-15s %s\n      [%s; %s]%s\n", n, p.Name, p.Desc, state, installMethod(p, fam), mark)
			if len(p.Requires) > 0 {
				fmt.Fprintf(a.out, "      needs: %s\n", strings.Join(p.Requires, ", "))
			}
		}
	}
	def := ""
	if len(needed) > 0 {
		def = strings.Join(needed, " ")
	}
	prompt := "\nNumbers or names to install, separated by spaces (all, or Enter for none): "
	if def != "" {
		prompt = fmt.Sprintf("\nNumbers or names to install, separated by spaces [%s]: ", def)
	}
	for {
		ans, ok := a.line(prompt)
		if !ok {
			return nil, fmt.Errorf("nothing chosen")
		}
		if ans == "" {
			ans = def
		}
		if ans == "" {
			return nil, nil
		}
		picked, bad := parsePick(ans, order)
		if len(bad) > 0 {
			fmt.Fprintf(a.out, "not in the list: %s\n", strings.Join(bad, ", "))
			continue
		}
		return picked, nil
	}
}

// parsePick reads numbers and names ("1 4 pat", "all") against the listed programs.
func parsePick(ans string, order []catalog.Package) (picked, bad []string) {
	add := func(n string) {
		if !slices.Contains(picked, n) {
			picked = append(picked, n)
		}
	}
	for _, w := range strings.FieldsFunc(ans, func(r rune) bool { return r == ' ' || r == ',' }) {
		if strings.EqualFold(w, "all") {
			for _, p := range order {
				add(p.Name)
			}
			continue
		}
		if i, err := strconv.Atoi(w); err == nil && i >= 1 && i <= len(order) {
			add(order[i-1].Name)
			continue
		}
		if p, ok := catalog.ByName(strings.ToLower(w)); ok {
			add(p.Name)
			continue
		}
		bad = append(bad, w)
	}
	return picked, bad
}

// expandSelection turns what was asked for into what to install: it refuses programs that conflict, adds what the
// chosen ones require (saying why), and offers what they suggest. With no terminal (or --yes) suggestions are only
// listed, never added.
func expandSelection(in io.Reader, out io.Writer, picked []string, ask bool) ([]string, error) {
	all, added := catalog.Closure(picked)
	// After the closure: something a chosen program requires can conflict too.
	if c := catalog.Conflicts(all); len(c) > 0 {
		return nil, fmt.Errorf("%s and %s provide the same program and cannot both be installed: choose one", c[0][0], c[0][1])
	}
	for _, n := range all {
		if why, ok := added[n]; ok {
			fmt.Fprintf(out, "also installing %s (%s)\n", n, why)
		}
	}
	names, from := catalog.Suggestions(all)
	if len(names) == 0 {
		return all, nil
	}
	fmt.Fprintln(out, "\nSuggested with these (not required):")
	for _, n := range names {
		p, _ := catalog.ByName(n)
		fmt.Fprintf(out, "  %-10s %s  (suggested by %s)\n", n, p.Desc, from[n])
	}
	if !ask {
		fmt.Fprintf(out, "Add them with: hamstack install %s\n", strings.Join(names, " "))
		return all, nil
	}
	a := &asker{in: bufio.NewReader(in), out: out}
	ans, ok := a.line("Add any? Names separated by spaces, all, or Enter for none: ")
	if !ok || ans == "" {
		return all, nil
	}
	var extra []string
	for _, w := range strings.Fields(strings.ReplaceAll(ans, ",", " ")) {
		switch {
		case strings.EqualFold(w, "all"):
			extra = append(extra, names...)
		case slices.Contains(names, strings.ToLower(w)):
			extra = append(extra, strings.ToLower(w))
		default:
			fmt.Fprintf(out, "ignoring %q (not one of the suggestions)\n", w)
		}
	}
	if len(extra) == 0 {
		return all, nil
	}
	return expandSelection(in, out, append(slices.Clone(all), extra...), false)
}
