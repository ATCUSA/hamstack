package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/gps"
	"github.com/ATCUSA/hamstack/internal/svc"
	"github.com/ATCUSA/hamstack/internal/units"
	"github.com/ATCUSA/hamstack/internal/usbdev"
	"github.com/ATCUSA/hamstack/internal/versions"
)

const gpsUsage = `usage:
  hamstack gps [status]                 is gpsd running, does the receiver have a fix
  hamstack gps use [DEVICE] [--port N]  use a GPS receiver plugged into this computer (found by itself if there is one)
  hamstack gps use --addr HOST:PORT     use a gpsd that already runs (a system gpsd, or another machine's)
  hamstack gps on|off                   run hamstack's gpsd or not (the setup is kept)
  hamstack gps remove                   forget the GPS setup
  hamstack gps grid [--save] [--timeout 90s] [--chars 6]
                                        read a fix and turn it into your grid square; --save sets it for this site`

func cmdGPS(args []string) error {
	if len(args) == 0 {
		return gpsStatus()
	}
	switch args[0] {
	case "status":
		return gpsStatus()
	case "use":
		return gpsUse(args[1:])
	case "on", "off":
		return gpsOnOff(args[0] == "off")
	case "remove":
		return gpsRemove()
	case "grid":
		return gpsGrid(args[1:])
	case "help", "-h", "--help":
		fmt.Println(gpsUsage)
		return nil
	}
	return fmt.Errorf("unknown gps command %q\n%s", args[0], gpsUsage)
}

// gpsCandidates are the USB serial ports that look like GPS receivers, one entry per physical device
// (a receiver can show several ports; the first interface is the data port).
func gpsCandidates(snap usbdev.Snapshot) []usbdev.Serial {
	var out []usbdev.Serial
	seen := map[string]bool{}
	for _, s := range snap.Serials {
		if !s.LooksLikeGPS() || seen[s.USBID()+"|"+s.Hub] {
			continue
		}
		seen[s.USBID()+"|"+s.Hub] = true
		out = append(out, s)
	}
	return out
}

// parseInterspersed parses flags that may come before or after the positional arguments, and returns the
// positional ones. (splitArgs cannot be used for flags that take a value: it would leave the value behind.)
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}

func serialDevice(s usbdev.Serial) string {
	if s.ByID != "" {
		return s.ByID
	}
	return s.Tty
}

func gpsUse(args []string) error {
	fs := flag.NewFlagSet("gps use", flag.ContinueOnError)
	addr := fs.String("addr", "", "use a gpsd that already runs, HOST:PORT")
	port := fs.Int("port", 0, "port for hamstack's gpsd (default "+fmt.Sprint(config.GPSPort)+")")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) > 1 {
		return fmt.Errorf("%s", gpsUsage)
	}
	var g config.GPS
	switch {
	case *addr != "":
		if len(pos) > 0 {
			return fmt.Errorf("give a device or --addr, not both")
		}
		g.Addr = gps.Addr(*addr)
	default:
		snap, _ := scanDevices()
		var chosen *usbdev.Serial
		switch {
		case len(pos) == 1:
			dev := pos[0]
			g.Device = dev
			if _, err := os.Stat(dev); err != nil {
				return fmt.Errorf("%s does not exist (see: hamstack devices)", dev)
			}
			// Remember which USB device it is, so it is found again if the path changes.
			real, _ := filepath.EvalSymlinks(dev)
			for i, s := range snap.Serials {
				if s.Tty == real || (s.ByID != "" && s.ByID == dev) {
					chosen = &snap.Serials[i]
				}
			}
		default:
			c := gpsCandidates(snap)
			switch len(c) {
			case 0:
				return fmt.Errorf("no GPS receiver found among the USB devices (see: hamstack devices).\n" +
					"Plug it in, or name it: hamstack gps use /dev/serial/by-id/...  A receiver on a generic serial chip\n" +
					"can't be recognised by itself. If gpsd already runs somewhere, use: hamstack gps use --addr HOST:PORT")
			case 1:
				chosen = &c[0]
				g.Device = serialDevice(c[0])
			default:
				var names []string
				for _, s := range c {
					names = append(names, "  "+serialDevice(s)+"  ("+label(s)+")")
				}
				return fmt.Errorf("several GPS receivers found; say which one: hamstack gps use DEVICE\n%s", strings.Join(names, "\n"))
			}
		}
		if chosen != nil {
			g.USB = chosen.USBID()
			if cfg, _ := config.Load(); usedByRig(cfg, *chosen) {
				return fmt.Errorf("%s is a radio's port in your rig setup, not a GPS receiver", serialDevice(*chosen))
			}
		}
		g.Port = *port
	}
	msg, err := mutate(func(cfg *config.Config) (string, error) {
		if err := cfg.SetGPS(g); err != nil {
			return "", err
		}
		return "", nil
	})
	if err != nil {
		return err
	}
	if g.Addr != "" {
		fmt.Printf("GPS: using the gpsd at %s. hamstack runs no gpsd of its own.\n", g.Addr)
	} else {
		fmt.Printf("GPS: receiver %s, gpsd on port %d.\n", g.Device, config.GPS{Port: g.Port}.ListenPort())
		if _, err := versions.WhichSbin("gpsd"); err != nil {
			fmt.Println("gpsd is not installed yet: hamstack install gpsd")
		}
		fmt.Println("Then: hamstack units install && hamstack start gpsd   (hamstack enable gpsd to start at login)")
	}
	if msg != "" {
		fmt.Println(msg)
	}
	restartGPSD()
	fmt.Println("Check it: hamstack gps   Your grid square from it: hamstack gps grid")
	return nil
}

// usedByRig reports whether a serial port is one a rig has been set up with.
func usedByRig(cfg config.Config, s usbdev.Serial) bool {
	for _, r := range cfg.Rigs {
		if r.CATID != "" && r.CATID == s.USBID() {
			return true
		}
		if r.CAT != "" && (r.CAT == s.ByID || r.CAT == s.Tty) {
			return true
		}
	}
	return false
}

// rigUsingPath returns the rig whose CAT port is the device at path (by name, or by what the names resolve
// to), or "".
func rigUsingPath(cfg config.Config, path string) string {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		real = path
	}
	for _, r := range cfg.Rigs {
		if r.CAT == "" {
			continue
		}
		if r.CAT == path {
			return r.ID
		}
		if rr, err := filepath.EvalSymlinks(r.CAT); err == nil && rr == real {
			return r.ID
		}
	}
	return ""
}

// restartGPSD restarts hamstack's gpsd if it is running, so a changed receiver takes effect.
func restartGPSD() {
	if svc.Available() != nil {
		return
	}
	if u := (units.Service{Kind: config.GPSD}).Unit(); svc.Status(u).Active == "active" {
		_ = svc.Run("restart", u)
	}
}

func gpsOnOff(off bool) error {
	_, err := mutate(func(cfg *config.Config) (string, error) {
		if cfg.GPS == nil {
			return "", fmt.Errorf("no GPS is set up (hamstack gps use)")
		}
		cfg.GPS.Off = off
		return "", nil
	})
	if err == nil && off {
		fmt.Println("GPS: off. The setup is kept; 'hamstack gps on' runs it again.")
	} else if err == nil {
		fmt.Println("GPS: on. Start it with: hamstack start gpsd")
	}
	return err
}

func gpsRemove() error {
	_, err := mutate(func(cfg *config.Config) (string, error) {
		if cfg.GPS == nil {
			return "", fmt.Errorf("no GPS is set up")
		}
		cfg.RemoveGPS()
		return "", nil
	})
	if err == nil {
		fmt.Println("GPS setup removed.")
	}
	return err
}

func gpsStatus() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	snap, _ := scanDevices()
	g := cfg.GPS
	if g == nil {
		fmt.Println("No GPS set up.")
		if c := gpsCandidates(snap); len(c) > 0 {
			for _, s := range c {
				fmt.Printf("A GPS receiver is plugged in: %s (%s). Use it with: hamstack gps use\n", serialDevice(s), label(s))
			}
		} else {
			fmt.Println("hamstack gps use         use a receiver plugged into this computer")
			fmt.Println("hamstack gps use --addr  use a gpsd that already runs")
		}
		return nil
	}
	switch {
	case g.Addr != "":
		fmt.Printf("gpsd:      %s (runs elsewhere; hamstack does not manage it)\n", g.Addr)
	default:
		dev, err := resolveGPS(*g, snap)
		if err != nil {
			dev = "NOT FOUND: " + err.Error()
		}
		fmt.Printf("receiver:  %s\n", dev)
		state := "not installed (hamstack units install)"
		if st := svc.Status((units.Service{Kind: config.GPSD}).Unit()); st.Load != "not-found" {
			state = st.Active + " (" + st.Sub + ")"
		}
		if g.Off {
			state = "switched off (hamstack gps on)"
		}
		fmt.Printf("service:   %s on port %d\n", state, g.ListenPort())
		if p, err := versions.WhichSbin("gpsd"); err == nil {
			fmt.Printf("program:   %s\n", p)
		} else {
			fmt.Println("program:   gpsd is not installed (hamstack install gpsd)")
		}
	}
	if g.Off {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	st, err := gps.Query(ctx, cfg.GPSAddr(), false)
	if err != nil && !errors.Is(err, gps.ErrNoFix) {
		fmt.Printf("gpsd:      not answering: %v\n", err)
		return nil
	}
	fmt.Printf("gpsd:      version %s, devices %s\n", orDash(st.Version), orDash(strings.Join(st.Devices, ", ")))
	fmt.Printf("fix:       %s, %d of %d satellites in use\n", st.ModeName(), st.SatsUsed, st.SatsSeen)
	if st.Fix() {
		grid, _ := gps.Grid(st.Lat, st.Lon, 6)
		fmt.Printf("position:  %s  ->  %s\n", gps.Describe(st.Lat, st.Lon), grid)
		if cur := cfg.EffectiveGrid(); cur != "" && !strings.EqualFold(cur, grid) {
			fmt.Printf("           your saved grid is %s: hamstack gps grid --save to change it\n", cur)
		}
	}
	return nil
}

func gpsGrid(args []string) error {
	fs := flag.NewFlagSet("gps grid", flag.ContinueOnError)
	save := fs.Bool("save", false, "save the grid for the current site (or as the default)")
	timeout := fs.Duration("timeout", 90*time.Second, "how long to wait for a fix (a cold receiver can take minutes)")
	chars := fs.Int("chars", 6, "4, 6 or 8 characters")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	addr := cfg.GPSAddr()
	if addr == "" {
		return fmt.Errorf("no GPS is set up (hamstack gps use)")
	}
	fmt.Printf("Waiting up to %s for a fix from gpsd at %s...\n", *timeout, addr)
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	st, err := gps.Query(ctx, addr, true)
	if err != nil {
		if errors.Is(err, gps.ErrNoFix) {
			return fmt.Errorf("no fix within %s (%d satellites in use). A receiver needs to see the sky, "+
				"and a cold start can take several minutes: try again outdoors or near a window, or with --timeout 5m", *timeout, st.SatsUsed)
		}
		return err
	}
	grid, err := gps.Grid(st.Lat, st.Lon, *chars)
	if err != nil {
		return err
	}
	fmt.Printf("Position: %s (%s)\n", gps.Describe(st.Lat, st.Lon), st.ModeName())
	fmt.Printf("Grid:     %s\n", grid)
	cur := cfg.EffectiveGrid()
	if strings.EqualFold(cur, grid) {
		fmt.Println("That is the grid square you already have saved.")
		return nil
	}
	if cur != "" {
		fmt.Printf("Saved:    %s\n", cur)
	}
	where := "the default grid"
	if s, ok := cfg.Here(); ok {
		where = "site " + s.ID
	}
	if !*save {
		if !isTerminal() {
			fmt.Println("Save it with: hamstack gps grid --save")
			return nil
		}
		a := &asker{in: bufio.NewReader(os.Stdin), out: os.Stdout}
		if !a.confirm("Save "+grid+" as the grid for "+where+"?", false) {
			return nil
		}
	}
	msg, err := mutateOnly(func(c *config.Config) (string, error) {
		return "saved as the grid for " + c.SetGrid(grid), nil
	})
	if err != nil {
		return err
	}
	fmt.Println(msg + ". Update Pat to match with: hamstack pat locator")
	return nil
}

// gpsdSystemActive reports whether a system-wide gpsd (socket or service) is running, which can hold
// the receiver hamstack's gpsd wants.
func gpsdSystemActive() (unit string) {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return ""
	}
	for _, u := range []string{"gpsd.socket", "gpsd.service"} {
		if exec.Command("systemctl", "is-active", "--quiet", u).Run() == nil {
			return u
		}
	}
	return ""
}
