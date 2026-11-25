package install

import (
	"fmt"

	"github.com/ATCUSA/hamstack/internal/ledger"
)

// GroupStep adds username to a group (for example uucp or dialout, which own USB serial ports).
func GroupStep(e Env, group, username, why string) (Step, error) {
	cmd, err := e.priv("usermod", "-aG", group, username)
	if err != nil {
		return Step{}, err
	}
	return Step{Desc: fmt.Sprintf("Add you to the %q group (%s)", group, why), Cmd: cmd, Track: func() func() []ledger.Change {
		return func() []ledger.Change {
			// Serial and sound access is for the radio programs: an uninstall that keeps them keeps this.
			return []ledger.Change{{Kind: ledger.Group, Role: ledger.App, Names: []string{group}, User: username}}
		}
	}}, nil
}
