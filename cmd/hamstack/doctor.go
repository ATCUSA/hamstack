package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/ATCUSA/hamstack/internal/audio"
	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/doctor"
	"github.com/ATCUSA/hamstack/internal/gps"
	"github.com/ATCUSA/hamstack/internal/groups"
	"github.com/ATCUSA/hamstack/internal/hamlib"
	"github.com/ATCUSA/hamstack/internal/patcfg"
	"github.com/ATCUSA/hamstack/internal/profile"
	"github.com/ATCUSA/hamstack/internal/svc"
	"github.com/ATCUSA/hamstack/internal/units"
	"github.com/ATCUSA/hamstack/internal/versions"
)

func realSystem() doctor.System {
	return doctor.System{
		SystemdErr: svc.Available(),
		LookPath:   versions.Which,
		Glob:       filepath.Glob,
		Cards:      audio.Cards,
		Access:     func(p string) error { return syscall.Access(p, 6) }, // read + write
		Group: func(p string) string {
			var st syscall.Stat_t
			if syscall.Stat(p, &st) != nil {
				return ""
			}
			g, err := user.LookupGroupId(strconv.Itoa(int(st.Gid)))
			if err != nil {
				return ""
			}
			return g.Name
		},
		Listening: func(port int) bool {
			c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 300*time.Millisecond)
			if err != nil {
				return false
			}
			c.Close()
			return true
		},
		ServiceActive: func(name string) bool {
			s, ok := units.ParseName(name)
			return ok && svc.Status(s.Unit()).Active == "active"
		},
		FileExists: func(p string) bool { _, err := os.Stat(p); return err == nil },
		UnitsState: unitsState,
		AnyEnabled: anyEnabled,
		PatLocator: func() (string, bool) {
			cfg, _ := config.Load()
			if st, ok := cfg.ActiveStation(); ok {
				return patcfg.Locator(st.PatConfig)
			}
			return "", false
		},
		SiteHint: func() (string, string, bool) {
			cfg, _ := config.Load()
			if b, ok := betterSite(cfg, scoreSites(cfg)); ok {
				return b.Site.ID, fmt.Sprintf("the radios of site %q are plugged in, not those of %q", b.Site.ID, cfg.CurrentSite), true
			}
			return "", "", false
		},
		PatPacketPort: func() (int, bool) {
			cfg, _ := config.Load()
			if st, ok := cfg.ActiveStation(); ok {
				return patcfg.PacketPort(st.PatConfig)
			}
			return 0, false
		},
		PatHFPort: func() (int, bool) {
			cfg, _ := config.Load()
			if st, ok := cfg.ActiveStation(); ok {
				return patcfg.HFPort(st.PatConfig)
			}
			return 0, false
		},
		SerialGroup: func() string { return groupRows()[0].info.Name },
		GroupAdvice: func(name string) (bool, string, string) {
			i := groups.Lookup(name)
			p, f := i.Advice()
			return i.Exists, p, f
		},
		Linger:   svc.LingerEnabled,
		GPSDPath: func() (string, error) { return versions.WhichSbin("gpsd") },
		GPSDevice: func() (string, error) {
			cfg, _ := config.Load()
			if cfg.GPS == nil {
				return "", fmt.Errorf("no GPS set up")
			}
			return resolveGPS(*cfg.GPS, snapshotOrEmpty())
		},
		SystemGPSD: gpsdSystemActive,
		GPSFix: func() (bool, string, error) {
			cfg, _ := config.Load()
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			st, err := gps.Query(ctx, cfg.GPSAddr(), false)
			if err != nil && !errors.Is(err, gps.ErrNoFix) {
				return false, "", err
			}
			if !st.Fix() {
				return false, fmt.Sprintf("%s, %d of %d satellites in use", st.ModeName(), st.SatsUsed, st.SatsSeen), nil
			}
			grid, _ := gps.Grid(st.Lat, st.Lon, 6)
			return true, fmt.Sprintf("%s, grid %s", st.ModeName(), grid), nil
		},
		RigModels: func() (map[int]string, error) {
			path, err := versions.Which("rigctld")
			if err != nil {
				return nil, err
			}
			m, err := hamlib.Installed(path)
			return map[int]string(m), err
		},
		HamlibVersion: func() string {
			if pkg, ok := catalogPkg("hamlib"); ok {
				if in := versions.FindInstalled(pkg); in != nil {
					return in.Version
				}
			}
			return ""
		},
	}
}

// unitsState reports whether the unit files exist and match what 'units install' would write now.
func unitsState() (installed, current bool) {
	dir, err := units.Dir()
	if err != nil {
		return false, false
	}
	p, _, err := buildPlan()
	if err != nil {
		have, _ := units.Installed()
		return len(have) > 0, false
	}
	gen := units.Generate(p)
	current = true
	for name, want := range gen {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return false, false
		}
		if string(got) != want {
			current = false
		}
	}
	if stale, _ := units.Stale(p); len(stale) > 0 {
		current = false // units nothing needs any more (or from before rigs existed) are still installed
	}
	return true, current
}

func cmdDoctor() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	profs, err := profile.All(userProfileDir())
	if err != nil {
		return err
	}
	results := doctor.Run(doctor.Inputs{Cfg: cfg, Profiles: profs, Sys: realSystem()})
	printDoctor(results)
	if doctor.Worst(results) == doctor.Fail {
		return fmt.Errorf("problems found")
	}
	return nil
}

func printDoctor(rs []doctor.Result) {
	mark := map[doctor.Level]string{doctor.OK: "ok  ", doctor.Warn: "warn", doctor.Fail: "FAIL"}
	for _, r := range rs {
		fmt.Printf("[%s] %-27s %s\n", mark[r.Level], r.Name, r.Detail)
		if r.Level != doctor.OK && r.Fix != "" {
			fmt.Printf("       fix: %s\n", r.Fix)
		}
	}
}

// anyEnabled reports whether any of the user's services is set to start at login.
func anyEnabled() bool {
	cfg, _ := config.Load()
	for _, s := range units.ServicesFor(cfg) {
		if svc.Status(s.Unit()).Enabled == "enabled" {
			return true
		}
	}
	return false
}
