package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ATCUSA/hamstack/internal/distro"
	"github.com/ATCUSA/hamstack/internal/install"
	"github.com/ATCUSA/hamstack/internal/web"
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

// A sudoers file with a mistake in it stops sudo working for everyone, including the steps that would check or
// remove it. Every rule hamstack writes must be checked with visudo before it is put in /etc/sudoers.d.
func TestFirewallSudoRuleIsCheckedBeforeItGoesLive(t *testing.T) {
	fakeSudo(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	steps, err := firewallSteps()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range steps {
		cmd := strings.Join(s.Cmd, " ")
		if !strings.Contains(cmd, "/etc/sudoers.d/") {
			continue
		}
		if strings.Contains(cmd, "chmod") && !strings.Contains(cmd, "install -m 0440") {
			t.Errorf("a separate step fixes the mode after the file is live: %s", cmd)
			continue
		}
		v, i := strings.Index(cmd, "visudo -cf"), strings.Index(cmd, "install -m 0440 -o root -g root")
		if v < 0 || i < 0 || v > i {
			t.Errorf("the rule must be checked with visudo, then installed 0440: %s", cmd)
		}
		if strings.Contains(cmd, "> '/etc/sudoers.d") {
			t.Errorf("written straight into /etc/sudoers.d: %s", cmd)
		}
		found = true
	}
	if !found {
		t.Fatal("no step installs the firewall's sudo rule")
	}
}

func TestSudoersFileStepChecksWithVisudoThenInstalls0440(t *testing.T) {
	step, err := sudoersFileStep(install.Env{Root: true}, "d", "/etc/sudoers.d/x", "u ALL=(root) NOPASSWD: /bin/true\n", "f")
	if err != nil {
		t.Fatal(err)
	}
	script := step.Cmd[len(step.Cmd)-1]
	t0, v, i := strings.Index(script, `> "$t"`), strings.Index(script, `visudo -cf "$t"`), strings.Index(script, `install -m 0440 -o root -g root "$t" '/etc/sudoers.d/x'`)
	if t0 < 0 || v < t0 || i < v || !strings.HasPrefix(script, "set -e;") {
		t.Fatalf("want: write a temp file, visudo it, then install it 0440, stopping at the first error:\n%s", script)
	}
	if strings.Contains(script, `> '/etc/sudoers.d/x'`) {
		t.Fatalf("written straight into place:\n%s", script)
	}
}

// --dry-run must never restart or power off the computer, with or without --yes.
func TestRebootAndShutdownDryRunDoNothing(t *testing.T) {
	old := powerAction
	var ran []string
	powerAction = func(verb, secret string) error { ran = append(ran, verb); return nil }
	defer func() { powerAction = old }()
	for _, args := range [][]string{{"reboot", "--dry-run", "--yes"}, {"reboot", "--dry-run"}, {"shutdown", "--dry-run", "--yes"}, {"shutdown", "--dry-run"}} {
		out := captureStdout(t, func() {
			if err := cmdSystem(args); err != nil {
				t.Errorf("%v: %v", args, err)
			}
		})
		if !strings.Contains(out, "dry run") || !strings.Contains(out, "systemctl") {
			t.Errorf("%v must show what it would run and say it is a dry run: %q", args, out)
		}
	}
	if len(ran) > 0 {
		t.Fatalf("a dry run ran %v", ran)
	}
	if err := cmdSystem([]string{"reboot", "--yes"}); err != nil || len(ran) != 1 || ran[0] != "reboot" {
		t.Fatalf("a real reboot with --yes must still run: %v %v", ran, err)
	}
}

// The firewall, group and browser-component plans used to run inside the dashboard with "sudo -n", which fails
// without a passwordless rule and reported only "exit status 1". They now ask for the password and run as a job.
func TestDashboardSetupPlanAsksForThePasswordAndRunsAsAJob(t *testing.T) {
	jobTest(t)
	fakeSudo(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	passwordless.Lock()
	passwordless.at = time.Time{}
	passwordless.Unlock()
	plan, err := webPlan("firewall", web.ManagementRequest{}, true)
	if err != nil || !strings.Contains(plan.(string), "visudo -cf") {
		t.Fatalf("the plan: %v %v", plan, err)
	}
	var need *web.SudoNeeded
	if _, err := webPlan("firewall", web.ManagementRequest{Confirm: true}, false); !errors.As(err, &need) || len(need.Commands) == 0 {
		t.Fatalf("without the password it must ask for it, listing what runs as root: %v", err)
	}
	res, err := webPlan("firewall", web.ManagementRequest{Confirm: true, Secret: "right"}, false)
	if err != nil {
		t.Fatal(err)
	}
	id := res.(map[string]string)["job"]
	j := findJob(id)
	if j == nil || j.info.Title != "Installing the firewall helper" {
		t.Fatalf("no job: %v", res)
	}
	waitJob(t, id, 5*time.Second)
}
