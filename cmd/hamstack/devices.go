package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/ATCUSA/hamstack/internal/audio"
	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/distro"
	"github.com/ATCUSA/hamstack/internal/groups"
	"github.com/ATCUSA/hamstack/internal/install"
	"github.com/ATCUSA/hamstack/internal/profile"
	"github.com/ATCUSA/hamstack/internal/svc"
	"github.com/ATCUSA/hamstack/internal/usbdev"
)

func scanDevices() (usbdev.Snapshot, error) { return usbdev.Scan("/sys", "/dev") }

func cmdDevices(args []string) error {
	if len(args) > 0 && args[0] == "watch" {
		return devicesWatch()
	}
	if len(args) > 0 {
		return fmt.Errorf("usage: hamstack devices [watch]")
	}
	snap, err := scanDevices()
	if err != nil {
		return err
	}
	profs, _ := profile.All(userProfileDir())
	printDevices(snap, profs)
	return nil
}

func printDevices(snap usbdev.Snapshot, profs []profile.Profile) {
	groupsFound := snap.Groups()
	if len(groupsFound) == 0 {
		fmt.Println("No USB serial ports or USB sound cards found. Plug your radio or interface in, then run this again")
		fmt.Println("(or 'hamstack devices watch' to see devices appear as you plug them in).")
	}
	for i, g := range groupsFound {
		where := "plugged straight into the computer"
		if g.Hub != "" {
			where = "sharing the hub " + filepath.Base(g.Hub) + " (devices on one hub probably belong together)"
		}
		fmt.Printf("Group %d: %s\n", i+1, where)
		for _, s := range g.Serials {
			id := "-"
			if s.ByID != "" {
				id = filepath.Base(s.ByID)
			}
			fmt.Printf("  serial  %-14s %s [%s:%s]", s.Tty, label(s), s.VID, s.PID)
			if s.SerialNumber != "" {
				fmt.Printf(" serial %s", s.SerialNumber)
			}
			fmt.Printf(" interface %d\n          stable name: %s\n", s.Interface, id)
			if guess := profile.Guess(profs, s.VID, s.PID); len(guess) > 0 {
				var names []string
				for _, p := range guess {
					names = append(names, p.Name)
				}
				fmt.Printf("          looks like: %s\n", strings.Join(names, " or "))
			}
			if s.LooksLikeGPS() {
				fmt.Println("          looks like a GPS receiver: hamstack gps use")
			}
			if s.SerialNumber == "" {
				fmt.Println("          note: no USB serial number, so an identical device could only be told apart by the port it is plugged into")
			}
		}
		for _, c := range g.Sounds {
			fmt.Printf("  sound   card %d [%s] %s [%s:%s]  (use as plughw:CARD=%s,DEV=0)\n", c.Card, c.ID, c.Product, c.VID, c.PID, c.ID)
		}
	}
	var builtin []string
	usb := map[string]bool{}
	for _, c := range snap.Sounds {
		usb[c.ID] = true
	}
	for _, id := range audio.Cards() {
		if !usb[id] {
			builtin = append(builtin, id)
		}
	}
	if len(builtin) > 0 {
		fmt.Printf("\nBuilt-in sound cards (not USB): %s\n", strings.Join(builtin, ", "))
	}
}

func label(s usbdev.Serial) string {
	switch {
	case s.Manufacturer != "" && s.Product != "":
		return s.Manufacturer + " " + s.Product
	case s.Product != "":
		return s.Product
	}
	return s.Manufacturer
}

// devicesWatch prints devices as they are plugged in and unplugged. It polls sysfs once a second,
// which needs no privileges (dmesg usually does) and works on every system.
func devicesWatch() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	prev, err := scanDevices()
	if err != nil {
		return err
	}
	fmt.Printf("Watching for USB serial ports and sound cards (%d serial, %d sound present). Ctrl+C to stop.\n", len(prev.Serials), len(prev.Sounds))
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
		cur, err := scanDevices()
		if err != nil {
			return err
		}
		for _, e := range usbdev.Diff(prev, cur) {
			sign := "-"
			if e.Added {
				sign = "+"
			}
			fmt.Printf("%s %s  %s   %s\n", time.Now().Format("15:04:05"), sign, e.Kind, e.Detail)
		}
		prev = cur
	}
}

// ---- groups ----

func groupRows() []struct {
	info    groups.Info
	purpose string
} {
	sample := ""
	if cfg, err := config.Load(); err == nil {
		for _, r := range cfg.ActiveRigs() {
			if r.CAT != "" {
				if _, err := os.Stat(r.CAT); err == nil {
					sample = r.CAT
					break
				}
			}
			if p, err := profile.Find(userProfileDir(), r.Profile); err == nil && p.Rig.DeviceGlob != "" {
				if m, _ := filepath.Glob(p.Rig.DeviceGlob); len(m) > 0 {
					sample = m[0]
					break
				}
			}
		}
	}
	serial := groups.SerialGroupName(distro.Detect().Family, sample)
	return []struct {
		info    groups.Info
		purpose string
	}{
		{groups.Lookup(serial), "USB serial ports (radio CAT, PTT)"},
		{groups.Lookup(groups.AudioGroupName), "sound cards when no one is logged in"},
	}
}

func cmdGroups(args []string) error {
	fix := len(args) > 0 && args[0] == "fix"
	if len(args) > 0 && !fix {
		return fmt.Errorf("usage: hamstack groups [fix [--yes] [--audio]]")
	}
	rows := groupRows()
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "GROUP\tPURPOSE\tYOU\tTHIS LOGIN\tSERVICES")
	yn := func(b bool) string {
		if b {
			return "yes"
		}
		return "no"
	}
	for _, r := range rows {
		if !r.info.Exists {
			fmt.Fprintf(tw, "%s\t%s\t(group does not exist on this system)\t\t\n", r.info.Name, r.purpose)
			continue
		}
		mgr := "unknown"
		if r.info.ManagerKnown {
			mgr = yn(r.info.Manager)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", r.info.Name, r.purpose, yn(r.info.Member), yn(r.info.Session), mgr)
	}
	tw.Flush()
	var advice []string
	linger, _ := svc.LingerEnabled()
	for _, r := range rows {
		if r.info.Name == groups.AudioGroupName && !linger {
			continue // a desktop login grants sound access; the group only matters with linger
		}
		if p, f := r.info.Advice(); p != "" {
			advice = append(advice, fmt.Sprintf("- %s: %s. Fix: %s", r.info.Name, p, f))
		}
	}
	if !fix {
		if len(advice) > 0 {
			fmt.Println()
			for _, a := range advice {
				fmt.Println(a)
			}
		}
		return nil
	}
	flags, _ := splitArgs(args[1:])
	fs := flag.NewFlagSet("groups fix", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "do not ask for confirmation")
	withAudio := fs.Bool("audio", false, "also add the audio group even if linger is off")
	if err := fs.Parse(flags); err != nil {
		return err
	}
	steps, err := groupSteps(rows[0].info, rows[1].info, *withAudio)
	if err != nil {
		return err
	}
	if len(steps) == 0 {
		fmt.Println("\nNothing to add: you are already in the groups you need.")
		return nil
	}
	fmt.Println()
	if err := install.Run(steps, install.Options{Yes: *yes}); err != nil {
		return err
	}
	fmt.Println("\nThe new membership reaches your services only after your session and the background service manager restart:")
	fmt.Println("log out and back in, and if linger is on also: sudo systemctl restart user@" + fmt.Sprint(os.Getuid()) + ".service (or just reboot).")
	fmt.Println("'hamstack doctor' tells you when it has taken effect.")
	return nil
}

// groupSteps plans the usermod commands for whichever groups the user is missing. The audio group
// is added when linger is on (services run with no login session), or when asked for.
func groupSteps(serial, audioG groups.Info, forceAudio bool) ([]install.Step, error) {
	u, err := user.Current()
	if err != nil {
		return nil, err
	}
	env := install.HostEnv()
	var steps []install.Step
	add := func(g groups.Info, why string) error {
		if !g.Exists || g.Member {
			return nil
		}
		s, err := install.GroupStep(env, g.Name, u.Username, why)
		if err != nil {
			return err
		}
		steps = append(steps, s)
		return nil
	}
	if err := add(serial, "lets you open the radio's USB serial port"); err != nil {
		return nil, err
	}
	linger, _ := svc.LingerEnabled()
	if linger || forceAudio {
		if err := add(audioG, "lets services use the sound card with nobody logged in"); err != nil {
			return nil, err
		}
	}
	return steps, nil
}

// ensureGroups is the setup wizard's Permissions step.
func ensureGroups(a *asker, cfg config.Config, o setupOpts) error {
	rows := groupRows()
	steps, err := groupSteps(rows[0].info, rows[1].info, false)
	if err != nil {
		return err
	}
	if len(steps) == 0 {
		if p, f := rows[0].info.Advice(); p != "" {
			fmt.Fprintf(a.out, "%s. Fix: %s\n", p, f)
		} else {
			fmt.Fprintln(a.out, "you are already in the group that lets you open USB serial ports")
		}
		return nil
	}
	// Explain only what is actually being asked for.
	if rows[0].info.Exists && !rows[0].info.Member {
		fmt.Fprintf(a.out, "To open a radio's USB serial port you need to be in the %q group. Without it, the radio's services\n", rows[0].info.Name)
		fmt.Fprintln(a.out, "fail with \"permission denied\" the moment it is plugged in.")
	}
	if rows[1].info.Exists && !rows[1].info.Member {
		if linger, _ := svc.LingerEnabled(); linger {
			fmt.Fprintln(a.out, "Linger is on, so your services run with nobody logged in, and then sound cards need the \"audio\" group.")
		}
	}
	if err := install.Run(steps, install.Options{DryRun: o.dry, Yes: o.yes, In: a.in, Out: a.out}); err != nil {
		fmt.Fprintf(a.out, "Skipped (%v). Later: hamstack groups fix\n", err)
		return nil
	}
	if !o.dry {
		fmt.Fprintln(a.out, "Done. It takes effect after you log out and back in (and, if linger is on, after the user service manager restarts or a reboot).")
	}
	return nil
}
