package svc

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strings"

	"github.com/ATCUSA/hamstack/internal/ledger"
)

// LingerEnabled reports whether the current user's services keep running (and start at boot)
// without an open login session.
func LingerEnabled() (bool, error) {
	u, err := user.Current()
	if err != nil {
		return false, err
	}
	out, err := exec.Command("loginctl", "show-user", u.Username, "-p", "Linger", "--value").Output()
	if err != nil {
		return false, fmt.Errorf("loginctl: %w", err)
	}
	return strings.TrimSpace(string(out)) == "yes", nil
}

// SetLinger turns lingering on or off for the current user. It first tries without privileges
// (polkit allows a user to manage their own), then with sudo.
func SetLinger(on bool) error {
	u, err := user.Current()
	if err != nil {
		return err
	}
	verb := "disable-linger"
	if on {
		verb = "enable-linger"
	}
	was, wasErr := LingerEnabled()
	err = exec.Command("loginctl", verb, u.Username).Run()
	if err != nil {
		if _, lookErr := exec.LookPath("sudo"); lookErr != nil {
			return fmt.Errorf("could not change lingering and sudo is not available: run 'loginctl %s %s' as root", verb, u.Username)
		}
		cmd := exec.Command("sudo", "loginctl", verb, u.Username)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = stdio()
		if err = cmd.Run(); err != nil {
			return err
		}
	}
	// Note it for 'hamstack uninstall' only when hamstack is what turned it on; turning it off again by hand
	// settles the matter.
	if home, herr := os.UserHomeDir(); herr == nil {
		switch {
		case on && wasErr == nil && !was:
			_ = ledger.Add(home, ledger.Change{Kind: ledger.Linger, Role: ledger.Support, User: u.Username})
		case !on:
			_ = ledger.Drop(home, func(c ledger.Change) bool { return c.Kind == ledger.Linger && c.User == u.Username })
		}
	}
	return nil
}
