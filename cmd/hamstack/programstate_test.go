package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ATCUSA/hamstack/internal/ledger"
)

func TestSwapPreviousGoesBackAndForward(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "hamstack")
	os.WriteFile(exe, []byte("new"), 0o755)
	os.WriteFile(exe+".previous", []byte("old"), 0o755)
	if err := swapPrevious(exe); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "old" {
		t.Errorf("running program after rollback: %q", b)
	}
	if b, _ := os.ReadFile(exe + ".previous"); string(b) != "new" {
		t.Errorf("the one replaced must be kept so it can be put back: %q", b)
	}
	if err := swapPrevious(exe); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "new" {
		t.Errorf("rolling forward: %q", b)
	}
	if fileExists(exe + ".swap") {
		t.Error("no scratch file may be left")
	}
	os.Remove(exe + ".previous")
	if err := swapPrevious(exe); err == nil || !strings.Contains(err.Error(), "no previous") {
		t.Errorf("nothing to go back to: %v", err)
	}
}

func TestRollbackProgramRemovesOnlyWhatWasRecorded(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	bin := filepath.Join(home, ".local", "bin")
	os.MkdirAll(bin, 0o755)
	mine, other, outside := filepath.Join(bin, "wsjtx"), filepath.Join(bin, "my-script"), filepath.Join(home, "logbook.adi")
	for _, p := range []string{mine, other, outside} {
		os.WriteFile(p, []byte("x"), 0o755)
	}
	os.MkdirAll(filepath.Join(home, ".local", "share", "hamstack", "installed"), 0o755)
	os.WriteFile(filepath.Join(home, ".local", "share", "hamstack", "installed", "wsjtx"), []byte("3.0\n"), 0o644)
	// A hand-edited ledger must not be able to point a removal outside ~/.local.
	if err := ledger.Add(home, ledger.Change{Kind: ledger.Files, Role: ledger.App, For: "wsjtx", Names: []string{mine, outside}}); err != nil {
		t.Fatal(err)
	}
	msg, err := rollbackProgram("wsjtx")
	if err != nil || !strings.Contains(msg, "Removed") {
		t.Fatalf("%q %v", msg, err)
	}
	if fileExists(mine) {
		t.Error("the recorded program must go")
	}
	if !fileExists(other) || !fileExists(outside) {
		t.Error("what was not recorded, or is outside ~/.local, must stay")
	}
	if fileExists(filepath.Join(home, ".local", "share", "hamstack", "installed", "wsjtx")) {
		t.Error("the version note goes with it")
	}
	if c, _ := ledger.Load(home); len(c) != 0 {
		t.Errorf("the record is dropped once undone: %+v", c)
	}
	if _, err := rollbackProgram("wsjtx"); err == nil || !strings.Contains(err.Error(), "no record") {
		t.Errorf("without a record nothing is guessed: %v", err)
	}
}

func TestCandidateLine(t *testing.T) {
	out := "tigervnc-standalone-server:\n  Installed: 1.15.0+dfsg-2.1~deb13u1\n  Candidate: 1.15.0+dfsg-2.1~deb13u2\n  Version table:\n"
	if m := candidateLine.FindStringSubmatch(out); m == nil || m[1] != "1.15.0+dfsg-2.1~deb13u2" {
		t.Errorf("%v", m)
	}
	if m := candidateLine.FindStringSubmatch("x:\n  Candidate: (none)\n"); m == nil || m[1] != "(none)" {
		t.Errorf("(none) must be recognised so it can be skipped: %v", m)
	}
}
