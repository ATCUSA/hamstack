package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/ATCUSA/hamstack/internal/audio"
	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/profile"
	"github.com/ATCUSA/hamstack/internal/units"
	"github.com/ATCUSA/hamstack/internal/usbdev"
	"github.com/ATCUSA/hamstack/internal/versions"
)

// exitRadioMissing is the exit status of a service whose radio is not there. The units list it in
// RestartPreventExitStatus so an unplugged radio does not cause a restart loop.
const exitRadioMissing = 3

// cmdRun is what the systemd units execute: `hamstack run rigctld RIG` and `hamstack run mercury RIG`.
// It reads the saved configuration and works out the radio's serial port and sound card right now,
// then replaces itself with the real program, so a replugged radio or a new USB port needs no new unit.
func cmdRun(args []string) error {
	if len(args) == 1 && args[0] == "gpsd" {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		return runGPSD(cfg)
	}
	if len(args) != 2 {
		return fmt.Errorf("usage: hamstack run rigctld|mercury|direwolf RIG, or hamstack run gpsd (this is run by the systemd units)")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	rig, prof, err := findRig(cfg, args[1])
	if err != nil {
		return err
	}
	switch args[0] {
	case "rigctld":
		return runRigctld(rig, prof)
	case "mercury":
		return runMercury(cfg, rig, prof)
	case "direwolf":
		return runDirewolf(cfg, rig, prof)
	}
	return fmt.Errorf("cannot run %q", args[0])
}

func findRig(cfg config.Config, id string) (config.Rig, profile.Profile, error) {
	for _, r := range cfg.Rigs {
		if r.ID != id {
			continue
		}
		prof, err := profile.Find(userProfileDir(), r.Profile)
		if err != nil {
			return r, prof, fmt.Errorf("rig %s: %w", id, err)
		}
		return r, prof, nil
	}
	return config.Rig{}, profile.Profile{}, fmt.Errorf("no rig named %q (see: hamstack rigs)", id)
}

var ifaceRE = regexp.MustCompile(`-if(\d+)`)

// resolveCAT finds the serial device a rig's rigctld should open: its saved assignment, found again by
// USB identity if that path is gone, or the first match of the profile's pattern. It returns "" for a
// radio with no device (the dummy rig).
func resolveCAT(r config.Rig, prof profile.Profile, snap usbdev.Snapshot) (string, error) {
	if r.CAT != "" {
		if _, err := os.Stat(r.CAT); err == nil {
			return r.CAT, nil
		}
		if r.CATID != "" {
			wantIf := -1
			if m := ifaceRE.FindStringSubmatch(r.CAT); m != nil {
				fmt.Sscanf(m[1], "%d", &wantIf)
			}
			var best *usbdev.Serial
			for i := range snap.Serials {
				s := &snap.Serials[i]
				if s.USBID() != r.CATID {
					continue
				}
				if wantIf >= 0 && s.Interface == wantIf {
					best = s
					break
				}
				if best == nil || s.Interface < best.Interface {
					best = s
				}
			}
			if best != nil {
				if best.ByID != "" {
					return best.ByID, nil
				}
				return best.Tty, nil
			}
		}
		return "", fmt.Errorf("radio not found: %s (USB %s)", r.CAT, orDash(r.CATID))
	}
	if g := prof.Rig.DeviceGlob; g != "" {
		if m, _ := filepath.Glob(g); len(m) > 0 {
			return m[0], nil
		}
		return "", fmt.Errorf("radio not found: %s", g)
	}
	if prof.HasCAT() && prof.Rig.Baud > 0 {
		// A serial radio hamstack has no way to recognise: it works once a port is assigned to it. Without
		// this, rigctld would start with no port, fail, and be restarted forever.
		return "", fmt.Errorf("no serial port is assigned to rig %s: plug the radio in and run: hamstack rig identify %s", r.ID, r.ID)
	}
	return "", nil
}

// resolveAudio returns the ALSA device for a rig's sound card, or "" when the profile uses none. The
// card is found by USB identity first (card numbers and ids can change between boots and ports), then
// by its saved ALSA id, then by the profile's hints.
func resolveAudio(r config.Rig, prof profile.Profile, snap usbdev.Snapshot) string {
	if prof.Audio.Null {
		return ""
	}
	alsa := func(id string) string { return "plughw:CARD=" + id + ",DEV=0" }
	if r.AudioUSB != "" {
		var same []usbdev.Sound
		for _, s := range snap.Sounds {
			if s.USBID() == r.AudioUSB {
				return alsa(s.ID)
			}
			if strings.Count(r.AudioUSB, ":") == 1 && s.VID+":"+s.PID == r.AudioUSB {
				same = append(same, s)
			}
		}
		if len(same) == 1 { // saved without a serial number: fine if it is the only one of its kind
			return alsa(same[0].ID)
		}
	}
	if r.Audio != "" {
		for _, c := range audio.Cards() {
			if strings.EqualFold(c, r.Audio) {
				return alsa(c)
			}
		}
	}
	dev, _ := audio.Resolve(prof.Audio.CardHints)
	return dev
}

func runRigctld(rig config.Rig, prof profile.Profile) error {
	snap, _ := scanDevices()
	dev, err := resolveCAT(rig, prof, snap)
	if err != nil {
		fmt.Fprintln(os.Stderr, "hamstack:", err)
		os.Exit(exitRadioMissing)
	}
	path, err := versions.Which("rigctld")
	if err != nil {
		return fmt.Errorf("rigctld is not installed (run: hamstack install hamlib)")
	}
	argv := []string{"rigctld", "-m", fmt.Sprint(prof.Rig.Model)}
	if dev != "" {
		argv = append(argv, "-r", dev)
		if prof.Rig.Baud > 0 {
			argv = append(argv, "-s", fmt.Sprint(prof.Rig.Baud))
		}
	}
	argv = append(argv, "-T", "127.0.0.1", "-t", fmt.Sprint(rig.RigPort))
	fmt.Fprintf(os.Stderr, "hamstack: rig %s: %s\n", rig.ID, strings.Join(argv, " "))
	return syscall.Exec(path, argv, os.Environ())
}

func runMercury(cfg config.Config, rig config.Rig, prof profile.Profile) error {
	snap, _ := scanDevices()
	st, _ := cfg.ActiveStation()
	dir, err := config.Dir()
	if err != nil {
		return err
	}
	rigDir := filepath.Join(dir, "rigs", rig.ID)
	if err := os.MkdirAll(rigDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(stateDir(), 0o755); err != nil {
		return err
	}
	audioDev := resolveAudio(rig, prof, snap)
	ini := filepath.Join(rigDir, "mercury.ini")
	if err := os.WriteFile(ini, []byte(units.MercuryConfig(cfg, rig, prof, st, audioDev, stateDir())), 0o644); err != nil {
		return err
	}
	// Type=simple counts a unit as started when it is spawned, and rigctld needs a moment to open its
	// port; Mercury's rig connection fails if it starts first.
	if err := waitPort(rig.RigPort, 15*time.Second); err != nil {
		return fmt.Errorf("rig %s: rig control is not running: %w", rig.ID, err)
	}
	path, err := versions.Which("mercury")
	if err != nil {
		return fmt.Errorf("mercury is not installed (run: hamstack install mercury)")
	}
	fmt.Fprintf(os.Stderr, "hamstack: rig %s: mercury -C %s (audio %s)\n", rig.ID, ini, orDash(audioDev))
	return syscall.Exec(path, []string{"mercury", "-C", ini}, os.Environ())
}

// direwolfPTTLine works out how a rig is keyed for packet and returns the Direwolf PTT line (or "").
// waitRig is true when the line depends on the rig's rigctld being up.
func direwolfPTTLine(rig config.Rig, prof profile.Profile, snap usbdev.Snapshot) (line string, waitRig bool, err error) {
	mode := prof.PacketPTT()
	switch mode {
	case "rig":
		if rig.NoCAT {
			return "", false, fmt.Errorf("rig %s has no CAT control, so it cannot be keyed through rigctld", rig.ID)
		}
		line, err = units.DirewolfPTT(mode, "", rig.RigPort)
		return line, true, err
	case "rts", "dtr":
		dev, err := resolveCAT(rig, prof, snap) // for these radios the serial port is the PTT line
		if err != nil {
			return "", false, err
		}
		line, err = units.DirewolfPTT(mode, dev, 0)
		return line, false, err
	}
	line, err = units.DirewolfPTT(mode, "", 0)
	return line, false, err
}

func runDirewolf(cfg config.Config, rig config.Rig, prof profile.Profile) error {
	snap, _ := scanDevices()
	st, _ := cfg.ActiveStation()
	dir, err := config.Dir()
	if err != nil {
		return err
	}
	rigDir := filepath.Join(dir, "rigs", rig.ID)
	if err := os.MkdirAll(rigDir, 0o755); err != nil {
		return err
	}
	ptt, waitRig, err := direwolfPTTLine(rig, prof, snap)
	if err != nil {
		fmt.Fprintln(os.Stderr, "hamstack:", err)
		os.Exit(exitRadioMissing) // not there, or cannot be keyed: do not restart in a loop
	}
	audioDev := resolveAudio(rig, prof, snap)
	conf := filepath.Join(rigDir, "direwolf.conf")
	extra, _ := os.ReadFile(filepath.Join(rigDir, "direwolf.extra")) // the user's own lines, if they made any
	if err := os.WriteFile(conf, []byte(units.DirewolfConfigWith(rig, prof, st, audioDev, ptt, string(extra))), 0o644); err != nil {
		return err
	}
	if waitRig {
		if err := waitPort(rig.RigPort, 15*time.Second); err != nil {
			return fmt.Errorf("rig %s: rig control is not running: %w", rig.ID, err)
		}
	}
	path, err := versions.Which("direwolf")
	if err != nil {
		return fmt.Errorf("direwolf is not installed (run: hamstack install direwolf)")
	}
	fmt.Fprintf(os.Stderr, "hamstack: rig %s: direwolf -c %s (audio %s, %s)\n", rig.ID, conf, orDash(audioDev), orDash(strings.TrimPrefix(ptt, "PTT ")))
	argv := []string{"direwolf", "-t", "0"}
	if cfg.AX25 != nil && cfg.AX25.Rig == rig.ID {
		argv = append(argv, "-p") // offer a KISS pseudo-terminal (/tmp/kisstnc) for kissattach: see hamstack ax25
	}
	return syscall.Exec(path, append(argv, "-c", conf), os.Environ())
}

// resolveGPS finds the receiver gpsd should open: its saved device if that still exists, else the same
// USB device found again by identity (the by-id path normally survives a replug, but not a change of
// the kind of path that was saved).
func resolveGPS(g config.GPS, snap usbdev.Snapshot) (string, error) {
	if g.Device != "" {
		if _, err := os.Stat(g.Device); err == nil {
			return g.Device, nil
		}
	}
	if g.USB != "" {
		for _, s := range snap.Serials {
			if s.USBID() == g.USB {
				if s.ByID != "" {
					return s.ByID, nil
				}
				return s.Tty, nil
			}
		}
	}
	return "", fmt.Errorf("GPS receiver not found: %s (USB %s)", orDash(g.Device), orDash(g.USB))
}

// gpsdArgs is gpsd's command line. -N stays in the foreground for systemd, -n starts reading the receiver
// without waiting for a client, and -S and -F keep it off gpsd's standard port and socket, which a
// system-wide gpsd may be using. -b makes it read-only: gpsd normally probes a receiver by sending it
// commands, and a device that turns out not to be a GPS (a radio's CAT port chosen by mistake) must not
// be sent anything.
func gpsdArgs(port int, control, device string) []string {
	argv := []string{"gpsd", "-N", "-n", "-b", "-S", fmt.Sprint(port)}
	if control != "" {
		argv = append(argv, "-F", control)
	}
	return append(argv, device)
}

func runGPSD(cfg config.Config) error {
	if !cfg.RunsGPSD() {
		fmt.Fprintln(os.Stderr, "hamstack: no GPS receiver is set up (see: hamstack gps use)")
		os.Exit(exitRadioMissing)
	}
	snap, _ := scanDevices()
	dev, err := resolveGPS(*cfg.GPS, snap)
	if err != nil {
		fmt.Fprintln(os.Stderr, "hamstack:", err)
		os.Exit(exitRadioMissing)
	}
	path, err := versions.WhichSbin("gpsd")
	if err != nil {
		return fmt.Errorf("gpsd is not installed (run: hamstack install gpsd)")
	}
	control := ""
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		control = filepath.Join(d, "hamstack-gpsd.sock")
	}
	argv := gpsdArgs(cfg.GPS.ListenPort(), control, dev)
	fmt.Fprintf(os.Stderr, "hamstack: %s\n", strings.Join(argv, " "))
	return syscall.Exec(path, argv, os.Environ())
}
