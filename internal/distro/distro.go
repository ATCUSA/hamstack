// Package distro identifies the host Linux distribution and its package manager.
package distro

import (
	"bufio"
	"os"
	"strings"
)

// Family is the package-management family of the host.
type Family string

const (
	Arch    Family = "arch"
	Debian  Family = "debian"
	Fedora  Family = "fedora"
	Unknown Family = "unknown"
)

// Info describes the host distribution.
type Info struct {
	ID      string // os-release ID, e.g. "arch", "ubuntu"
	Name    string // PRETTY_NAME
	Family  Family
	Manager string // pacman, apt, dnf, or ""
}

// Detect reads /etc/os-release.
func Detect() Info { return parse("/etc/os-release") }

func parse(path string) Info {
	info := Info{Family: Unknown}
	f, err := os.Open(path)
	if err != nil {
		return info
	}
	defer f.Close()
	var like string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		v = strings.Trim(v, `"'`)
		switch k {
		case "ID":
			info.ID = v
		case "ID_LIKE":
			like = v
		case "PRETTY_NAME":
			info.Name = v
		}
	}
	info.Family = family(info.ID + " " + like)
	switch info.Family {
	case Arch:
		info.Manager = "pacman"
	case Debian:
		info.Manager = "apt"
	case Fedora:
		info.Manager = "dnf"
	}
	return info
}

func family(ids string) Family {
	for _, id := range strings.Fields(ids) {
		switch id {
		case "arch", "archarm", "manjaro", "endeavouros", "cachyos", "omarchy":
			return Arch
		case "debian", "ubuntu", "raspbian", "linuxmint", "pop":
			return Debian
		case "fedora", "rhel", "centos", "rocky", "almalinux":
			return Fedora
		}
	}
	return Unknown
}
