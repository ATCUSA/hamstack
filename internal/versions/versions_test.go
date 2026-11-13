package versions

import (
	"os"
	"regexp"
	"testing"

	"github.com/ATCUSA/hamstack/internal/catalog"
)

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.9.23", "1.9.23", 0},
		{"1.9.23", "1.10.0", -1},
		{"1.10", "1.9.23", 1},
		{"v1.0.1", "1.0.1", 0},
		{"4.7.2", "4.6.5", 1},
		{"1.7", "1.7.0", 0},
		{"1.0", "1.0.1", -1},
	}
	for _, c := range cases {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%q,%q)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestNewestInSourceForgeListings(t *testing.T) {
	rss := `<link>https://sourceforge.net/projects/fldigi/files/flrig/flrig-2.0.12.tar.gz/download</link>
<link>https://sourceforge.net/projects/fldigi/files/flrig/flrig-2.0.9.tar.gz/download</link>
<link>https://sourceforge.net/projects/fldigi/files/flrig/flrig-2.0.12_setup.exe/download</link>
<link>https://sourceforge.net/projects/fldigi/files/flrig/readme.txt/download</link>`
	flrig := &catalog.SourceForge{File: regexp.MustCompile(`^flrig-(\d+\.\d+\.\d+)\.tar\.gz$`)}
	if got := newestIn(rss, flrig); got != "2.0.12" {
		t.Errorf("rss: %q (2.0.12 beats 2.0.9, and .exe/.txt are not releases)", got)
	}
	html := `<a href="/projects/wsjt/files/wsjtx-3.0.0-rc1/">x</a><a href="/projects/wsjt/files/wsjtx-3.0.1/">y</a>
<a href="/projects/wsjt/files/wsjtx-3.0.0/">z</a><a href="/projects/wsjt/files/samples/">s</a>`
	wsjt := &catalog.SourceForge{File: regexp.MustCompile(`^wsjtx-(\d+\.\d+\.\d+)$`), HTML: true}
	if got := newestIn(html, wsjt); got != "3.0.1" {
		t.Errorf("html: %q (a release candidate is not a release)", got)
	}
	if got := newestIn("nothing here", wsjt); got != "" {
		t.Errorf("empty: %q", got)
	}
}

func TestUpstreamVersionFromPackageVersions(t *testing.T) {
	for in, want := range map[string]string{
		"2.8.0+250501+repack-1": "2.8.0",
		"1:20250502-1":          "",
		"3.0.1-2.fc45":          "3.0.1",
		"4.2.06-1+deb13u1":      "4.2.06",
		"1:2.2.0-11.fc45":       "2.2.0",
		"0.0.10-rc5+git2023-3":  "0.0.10",
		"3.27.5-1":              "3.27.5",
		"":                      "",
	} {
		if got := UpstreamVersion(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

func TestRecordedVersions(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if RecordedVersion("flrig") != "" {
		t.Error("nothing recorded yet")
	}
	if err := Record("flrig", "2.0.12"); err != nil {
		t.Fatal(err)
	}
	if got := RecordedVersion("flrig"); got != "2.0.12" {
		t.Errorf("got %q", got)
	}
}

func TestProgramsSharingABinaryNeedEvidence(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	bin := home + "/.local/bin"
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin+"/wsjtx", []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	improved, _ := catalog.ByName("wsjtx-improved")
	official, _ := catalog.ByName("wsjtx")
	if FindInstalled(improved) != nil || FindInstalled(official) != nil {
		t.Error("finding a wsjtx binary says nothing about which of the two it is")
	}
	if err := Record("wsjtx-improved", "3.2.1"); err != nil {
		t.Fatal(err)
	}
	if in := FindInstalled(improved); in == nil || in.Version != "3.2.1" {
		t.Errorf("hamstack's record names the one it built: %+v", in)
	}
	if FindInstalled(official) != nil {
		t.Error("and the other is still not installed")
	}
	// A program with a binary of its own needs no such proof.
	if p, _ := catalog.ByName("flrig"); sharesBinary(p) {
		t.Error("flrig shares its binary with nothing")
	}
}
