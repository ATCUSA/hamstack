package install

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

// The administrator password a dashboard job was given.
//
// The job runner hands it to this process on an inherited socket (SudoPasswordEnv names it), main reads it before
// anything else runs and closes the socket, and from then on it lives only in this process's memory. Each step that
// priv built gets it on its own standard input (sudo -S); no other step can reach it: there is no askpass helper,
// socket, file or environment value for downloaded build code (which runs as the same user) to find and use.
// What this does not protect against: a program running as the same user that may read this process's memory
// (ptrace, allowed between same-user processes when kernel.yama.ptrace_scope is 0).
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

// passwordInput is the input for a step: the password line for a command priv built with it, nil otherwise.
func passwordInput(cmd []string) io.Reader {
	if len(cmd) < len(sudoPrefix) {
		return nil
	}
	for i, a := range sudoPrefix {
		if cmd[i] != a {
			return nil
		}
	}
	sudoPassword.Lock()
	defer sudoPassword.Unlock()
	if len(sudoPassword.b) == 0 {
		return nil
	}
	return bytes.NewReader(append(append([]byte{}, sudoPassword.b...), '\n'))
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
