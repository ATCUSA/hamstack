package main

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
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
// job's environment or arguments, not to disk. sudo is always run with -k, so it asks every time and leaves no
// cached credential behind for anything else to ride on.

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

// askpass is the per-job password server. sudo -A runs a helper program to get the password; the helper is
// "hamstack askpass", which asks this server over a socket that lives in a private directory (0700, the user's own)
// and exists only while the job runs. The job's input stays /dev/null.
type askpass struct {
	dir    string
	ln     net.Listener
	secret []byte
	done   chan struct{}
}

func startAskpass(self, secret string) (*askpass, error) {
	base := os.Getenv("XDG_RUNTIME_DIR")
	if fi, err := os.Stat(base); err != nil || !fi.IsDir() {
		base = ""
	}
	dir, err := os.MkdirTemp(base, "hamstack-askpass-")
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	sock := filepath.Join(dir, "s")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	script := filepath.Join(dir, "askpass")
	body := "#!/bin/sh\nHAMSTACK_ASKPASS_SOCK='" + sock + "' exec '" + self + "' askpass \"$@\"\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		ln.Close()
		os.RemoveAll(dir)
		return nil, err
	}
	a := &askpass{dir: dir, ln: ln, secret: []byte(secret), done: make(chan struct{})}
	go a.serve()
	return a, nil
}

func (a *askpass) serve() {
	for {
		c, err := a.ln.Accept()
		if err != nil {
			close(a.done)
			return
		}
		_, _ = c.Write(append(append([]byte{}, a.secret...), '\n'))
		c.Close()
	}
}

// env is what the job's command needs to use sudo through the helper.
func (a *askpass) env() []string {
	return []string{"HAMSTACK_SUDO=askpass", "SUDO_ASKPASS=" + filepath.Join(a.dir, "askpass")}
}

// close removes the socket and forgets the password.
func (a *askpass) close() {
	_ = a.ln.Close()
	<-a.done
	for i := range a.secret {
		a.secret[i] = 0
	}
	_ = os.RemoveAll(a.dir)
}

// cmdAskpass is the helper sudo -A runs: it prints the password the job's server holds. Not for people to run.
func cmdAskpass(args []string) error {
	sock := os.Getenv("HAMSTACK_ASKPASS_SOCK")
	if sock == "" {
		return fmt.Errorf("askpass is run by sudo from a dashboard job")
	}
	c, err := net.DialTimeout("unix", sock, 3*time.Second)
	if err != nil {
		return err
	}
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	b, err := io.ReadAll(io.LimitReader(c, 4096))
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(b)
	return err
}

var _ = install.HostEnv
