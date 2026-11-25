package install

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ATCUSA/hamstack/internal/distro"
	"github.com/ATCUSA/hamstack/internal/ledger"
)

func TestParsePackages(t *testing.T) {
	got := parsePackages(distro.Debian, "ii novnc\nrc oldconfig\nii libneatvnc1:arm64\nun never\n")
	if len(got) != 2 || !got["novnc"] || !got["libneatvnc1:arm64"] {
		t.Errorf("debian: only fully installed packages count: %v", got)
	}
	if got := parsePackages(distro.Arch, "tigervnc\nnovnc\n"); len(got) != 2 {
		t.Errorf("arch: %v", got)
	}
}

func TestRemovalSimulations(t *testing.T) {
	apt := "Reading package lists...\nThe following packages will be REMOVED:\n  novnc* websockify*\nPurg novnc [1:1.6.0-2]\nRemv websockify [0.12.0]\n"
	if got := parseAptSimulation(apt); !slices.Equal(got, []string{"novnc", "websockify"}) {
		t.Errorf("apt: %v", got)
	}
	if got := outside([]string{"novnc", "libfoo", "websockify"}, []string{"novnc", "websockify:arm64"}); !slices.Equal(got, []string{"libfoo"}) {
		t.Errorf("outside: %v", got)
	}
	pac := "error: failed to prepare transaction (could not satisfy dependencies)\n:: removing hamlib breaks dependency 'hamlib' required by gpredict\n"
	if got := parsePacmanBreaks(pac); !slices.Equal(got, []string{"gpredict"}) {
		t.Errorf("pacman: %v", got)
	}
	rpm := "error: Failed dependencies:\n\tlibhamlib.so.4()(64bit) is needed by (installed) gpredict-2.2.1-1.fc45.x86_64\n"
	if got := parseRpmNeeded(rpm); !slices.Equal(got, []string{"gpredict-2.2.1-1.fc45.x86_64"}) {
		t.Errorf("rpm: %v", got)
	}
}

func write(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestTrackTreeRecordsOnlyWhatTheStepAdded(t *testing.T) {
	home := t.TempDir()
	e := Env{Home: home}
	prefix := e.prefix()
	write(t, filepath.Join(prefix, "bin", "mine"))                    // the user's own
	write(t, filepath.Join(prefix, "share", "Steam", "huge"))         // not looked into
	write(t, filepath.Join(prefix, "share", "hamstack", "installed")) // hamstack's own folder
	done := TrackTree(e, "fldigi")()
	write(t, filepath.Join(prefix, "bin", "fldigi"))
	write(t, filepath.Join(prefix, "share", "fldigi", "help.html"))
	write(t, filepath.Join(prefix, "share", "applications", "fldigi.desktop"))
	write(t, filepath.Join(prefix, "share", "Steam", "new-game"))
	write(t, filepath.Join(prefix, "share", "hamstack", "changes.json"))
	c := done()
	if len(c) != 1 || c[0].Kind != ledger.Files || c[0].For != "fldigi" || c[0].Role != ledger.App {
		t.Fatalf("%+v", c)
	}
	var rel []string
	for _, p := range c[0].Names {
		r, _ := filepath.Rel(prefix, p)
		rel = append(rel, r)
	}
	if want := []string{"bin/fldigi", "share/applications/fldigi.desktop", "share/fldigi/help.html"}; !slices.Equal(rel, want) {
		t.Errorf("got %v, want %v", rel, want)
	}
}

func TestRemoveFilesStaysInsideThePrefix(t *testing.T) {
	home := t.TempDir()
	prefix := filepath.Join(home, ".local")
	outsideFile := filepath.Join(home, "logbook.adi")
	for _, p := range []string{outsideFile, filepath.Join(prefix, "bin", "fldigi"), filepath.Join(prefix, "bin", "mine"),
		filepath.Join(prefix, "share", "fldigi", "a", "b"), filepath.Join(prefix, "share", "data2g", "lib", "x"), filepath.Join(prefix, "share", "hamstack", "changes.json")} {
		write(t, p)
	}
	n, skipped := RemoveFiles(prefix, []string{
		outsideFile, filepath.Join(prefix, "..", "logbook.adi"), prefix, filepath.Join(prefix, "bin"), filepath.Join(prefix, "share", "hamstack"),
		filepath.Join(prefix, "bin", "fldigi"), filepath.Join(prefix, "share", "fldigi", "a", "b"), filepath.Join(prefix, "share", "data2g"), filepath.Join(prefix, "bin", "gone"),
	})
	if n != 3 {
		t.Errorf("removed %d, want 3 (skipped %v)", n, skipped)
	}
	for _, keep := range []string{outsideFile, filepath.Join(prefix, "bin", "mine"), filepath.Join(prefix, "share", "hamstack", "changes.json"), filepath.Join(prefix, "share")} {
		if _, err := os.Stat(keep); err != nil {
			t.Errorf("%s must be left: %v", keep, err)
		}
	}
	for _, gone := range []string{filepath.Join(prefix, "bin", "fldigi"), filepath.Join(prefix, "share", "fldigi"), filepath.Join(prefix, "share", "data2g")} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s must be gone: %v", gone, err)
		}
	}
	if len(skipped) != 5 {
		t.Errorf("skipped %v, want the 5 paths outside the prefix or too broad", skipped)
	}
}

func TestRunRecordsATrackedStepOnlyWhenItSucceeds(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	change := func() func() []ledger.Change {
		return func() []ledger.Change {
			return []ledger.Change{{Kind: ledger.Group, Role: ledger.App, Names: []string{"dialout"}, User: "op"}}
		}
	}
	var out strings.Builder
	err := Run([]Step{{Desc: "fails", Cmd: []string{"false"}, Track: change}}, Options{Yes: true, Out: &out})
	if err == nil {
		t.Fatal("want the failure reported")
	}
	if c, _ := ledger.Load(home); len(c) != 0 {
		t.Fatalf("a failed step must not be recorded: %+v", c)
	}
	steps := []Step{{Desc: "fails but optional", Cmd: []string{"false"}, Optional: true, Track: change}, {Desc: "works", Cmd: []string{"true"}, Track: change}}
	if err := Run(steps, Options{Yes: true, Out: &out}); err != nil {
		t.Fatal(err)
	}
	if c, _ := ledger.Load(home); len(c) != 1 || c[0].Kind != ledger.Group {
		t.Fatalf("want the one group change: %+v", c)
	}
}

func TestRootFileStepKeepsTheOriginalCopy(t *testing.T) {
	s, err := RootFileStep(Env{Root: true}, "write", "/etc/ax25/axports", "x")
	if err != nil {
		t.Fatal(err)
	}
	script := s.Cmd[len(s.Cmd)-1]
	if !strings.Contains(script, "[ ! -e '/etc/ax25/axports'.hamstack-bak ]") {
		t.Errorf("a second write must not overwrite the copy of the original: %s", script)
	}
	c := s.Track()()
	if len(c) != 1 || c[0].Kind != ledger.RootFile || c[0].Path != "/etc/ax25/axports" {
		t.Errorf("%+v", c)
	}
}
