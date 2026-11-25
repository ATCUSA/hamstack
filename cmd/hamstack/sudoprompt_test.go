package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ATCUSA/hamstack/internal/web"
)

// fakeSudo puts a "sudo" on PATH that accepts one password, "right", the way the real one reads it: from stdin
// with -S, or from the SUDO_ASKPASS helper with -A. It records its arguments, never the password.
func fakeSudo(t *testing.T) (argsLog string) {
	t.Helper()
	dir := t.TempDir()
	argsLog = filepath.Join(dir, "args")
	script := `#!/bin/sh
echo "$*" >> '` + argsLog + `'
mode=""
for a in "$@"; do
  case "$a" in -S) mode=stdin;; -A) mode=askpass;; esac
done
if [ "$mode" = stdin ]; then read pw; fi
if [ "$mode" = askpass ]; then pw=$("$SUDO_ASKPASS" "[sudo] password"); fi
if [ "$pw" != right ]; then echo "Sorry, try again." >&2; echo "sudo: 1 incorrect password attempt" >&2; exit 1; fi
# run the command after the options
while [ $# -gt 0 ]; do case "$1" in -*) shift;; -p) shift 2;; *) break;; esac; done
[ $# -gt 0 ] && [ "$1" != "" ] && exec "$@"
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

func TestAskpassServesPasswordToSudoAndCleansUp(t *testing.T) {
	fakeSudo(t)
	ap, err := startAskpass("/nonexistent/hamstack", "right")
	if err != nil {
		t.Fatal(err)
	}
	dir := ap.dir
	fi, err := os.Stat(dir)
	if err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("private dir: %v %v", fi, err)
	}
	t.Setenv("HAMSTACK_ASKPASS_SOCK", filepath.Join(dir, "s"))
	out := captureStdout(t, func() {
		if err := cmdAskpass([]string{"[sudo] password"}); err != nil {
			t.Fatal(err)
		}
	})
	if out != "right\n" {
		t.Fatalf("askpass printed %q", out)
	}
	if !strings.Contains(strings.Join(ap.env(), " "), "HAMSTACK_SUDO=askpass") {
		t.Fatal("env must switch install.Priv to sudo -k -A")
	}
	ap.close()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("socket directory must be removed when the job ends")
	}
	for _, b := range ap.secret {
		if b != 0 {
			t.Fatal("password not wiped from memory")
		}
	}
}

func TestJobWithSecretNeverWritesItToTheLog(t *testing.T) {
	fakeSudo(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	// The "hamstack" the job runs is a script that calls sudo -k -A like install does and prints what happens.
	dir := t.TempDir()
	helper := filepath.Join(dir, "hamstack")
	body := "#!/bin/sh\nif [ \"$1\" = askpass ]; then HAMSTACK_TEST_ASKPASS=1 exec " + os.Args[0] + " -test.run=TestAskpassHelperProcess; fi\n" +
		"echo \"env: $HAMSTACK_SUDO\"\nsudo -k -A true && echo privileged-ok\n"
	_ = os.WriteFile(helper, []byte(body), 0o755)
	old := jobExecutable
	jobExecutable = func() (string, error) { return helper, nil }
	defer func() { jobExecutable = old }()
	id, err := startJob(jobSpec{Title: "t", Kind: "install", Cmds: [][]string{{"x"}}, Secret: "right"})
	if err != nil {
		t.Fatal(err)
	}
	var j *job
	for i := 0; i < 100; i++ {
		if j = findJob(id); j != nil {
			j.mu.Lock()
			st := j.info.State
			j.mu.Unlock()
			if st != "running" {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	log, _ := os.ReadFile(j.path)
	if strings.Contains(string(log), "right\n") || strings.Contains(string(log), "Sorry") {
		t.Fatalf("password in log or not accepted: %s", log)
	}
	if !strings.Contains(string(log), "privileged-ok") {
		t.Fatalf("sudo -A did not get the password from the helper: %s", log)
	}
	if !strings.Contains(string(log), "env: askpass") {
		t.Fatalf("job did not get HAMSTACK_SUDO=askpass: %s", log)
	}
}

func TestNeedSudoIsAQuestionNotAFailure(t *testing.T) {
	err := needSudo("why", []string{"sudo pacman -S x"}, "hamstack install x")
	var need *web.SudoNeeded
	if !errors.As(err, &need) || need.Why != "why" || len(need.Commands) != 1 || need.Terminal != "hamstack install x" {
		t.Fatalf("%v", err)
	}
}

// TestAskpassHelperProcess is the "hamstack askpass" the test's fake hamstack script runs.
func TestAskpassHelperProcess(t *testing.T) {
	if os.Getenv("HAMSTACK_TEST_ASKPASS") != "1" {
		t.Skip("helper process")
	}
	if err := cmdAskpass(nil); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
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
