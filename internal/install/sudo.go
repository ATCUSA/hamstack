package install

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"syscall"
)

// The administrator password a dashboard job was given.
//
// The job runner hands it to this process on an inherited socket (SudoPasswordEnv names it), main reads it before
// anything else runs and closes the socket, and from then on it lives only in this process's memory. Each step that
// priv built gets it on its own standard input (sudo -S); no other step can reach it: there is no askpass helper,
// socket, file or environment value for downloaded build code (which runs as the same user) to find and use.
// The password reaches sudo on a socket, not a pipe (a pipe can be reopened through /proc/PID/fd), and only the
// system's own sudo (root-owned, setuid, in /usr/bin or /bin) is given it, never a "sudo" found first on PATH: a
// build could put one in ~/.local/bin.
// What this does not protect against: a program running as the same user that may read this process's memory
// (ptrace, allowed between same-user processes when kernel.yama.ptrace_scope is 0), or one that replaces a
// user-writable hamstack binary (~/.local/bin/hamstack) before the next job starts it.
var sudoPassword struct {
	sync.Mutex
	b []byte
}

// SudoPasswordEnv, set to "fd", tells a hamstack started by a dashboard job that descriptor 3 holds the password.
const SudoPasswordEnv = "HAMSTACK_SUDO"

// SudoPasswordFD is the descriptor the job runner passes the password on.
const SudoPasswordFD = 3

// sudoPrefix is how priv runs a command when it has the password. -k: ignore any cached credential and leave none
// behind. -S -p "": read the password from standard input, print no prompt. The command then runs with its input
// from /dev/null, so that if sudo never reads the password (a NOPASSWD rule), it is not left for the command to read.
var sudoPrefix = []string{"sudo", "-k", "-S", "-p", "", "--", "sh", "-c", `exec "$@" </dev/null`, "sh"}

// ReadSudoPassword takes the password from f (the job runner's socket) and closes f.
func ReadSudoPassword(f *os.File) error {
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil {
		return fmt.Errorf("reading the administrator password from the dashboard: %w", err)
	}
	if err := CheckSudoPassword(string(b)); err != nil {
		clear(b)
		return err
	}
	sudoPassword.Lock()
	sudoPassword.b = b
	sudoPassword.Unlock()
	return nil
}

// CheckSudoPassword refuses what cannot be one password line for sudo: a line break would give sudo a second and
// third try in one request, and the rest would be left on the command's input.
func CheckSudoPassword(s string) error {
	switch {
	case s == "":
		return fmt.Errorf("no administrator password was given")
	case len(s) > 4096:
		return fmt.Errorf("the administrator password is too long")
	case strings.ContainsAny(s, "\r\n\x00"):
		return fmt.Errorf("the administrator password cannot contain a line break")
	}
	return nil
}

// ForgetSudoPassword wipes the password from memory.
func ForgetSudoPassword() {
	sudoPassword.Lock()
	clear(sudoPassword.b)
	sudoPassword.b = nil
	sudoPassword.Unlock()
}

func hasSudoPassword() bool {
	sudoPassword.Lock()
	defer sudoPassword.Unlock()
	return len(sudoPassword.b) > 0
}

// usesPassword reports whether cmd is one priv built with the password.
func usesPassword(cmd []string) bool {
	if len(cmd) < len(sudoPrefix) || !hasSudoPassword() {
		return false
	}
	for i, a := range sudoPrefix {
		if cmd[i] != a {
			return false
		}
	}
	return true
}

// passwordInput is the input for a step priv built with the password: a socket holding the password line, then
// the end of input.
func passwordInput() (*os.File, error) {
	sudoPassword.Lock()
	line := append(append(make([]byte, 0, len(sudoPassword.b)+1), sudoPassword.b...), '\n')
	sudoPassword.Unlock()
	defer clear(line)
	return PasswordSocket(line)
}

// PasswordSocket returns one end of a new socket pair with b written into it and the other end closed, so the
// reader gets b and then the end of input. A socket rather than a pipe: a pipe could be reopened through /proc.
func PasswordSocket(b []byte) (*os.File, error) {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("could not pass the administrator password on: %w", err)
	}
	ours, theirs := os.NewFile(uintptr(fds[0]), "sudo-password"), os.NewFile(uintptr(fds[1]), "sudo-password")
	_, err = ours.Write(b)
	ours.Close()
	if err != nil {
		theirs.Close()
		return nil, fmt.Errorf("could not pass the administrator password on: %w", err)
	}
	return theirs, nil
}

// TrustedSudo is the sudo the password may be given to. A variable so that a test can use a fake one.
var TrustedSudo = func() (string, error) { return trustedSudoIn("/usr/bin/sudo", "/bin/sudo") }

// trustedSudoIn returns the first of paths that is a root-owned setuid program nobody else can write.
func trustedSudoIn(paths ...string) (string, error) {
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		st, ok := fi.Sys().(*syscall.Stat_t)
		if ok && st.Uid == 0 && fi.Mode().IsRegular() && fi.Mode()&os.ModeSetuid != 0 && fi.Mode().Perm()&0o022 == 0 {
			return p, nil
		}
	}
	return "", fmt.Errorf("the system's sudo was not found in /usr/bin or /bin (root-owned and setuid), so the password is not given to any other")
}

// stepEnv is the environment for a step: hamstack's own, without anything that once led to the password.
func stepEnv(extra []string) []string {
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch k {
		case SudoPasswordEnv, "SUDO_ASKPASS", "HAMSTACK_ASKPASS_SOCK":
			continue
		}
		env = append(env, kv)
	}
	return append(env, extra...)
}
