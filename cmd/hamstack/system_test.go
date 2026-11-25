package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ATCUSA/hamstack/internal/distro"
)

func TestParseUpgradable(t *testing.T) {
	apt := "Listing... Done\nlibfoo1/stable 1.2-1 arm64 [upgradable from: 1.1-1]\nopenssh-server/stable-security 1:10.0p2-7+deb13u5 arm64 [upgradable from: 1:10.0p2-7+deb13u4]\n"
	if got := parseUpgradable(distro.Debian, apt); strings.Join(got, ",") != "libfoo1,openssh-server" {
		t.Errorf("apt: %v", got)
	}
	if got := parseUpgradable(distro.Debian, "Listing... Done\n"); len(got) != 0 {
		t.Errorf("nothing waiting: %v", got)
	}
	if got := parseUpgradable(distro.Arch, "linux 6.1-1 -> 6.2-1\nhamlib 4.6-1 -> 4.7-1\n"); strings.Join(got, ",") != "linux,hamlib" {
		t.Errorf("arch: %v", got)
	}
	dnf := "\nkernel.x86_64   6.2.1-1.fc45   updates\nhamlib.x86_64   4.7-1.fc45     updates\n\nObsoleting Packages\nfoo.x86_64      1-1   updates\n   old.x86_64   0-1   @fedora\n"
	if got := parseUpgradable(distro.Fedora, dnf); strings.Join(got, ",") != "kernel.x86_64,hamlib.x86_64" {
		t.Errorf("dnf: %v", got)
	}
}

func TestSudoersEscape(t *testing.T) {
	if got := sudoersEscape("Dpkg::Options::=--force-confdef"); got != `Dpkg\:\:Options\:\:\=--force-confdef` {
		t.Errorf("%s", got)
	}
	if got := sudoersEscape(`a,b\c`); got != `a\,b\\c` {
		t.Errorf("%s", got)
	}
}

// The rule names exact commands, by full path, and nothing else: a shell, a wildcard or another account would
// be a hole in sudo.
func TestSystemRuleNamesOnlyTheExactCommands(t *testing.T) {
	old := lookPath
	t.Cleanup(func() { lookPath = old })
	lookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	rule, err := systemRule("u", distro.Debian)
	if err != nil {
		t.Fatal(err)
	}
	line := ""
	for _, l := range strings.Split(rule, "\n") {
		if !strings.HasPrefix(l, "#") && l != "" {
			line = l
		}
	}
	want := `u ALL=(root) NOPASSWD: /usr/bin/apt-get update, ` +
		`/usr/bin/apt-get -y -o Dpkg\:\:Options\:\:\=--force-confdef -o Dpkg\:\:Options\:\:\=--force-confold upgrade, ` +
		`/usr/bin/systemctl --version, /usr/bin/systemctl reboot, /usr/bin/systemctl poweroff`
	if line != want {
		t.Errorf("rule:\n got %s\nwant %s", line, want)
	}
	for _, bad := range []string{"*", "ALL=(ALL)", "/bin/sh", "bash", "(ALL)"} {
		if strings.Contains(line, bad) {
			t.Errorf("rule must not contain %q: %s", bad, line)
		}
	}
	for _, name := range []string{"root", "u; rm", "a b", "$(x)", ""} {
		if _, err := systemRule(name, distro.Debian); err == nil && name != "root" {
			t.Errorf("username %q must be refused", name)
		}
	}
	if _, err := systemRule("u", distro.Unknown); err == nil {
		t.Error("an unknown distribution has no known commands to allow")
	}
	lookPath = func(name string) (string, error) { return "", fmt.Errorf("not found") }
	if _, err := systemRule("u", distro.Debian); err == nil {
		t.Error("a command that is not installed cannot be allowed")
	}
}

func TestShutdownWarningSaysItCannotBeUndoneFromHere(t *testing.T) {
	w := shutdownWarning([]string{"Pat Winlink client", "rig control"})
	for _, want := range []string{"POWERS THE COMPUTER OFF", "power button", "Winlink", "Pat Winlink client, rig control"} {
		if !strings.Contains(w, want) {
			t.Errorf("warning lacks %q:\n%s", want, w)
		}
	}
	if strings.Contains(shutdownWarning(nil), "Running now") {
		t.Error("nothing running: no list")
	}
}
