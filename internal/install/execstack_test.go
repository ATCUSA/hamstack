package install

import (
	"os"
	"path/filepath"
	"testing"
)

// The test binary itself is a real ELF program with a non-executable stack (Go marks it so), like a jt9 built that way.
func TestMarkExecStack(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "jt9")
	if err := os.WriteFile(p, b, 0o755); err != nil {
		t.Fatal(err)
	}
	if need, err := NeedsExecStack(p); err != nil || !need {
		t.Fatalf("a Go binary has a non-executable stack: need %v, %v", need, err)
	}
	if changed, err := MarkExecStack(p); err != nil || !changed {
		t.Fatalf("mark: %v %v", changed, err)
	}
	if need, _ := NeedsExecStack(p); need {
		t.Fatal("still non-executable after marking")
	}
	if changed, err := MarkExecStack(p); err != nil || changed {
		t.Fatalf("marking twice must change nothing: %v %v", changed, err)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o755 {
		t.Errorf("mode %v", fi.Mode().Perm())
	}
	after, _ := os.ReadFile(p)
	diff := 0
	for i := range b {
		if b[i] != after[i] {
			diff++
		}
	}
	if len(after) != len(b) || diff != 1 {
		t.Errorf("exactly one byte may change: %d changed, size %d -> %d", diff, len(b), len(after))
	}
}

func TestExecStackOnAScript(t *testing.T) {
	p := filepath.Join(t.TempDir(), "script")
	_ = os.WriteFile(p, []byte("#!/bin/sh\necho hi\n"), 0o755)
	if need, err := NeedsExecStack(p); need || err != nil {
		t.Errorf("a script: %v %v", need, err)
	}
	if _, err := MarkExecStack(p); err == nil {
		t.Error("marking a script must fail")
	}
}
