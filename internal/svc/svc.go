// Package svc wraps `systemctl --user` and `journalctl --user`.
package svc

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// State is a unit's runtime state.
type State struct {
	Load    string // loaded, not-found
	Active  string // active, inactive, failed, activating...
	Sub     string // running, dead, failed...
	Enabled string // enabled, disabled, static, ...
	Since   string // ActiveEnterTimestamp, empty if never
	// ExitStatus is the exit code of the service's main process the last time it ran.
	ExitStatus int
}

// ExitRadioMissing is the exit status of a hamstack service whose radio is not there (hamstack's units
// list it in RestartPreventExitStatus, so there is no restart loop).
const ExitRadioMissing = 3

// NoRadio reports a service that stopped because its radio, receiver or sound card is not plugged in:
// that is waiting for hardware, not a fault in the service.
func (s State) NoRadio() bool { return s.Active == "failed" && s.ExitStatus == ExitRadioMissing }

// Display is the state as a person should read it.
func (s State) Display() string {
	if s.NoRadio() {
		return "no radio"
	}
	return s.Active
}

func systemctl(args ...string) ([]byte, error) {
	return exec.Command("systemctl", append([]string{"--user"}, args...)...).CombinedOutput()
}

// Available reports whether a systemd user manager is reachable.
func Available() error {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return fmt.Errorf("systemctl not found: hamstack needs systemd")
	}
	if out, err := systemctl("show-environment"); err != nil {
		return fmt.Errorf("no systemd user session: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// Status returns the state of one unit.
func Status(unit string) State {
	out, _ := systemctl("show", unit, "-p", "LoadState,ActiveState,SubState,UnitFileState,ActiveEnterTimestamp,ExecMainStatus")
	var s State
	for _, line := range strings.Split(string(out), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch k {
		case "LoadState":
			s.Load = v
		case "ActiveState":
			s.Active = v
		case "SubState":
			s.Sub = v
		case "UnitFileState":
			s.Enabled = v
		case "ActiveEnterTimestamp":
			s.Since = v
		case "ExecMainStatus":
			s.ExitStatus, _ = strconv.Atoi(v)
		}
	}
	return s
}

// Run executes a systemctl verb (start, stop, restart, enable, disable, daemon-reload) on units.
func Run(verb string, units ...string) error {
	out, err := systemctl(append([]string{verb}, units...)...)
	if err != nil {
		return fmt.Errorf("systemctl %s %s: %s", verb, strings.Join(units, " "), strings.TrimSpace(string(out)))
	}
	return nil
}

// Logs streams the journal for a unit to stdout.
func Logs(unit string, follow bool, lines int) error {
	args := []string{"--user", "-u", unit, "-n", fmt.Sprint(lines), "--no-pager"}
	if follow {
		args = append(args, "-f")
	}
	cmd := exec.Command("journalctl", args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

// LogsText returns the last lines of a unit's journal.
func LogsText(unit string, lines int) (string, error) {
	out, err := exec.Command("journalctl", "--user", "-u", unit, "-n", fmt.Sprint(lines), "--no-pager", "-o", "short").CombinedOutput()
	return string(out), err
}
