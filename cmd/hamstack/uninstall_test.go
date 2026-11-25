package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ATCUSA/hamstack/internal/install"
	"github.com/ATCUSA/hamstack/internal/ledger"
)

func uninstallTestFacts(t *testing.T) uninstallFacts {
	home := t.TempDir()
	return uninstallFacts{
		home: home, username: "op", uid: "1000",
		env:      install.Env{Home: home, Root: true},
		cfgDir:   filepath.Join(home, ".config", "hamstack"),
		stateDir: filepath.Join(home, ".local", "state", "hamstack"),
		shareDir: filepath.Join(home, ".local", "share", "hamstack"),
		unitDir:  filepath.Join(home, ".config", "systemd", "user"),
		patDir:   filepath.Join(home, ".config", "pat"),
		exe:      filepath.Join(home, ".local", "bin", "hamstack"), exeInHome: true,
		exists: func(string) bool { return false },
		sysctl: func(string) string { return "443" },
		removal: func(pkgs []string) (install.Removal, error) {
			r := install.Removal{Needed: map[string][]string{}}
			for _, p := range pkgs {
				if p == "nftables" {
					r.Needed[p] = []string{"firewalld"}
				} else {
					r.Remove = append(r.Remove, p)
				}
			}
			r.Cmds = [][]string{append([]string{"apt-get", "remove", "--purge", "-y"}, r.Remove...)}
			return r, nil
		},
		changes: []ledger.Change{
			{Kind: ledger.Packages, Role: ledger.Support, For: "web", Names: []string{"tigervnc-standalone-server", "novnc", "nftables"}},
			{Kind: ledger.Packages, Role: ledger.App, For: "wsjtx", Names: []string{"wsjtx"}},
			{Kind: ledger.Packages, Role: ledger.Build, For: "mercury", Names: []string{"build-essential", "libhamlib-dev"}},
			{Kind: ledger.RootFile, Role: ledger.Support, For: "web", Path: sysctlFile},
			{Kind: ledger.Sysctl, Role: ledger.Support, For: "web", Key: portStartKey, Prev: "1024"},
			{Kind: ledger.RootFile, Role: ledger.App, For: "ax25", Path: "/etc/ax25/axports", Existed: true},
			{Kind: ledger.Group, Role: ledger.App, Names: []string{"dialout"}, User: "op"},
			{Kind: ledger.Files, Role: ledger.App, For: "pat", Names: []string{filepath.Join(home, ".local", "bin", "pat")}},
		},
	}
}

func planText(steps []install.Step) string {
	var b strings.Builder
	for _, s := range steps {
		b.WriteString(s.Desc + " | " + strings.Join(s.Cmd, " ") + "\n")
	}
	return b.String()
}

func TestUninstallKeepingAppsUndoesOnlyWhatHamstackNeeded(t *testing.T) {
	steps, notes, err := uninstallPlan(uninstallTestFacts(t), true)
	if err != nil {
		t.Fatal(err)
	}
	text := planText(steps)
	for _, want := range []string{"apt-get remove --purge -y tigervnc-standalone-server novnc", "rm -f " + sysctlFile, "sysctl -w " + portStartKey + "=1024", "Remove hamstack itself"} {
		if !strings.Contains(text, want) {
			t.Errorf("plan lacks %q:\n%s", want, text)
		}
	}
	for _, not := range []string{"wsjtx", "build-essential", "axports", "gpasswd", "bin/pat", "nftables"} {
		if strings.Contains(text, not) {
			t.Errorf("keeping the apps must leave %q alone:\n%s", not, text)
		}
	}
	all := strings.Join(notes, "\n")
	for _, want := range []string{"nftables was added by hamstack but is left installed: firewalld now needs it", "Kept: the 1 packages of the ham radio programs", "Kept: 2 packages (compilers and libraries)", "The record of what was kept stays"} {
		if !strings.Contains(all, want) {
			t.Errorf("notes lack %q:\n%s", want, all)
		}
	}
}

func TestUninstallRemovingAppsUndoesEverythingRecorded(t *testing.T) {
	steps, _, err := uninstallPlan(uninstallTestFacts(t), false)
	if err != nil {
		t.Fatal(err)
	}
	text := planText(steps)
	for _, want := range []string{"wsjtx", "build-essential", "mv -f '/etc/ax25/axports.hamstack-bak' '/etc/ax25/axports'", "gpasswd -d op dialout", "Remove pat from"} {
		if !strings.Contains(text, want) {
			t.Errorf("plan lacks %q:\n%s", want, text)
		}
	}
	// hamstack itself goes last: every step before it may need it.
	if last := steps[len(steps)-1].Desc; !strings.HasPrefix(last, "Remove hamstack itself") {
		t.Errorf("last step is %q", last)
	}
}

func TestUninstallFindsChangesFromBeforeTheLedger(t *testing.T) {
	f := uninstallTestFacts(t)
	f.changes = nil
	f.exists = func(p string) bool { return slices.Contains([]string{sysctlFile, firewallHelper, modulesFile}, p) }
	steps, notes, err := uninstallPlan(f, true)
	if err != nil {
		t.Fatal(err)
	}
	text := planText(steps)
	for _, want := range []string{"rm -f " + sysctlFile, "sysctl -w " + portStartKey + "=1024", "nft delete table inet hamstack_1000", "hamstack-network-access.service"} {
		if !strings.Contains(text, want) {
			t.Errorf("plan lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, modulesFile) {
		t.Errorf("the AX.25 setup belongs to the apps, which are kept:\n%s", text)
	}
	if !strings.Contains(strings.Join(notes, "\n"), "set back to Linux's default, 1024") {
		t.Errorf("the guessed value must be said: %v", notes)
	}
}

func TestUninstallMovesPatSettingsOutAndRemovesOnlyHamstackFolders(t *testing.T) {
	f := uninstallTestFacts(t)
	f.changes = nil
	patConf := filepath.Join(f.cfgDir, "pat", "k0test.json")
	mailbox := filepath.Join(f.home, ".local", "share", "pat", "mailbox", "K0TEST", "in", "msg.b2f")
	for _, p := range []string{patConf, mailbox, filepath.Join(f.cfgDir, "config.json"), filepath.Join(f.stateDir, "pat.log"), filepath.Join(f.shareDir, "installed", "wsjtx"), f.exe} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	f.patConfigs = map[string]string{"k0test": patConf}
	steps, _, err := uninstallPlan(f, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range steps {
		if s.Do != nil {
			if err := s.Do(); err != nil {
				t.Fatalf("%s: %v", s.Desc, err)
			}
		}
	}
	for _, gone := range []string{f.cfgDir, f.stateDir, f.shareDir, f.exe} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s must be gone: %v", gone, err)
		}
	}
	for _, kept := range []string{mailbox, filepath.Join(f.patDir, "hamstack-k0test.json")} {
		if _, err := os.Stat(kept); err != nil {
			t.Errorf("%s must be kept: %v", kept, err)
		}
	}
}
