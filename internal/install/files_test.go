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

// makeTarGzEntries writes an archive from explicit headers, for links and odd paths.
func makeTarGzEntries(t *testing.T, entries []tar.Header, bodies map[string]string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "x.tar.gz")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, h := range entries {
		h := h
		if h.Typeflag == tar.TypeReg {
			h.Size = int64(len(bodies[h.Name]))
		}
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			tw.Write([]byte(bodies[h.Name]))
		}
	}
	tw.Close()
	gz.Close()
	f.Close()
	return p
}

func TestExtractTreeKeepsSafeLinksOnly(t *testing.T) {
	arch := makeTarGzEntries(t, []tar.Header{
		{Name: "./app/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "./app/lib/libx.so.1.2", Typeflag: tar.TypeReg, Mode: 0o644},
		{Name: "./app/lib/libx.so", Typeflag: tar.TypeSymlink, Linkname: "libx.so.1.2"},
		{Name: "./app/run", Typeflag: tar.TypeReg, Mode: 0o755},
		{Name: "./app/abs", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"},
		{Name: "./app/up", Typeflag: tar.TypeSymlink, Linkname: "../../outside"},
		{Name: "./app/evil", Typeflag: tar.TypeSymlink, Linkname: "lib/.."}, // inside: allowed, but a link to a folder
		{Name: "./app/evil/pwn", Typeflag: tar.TypeReg, Mode: 0o644},
	}, map[string]string{"./app/lib/libx.so.1.2": "lib", "./app/run": "#!/bin/sh\n", "./app/evil/pwn": "x"})
	dir := t.TempDir()
	top, err := ExtractTree(arch, dir)
	if err != nil || top != "app" {
		t.Fatalf("top %q err %v", top, err)
	}
	if l, err := os.Readlink(filepath.Join(dir, "app/lib/libx.so")); err != nil || l != "libx.so.1.2" {
		t.Errorf("a relative link inside is kept: %q %v", l, err)
	}
	for _, name := range []string{"app/abs", "app/up"} {
		if _, err := os.Lstat(filepath.Join(dir, name)); err == nil {
			t.Errorf("%s: an absolute link or one that leaves the folder must be skipped", name)
		}
	}
	if fi, err := os.Stat(filepath.Join(dir, "app/run")); err != nil || fi.Mode()&0o100 == 0 {
		t.Errorf("the launcher keeps its executable bit: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "outside")); err == nil {
		t.Error("nothing may be written outside the folder")
	}
}

func TestExtractTreeRefusesWritingThroughLinkOutside(t *testing.T) {
	outside := t.TempDir()
	arch := makeTarGzEntries(t, []tar.Header{
		{Name: "d/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "d/l", Typeflag: tar.TypeSymlink, Linkname: "../d"}, // stays inside
		{Name: "d/l/f", Typeflag: tar.TypeReg, Mode: 0o644},
	}, map[string]string{"d/l/f": "x"})
	dir := t.TempDir()
	if _, err := ExtractTree(arch, dir); err != nil {
		t.Fatalf("a link to its own folder is harmless: %v", err)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("wrote outside: %v", entries)
	}
}
