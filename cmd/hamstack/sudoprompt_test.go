package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ATCUSA/hamstack/internal/install"
	"github.com/ATCUSA/hamstack/internal/web"
)

// fakeSudo puts a "sudo" on PATH that accepts one password, "right", the way the real one reads it: from stdin
// with -S. It records its arguments, never the password.
func fakeSudo(t *testing.T) (argsLog string) {
	t.Helper()
	dir := t.TempDir()
	argsLog = filepath.Join(dir, "args")
	script := `#!/bin/sh
echo "$*" >> '` + argsLog + `'
case " $* " in *" -S "*) read -r pw;; esac
if [ "$pw" != right ]; then echo "Sorry, try again." >&2; echo "sudo: 1 incorrect password attempt" >&2; exit 1; fi
# run the command after the options
while [ $# -gt 0 ]; do case "$1" in -p) shift 2;; --) shift; break;; -*) shift;; *) break;; esac; done
[ $# -gt 0 ] && exec "$@"
exit 0
`
	if err := os.WriteFile(filepath.Join(dir, "sudo"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	sudoGateClear()
	t.Cleanup(sudoGateClear)
	return argsLog
}

func TestVerifySudoPassword(t *testing.T) {
	fakeSudo(t)
	var rej *web.SudoRejected
	if err := verifySudoPassword("wrong"); !errors.As(err, &rej) || rej.Locked {
		t.Fatalf("wrong password: %v", err)
	}
	if err := verifySudoPassword("right"); err != nil {
		t.Fatalf("right password: %v", err)
	}
}

func TestSudoGateLocksAfterThreeWrong(t *testing.T) {
	argsLog := fakeSudo(t)
	for i := 0; i < sudoMaxFails; i++ {
		_ = verifySudoPassword("wrong")
	}
	before, _ := os.ReadFile(argsLog)
	var rej *web.SudoRejected
	err := verifySudoPassword("right") // even the right one: the gate is shut, so sudo is not asked at all
	if !errors.As(err, &rej) || !rej.Locked {
		t.Fatalf("want locked, got %v", err)
	}
	after, _ := os.ReadFile(argsLog)
	if string(before) != string(after) {
		t.Fatal("sudo was run while the gate was shut")
	}
}

func TestSudoPasswordNeverOnCommandLine(t *testing.T) {
	argsLog := fakeSudo(t)
	_ = verifySudoPassword("hunter2-secret")
	_ = runPrivileged("hunter2-secret", "true")
	b, _ := os.ReadFile(argsLog)
	if strings.Contains(string(b), "hunter2") {
		t.Fatalf("password on a command line: %s", b)
	}
	if !strings.Contains(string(b), "-k") {
		t.Fatalf("sudo must ignore and not leave a cached credential (-k): %s", b)
	}
}

func TestJobGivesThePasswordOnlyToPrivilegedSteps(t *testing.T) {
	fakeSudo(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("HAMSTACK_TEST_JOB_HELPER", "1")
	old := jobExecutable
	jobExecutable = func() (string, error) { return os.Args[0], nil }
	defer func() { jobExecutable = old }()
	id, err := startJob(jobSpec{Title: "t", Kind: "install", Cmds: [][]string{{"-test.run=^TestJobHelperProcess$"}}, Secret: "right"})
	if err != nil {
		t.Fatal(err)
	}
	var log []byte
	for i := 0; i < 200; i++ {
		j := findJob(id)
		j.mu.Lock()
		st := j.info.State
		j.mu.Unlock()
		if st != "running" {
			log, _ = os.ReadFile(j.path)
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	text := string(log)
	if !strings.Contains(text, "privileged-ok") {
		t.Fatalf("the privileged step did not get the password:\n%s", text)
	}
	if !strings.Contains(text, "build-fds: 0 1 2 \n") {
		t.Errorf("the build step inherited a descriptor beyond 0-2:\n%s", text)
	}
	for _, leak := range []string{"right", "HAMSTACK_SUDO=", "SUDO_ASKPASS="} {
		if strings.Contains(text, leak) {
			t.Errorf("%q in the job log, so a build step could see it:\n%s", leak, text)
		}
	}
}

func TestJobPasswordWithALineBreakIsRefused(t *testing.T) {
	jobTest(t)
	if _, err := startJob(jobSpec{Title: "t", Kind: "install", Cmds: [][]string{{"-c", "true"}}, Secret: "a\nb"}); err == nil {
		t.Fatal("a password with a line break gives sudo several tries in one request")
	}
	argsLog := fakeSudo(t)
	if err := verifySudoPassword("right\nright"); err == nil {
		t.Fatal("verify accepted a password with a line break")
	}
	if b, _ := os.ReadFile(argsLog); len(b) > 0 {
		t.Fatal("sudo was asked at all")
	}
}

// TestJobHelperProcess is the hamstack a job runs in TestJobGivesThePasswordOnlyToPrivilegedSteps: it takes the
// password the way main does, then runs a build step and a privileged step.
func TestJobHelperProcess(t *testing.T) {
	if os.Getenv("HAMSTACK_TEST_JOB_HELPER") != "1" {
		t.Skip("helper process")
	}
	if err := takeJobPassword(); err != nil {
		fmt.Println("take:", err)
		os.Exit(1)
	}
	priv, err := install.HostEnv().Priv("sh", "-c", "echo privileged-ok; cat")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	steps := []install.Step{
		{Desc: "build", Cmd: []string{"sh", "-c", `env | grep -E '^(HAMSTACK_SUDO|SUDO_ASKPASS)='; printf 'build-fds: '; ls /proc/self/fd | sort -n | grep -v "^$" | while read f; do [ "$f" -le 3 ] && [ -e /proc/self/fd/$f ] && printf '%s ' "$f"; done; echo; cat`}},
		{Desc: "root", Cmd: priv},
	}
	if err := install.Run(steps, install.Options{Yes: true, In: strings.NewReader("")}); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	os.Exit(0)
}

func TestNeedSudoIsAQuestionNotAFailure(t *testing.T) {
	err := needSudo("why", []string{"sudo pacman -S x"}, "hamstack install x")
	var need *web.SudoNeeded
	if !errors.As(err, &need) || need.Why != "why" || len(need.Commands) != 1 || need.Terminal != "hamstack install x" {
		t.Fatalf("%v", err)
	}
}

func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	f()
	os.Stdout = old
	w.Close()
	b, _ := io.ReadAll(r)
	return string(b)
}
