package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ATCUSA/hamstack/internal/catalog"
)

func put(t *testing.T, root, rel, body string, mode os.FileMode) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

func TestInstallStagedNeverWritesOutsideThePrefix(t *testing.T) {
	stage, prefix := t.TempDir(), filepath.Join(t.TempDir(), ".local")
	rel := filepath.Join(stage, prefix) // how DESTDIR lays out an absolute path
	put(t, rel, "bin/js8call", "binary", 0o755)
	put(t, stage, "usr/share/applications/js8call.desktop", "desktop", 0o644) // a path the project fixed
	put(t, stage, "usr/local/share/man/man1/x.1", "man", 0o644)
	put(t, stage, "etc/evil.conf", "outside", 0o644)
	put(t, stage, "var/lib/evil", "outside", 0o644)
	if err := os.Symlink("js8call", filepath.Join(rel, "bin", "js8")); err != nil {
		t.Fatal(err)
	}
	n, err := InstallStaged(stage, prefix)
	if err != nil || n != 4 {
		t.Fatalf("copied %d files: %v", n, err)
	}
	for f, want := range map[string]string{"bin/js8call": "binary", "share/applications/js8call.desktop": "desktop", "share/man/man1/x.1": "man"} {
		b, err := os.ReadFile(filepath.Join(prefix, f))
		if err != nil || string(b) != want {
			t.Errorf("%s: %q %v", f, b, err)
		}
	}
	if st, err := os.Stat(filepath.Join(prefix, "bin/js8call")); err != nil || st.Mode().Perm() != 0o755 {
		t.Errorf("an executable stays executable: %v %v", st, err)
	}
	if l, err := os.Readlink(filepath.Join(prefix, "bin/js8")); err != nil || l != "js8call" {
		t.Errorf("symlink: %q %v", l, err)
	}
	for _, bad := range []string{"etc", "var"} {
		if _, err := os.Stat(filepath.Join(prefix, bad)); err == nil {
			t.Errorf("%s was copied under the prefix", bad)
		}
	}
	// Running it again over what is there replaces files.
	put(t, rel, "bin/js8call", "newer", 0o755)
	if _, err := InstallStaged(stage, prefix); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(prefix, "bin/js8call")); string(b) != "newer" {
		t.Errorf("not replaced: %q", b)
	}
}

func TestApplyPatchesIsExactAndRefusesAChangedSource(t *testing.T) {
	dir := t.TempDir()
	put(t, dir, "CMakeLists.txt", "a\nadd_custom_target (install DEPENDS wsjtx-install)\nb\n", 0o644)
	ok := []catalog.Patch{{File: "CMakeLists.txt", Old: "add_custom_target (install DEPENDS wsjtx-install)", New: ""}}
	if err := applyPatches(dir, ok); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "CMakeLists.txt")); string(b) != "a\n\nb\n" {
		t.Errorf("patched to %q", b)
	}
	// The text is gone now, as when upstream changes: the build must stop rather than carry on unpatched.
	if err := applyPatches(dir, ok); err == nil || !strings.Contains(err.Error(), "needs updating") {
		t.Errorf("a missing patch target must fail loudly: %v", err)
	}
	if err := applyPatches(dir, []catalog.Patch{{File: "nosuch.txt", Old: "x", New: "y"}}); err == nil {
		t.Error("a missing file must fail")
	}
}

func TestFinishBuildChecksTheRealBinaryAndLinksTheKnownName(t *testing.T) {
	home := t.TempDir()
	e := Env{Home: home}
	bin := filepath.Join(home, ".local", "bin")
	pkg := catalog.Package{Name: "wsjtx-improved", Binary: "wsjtx", Source: &catalog.Source{Binary: "ws", Links: map[string]string{"wsjtx": "ws"}}}
	if err := finishBuild(e, pkg, "3.2.1"); err == nil || !strings.Contains(err.Error(), "ws is not at") {
		t.Errorf("it must look for the program the build installs, not the known name: %v", err)
	}
	put(t, bin, "ws", "x", 0o755)
	if err := finishBuild(e, pkg, "3.2.1"); err != nil {
		t.Fatal(err)
	}
	if l, err := os.Readlink(filepath.Join(bin, "wsjtx")); err != nil || l != "ws" {
		t.Errorf("link: %q %v", l, err)
	}
	if err := finishBuild(e, pkg, "3.2.2"); err != nil { // running again replaces the link
		t.Errorf("second run: %v", err)
	}
}
