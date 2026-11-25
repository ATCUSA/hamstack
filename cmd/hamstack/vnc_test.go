package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWindowManagerPrefersIceWMAndKeepsMatchboxWorking(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	t.Setenv("HOME", t.TempDir())
	if _, _, ok := windowManager(); ok {
		t.Fatal("no window manager installed: none is started")
	}
	put := func(name string) { _ = os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"), 0o755) }
	put("matchbox-window-manager")
	if p, args, ok := windowManager(); !ok || filepath.Base(p) != "matchbox-window-manager" || len(args) == 0 {
		t.Fatalf("an existing matchbox keeps working: %v %v %v", p, args, ok)
	}
	put("icewm")
	if p, args, ok := windowManager(); !ok || filepath.Base(p) != "icewm" || len(args) != 0 {
		t.Fatalf("IceWM runs as it is, with no options of ours: %v %v %v", p, args, ok)
	}
}

func TestIceWMThemeLivesInHamstacksOwnFolder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "")
	themes := t.TempDir()
	old := iceWMThemeDirs
	t.Cleanup(func() { iceWMThemeDirs = old })
	iceWMThemeDirs = []string{themes}
	if dir := iceWMConfigDir(); dir != "" {
		t.Fatalf("this IceWM does not ship the theme: nothing is written, got %q", dir)
	}
	if err := os.MkdirAll(filepath.Join(themes, iceWMTheme), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(themes, iceWMTheme, "default.theme"), []byte("x"), 0o644)
	dir := iceWMConfigDir()
	b, _ := os.ReadFile(filepath.Join(dir, "theme"))
	if dir == "" || string(b) != "Theme=\"Infadel2/default.theme\"\n" {
		t.Fatalf("%q %q", dir, b)
	}
	if rel, err := filepath.Rel(filepath.Join(home, ".local", "share", "hamstack"), dir); err != nil || rel != "icewm" {
		t.Errorf("must be inside hamstack's data folder, never ~/.icewm: %s", dir)
	}
	if _, err := os.Stat(filepath.Join(home, ".icewm")); err == nil {
		t.Error("the person's own IceWM folder must not be touched")
	}
}
