package ledger

import (
	"os"
	"slices"
	"testing"
)

func TestMergeKeepsTheStateFromBeforeHamstack(t *testing.T) {
	have := Merge(nil,
		Change{Kind: RootFile, Path: "/etc/ax25/axports", Existed: true},
		Change{Kind: Sysctl, Key: "k", Prev: "1024"},
		Change{Kind: Packages, Role: Support, For: "web", Manager: "apt", Names: []string{"novnc", "websockify"}},
	)
	// A second write of the same file finds hamstack's own copy there; the first record must win.
	have = Merge(have,
		Change{Kind: RootFile, Path: "/etc/ax25/axports", Existed: false},
		Change{Kind: Sysctl, Key: "k", Prev: "443"},
		Change{Kind: Packages, Role: Support, For: "web", Manager: "apt", Names: []string{"tigervnc-standalone-server", "novnc"}},
		Change{Kind: Packages, Role: App, For: "wsjtx", Manager: "apt", Names: nil}, // nothing new: no record
	)
	if len(have) != 3 {
		t.Fatalf("want 3 records, got %d: %+v", len(have), have)
	}
	if !have[0].Existed || have[1].Prev != "1024" {
		t.Errorf("the first record of a file or setting must win: %+v", have[:2])
	}
	if want := []string{"novnc", "tigervnc-standalone-server", "websockify"}; !slices.Equal(have[2].Names, want) {
		t.Errorf("packages: got %v, want %v", have[2].Names, want)
	}
}

func TestAPackageBelongsToOneRecord(t *testing.T) {
	have := Merge(nil, Change{Kind: Packages, Role: Build, For: "mercury", Names: []string{"libhamlib4", "gcc"}})
	have = Merge(have, Change{Kind: Packages, Role: App, For: "hamlib", Names: []string{"libhamlib4", "libhamlib-utils"}})
	if !slices.Equal(have[0].Names, []string{"gcc", "libhamlib4"}) && !slices.Equal(have[0].Names, []string{"libhamlib4", "gcc"}) {
		t.Errorf("first record lost a package: %v", have[0].Names)
	}
	if !slices.Equal(have[1].Names, []string{"libhamlib-utils"}) {
		t.Errorf("second record must not repeat libhamlib4: %v", have[1].Names)
	}
}

func TestAddLoadDrop(t *testing.T) {
	home := t.TempDir()
	if c, err := Load(home); err != nil || len(c) != 0 {
		t.Fatalf("empty ledger: %v %v", c, err)
	}
	if err := Add(home, Change{Kind: Group, Role: App, Names: []string{"dialout"}, User: "op"}, Change{Kind: Linger, Role: Support, User: "op"}); err != nil {
		t.Fatal(err)
	}
	if err := Drop(home, func(c Change) bool { return c.Role == Support }); err != nil {
		t.Fatal(err)
	}
	c, _ := Load(home)
	if len(c) != 1 || c[0].Kind != Group {
		t.Fatalf("after dropping support: %+v", c)
	}
	if err := Drop(home, func(Change) bool { return true }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(Path(home)); !os.IsNotExist(err) {
		t.Errorf("an empty ledger must leave no file: %v", err)
	}
}
