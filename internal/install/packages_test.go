package install

import (
	"context"
	"strings"
	"testing"

	"github.com/ATCUSA/hamstack/internal/distro"
)

func env(f distro.Family, root bool) Env {
	return Env{Distro: distro.Info{ID: string(f), Family: f}, Home: "/home/u", Root: root, Sudo: !root, Arch: "amd64", Jobs: 4}
}

func TestHamlibPlanPerDistro(t *testing.T) {
	cases := map[distro.Family]string{
		distro.Arch:   "sudo pacman -S --needed --noconfirm --disable-download-timeout hamlib",
		distro.Debian: "sudo apt-get install -y libhamlib-utils",
		distro.Fedora: "sudo dnf install -y hamlib",
	}
	for fam, want := range cases {
		steps, err := HamlibPlan(env(fam, false))
		if err != nil {
			t.Fatalf("%s: %v", fam, err)
		}
		last := steps[len(steps)-1].Display()
		if !strings.Contains(last, want) {
			t.Errorf("%s: got %q want %q", fam, last, want)
		}
	}
	// Debian refreshes package lists first.
	if s, _ := HamlibPlan(env(distro.Debian, false)); len(s) != 2 || !strings.Contains(s[0].Display(), "apt-get update") {
		t.Errorf("debian should update first: %+v", s)
	}
}

func TestRootNeedsNoSudo(t *testing.T) {
	steps, _ := HamlibPlan(env(distro.Debian, true))
	for _, s := range steps {
		if strings.Contains(s.Display(), "sudo") {
			t.Errorf("root plan uses sudo: %s", s.Display())
		}
	}
}

func TestNoSudoNoRoot(t *testing.T) {
	e := env(distro.Arch, false)
	e.Sudo = false
	if _, err := HamlibPlan(e); err == nil {
		t.Error("expected an error when neither root nor sudo is available")
	}
}

func TestUnsupportedDistro(t *testing.T) {
	e := env(distro.Unknown, false)
	if _, err := HamlibPlan(e); err == nil {
		t.Error("unknown distro should fail")
	}
}

func TestDirewolfUsesThePackageWhereOneExists(t *testing.T) {
	for fam, want := range map[distro.Family]string{
		distro.Debian: "sudo apt-get install -y direwolf",
		distro.Fedora: "sudo dnf install -y direwolf",
	} {
		steps, notice, err := DirewolfPlan(context.Background(), env(fam, false), false)
		if err != nil {
			t.Fatalf("%s: %v", fam, err)
		}
		if notice != "" {
			t.Errorf("%s: a package install needs no source-build disclaimer: %q", fam, notice)
		}
		if last := steps[len(steps)-1].Display(); !strings.Contains(last, want) {
			t.Errorf("%s: got %q want %q", fam, last, want)
		}
	}
}

func TestDirewolfBuildDepsForEveryDistro(t *testing.T) {
	for _, fam := range []distro.Family{distro.Arch, distro.Debian, distro.Fedora} {
		deps := direwolfBuildDeps[fam]
		joined := strings.Join(deps, " ")
		// Debian calls the ALSA development package libasound2-dev.
		for _, need := range [][]string{{"cmake"}, {"alsa", "asound"}, {"hamlib"}} {
			found := false
			for _, n := range need {
				found = found || strings.Contains(joined, n)
			}
			if !found {
				t.Errorf("%s: build deps %v lack %v", fam, deps, need)
			}
		}
	}
	n := DirewolfSourceNotice(env(distro.Arch, false), "1.8.1")
	for _, want := range []string{"1.8.1", "base-devel", "no published checksum", "/home/u/.local", "stay installed"} {
		if !strings.Contains(n, want) {
			t.Errorf("disclaimer missing %q:\n%s", want, n)
		}
	}
}

func TestDirewolfUnsupportedDistro(t *testing.T) {
	if _, _, err := DirewolfPlan(context.Background(), env(distro.Unknown, false), false); err == nil {
		t.Error("an unknown distribution has neither a package nor known build deps")
	}
}

func TestEverySourceBuildHasADisclaimer(t *testing.T) {
	e := env(distro.Debian, false)
	for name, notice := range map[string]string{
		"mercury":  MercurySourceNotice(e, "1.9.23"),
		"direwolf": DirewolfSourceNotice(e, "1.8.1"),
		"hamlib":   HamlibSourceNotice(e, "4.7.2"),
	} {
		for _, want := range []string{"compilers", "stay installed", "/home/u/.local"} {
			if !strings.Contains(notice, want) {
				t.Errorf("%s disclaimer lacks %q:\n%s", name, want, notice)
			}
		}
	}
}
