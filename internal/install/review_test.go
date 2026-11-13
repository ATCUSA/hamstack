package install

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// A program that is running cannot be written in place ("text file busy"); an update must still replace it.
func TestInstallProgramReplacesARunningProgram(t *testing.T) {
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no sleep program")
	}
	dir := t.TempDir()
	dest := filepath.Join(dir, "bin", "prog")
	if err := installProgram(sleep, dest); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(dest, "30")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot run a program from %s: %v", dir, err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	if err := os.WriteFile(dest, []byte("x"), 0o755); !errors.Is(err, syscall.ETXTBSY) {
		t.Logf("writing a running program in place gave %v here (expected text file busy)", err)
	}
	built := filepath.Join(dir, "built")
	if err := os.WriteFile(built, []byte("#!/bin/sh\necho new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := installProgram(built, dest); err != nil {
		t.Fatalf("replacing a running program: %v", err)
	}
	st, err := os.Stat(dest)
	if err != nil || st.Mode().Perm() != 0o755 {
		t.Errorf("mode %v %v", st, err)
	}
	if b, _ := os.ReadFile(dest); !strings.Contains(string(b), "echo new") {
		t.Errorf("not replaced: %q", b)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "bin", ".hamstack-new-*")); len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}
	if err := installProgram(filepath.Join(dir, "missing"), dest); err == nil || !strings.Contains(err.Error(), "did not produce") {
		t.Errorf("a missing build result: %v", err)
	}
}

func TestRunCleansUpWhateverHappens(t *testing.T) {
	for _, fail := range []bool{false, true} {
		var order []string
		steps := []Step{
			{Desc: "download", Do: func() error { return nil }, Cleanup: func() { order = append(order, "clean-download") }},
			{Desc: "build", Do: func() error {
				if fail {
					return errors.New("compiler exploded")
				}
				return nil
			}, Cleanup: func() { order = append(order, "clean-build") }},
			{Desc: "install", Do: func() error { order = append(order, "install"); return nil }, Cleanup: func() { order = append(order, "clean-install") }},
		}
		var out bytes.Buffer
		err := Run(steps, Options{Yes: true, Out: &out})
		want := "install clean-install clean-build clean-download"
		if fail {
			want = "clean-build clean-download" // the step that never started has nothing to clean
		}
		if (err != nil) != fail || strings.Join(order, " ") != want {
			t.Errorf("fail=%v: err %v, order %v", fail, err, order)
		}
	}
	// Declining, or a dry run, starts nothing and so cleans nothing.
	cleaned := false
	steps := []Step{{Desc: "x", Do: func() error { return nil }, Cleanup: func() { cleaned = true }}}
	_ = Run(steps, Options{DryRun: true, Out: &bytes.Buffer{}})
	_ = Run(steps, Options{In: strings.NewReader("n\n"), Out: &bytes.Buffer{}})
	if cleaned {
		t.Error("cleanup ran for a step that never started")
	}
}

func TestDownloadRefusesARedirectFromHTTPSToHTTP(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("tampered")) }))
	defer plain.Close()
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ok" {
			w.Write([]byte("fine"))
			return
		}
		http.Redirect(w, r, plain.URL, http.StatusFound)
	}))
	defer secure.Close()
	old := downloadClient
	downloadClient = &http.Client{Transport: secure.Client().Transport, CheckRedirect: noDowngrade}
	defer func() { downloadClient = old }()

	dest := filepath.Join(t.TempDir(), "f")
	err := Download(context.Background(), secure.URL+"/src.tar.gz", dest, "")
	if err == nil || !strings.Contains(err.Error(), "refusing a redirect from HTTPS") {
		t.Fatalf("a downgrade must be refused, got %v", err)
	}
	if _, err := os.Stat(dest); err == nil {
		t.Error("the tampered file was kept")
	}
	if err := Download(context.Background(), secure.URL+"/ok", dest, ""); err != nil {
		t.Fatalf("an HTTPS download must still work: %v", err)
	}
}

func TestDownloadHasASizeLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(bytes.Repeat([]byte("x"), 4096)) }))
	defer srv.Close()
	old := maxDownload
	maxDownload = 1024
	defer func() { maxDownload = old }()
	dir := t.TempDir()
	if err := Download(context.Background(), srv.URL, filepath.Join(dir, "f"), ""); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("got %v", err)
	}
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Errorf("files left behind: %v", left)
	}
}

func TestFetchSourceUnpacksAndCleansUpAfterItself(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	good, _ := os.ReadFile(makeTarGz(t, map[string]string{"proj-1.0/Makefile": "all:"}))
	flat, _ := os.ReadFile(makeTarGz(t, map[string]string{"a/x": "1", "b/y": "2"}))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/good":
			w.Write(good)
		case "/flat":
			w.Write(flat)
		case "/junk":
			w.Write([]byte("not a tarball"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	work, src, err := fetchSource("proj", srv.URL+"/good", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(src, "Makefile")); err != nil || !strings.HasPrefix(src, work) {
		t.Errorf("source at %s (work %s): %v", src, work, err)
	}
	removeWork(work)
	for _, bad := range []string{"/flat", "/junk", "/missing"} {
		if w, _, err := fetchSource("proj", srv.URL+bad, ""); err == nil || w != "" {
			t.Errorf("%s: work %q err %v", bad, w, err)
		}
	}
	if _, _, err := fetchSource("proj", srv.URL+"/good", "00"); err == nil {
		t.Error("a wrong checksum must fail")
	}
	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		t.Errorf("temporary folders left behind: %v", left)
	}
	removeWork("") // nothing to remove is not an error (and must not remove the current folder)
}

func TestInstallStagedSkipsSpecialFilesAndDropsSetuid(t *testing.T) {
	stage, prefix := t.TempDir(), filepath.Join(t.TempDir(), ".local")
	rel := filepath.Join(stage, prefix)
	put(t, rel, "bin/tool", "binary", 0o755)
	if err := os.Chmod(filepath.Join(rel, "bin/tool"), 0o755|os.ModeSetuid); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(rel, "bin", "pipe"), 0o644); err != nil {
		t.Skipf("cannot make a named pipe: %v", err)
	}
	n, err := InstallStaged(stage+"/", prefix) // a trailing slash must not confuse the path arithmetic
	if err != nil || n != 1 {
		t.Fatalf("copied %d: %v", n, err)
	}
	st, err := os.Stat(filepath.Join(prefix, "bin/tool"))
	if err != nil || st.Mode()&os.ModeSetuid != 0 || st.Mode().Perm() != 0o755 {
		t.Errorf("mode %v %v", st.Mode(), err)
	}
	if _, err := os.Lstat(filepath.Join(prefix, "bin/pipe")); err == nil {
		t.Error("a named pipe was installed")
	}
}

// The script RootFileStep builds is run by a shell as root, so its quoting has to hold for any content and path.
func TestRootFileStepQuoting(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "it's a dir; $(touch pwned)")
	path := filepath.Join(dir, "ax ports")
	marker := filepath.Join(t.TempDir(), "pwned")
	content := "# comment with 'quotes' and \"doubles\"\nhamstack\tK0TEST-1\t1200\t255\t2\t$(touch " + marker + ") `touch " + marker + "` %s \\n ;rm -rf ~\n"
	run := func(content string) {
		t.Helper()
		s, err := RootFileStep(Env{Root: true}, "write", path, content)
		if err != nil {
			t.Fatal(err)
		}
		if len(s.Cmd) != 3 || s.Cmd[0] != "sh" || s.Cmd[1] != "-c" {
			t.Fatalf("command %q", s.Cmd)
		}
		cmd := exec.Command(s.Cmd[0], s.Cmd[1:]...)
		cmd.Dir = t.TempDir()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
	run("first\n")
	run(content)
	if b, _ := os.ReadFile(path); string(b) != content {
		t.Errorf("written %q, want %q", b, content)
	}
	if b, _ := os.ReadFile(path + ".hamstack-bak"); string(b) != "first\n" {
		t.Errorf("backup %q", b)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("content was executed by the shell")
	}
	if s, err := RootFileStep(Env{Sudo: true}, "write", "/etc/x", "y"); err != nil || s.Cmd[0] != "sudo" {
		t.Errorf("not root: %q %v", s.Cmd, err)
	}
	if _, err := RootFileStep(Env{}, "write", "/etc/x", "y"); err == nil {
		t.Error("no root and no sudo must be an error")
	}
}
