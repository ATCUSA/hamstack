// Package ax25 looks at, and sets up, the Linux kernel's AX.25 stack: the kernel module, the
// /etc/ax25/axports file, and the network interface (ax0) that kissattach creates for a KISS TNC.
//
// It is optional. hamstack's own packet tools talk to Direwolf directly (package agw) and need none of
// this; the kernel stack is for the classic tools (axcall, axlisten, ax25d, listen) and for anything
// that wants an AX.25 socket. Most of it needs root, so this package only reads and plans: the commands
// that change the system are shown to the user and run by the install steps.
package ax25

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// KernelState says whether the running kernel can do AX.25.
type KernelState int

const (
	KernelMissing   KernelState = iota // the kernel has no AX.25 module (recent kernels dropped it)
	KernelAvailable                    // there is a module, not loaded yet
	KernelLoaded                       // loaded or built in
)

func (k KernelState) String() string {
	return [...]string{"not in this kernel", "available, not loaded", "loaded"}[k]
}

// Kernel checks the kernel under root ("/" on a real system; tests pass a fake tree).
func Kernel(root string) (KernelState, string) {
	release := ""
	if b, err := os.ReadFile(filepath.Join(root, "proc/sys/kernel/osrelease")); err == nil {
		release = strings.TrimSpace(string(b))
	}
	for _, p := range []string{"sys/module/ax25", "proc/net/ax25"} {
		if _, err := os.Stat(filepath.Join(root, p)); err == nil {
			return KernelLoaded, release
		}
	}
	if release != "" {
		for _, base := range []string{"usr/lib/modules", "lib/modules"} {
			m, _ := filepath.Glob(filepath.Join(root, base, release, "kernel/net/ax25/ax25.ko*"))
			if len(m) > 0 {
				return KernelAvailable, release
			}
		}
	}
	return KernelMissing, release
}

// Port is one line of /etc/ax25/axports: a name for a radio port, and how to use it.
type Port struct {
	Name   string // the name the tools use (axcall PORT ...)
	Call   string // the callsign of this port, usually with an SSID
	Speed  int    // bits per second on the air
	Paclen int    // largest packet
	Window int    // frames in flight
	Desc   string
}

// Line is the axports line for p.
func (p Port) Line() string {
	return fmt.Sprintf("%s\t%s\t%d\t%d\t%d\t%s", p.Name, p.Call, p.Speed, p.Paclen, p.Window, p.Desc)
}

// ParsePorts reads an axports file; comments and blank lines are skipped.
func ParsePorts(text string) []Port {
	var out []Port
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		f := strings.Fields(l)
		if len(f) < 5 {
			continue
		}
		sp, _ := strconv.Atoi(f[2])
		pl, _ := strconv.Atoi(f[3])
		w, _ := strconv.Atoi(f[4])
		out = append(out, Port{Name: f[0], Call: f[1], Speed: sp, Paclen: pl, Window: w, Desc: strings.Join(f[5:], " ")})
	}
	return out
}

// Upsert returns the file with p's line added, or replacing the line of the same name. Everything else,
// comments included, is kept. changed is false when the file already says exactly this.
func Upsert(text string, p Port) (out string, changed bool) {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if text == "" {
		lines = nil
	}
	want := p.Line()
	done := false
	for i, l := range lines {
		f := strings.Fields(l)
		if len(f) >= 1 && !strings.HasPrefix(strings.TrimSpace(l), "#") && f[0] == p.Name {
			if strings.Join(strings.Fields(l), " ") == strings.Join(strings.Fields(want), " ") {
				return text, false
			}
			lines[i] = want
			done = true
			break
		}
	}
	if !done {
		lines = append(lines, want)
	}
	return strings.Join(lines, "\n") + "\n", true
}

// ValidPortName reports whether s can name an axports port: short, no spaces.
func ValidPortName(s string) bool {
	if s == "" || len(s) > 8 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// Iface is an AX.25 network interface (ax0, ax1, ...) the kernel has.
type Iface struct{ Name, Addr string }

// Interfaces lists the AX.25 interfaces under root: network devices of ARPHRD_AX25 type (3).
func Interfaces(root string) []Iface {
	dirs, _ := filepath.Glob(filepath.Join(root, "sys/class/net/*"))
	var out []Iface
	for _, d := range dirs {
		t, err := os.ReadFile(filepath.Join(d, "type"))
		if err != nil || strings.TrimSpace(string(t)) != "3" {
			continue
		}
		out = append(out, Iface{Name: filepath.Base(d)})
	}
	return out
}

// PtyPath is where Direwolf (started with -p) offers its KISS pseudo-terminal. Direwolf fixes the name, so
// only one Direwolf can offer it.
const PtyPath = "/tmp/kisstnc"

// PortsFile is the system's axports file.
const PortsFile = "/etc/ax25/axports"

// DefaultPort returns the port hamstack proposes for a station: one name, the station's callsign, and
// settings that suit Direwolf at 1200 baud.
func DefaultPort(call string) Port {
	return Port{Name: "hamstack", Call: call, Speed: 1200, Paclen: 255, Window: 2, Desc: "hamstack (Direwolf)"}
}
