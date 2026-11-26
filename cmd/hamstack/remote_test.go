package main

import (
	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/versions"
	"github.com/ATCUSA/hamstack/internal/web"
	"strings"
	"testing"
)

func TestDesktopCompatibility(t *testing.T) {
	for _, desktop := range []string{"GNOME", "KDE", "Plasma", "Weston"} {
		if bin, why := desktopBackend("wayland", desktop); bin != "" || why == "" {
			t.Fatalf("unsupported desktop accepted: %s", desktop)
		}
	}
	if bin, _ := desktopBackend("wayland", "Hyprland"); bin != "wayvnc" {
		t.Fatal(bin)
	}
	if bin, _ := desktopBackend("x11", "XFCE"); bin != "x0vncserver" {
		t.Fatal(bin)
	}
	if bin, _ := desktopBackend("wayland", ""); bin != "" {
		t.Fatal("unknown compositor must not be reported ready")
	}
	if bin, _ := desktopBackend("", ""); bin != "" {
		t.Fatal("headless must not claim full desktop")
	}
}
func TestNetworkChangesOnlyAffectTheirService(t *testing.T) {
	before := config.Config{Web: &config.WebSettings{Port: 8443, Access: "local"}, NoPat: true}
	after := cloneConfig(before)
	if err := after.SetNetworkAccess("web", "network"); err != nil {
		t.Fatal(err)
	}
	names := changedServices(before, after)
	if len(names) != 1 || names[0] != "web" {
		t.Fatalf("wrong restart scope: %v", names)
	}
	if before.Network["web"] != "" {
		t.Fatal("copy mutated original policy")
	}
}
func TestSelfUpdateChoosesCorrectPlatform(t *testing.T) {
	asset, err := selfUpdateAsset([]versions.Asset{{Name: "hamstack_linux_amd64.sha256"}, {Name: "hamstack_linux_arm64.tar.gz"}, {Name: "hamstack_linux_amd64.tar.gz"}}, "linux", "amd64")
	if err != nil || asset.Name != "hamstack_linux_amd64.tar.gz" {
		t.Fatalf("%+v %v", asset, err)
	}
	if a, e := selfUpdateAsset([]versions.Asset{{Name: "hamstack-linux-arm64"}, {Name: "hamstack-linux-armv7"}}, "linux", "arm"); e != nil || a.Name != "hamstack-linux-armv7" {
		t.Fatalf("armv7 selected wrong asset: %+v %v", a, e)
	}
	if _, err := selfUpdateAsset([]versions.Asset{{Name: "checksums.txt"}}, "linux", "amd64"); err == nil {
		t.Fatal("must not install checksum file")
	}
}
func TestWebManagementRejectsUnknownAndUnconfirmedActions(t *testing.T) {
	// These requests must fail before touching configuration, services or the OS.
	for _, action := range []string{"shell", "maintenance.reboot", "network.access", "updates.self", "program.install"} {
		_, err := (webBackend{}).Manage(web.ManagementRequest{Action: action})
		if err == nil {
			t.Fatalf("accepted %s", action)
		}
	}
}
func TestInstalledAwareSuggestionLabels(t *testing.T) {
	if got := dependencyLabels(nil); got != "" {
		t.Fatal(got)
	}
	if got := dependencyLabels([]string{"not-a-catalog-program"}); strings.Contains(got, "installed") {
		t.Fatal(got)
	}
}

// hamstack never updates itself to an older release: a build between releases counts as at least its release.
func TestSelfUpdateNeverGoesBack(t *testing.T) {
	for _, tc := range []struct {
		current, release string
		want             bool
	}{
		{"v0.2.0-75-gff7d909", "v0.2.0", false}, // what happened on the Pi
		{"v0.2.0-75-gff7d909", "v0.3.0", true},
		{"v0.3.0", "v0.3.0", false},
		{"v0.3.0", "v0.2.0", false},
		{"v0.2.0", "v0.3.0", true},
		{"dev", "v0.3.0", true},
	} {
		if got := selfUpdateGoesForward(tc.current, tc.release); got != tc.want {
			t.Errorf("%s -> %s: %v", tc.current, tc.release, got)
		}
	}
}
