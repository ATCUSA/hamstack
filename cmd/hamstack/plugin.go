package main

import (
	"fmt"
	"os"

	"github.com/ATCUSA/hamstack/internal/plugin"
)

// cmdPlugin: "hamstack plugin validate FILE" checks a plugin manifest against the draft spec (docs/plugins/spec.md).
// The runtime that installs and runs plugins is not built yet; the validator is the ground truth for the spec.
func cmdPlugin(args []string) error {
	if len(args) != 2 || args[0] != "validate" {
		return fmt.Errorf("usage: hamstack plugin validate FILE   (plugin support is a draft: see docs/plugins/)")
	}
	b, err := os.ReadFile(args[1])
	if err != nil {
		return err
	}
	m, err := plugin.Parse(b)
	if err != nil {
		return err
	}
	problems := m.Validate()
	if len(problems) == 0 {
		fmt.Printf("%s: valid (plugin %q, schema %d)\n", args[1], m.ID, m.Schema)
		return nil
	}
	for _, p := range problems {
		fmt.Println("  -", p)
	}
	return fmt.Errorf("%s: %d problem(s)", args[1], len(problems))
}
