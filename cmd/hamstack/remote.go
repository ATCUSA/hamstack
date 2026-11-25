package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/ATCUSA/hamstack/internal/apps"
	"github.com/ATCUSA/hamstack/internal/distro"
	"github.com/ATCUSA/hamstack/internal/install"
	"github.com/ATCUSA/hamstack/internal/versions"
	"slices"
)

type remoteCheck struct {
	Name   string `json:"name"`
	Ready  bool   `json:"ready"`
	Detail string `json:"detail"`
}

func desktopBackend(session, desktop string) (string, string) {
	if session == "x11" {
		return "x0vncserver", "Shares the existing X11 desktop"
	}
	d := strings.ToLower(desktop)
	for _, unsupported := range []string{"gnome", "kde", "plasma", "weston"} {
		if strings.Contains(d, unsupported) {
			return "", "wayvnc does not support " + desktop + "; individual apps on Xvnc still work"
		}
	}
	if session == "wayland" {
		known := false
		for _, name := range []string{"sway", "hyprland", "labwc", "wayfire"} {
			if strings.Contains(d, name) {
				known = true
			}
		}
		if !known {
			return "", "Wayland compositor compatibility is unverified; no supported compositor identity was detected. Individual app displays remain available."
		}
		return "wayvnc", "Requires a compositor supporting Wayland screen capture and virtual input (for example Sway or Hyprland)"
	}
	return "", "No graphical session detected for this user; individual apps can run headlessly with Xvnc"
}
func desktopCapability() (string, string) {
	session := os.Getenv("XDG_SESSION_TYPE")
	desktop := os.Getenv("XDG_CURRENT_DESKTOP")
	if desktop == "" {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "loginctl", "show-user", strconv.Itoa(os.Getuid()), "-p", "Display", "--value").Output()
		if err == nil {
			id := strings.TrimSpace(string(out))
			if id != "" {
				out, err = exec.CommandContext(ctx, "loginctl", "show-session", id, "-p", "Type", "-p", "Desktop").Output()
				if err == nil {
					for _, line := range strings.Split(string(out), "\n") {
						key, value, ok := strings.Cut(line, "=")
						if !ok {
							continue
						}
						if key == "Desktop" {
							desktop = value
						}
						if key == "Type" && session == "" {
							session = value
						}
					}
				}
			}
		}
	}
	if waylandDisplay() != "" {
		session = "wayland"
	} else if os.Getenv("DISPLAY") != "" {
		session = "x11"
	}
	return desktopBackend(session, desktop)
}
func remoteChecks() []remoteCheck {
	var out []remoteCheck
	for _, item := range []struct{ name, bin string }{{"Individual app displays", "Xvnc"}, {"Window manager (extra windows)", "icewm"}} {
		path, err := desktopPath(item.bin)
		detail := path
		if err != nil {
			detail = item.bin + " is not installed"
		}
		if real := apps.RealVNCServer(); item.bin == "Xvnc" && real != "" {
			if err != nil {
				detail = "RealVNC's Xvnc is at " + real + "; app displays need TigerVNC, which installs beside it (hamstack dashboard on)"
			} else {
				detail = path + " (TigerVNC; RealVNC's Xvnc at " + real + " is left alone)"
			}
		}
		out = append(out, remoteCheck{item.name, err == nil, detail})
	}
	dir := noVNCDir()
	out = append(out, remoteCheck{"Browser viewer", dir != "", func() string {
		if dir == "" {
			return "noVNC web assets are not installed"
		}
		return dir
	}()})
	bin, detail := desktopCapability()
	ready := false
	if bin != "" {
		_, err := desktopPath(bin)
		ready = err == nil
		if err != nil {
			detail += "; install " + bin
		}
	}
	out = append(out, remoteCheck{"Whole desktop", ready, detail})
	return out
}
func printRemoteChecks() {
	for _, c := range remoteChecks() {
		state := "ready"
		if !c.Ready {
			state = "needs attention"
		}
		fmt.Printf("%-24s %-16s %s\n", c.Name, state, c.Detail)
	}
}

func remoteInstallSteps(desktop bool) ([]install.Step, error) {
	env := install.HostEnv()
	pkgs := webPackages(env.Distro.Family)
	if desktop {
		bin, reason := desktopCapability()
		if bin == "" {
			return nil, fmt.Errorf("%s", reason)
		}
		if _, err := desktopPath(bin); err != nil {
			pkg := "wayvnc"
			if bin == "x0vncserver" {
				switch env.Distro.Family {
				case distro.Debian:
					pkg = "tigervnc-scraping-server"
				case distro.Arch:
					pkg = "tigervnc"
				case distro.Fedora:
					pkg = "tigervnc-server"
				default:
					return nil, fmt.Errorf("install %s first", bin)
				}
			}
			if !slices.Contains(pkgs, pkg) {
				pkgs = append(pkgs, pkg)
			}
		}
	}
	if len(pkgs) == 0 {
		return nil, nil
	}
	steps, err := env.PkgSteps("Install missing remote access components: "+strings.Join(pkgs, ", "), pkgs...)
	return install.For("web", steps), err
}

func desktopPath(bin string) (string, error) {
	if bin == "Xvnc" {
		return apps.VNCServer()
	}
	path, err := versions.Which(bin)
	if err != nil && bin == "x0vncserver" {
		return versions.Which("x0tigervncserver")
	}
	return path, err
}
