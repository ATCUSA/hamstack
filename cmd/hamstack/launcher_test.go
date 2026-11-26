package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ATCUSA/hamstack/internal/apps"
	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/ledger"
)

// launcherTest gives the test a home of its own and two "installed" programs.
func launcherTest(t *testing.T) (home string) {
	t.Helper()
	home = t.TempDir()
	bin := t.TempDir()
	for _, name := range []string{"fldigi", "flrig"} {
		os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nexit 0\n"), 0o755)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", bin)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	return home
}

func TestExecFieldQuotesWhatNeedsIt(t *testing.T) {
	got, err := execField([]string{"/home/u/my tools/hamstack", "launch", "wsjtx"})
	if err != nil || got != `"/home/u/my tools/hamstack" launch wsjtx` {
		t.Errorf("%q %v", got, err)
	}
	for _, bad := range []string{`/x/"y`, "/x/$y", "/x/`y", `/x/\y`, "/x/%f", "a\nb"} {
		if _, err := execField([]string{bad}); err == nil {
			t.Errorf("%q must be refused, not half-escaped", bad)
		}
	}
}

func TestLauncherEntriesFollowTheSetup(t *testing.T) {
	launcherTest(t)
	cfg := config.Config{}
	got := launcherEntries(cfg, "/usr/bin/hamstack")
	names := map[string]launcherEntry{}
	for _, e := range got {
		names[e.File] = e
	}
	if _, ok := names["hamstack-dashboard.desktop"]; ok {
		t.Error("no dashboard, no dashboard entry")
	}
	if tui := names["hamstack-tui.desktop"]; !tui.Terminal || tui.Exec[1] != "tui" {
		t.Errorf("the terminal interface needs a terminal: %+v", tui)
	}
	for _, want := range []string{"hamstack-app-fldigi.desktop", "hamstack-app-flrig.desktop"} {
		if _, ok := names[want]; !ok {
			t.Errorf("an installed program has an entry: missing %s", want)
		}
	}
	for _, not := range []string{"hamstack-app-wsjtx.desktop", "hamstack-app-console.desktop"} {
		if _, ok := names[not]; ok {
			t.Errorf("%s: a program that is not installed (or the console) has no entry", not)
		}
	}
	cfg.Web = &config.WebSettings{Port: 8443, Access: "local"}
	for _, e := range launcherEntries(cfg, "/usr/bin/hamstack") {
		if e.File == "hamstack-dashboard.desktop" && (e.Exec[0] != "xdg-open" || !strings.HasPrefix(e.Exec[1], "https://")) {
			t.Errorf("the dashboard entry opens its address: %v", e.Exec)
		}
	}
}

func TestDesktopFileHasTheDebugAction(t *testing.T) {
	text, err := desktopFile(launcherEntry{File: "x.desktop", Name: "Fldigi (hamstack)", Comment: "c", Icon: "fldigi",
		Exec: []string{"/usr/bin/hamstack", "launch", "fldigi"}, Debug: []string{"/usr/bin/hamstack", "launch", "fldigi", "--debug"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"[Desktop Entry]", "Name=Fldigi (hamstack)", "Exec=/usr/bin/hamstack launch fldigi\n", "Terminal=false", "Actions=debug;",
		"[Desktop Action debug]", "Name=Launch with debug output", "Exec=/usr/bin/hamstack launch fldigi --debug\n", launcherMarker} {
		if !strings.Contains(text, want) {
			t.Errorf("lacks %q:\n%s", want, text)
		}
	}
	plain, _ := desktopFile(launcherEntry{File: "y.desktop", Name: "Hamstack", Exec: []string{"hamstack", "tui"}, Terminal: true})
	if strings.Contains(plain, "Actions=") || !strings.Contains(plain, "Terminal=true") {
		t.Errorf("no debug action unless asked for:\n%s", plain)
	}
}

func TestInstallAndRemoveLaunchersOnlyTouchTheirOwn(t *testing.T) {
	home := launcherTest(t)
	dir := filepath.Join(home, ".local", "share", "applications")
	os.MkdirAll(dir, 0o755)
	theirs := filepath.Join(dir, "hamstack-app-fldigi.desktop") // a file with our name that is not ours
	os.WriteFile(theirs, []byte("[Desktop Entry]\nName=Mine\n"), 0o644)
	other := filepath.Join(dir, "firefox.desktop")
	os.WriteFile(other, []byte("[Desktop Entry]\nName=Firefox\n"), 0o644)
	stale := filepath.Join(dir, "hamstack-app-xastir.desktop") // from a program that has since gone
	os.WriteFile(stale, []byte("[Desktop Entry]\nName=Old\n"+launcherMarker+"\n"), 0o644)

	written, err := installLaunchers(config.Config{}, "/usr/bin/hamstack")
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(theirs); !strings.Contains(string(b), "Name=Mine") {
		t.Error("someone else's file must not be replaced")
	}
	if fileExists(stale) {
		t.Error("an entry for a program that is gone is removed")
	}
	flrig := filepath.Join(dir, "hamstack-app-flrig.desktop")
	if !fileExists(flrig) || !fileExists(filepath.Join(dir, "hamstack-tui.desktop")) {
		t.Errorf("entries missing: %v", written)
	}
	ch, _ := ledger.Load(home)
	if len(ch) != 1 || ch[0].Kind != ledger.Files || ch[0].For != "launcher" || ch[0].Role != ledger.Support {
		t.Fatalf("ledger: %+v", ch)
	}
	// Running it again changes nothing and does not grow the record.
	if _, err := installLaunchers(config.Config{}, "/usr/bin/hamstack"); err != nil {
		t.Fatal(err)
	}
	if ch, _ := ledger.Load(home); len(ch) != 1 || len(ch[0].Names) != len(written) {
		t.Errorf("the record after a second install: %+v", ch)
	}

	n, err := removeLaunchers()
	if err != nil || n != len(written)-1 { // the menu entries; the icon they share is removed too
		t.Fatalf("removed %d, want %d: %v", n, len(written)-1, err)
	}
	if fileExists(iconPath()) {
		t.Error("the icon we wrote must go with the entries")
	}
	if fileExists(flrig) {
		t.Error("ours must go")
	}
	if !fileExists(theirs) || !fileExists(other) {
		t.Error("what is not ours must stay")
	}
	if ch, _ := ledger.Load(home); len(ch) != 0 {
		t.Errorf("the record goes with them: %+v", ch)
	}
}

func TestUninstallRemovesLaunchersEvenWhenKeepingApps(t *testing.T) {
	f := uninstallTestFacts(t)
	f.changes = []ledger.Change{{Kind: ledger.Files, Role: ledger.Support, For: "launcher", Names: []string{filepath.Join(f.home, ".local", "share", "applications", "hamstack-tui.desktop")}}}
	steps, _, err := uninstallPlan(f, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(planText(steps), "Remove launcher from") {
		t.Errorf("the menu entries are hamstack's own:\n%s", planText(steps))
	}
}

func TestTerminalCommandPicksTheFirstInstalled(t *testing.T) {
	old := lookPath
	t.Cleanup(func() { lookPath = old })
	lookPath = func(name string) (string, error) {
		if name == "konsole" || name == "xterm" {
			return "/usr/bin/" + name, nil
		}
		return "", fmt.Errorf("no")
	}
	got, ok := terminalCommand()
	if !ok || strings.Join(got, " ") != "/usr/bin/konsole -e" {
		t.Errorf("%v %v", got, ok)
	}
	lookPath = func(string) (string, error) { return "", fmt.Errorf("no") }
	if _, ok := terminalCommand(); ok {
		t.Error("no terminal installed")
	}
}

func TestLaunchRefusesWhatItCannotStart(t *testing.T) {
	launcherTest(t)
	for args, want := range map[string]string{"nosuch": "no program", "console": "no program", "wsjtx": "not installed", "": "usage", "fldigi --bogus": "usage"} {
		err := cmdLaunch(strings.Fields(args))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("launch %q: %v, want an error saying %q", args, err, want)
		}
	}
}

func TestIconIsInstalledForLauncherEntriesAndRemovedWithThem(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	path, err := installIcon()
	if err != nil || path != iconPath() {
		t.Fatalf("%q %v", path, err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), iconMarker) || !strings.Contains(string(b), "<svg") {
		t.Fatalf("icon not written as expected: %.80s", b)
	}
	// someone else's icon of that name is not replaced
	other := t.TempDir()
	t.Setenv("XDG_DATA_HOME", other)
	_ = os.MkdirAll(filepath.Dir(iconPath()), 0o755)
	_ = os.WriteFile(iconPath(), []byte("<svg>mine</svg>"), 0o644)
	if p, _ := installIcon(); p != "" {
		t.Fatal("replaced a file that is not ours")
	}
	if _, err := removeLaunchers(); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(iconPath()); string(b) != "<svg>mine</svg>" {
		t.Fatal("removed a file that is not ours")
	}
}

// A radio program's own menu entry starts it without stopping the modem: with hamstack's entry installed, the own one
// is hidden (an override for a system entry, a marked copy for one in the user's folder), and put back on remove.
func TestOwnMenuEntriesOfRadioProgramsAreHiddenAndPutBack(t *testing.T) {
	user, sys := t.TempDir(), t.TempDir()
	wsOwn := "[Desktop Entry]\r\nName=WS\r\nExec=ws\r\nType=Application\r\n"                          // as WSJT-X Improved installs it: Windows line endings
	os.WriteFile(filepath.Join(user, "ws.desktop"), []byte(wsOwn), 0o644)                             // a source build's, in the user's folder
	os.WriteFile(filepath.Join(sys, "wsjtx.desktop"), []byte("[Desktop Entry]\nExec=wsjtx\n"), 0o644) // the distribution's
	os.WriteFile(filepath.Join(sys, "fldigi.desktop"), []byte("[Desktop Entry]\nExec=fldigi\n"), 0o644)
	installed := func(a apps.App) bool { return a.Name == "wsjtx" } // fldigi has no hamstack entry: its own stays

	created, edited := hideOwnEntries(user, []string{sys}, installed)
	if len(created) != 1 || filepath.Base(created[0]) != "wsjtx.desktop" {
		t.Fatalf("override files: %v", created)
	}
	if b, _ := os.ReadFile(created[0]); !strings.Contains(string(b), "Hidden=true") {
		t.Errorf("the override must hide the system entry: %s", b)
	}
	if len(edited) != 1 || edited[0].Kind != ledger.AppConfig || edited[0].Path != filepath.Join(user, "ws.desktop") {
		t.Fatalf("edited: %+v", edited)
	}
	if b, _ := os.ReadFile(filepath.Join(user, "ws.desktop")); !strings.Contains(string(b), "NoDisplay=true") || !strings.Contains(string(b), "Exec=ws") {
		t.Errorf("the user's entry is marked, not replaced: %s", b)
	}
	if fileExists(filepath.Join(user, "fldigi.desktop")) {
		t.Error("a program without a hamstack entry keeps its own")
	}
	if c, e := hideOwnEntries(user, []string{sys}, installed); len(c) != 1 || len(e) != 0 {
		t.Errorf("a second run must not mark twice: %v %v", c, e)
	}

	if n := unhideOwnEntries(user); n != 2 {
		t.Errorf("put back %d, want 2", n)
	}
	if fileExists(filepath.Join(user, "wsjtx.desktop")) {
		t.Error("the override goes")
	}
	if b, _ := os.ReadFile(filepath.Join(user, "ws.desktop")); string(b) != wsOwn {
		t.Errorf("the user's entry is back as it was: %q", b)
	}
}
