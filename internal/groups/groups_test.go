package groups

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ATCUSA/hamstack/internal/distro"
)

func TestParseStatusGroups(t *testing.T) {
	status := "Name:\tsystemd\nUid:\t1000\t1000\t1000\t1000\nGroups:\t10 18 90 957 1000 \nVmPeak:\t1 kB\n"
	if got := ParseStatusGroups(status); !reflect.DeepEqual(got, []int{10, 18, 90, 957, 1000}) {
		t.Errorf("got %v", got)
	}
	if got := ParseStatusGroups("Name:\tx\n"); got != nil {
		t.Errorf("no Groups line: %v", got)
	}
}

func TestSerialGroupDefaults(t *testing.T) {
	if g := SerialGroupName(distro.Arch, ""); g != "uucp" {
		t.Errorf("arch: %s", g)
	}
	for _, f := range []distro.Family{distro.Debian, distro.Fedora, distro.Unknown} {
		if g := SerialGroupName(f, ""); g != "dialout" {
			t.Errorf("%s: %s", f, g)
		}
	}
	// A device that does not exist falls back to the distribution's usual group.
	if g := SerialGroupName(distro.Arch, "/nonexistent/ttyACM0"); g != "uucp" {
		t.Errorf("missing sample: %s", g)
	}
}

func TestAdviceLadder(t *testing.T) {
	cases := []struct {
		name string
		in   Info
		want string // substring of the problem, "" for fine
		fix  string
	}{
		{"no such group", Info{Name: "uucp"}, "", ""},
		{"not a member", Info{Name: "uucp", Exists: true}, "not in", "hamstack groups fix"},
		{"login predates it", Info{Name: "uucp", Exists: true, Member: true}, "this login started before", "log out"},
		{"manager predates it", Info{Name: "uucp", Exists: true, Member: true, Session: true, ManagerKnown: true}, "service manager", "systemctl restart user@"},
		{"manager unknown is not blamed", Info{Name: "uucp", Exists: true, Member: true, Session: true}, "", ""},
		{"all good", Info{Name: "uucp", Exists: true, Member: true, Session: true, Manager: true, ManagerKnown: true}, "", ""},
	}
	for _, c := range cases {
		problem, fix := c.in.Advice()
		if c.want == "" && problem != "" || c.want != "" && !strings.Contains(problem, c.want) {
			t.Errorf("%s: problem %q, want %q", c.name, problem, c.want)
		}
		if c.fix != "" && !strings.Contains(fix, c.fix) {
			t.Errorf("%s: fix %q, want %q", c.name, fix, c.fix)
		}
	}
}
