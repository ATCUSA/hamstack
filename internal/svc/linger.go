package svc

import (
	"fmt"
	"os/exec"
	"os/user"
	"strings"
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
	if err := exec.Command("loginctl", verb, u.Username).Run(); err == nil {
		return nil
	}
	if _, err := exec.LookPath("sudo"); err != nil {
		return fmt.Errorf("could not change lingering and sudo is not available: run 'loginctl %s %s' as root", verb, u.Username)
	}
	cmd := exec.Command("sudo", "loginctl", verb, u.Username)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdio()
	return cmd.Run()
}
