package install

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A build step is downloaded code running as the user. It must find no way to the dashboard's sudo password:
// no askpass helper or socket named in its environment, and no inherited descriptor.
func TestUnprivilegedStepCannotReachTheSudoPassword(t *testing.T) {
	t.Setenv("HAMSTACK_SUDO", "askpass")
	t.Setenv("SUDO_ASKPASS", "/run/user/1000/hamstack-askpass-x/askpass")
	t.Setenv("HAMSTACK_ASKPASS_SOCK", "/run/user/1000/hamstack-askpass-x/s")
	useSudoPasswordForTest(t, "right")
	var out bytes.Buffer
	step := Step{Desc: "configure", Cmd: []string{"sh", "-c", `env | grep -E '^(HAMSTACK_SUDO|SUDO_ASKPASS|HAMSTACK_ASKPASS_SOCK)=' ; ls /proc/self/fd | tr '\n' ' '; echo; cat`}}
	if err := Run([]Step{step}, Options{Yes: true, Out: &out, In: strings.NewReader("")}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, leak := range []string{"HAMSTACK_SUDO=", "SUDO_ASKPASS=", "HAMSTACK_ASKPASS_SOCK=", "right"} {
		if strings.Contains(got, leak) {
			t.Errorf("a build step can see %q:\n%s", leak, got)
		}
	}
}

// fakeSudo is a "sudo" on PATH that wants the password "right" on stdin when given -S, or reads nothing at all
// when nopasswd is set (a NOPASSWD rule). It then runs the command after its options.
func fakeSudo(t *testing.T, nopasswd bool) {
	t.Helper()
	dir := t.TempDir()
	read := `read -r pw; [ "$pw" = right ] || { echo "Sorry, try again." >&2; exit 1; }`
	if nopasswd {
		read = ":"
	}
	script := "#!/bin/sh\n" + read + `
while [ $# -gt 0 ]; do case "$1" in -p) shift 2;; --) shift; break;; -*) shift;; *) break;; esac; done
exec "$@"
`
	if err := os.WriteFile(filepath.Join(dir, "sudo"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestPrivilegedStepGetsThePasswordOnItsOwnStdin(t *testing.T) {
	fakeSudo(t, false)
	useSudoPasswordForTest(t, "right")
	argv, err := Env{Sudo: true}.priv("sh", "-c", "echo privileged-ok; cat")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(argv[:5], " ") != "sudo -k -S -p " {
		t.Fatalf("want sudo -k -S -p '': %q", argv)
	}
	var out bytes.Buffer
	if err := Run([]Step{{Desc: "x", Cmd: argv}}, Options{Yes: true, Out: &out}); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "privileged-ok") || strings.Contains(out.String(), "right") {
		t.Fatalf("sudo did not get the password, or the command saw it:\n%s", out.String())
	}
}

// With a NOPASSWD rule sudo never reads the password, so what is left on its input would reach the command and,
// if the command prints its input, the job log. The command's own input must be empty whatever sudo does.
func TestPasswordNeverSpillsIntoTheCommandWhenSudoDoesNotAsk(t *testing.T) {
	fakeSudo(t, true)
	useSudoPasswordForTest(t, "right")
	argv, _ := Env{Sudo: true}.priv("cat")
	var out bytes.Buffer
	if err := Run([]Step{{Desc: "x", Cmd: argv}}, Options{Yes: true, Out: &out}); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if strings.Contains(out.String(), "right") {
		t.Fatalf("the password reached the command's input:\n%s", out.String())
	}
}

func TestReadSudoPasswordTakesItFromTheDescriptorAndClosesIt(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.WriteString("right")
	w.Close()
	t.Cleanup(ForgetSudoPassword)
	if err := ReadSudoPassword(r); err != nil {
		t.Fatal(err)
	}
	if !hasSudoPassword() {
		t.Fatal("password not kept")
	}
	if _, err := r.Read(make([]byte, 1)); err == nil {
		t.Fatal("descriptor left open")
	}
	ForgetSudoPassword()
	if hasSudoPassword() {
		t.Fatal("password not forgotten")
	}
}

func TestReadSudoPasswordRefusesMoreThanOneLine(t *testing.T) {
	r, w, _ := os.Pipe()
	_, _ = w.WriteString("a\nb")
	w.Close()
	t.Cleanup(ForgetSudoPassword)
	if err := ReadSudoPassword(r); err == nil || hasSudoPassword() {
		t.Fatal("a password with a line break would give sudo two tries in one")
	}
}

func useSudoPasswordForTest(t *testing.T, pw string) {
	t.Helper()
	sudoPassword.Lock()
	sudoPassword.b = []byte(pw)
	sudoPassword.Unlock()
	t.Cleanup(ForgetSudoPassword)
}
