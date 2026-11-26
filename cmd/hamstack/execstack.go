package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ATCUSA/hamstack/internal/catalog"
	"github.com/ATCUSA/hamstack/internal/install"
)

// execStackPrograms are the programs in ~/.local/bin (where hamstack's source builds go) that a recipe says need an
// executable stack. A distribution's own copy in /usr/bin is the distribution's business.
func execStackPrograms(home string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range catalog.All {
		if p.Source == nil {
			continue
		}
		for _, name := range p.Source.ExecStack {
			path := filepath.Join(home, ".local", "bin", name)
			if seen[path] {
				continue
			}
			seen[path] = true
			if _, err := os.Stat(path); err == nil {
				out = append(out, path)
			}
		}
	}
	return out
}

// execStackMissing is the doctor's view: those programs that are not marked yet.
func execStackMissing() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	var bad []string
	for _, p := range execStackPrograms(home) {
		if need, _ := install.NeedsExecStack(p); need {
			bad = append(bad, p)
		}
	}
	return bad
}

// appRepair marks the programs hamstack built that need an executable stack, for builds made before the recipe did.
func appRepair() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	done := 0
	for _, p := range execStackPrograms(home) {
		changed, err := install.MarkExecStack(p)
		if err != nil {
			return err
		}
		if changed {
			fmt.Printf("marked %s as needing an executable stack (its Fortran code builds code on the stack)\n", p)
			done++
		}
	}
	if done == 0 {
		fmt.Println("nothing to repair")
	} else {
		fmt.Println("restart the program for the change to take effect")
	}
	return nil
}
