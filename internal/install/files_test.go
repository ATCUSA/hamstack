package install

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func makeTarGz(t *testing.T, files map[string]string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "a.tar.gz")
	f, _ := os.Create(p)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg, ModTime: time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)})
		tw.Write([]byte(body))
	}
	tw.Close()
	gz.Close()
	f.Close()
	return p
}

func TestExtractFile(t *testing.T) {
	a := makeTarGz(t, map[string]string{"pkg/._pat": "junk", "pkg/pat": "BINARY", "pkg/README": "x"})
	dest := filepath.Join(t.TempDir(), "pat")
	if err := ExtractFile(a, "pat", dest); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(dest)
	st, _ := os.Stat(dest)
	if string(b) != "BINARY" || st.Mode().Perm() != 0o755 {
		t.Errorf("got %q mode %v", b, st.Mode())
	}
	if err := ExtractFile(makeTarGz(t, map[string]string{"x/other": "1"}), "pat", dest); err == nil {
		t.Error("missing file should fail")
	}
}

func TestExtractAllSkipsTraversal(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, "out")
	os.Mkdir(out, 0o755)
	a := makeTarGz(t, map[string]string{"proj-1.0/Makefile": "all:", "../escape.txt": "bad", "/abs.txt": "bad"})
	top, err := ExtractAll(a, out)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "proj-1.0", "Makefile")); err != nil {
		t.Error("Makefile not extracted")
	}
	for _, p := range []string{filepath.Join(root, "escape.txt"), filepath.Join(out, "abs.txt"), "/abs.txt"} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s must not exist", p)
		}
	}
	_ = top
}

func TestDownloadChecksum(t *testing.T) {
	body := "hello"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
	defer srv.Close()
	sum := sha256.Sum256([]byte(body))
	good := hex.EncodeToString(sum[:])
	dir := t.TempDir()
	dest := filepath.Join(dir, "f")
	if err := Download(context.Background(), srv.URL, dest, "deadbeef"); err == nil {
		t.Fatal("bad checksum must fail")
	}
	if _, err := os.Stat(dest); err == nil {
		t.Error("file must not be left behind after a mismatch")
	}
	if err := Download(context.Background(), srv.URL, dest, good); err != nil {
		t.Fatal(err)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".download-*")); len(left) != 0 {
		t.Errorf("temp files left: %v", left)
	}
}

// Autotools tarballs need their timestamps kept, or make tries to regenerate configure.
func TestExtractAllKeepsTimestamps(t *testing.T) {
	out := t.TempDir()
	a := makeTarGz(t, map[string]string{"proj-1.0/configure": "#!/bin/sh"})
	if _, err := ExtractAll(a, out); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(out, "proj-1.0", "configure"))
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	if !st.ModTime().Equal(want) {
		t.Errorf("mtime = %v, want %v", st.ModTime(), want)
	}
}
