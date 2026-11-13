package catalog

import "testing"

// Each program prints its version in its own way, and the way changes between releases. These are the
// banners seen in real runs; a pattern that misses one makes hamstack report a wrong version (and so a
// false "update available").
func TestVersionBanners(t *testing.T) {
	cases := []struct{ pkg, banner, want string }{
		{"direwolf", "Dire Wolf version 1.7\nIncludes optional support for:  gpsd hamlib cm108-ptt\n", "1.7"},
		{"direwolf", "Dire Wolf Release 1.8.1, November 2025\nIncludes optional support for:  gpsd hamlib\n", "1.8.1"},
		{"direwolf", "Dire Wolf Release 1.8.1, November 2025\nUsage: direwolf [options]\n   -c fname  Configuration file name.\n   (see the version 1.8 notes)\n", "1.8.1"},
		{"mercury", "\x1b[0;31mRhizomatica Mercury Version 1.9.23 (git v1.9.23)\x1b[0m\n", "1.9.23"},
		{"pat", "Pat v1.0.1 (2ad31e8) linux/amd64 - go1.27.3\n", "1.0.1"},
		{"ardopcf", "ardopcf Version 1.0.4.1.3 (https://www.github.com/pflarue/ardop)\nCopyright (c) 2014-2024\n", "1.0.4.1.3"},
		{"ardopcf", "ardopcf Version 1.0.4.1.3 (https://www.github.com/pflarue/ardop)\nCopyright (c) 2014-2024\n", "1.0.4.1.3"},
		{"hamlib", "rigctld Hamlib 4.7.2 2026-06-21T13:07:37Z SHA=40f63488f 64-bit\n", "4.7.2"},
		{"hamlib", "rigctld Hamlib 4.6.2 2025-02-09T21:03:50Z SHA=870364 64-bit\n", "4.6.2"},
	}
	for _, c := range cases {
		var p *Package
		for i := range All {
			if All[i].Name == c.pkg {
				p = &All[i]
			}
		}
		if p == nil {
			t.Fatalf("no catalog entry %q", c.pkg)
		}
		m := p.Version.FindStringSubmatch(c.banner)
		if m == nil || m[1] != c.want {
			t.Errorf("%s: %q -> %v, want %s", c.pkg, c.banner, m, c.want)
		}
	}
}

func TestEveryPackageHasAnUpstream(t *testing.T) {
	for _, p := range All {
		if (p.Repo == "" && p.SF == nil && p.Name != "gpsd" && p.Name != "ax25") || p.Binary == "" || p.Version == nil {
			t.Errorf("%s is incomplete: %+v", p.Name, p)
		}
	}
}

// Every dependency, suggestion and conflict must name a program in the catalog, and nothing can need itself.
func TestRelationsPointAtRealPackages(t *testing.T) {
	for _, p := range All {
		for _, list := range [][]string{p.Requires, p.Suggests, p.Conflicts} {
			for _, n := range list {
				if _, ok := ByName(n); !ok {
					t.Errorf("%s refers to %q, which is not in the catalog", p.Name, n)
				}
				if n == p.Name {
					t.Errorf("%s refers to itself", p.Name)
				}
			}
		}
	}
}

func TestEveryPackageCanBeInstalledSomehow(t *testing.T) {
	for _, p := range All {
		custom := p.Name == "pat" || p.Name == "mercury" || p.Name == "hamlib" || p.Name == "direwolf"
		if !custom && len(p.Distro) == 0 && p.Source == nil {
			t.Errorf("%s has no distribution package and no source recipe", p.Name)
		}
		for fam, pkgs := range p.Distro {
			if fam != "arch" && fam != "debian" && fam != "fedora" || len(pkgs) == 0 {
				t.Errorf("%s: bad distribution entry %q %v", p.Name, fam, pkgs)
			}
		}
		if p.Source != nil {
			for _, fam := range []string{"arch", "debian", "fedora"} {
				if len(p.Source.Deps[fam]) == 0 && p.Source.NotOn[fam] == "" {
					t.Errorf("%s: the source recipe has neither build dependencies nor a reason it cannot build on %s", p.Name, fam)
				}
			}
			if p.Source.Build != "autotools" && p.Source.Build != "cmake" && p.Source.Build != "make" {
				t.Errorf("%s: unknown build kind %q", p.Name, p.Source.Build)
			}
		}
		if p.NoVersionFlag == (len(p.Args) > 0) && p.Name != "gpsd" {
			t.Errorf("%s: a program that cannot print its version needs no version flag, and one that can needs it", p.Name)
		}
	}
}

func TestFlSuiteVersionFiles(t *testing.T) {
	p, _ := ByName("flrig")
	for file, want := range map[string]string{"flrig-2.0.12.tar.gz": "2.0.12", "flrig-2.0.12_setup.exe": "", "readme.txt": "", "flrig-help.zip": ""} {
		m := p.SF.File.FindStringSubmatch(file)
		got := ""
		if m != nil {
			got = m[1]
		}
		if got != want {
			t.Errorf("%s: %q, want %q", file, got, want)
		}
	}
}

func TestClosureAddsWhatIsNeededFirst(t *testing.T) {
	order, added := Closure([]string{"mercury"})
	if len(order) != 2 || order[0] != "hamlib" || order[1] != "mercury" {
		t.Errorf("order = %v: hamlib must come before the modem that needs it", order)
	}
	if added["hamlib"] != "needed by mercury" || len(added) != 1 {
		t.Errorf("added = %v", added)
	}
	// Already chosen: nothing is "added", and it is listed once.
	order, added = Closure([]string{"mercury", "hamlib", "mercury"})
	if len(order) != 2 || len(added) != 0 {
		t.Errorf("order %v added %v", order, added)
	}
	if order, _ := Closure([]string{"nosuch"}); len(order) != 0 {
		t.Errorf("unknown: %v", order)
	}
}

func TestSuggestions(t *testing.T) {
	names, from := Suggestions([]string{"fldigi"})
	if len(names) != 3 || from["flrig"] != "fldigi" {
		t.Errorf("fldigi suggests %v %v", names, from)
	}
	// What is already chosen is not suggested again; a suggestion appears once however many ask.
	names, _ = Suggestions([]string{"fldigi", "flrig", "flmsg"})
	if len(names) != 1 || names[0] != "flamp" {
		t.Errorf("got %v", names)
	}
	names, from = Suggestions([]string{"xastir", "direwolf"})
	for _, n := range names {
		if n == "direwolf" || n == "xastir" {
			t.Errorf("suggested something already chosen: %v", names)
		}
	}
	if from["gpsd"] != "xastir" {
		t.Errorf("gpsd is suggested by xastir: %v", from)
	}
}

func TestConflicts(t *testing.T) {
	if c := Conflicts([]string{"wsjtx", "wsjtx-improved", "fldigi"}); len(c) != 1 {
		t.Errorf("got %v", c)
	}
	if c := Conflicts([]string{"fldigi", "flrig"}); len(c) != 0 {
		t.Errorf("got %v", c)
	}
	// A program that conflicts with what is chosen is not suggested.
	for _, n := range func() []string { n, _ := Suggestions([]string{"wsjtx", "hamlib"}); return n }() {
		if n == "wsjtx-improved" {
			t.Error("wsjtx-improved conflicts with wsjtx")
		}
	}
}
