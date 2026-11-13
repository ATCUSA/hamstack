// Package netinfo reports what is listening on this machine and how far each listener can be reached.
// It reads /proc/net/tcp and tcp6, so it needs no privileges and no tools like ss.
package netinfo

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Scope is how far a listening socket can be reached from.
type Scope int

const (
	Local   Scope = iota // loopback only: this computer
	Network              // all interfaces: anyone who can reach this computer
	Address              // one specific address
)

// Listener is a TCP socket in the listening state.
type Listener struct {
	IP   net.IP
	Port int
}

// Scope classifies the listener's address.
func (l Listener) Scope() Scope {
	switch {
	case l.IP.IsLoopback():
		return Local
	case l.IP.IsUnspecified():
		return Network
	}
	return Address
}

// parseHexIP reads the address column of /proc/net/tcp: IPv4 as one little-endian 32-bit word, IPv6 as
// four of them.
func parseHexIP(h string) net.IP {
	if len(h) != 8 && len(h) != 32 {
		return nil
	}
	ip := make([]byte, len(h)/2)
	for w := 0; w < len(h)/8; w++ {
		for i := 0; i < 4; i++ {
			b, err := strconv.ParseUint(h[w*8+i*2:w*8+i*2+2], 16, 8)
			if err != nil {
				return nil
			}
			ip[w*4+3-i] = byte(b) // each 32-bit word is stored in host (little-endian) order
		}
	}
	return net.IP(ip)
}

// Parse reads the text of /proc/net/tcp or /proc/net/tcp6 and returns the listening sockets.
func Parse(text string) []Listener {
	var out []Listener
	for i, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if i == 0 || len(f) < 4 || f[3] != "0A" { // 0A is TCP_LISTEN
			continue
		}
		addr, port, ok := strings.Cut(f[1], ":")
		if !ok {
			continue
		}
		p, err := strconv.ParseUint(port, 16, 16)
		ip := parseHexIP(addr)
		if err != nil || ip == nil {
			continue
		}
		out = append(out, Listener{IP: ip, Port: int(p)})
	}
	return out
}

// Listeners returns every listening TCP socket under root ("/" for the real system).
func Listeners(root string) []Listener {
	var out []Listener
	for _, f := range []string{"proc/net/tcp", "proc/net/tcp6"} {
		if b, err := os.ReadFile(filepath.Join(root, f)); err == nil {
			out = append(out, Parse(string(b))...)
		}
	}
	return out
}

// Find returns the widest-reaching listener on a port: if a port is open on all interfaces and on
// loopback, what matters is that it can be reached from the network.
func Find(ls []Listener, port int) (Listener, bool) {
	var best Listener
	found := false
	for _, l := range ls {
		if l.Port != port {
			continue
		}
		if !found || reach(l.Scope()) > reach(best.Scope()) {
			best, found = l, true
		}
	}
	return best, found
}

// reach orders the scopes by how far they reach: loopback, then one address, then every interface.
func reach(s Scope) int {
	switch s {
	case Network:
		return 2
	case Address:
		return 1
	}
	return 0
}

// LANAddrs returns this computer's non-loopback IPv4 addresses, the ones other computers would use.
func LANAddrs() []string {
	addrs, _ := net.InterfaceAddrs()
	var out []string
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && n.IP.IsGlobalUnicast() {
			out = append(out, n.IP.String())
		}
	}
	return out
}
