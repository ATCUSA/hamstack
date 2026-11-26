package patcfg

import (
	"os"
	"path/filepath"
	"testing"
)

// The config is replaced whole, private, with nothing left beside it; a symlinked config keeps its link.
func TestWriteAtomic(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.json")
	if err := os.WriteFile(real, []byte(`{"old":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "config.json")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(link, []byte(`{"new":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(real); string(b) != `{"new":true}` {
		t.Errorf("content: %s", b)
	}
	if fi, _ := os.Lstat(link); fi.Mode()&os.ModeSymlink == 0 {
		t.Error("the symlink was replaced by a file")
	}
	if fi, _ := os.Stat(real); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode().Perm())
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Errorf("left beside it: %v", entries)
	}
	if err := writeAtomic(filepath.Join(dir, "missing", "x.json"), []byte("x"), 0o600); err == nil {
		t.Error("a folder that is not there is an error, not a half-written file")
	}
}
