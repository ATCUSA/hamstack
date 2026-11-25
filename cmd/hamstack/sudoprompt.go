package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strings"
	"sync"
	"time"

	"github.com/ATCUSA/hamstack/internal/install"
	"github.com/ATCUSA/hamstack/internal/web"
)

// Asking for the administrator password from the dashboard.
//
// The page shows why it is needed and what would run as root, and only then sends the password, for that one action.
// It is kept in memory for as long as the action runs and never written anywhere: not to the job log, not to the
// job's environment or arguments, not to disk. A job's command gets it on a socket it reads and closes at once, and
// gives it only to the sudo of each privileged step, on that sudo's input (internal/install/sudo.go), so the
// unprivileged steps of a source build, which run downloaded code, have no way to it. sudo is always run with -k,
// so it asks every time and leaves no cached credential behind for anything else to ride on.

// sudoGate limits wrong passwords. Every wrong one is an entry in the system log and counts towards the account's
// lock-out (pam_faillock), so after a few the dashboard stops asking for a while rather than let a page, or someone
// at it, guess.
var sudoGate struct {
	sync.Mutex
	fails []time.Time
}

const (
	sudoMaxFails = 3
	sudoFailSpan = 15 * time.Minute
)

func sudoGateOpen() (bool, time.Duration) {
	sudoGate.Lock()
	defer sudoGate.Unlock()
	keep := sudoGate.fails[:0]
	for _, t := range sudoGate.fails {
		if time.Since(t) < sudoFailSpan {
			keep = append(keep, t)
		}
	}
	sudoGate.fails = keep
	if len(keep) >= sudoMaxFails {
		return false, sudoFailSpan - time.Since(keep[0])
	}
	return true, 0
}

func sudoGateFail() {
	sudoGate.Lock()
	sudoGate.fails = append(sudoGate.fails, time.Now())
	sudoGate.Unlock()
}

func sudoGateClear() {
	sudoGate.Lock()
	sudoGate.fails = nil
	sudoGate.Unlock()
}

// needSudo is the question the page asks the person: why, what would run as root, and what to do instead.
func needSudo(why string, cmds []string, terminal ...string) error {
	name := ""
	if u, err := user.Current(); err == nil {
		name = u.Username
	}
	return &web.SudoNeeded{User: name, Why: why, Commands: cmds, Terminal: strings.Join(terminal, "\n")}
}

// verifySudoPassword checks the password with one real sudo attempt on its own pipe: sudo -k ignores any cached
// credential, so a yes means this password, and it does not leave one behind.
func verifySudoPassword(secret string) error {
	if err := install.CheckSudoPassword(secret); err != nil {
		return &web.SudoRejected{Message: "That password was not accepted: " + err.Error() + "."}
	}
	if ok, wait := sudoGateOpen(); !ok {
		return &web.SudoRejected{Locked: true, Message: fmt.Sprintf(
			"Too many wrong passwords. To protect the account from being locked, the dashboard will not ask again for %d minutes. Run the command on the computer instead.", int(wait.Minutes())+1)}
	}
	cmd := exec.Command("sudo", "-k", "-S", "-p", "", "-v")
	cmd.Stdin = strings.NewReader(secret + "\n")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	if err := cmd.Run(); err != nil {
		text := strings.ToLower(out.String())
		switch {
		case strings.Contains(text, "not in the sudoers") || strings.Contains(text, "may not run sudo") || strings.Contains(text, "not allowed"):
			return fmt.Errorf("this account is not allowed to use sudo, so the dashboard cannot do this")
		case strings.Contains(text, "incorrect password") || strings.Contains(text, "try again") || strings.Contains(text, "authentication failure"):
			sudoGateFail()
			return &web.SudoRejected{Message: "That password was not accepted."}
		}
		return fmt.Errorf("sudo could not be used: %s", firstLine(out.String()))
	}
	sudoGateClear()
	return nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return "no reason given"
	}
	return s
}

// runPrivileged runs one command as root with the password on its own pipe, for the power commands (a reboot has no
// job to hand the password to). The password never appears on the command line.
func runPrivileged(secret string, argv ...string) error {
	if err := verifySudoPassword(secret); err != nil {
		return err
	}
	cmd := exec.Command("sudo", append([]string{"-k", "-S", "-p", ""}, argv...)...)
	cmd.Stdin = strings.NewReader(secret + "\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s failed: %s: %w", argv[0], firstLine(string(out)), err)
	}
	return nil
}
