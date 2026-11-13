// Package groups checks the group memberships that let a user open USB serial ports and sound
// cards, and reports whether each reaches the places that matter: the account, the current login,
// and the background user manager that actually runs hamstack's services.
package groups

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"syscall"

	"github.com/ATCUSA/hamstack/internal/distro"
)

// SerialGroupName is the group that owns USB serial ports: uucp on Arch, dialout on Debian and
// Fedora. If sample (an existing serial device) is given, its real owner wins.
func SerialGroupName(fam distro.Family, sample string) string {
	if sample != "" {
		var st syscall.Stat_t
		if syscall.Stat(sample, &st) == nil && st.Gid != 0 {
			if g, err := user.LookupGroupId(strconv.Itoa(int(st.Gid))); err == nil {
				return g.Name
			}
		}
	}
	if fam == distro.Arch {
		return "uucp"
	}
	return "dialout"
}

// AudioGroupName is the group that owns sound devices. A logged-in desktop session gets access
// through logind regardless; services running with nobody logged in need real membership.
const AudioGroupName = "audio"

// Info is one group's state for the current user.
type Info struct {
	Name         string
	Exists       bool
	GID          int
	Member       bool // listed for the account (/etc/group): effective at the next login
	Session      bool // this process has it: the current login already includes it
	Manager      bool // the user's service manager has it (what hamstack's services run under)
	ManagerKnown bool // false when the manager could not be inspected
}

// Lookup reports the state of a group for the current user.
func Lookup(name string) Info {
	info := Info{Name: name}
	g, err := user.LookupGroup(name)
	if err != nil {
		return info
	}
	info.Exists = true
	info.GID, _ = strconv.Atoi(g.Gid)
	if u, err := user.Current(); err == nil {
		if ids, err := u.GroupIds(); err == nil {
			for _, id := range ids {
				if id == g.Gid {
					info.Member = true
				}
			}
		}
	}
	if gs, err := os.Getgroups(); err == nil {
		for _, id := range gs {
			if id == info.GID {
				info.Session = true
			}
		}
	}
	if gids, err := ManagerGIDs(); err == nil {
		info.ManagerKnown = true
		for _, id := range gids {
			if id == info.GID {
				info.Manager = true
			}
		}
	}
	return info
}

// ManagerGIDs returns the supplementary groups of the user's systemd service manager.
func ManagerGIDs() ([]int, error) {
	u, err := user.Current()
	if err != nil {
		return nil, err
	}
	out, err := exec.Command("systemctl", "show", "user@"+u.Uid+".service", "-p", "MainPID", "--value").Output()
	if err != nil {
		return nil, err
	}
	pid := strings.TrimSpace(string(out))
	if pid == "" || pid == "0" {
		return nil, fmt.Errorf("the user service manager is not running")
	}
	b, err := os.ReadFile("/proc/" + pid + "/status")
	if err != nil {
		return nil, err
	}
	return ParseStatusGroups(string(b)), nil
}

// ParseStatusGroups reads the "Groups:" line of /proc/<pid>/status.
func ParseStatusGroups(status string) []int {
	for _, line := range strings.Split(status, "\n") {
		rest, ok := strings.CutPrefix(line, "Groups:")
		if !ok {
			continue
		}
		var out []int
		for _, f := range strings.Fields(rest) {
			if n, err := strconv.Atoi(f); err == nil {
				out = append(out, n)
			}
		}
		return out
	}
	return nil
}

// Advice says what is wrong with a group's state and how to fix it; "" means all is well.
func (i Info) Advice() (problem, fix string) {
	switch {
	case !i.Exists:
		return "", "" // this system has no such group: nothing to do
	case !i.Member:
		return fmt.Sprintf("you are not in the %q group", i.Name), "hamstack groups fix"
	case !i.Session:
		return fmt.Sprintf("you were added to %q, but this login started before that", i.Name), "log out and back in (a reboot works too)"
	case i.ManagerKnown && !i.Manager:
		return fmt.Sprintf("your background service manager started before you were added to %q, so services cannot use it yet", i.Name),
			"restart it with: sudo systemctl restart user@" + strconv.Itoa(os.Getuid()) + ".service (stops your hamstack services; start them again), or reboot"
	}
	return "", ""
}
