package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ATCUSA/hamstack/internal/ax25"
	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/install"
	"github.com/ATCUSA/hamstack/internal/units"
	"github.com/ATCUSA/hamstack/internal/versions"
)

const ax25Usage = `usage:
  hamstack ax25 [status]                 the kernel AX.25 stack: module, tools, axports, interfaces, what is missing
  hamstack ax25 setup [--rig ID] [--call CALL] [--speed N] [--paclen N] [--window N] [--dry-run] [--yes]
                                         install the tools, write the axports entry, load the modules (asks for sudo)
  hamstack ax25 port [set KEY VALUE]     the axports entry (keys: call, speed, paclen, window) and what each means
  hamstack ax25 attach                   kissattach: connect Direwolf to the kernel (creates ax0; needs sudo)
  hamstack ax25 detach                   undo attach
  hamstack ax25 call [--port P] CALL [VIA...]   axcall: connected-mode call through the kernel stack
  hamstack ax25 listen                   axlisten: watch every frame on the kernel's AX.25 ports (needs sudo)

This is optional. 'hamstack packet listen|call' does the same job through Direwolf with no root and no kernel
AX.25; use this when you want the classic tools (axcall, axlisten, ax25d) or an AX.25 socket for other software.`

func cmdAX25(args []string) error {
	if len(args) == 0 {
		return ax25Status()
	}
	switch args[0] {
	case "status":
		return ax25Status()
	case "setup":
		return ax25Setup(args[1:])
	case "port":
		return ax25PortCmd(args[1:])
	case "attach":
		return ax25Attach()
	case "detach":
		return ax25Detach()
	case "call":
		return ax25Call(args[1:])
	case "listen":
		return ax25Listen()
	case "help", "-h", "--help":
		fmt.Println(ax25Usage)
		return nil
	}
	return fmt.Errorf("unknown ax25 command %q\n%s", args[0], ax25Usage)
}

func haveAX25Tool(name string) bool { _, err := versions.WhichSbin(name); return err == nil }

// kissattachPattern matches a kissattach on a pseudo-terminal. The [k] keeps pkill and pgrep from matching
// their own command line (and sudo's).
const kissattachPattern = "[k]issattach /dev/pts/"

// kissattachRunning reports whether a kissattach attached to a pseudo-terminal (Direwolf's) is running.
func kissattachRunning() bool {
	return exec.Command("pgrep", "-f", kissattachPattern).Run() == nil
}

// ax25Advice explains what the running kernel means for AX.25, or "" when it is fine.
func ax25KernelNote(k ax25.KernelState, release string) string {
	if k != ax25.KernelMissing {
		return ""
	}
	return fmt.Sprintf("Kernel %s has no AX.25 support (recent kernels removed it, so the kernel tools cannot work here).\n"+
		"Your options: use 'hamstack packet listen|call', which needs no kernel support; or boot a kernel that has it\n"+
		"(on Arch, for example, the linux-lts kernel; Debian and Fedora kernels normally include it).", release)
}

func ax25Status() error {
	cfg, _ := config.Load()
	k, release := ax25.Kernel("/")
	fmt.Printf("kernel:      %s (%s)\n", k, orDash(release))
	if note := ax25KernelNote(k, release); note != "" {
		fmt.Println("\n" + note)
		return nil
	}
	for _, t := range []string{"kissattach", "axcall", "axlisten"} {
		if p, err := versions.WhichSbin(t); err == nil {
			fmt.Printf("%-12s %s\n", t+":", p)
		} else {
			fmt.Printf("%-12s not installed\n", t+":")
		}
	}
	b, err := os.ReadFile(ax25.PortsFile)
	ports := ax25.ParsePorts(string(b))
	switch {
	case err != nil:
		fmt.Printf("axports:     %s does not exist\n", ax25.PortsFile)
	case len(ports) == 0:
		fmt.Printf("axports:     %s has no ports\n", ax25.PortsFile)
	default:
		fmt.Printf("axports:     %s\n", ax25.PortsFile)
		for _, p := range ports {
			fmt.Printf("             %s  %s  %d baud\n", p.Name, p.Call, p.Speed)
		}
	}
	if ifs := ax25.Interfaces("/"); len(ifs) > 0 {
		var n []string
		for _, i := range ifs {
			n = append(n, i.Name)
		}
		fmt.Printf("interfaces:  %s\n", strings.Join(n, ", "))
	} else {
		fmt.Println("interfaces:  none (hamstack ax25 attach creates one)")
	}
	if a := cfg.AX25; a != nil {
		fmt.Printf("hamstack:    rig %s's Direwolf is offered to the kernel as port %s\n", a.Rig, a.Port)
		if _, err := os.Stat(ax25.PtyPath); err != nil {
			fmt.Printf("             but %s is missing: start Direwolf (hamstack start direwolf@%s)\n", ax25.PtyPath, a.Rig)
		}
		if kissattachRunning() {
			fmt.Println("             attached")
		} else {
			fmt.Println("             not attached: hamstack ax25 attach")
		}
	} else {
		fmt.Println("hamstack:    not set up: hamstack ax25 setup")
	}
	return nil
}

func ax25Setup(args []string) error {
	fs := flag.NewFlagSet("ax25 setup", flag.ContinueOnError)
	rigID := fs.String("rig", "", "the rig whose Direwolf to attach (default: the first)")
	call := fs.String("call", "", "the callsign of the AX.25 port (default: the active station's)")
	speed := fs.Int("speed", 0, "the port's speed in baud (default: the rig's Direwolf speed)")
	paclen := fs.Int("paclen", 0, "largest packet in bytes (default 255)")
	window := fs.Int("window", 0, "frames in flight (default 2)")
	dry := fs.Bool("dry-run", false, "show the plan, change nothing")
	yes := fs.Bool("yes", false, "do not ask for confirmation")
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	k, release := ax25.Kernel("/")
	if note := ax25KernelNote(k, release); note != "" {
		return fmt.Errorf("%s", note)
	}
	var rig config.Rig
	for _, r := range cfg.ActiveRigs() {
		if r.Modem == config.Direwolf && (*rigID == "" || r.ID == *rigID) {
			rig = r
			break
		}
	}
	if rig.ID == "" && *rigID != "" {
		return fmt.Errorf("rig %q is not an active rig with a Direwolf modem (see: hamstack rigs)", *rigID)
	}
	if rig.ID == "" {
		return fmt.Errorf("no rig uses Direwolf: add one first (hamstack rig add pkt --profile digirig --modem direwolf)")
	}
	// What is saved, then what was asked for now, then the defaults; and, at a terminal, a chance to change each.
	saved := config.AX25{}
	if cfg.AX25 != nil {
		saved = *cfg.AX25
	}
	if *call != "" {
		saved.Call = strings.ToUpper(*call)
	}
	for _, v := range []struct {
		flag int
		dst  *int
	}{{*speed, &saved.Speed}, {*paclen, &saved.Paclen}, {*window, &saved.Window}} {
		if v.flag != 0 {
			*v.dst = v.flag
		}
	}
	if !*yes && !*dry && isTerminal() && stdinIsTerminal() {
		a := &asker{in: bufio.NewReader(os.Stdin), out: os.Stdout}
		askAX25Port(a, cfg, rig, &saved)
	}
	port, err := ax25PortFrom(cfg, rig, saved)
	if err != nil {
		return err
	}
	env := install.HostEnv()
	var steps []install.Step
	if !haveAX25Tool("kissattach") || !haveAX25Tool("axcall") || !haveAX25Tool("axlisten") {
		s, err := install.AX25ToolsPlan(env)
		if err != nil {
			return err
		}
		steps = append(steps, s...)
	}
	old, _ := os.ReadFile(ax25.PortsFile)
	if text, changed := ax25.Upsert(string(old), port); changed {
		s, err := install.RootFileStep(env, "Add the port to "+ax25.PortsFile+" (a copy of the old file is kept)", ax25.PortsFile, text)
		if err != nil {
			return err
		}
		steps = append(steps, s)
	}
	// kissattach on a pseudo-terminal needs the mkiss line discipline as well as ax25 itself.
	if k != ax25.KernelLoaded || !moduleLoaded("mkiss") {
		cmd, err := env.Priv("modprobe", "-a", "ax25", "mkiss")
		if err != nil {
			return err
		}
		steps = append(steps, install.Step{Desc: "Load the AX.25 kernel modules", Cmd: cmd})
	}
	s, err := install.RootFileStep(env, "Load the AX.25 modules at every boot", "/etc/modules-load.d/hamstack-ax25.conf", "ax25\nmkiss\n")
	if err != nil {
		return err
	}
	if _, statErr := os.Stat("/etc/modules-load.d/hamstack-ax25.conf"); statErr != nil {
		steps = append(steps, s)
	}
	steps = append(steps, install.Step{Desc: "Offer rig " + rig.ID + "'s Direwolf to the kernel (restarts it)", Do: func() error {
		want := saved
		want.Rig, want.Port = rig.ID, port.Name
		if cfg.AX25 != nil && *cfg.AX25 == want {
			return nil // already so: do not restart Direwolf (that would drop an attached kissattach)
		}
		if _, err := mutate(func(c *config.Config) (string, error) {
			c.AX25 = &want
			return "", nil
		}); err != nil {
			return err
		}
		restartDirewolf(rig.ID)
		return nil
	}})
	if err := install.Run(steps, install.Options{DryRun: *dry, Yes: *yes}); err != nil {
		return err
	}
	if !*dry {
		fmt.Printf("\nNext: hamstack ax25 attach   (then: hamstack ax25 call CALL, hamstack ax25 listen)\n")
		fmt.Println("kissattach must be run again whenever Direwolf restarts: the connection is to its pseudo-terminal.")
	}
	return nil
}

func moduleLoaded(name string) bool {
	_, err := os.Stat("/sys/module/" + name)
	return err == nil
}

func restartDirewolf(rig string) {
	u := (units.Service{Kind: config.Direwolf, Rig: rig}).Unit()
	if svcActive(u) {
		_ = exec.Command("systemctl", "--user", "restart", u).Run()
	}
}

func svcActive(unit string) bool {
	return exec.Command("systemctl", "--user", "is-active", "--quiet", unit).Run() == nil
}

func ax25Ready(needTool string) (config.Config, error) {
	cfg, _ := config.Load()
	if k, rel := ax25.Kernel("/"); k != ax25.KernelLoaded {
		if note := ax25KernelNote(k, rel); note != "" {
			return cfg, fmt.Errorf("%s", note)
		}
		return cfg, fmt.Errorf("the AX.25 kernel module is not loaded: hamstack ax25 setup")
	}
	if needTool != "" && !haveAX25Tool(needTool) {
		return cfg, fmt.Errorf("%s is not installed: hamstack ax25 setup", needTool)
	}
	if cfg.AX25 == nil {
		return cfg, fmt.Errorf("not set up: hamstack ax25 setup")
	}
	return cfg, nil
}

// releasePty makes Direwolf let go of its pseudo-terminal by restarting it. Killing kissattach is not
// enough: Direwolf keeps the terminal open, the kernel keeps the AX.25 line discipline (and ax0) on it,
// and a new kissattach then fails with "tcgetattr: Inappropriate ioctl". Only the terminal closing,
// which is Direwolf restarting, clears it.
func releasePty(rig string) error {
	u := (units.Service{Kind: config.Direwolf, Rig: rig}).Unit()
	if !svcActive(u) {
		return fmt.Errorf("an AX.25 interface is left over and Direwolf is not running: start it (hamstack start direwolf@%s), then try again", rig)
	}
	_ = exec.Command("systemctl", "--user", "restart", u).Run()
	for i := 0; i < 30; i++ {
		time.Sleep(500 * time.Millisecond)
		if _, err := os.Stat(ax25.PtyPath); err == nil && len(ax25.Interfaces("/")) == 0 {
			return nil
		}
	}
	return fmt.Errorf("Direwolf did not come back with a fresh terminal: hamstack logs direwolf@%s", rig)
}

func ax25Attach() error {
	cfg, err := ax25Ready("kissattach")
	if err != nil {
		return err
	}
	if kissattachRunning() {
		fmt.Println("Already attached.")
		return ax25Status()
	}
	if len(ax25.Interfaces("/")) > 0 {
		fmt.Println("A previous attach left an AX.25 interface behind: restarting Direwolf to release it.")
		if err := releasePty(cfg.AX25.Rig); err != nil {
			return err
		}
	}
	if _, err := os.Stat(ax25.PtyPath); err != nil {
		return fmt.Errorf("Direwolf is not offering %s: start it (hamstack start direwolf@%s); it must run with the setup from 'hamstack ax25 setup'", ax25.PtyPath, cfg.AX25.Rig)
	}
	// Give kissattach the real device. As root it cannot open Direwolf's /tmp/kisstnc symlink: /tmp is sticky
	// and the link belongs to another user, which the kernel refuses to follow (fs.protected_symlinks).
	pty, err := filepath.EvalSymlinks(ax25.PtyPath)
	if err != nil {
		return fmt.Errorf("cannot follow %s: %v", ax25.PtyPath, err)
	}
	path, _ := versions.WhichSbin("kissattach")
	argv := []string{path, pty, cfg.AX25.Port}
	if os.Geteuid() != 0 {
		argv = append([]string{"sudo"}, argv...)
	}
	fmt.Println("$", strings.Join(argv, " "))
	var out bytes.Buffer
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, &out, &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("kissattach failed: %v\n%s", err, strings.TrimSpace(out.String()))
	}
	fmt.Print(out.String())
	return nil
}

func ax25Detach() error {
	cfg, _ := config.Load()
	if !kissattachRunning() {
		if len(ax25.Interfaces("/")) > 0 && cfg.AX25 != nil {
			fmt.Println("kissattach is gone but its interface is not: restarting Direwolf to release it.")
			return releasePty(cfg.AX25.Rig)
		}
		fmt.Println("Not attached.")
		return nil
	}
	argv := []string{"pkill", "-f", kissattachPattern}
	if os.Geteuid() != 0 {
		argv = append([]string{"sudo"}, argv...)
	}
	fmt.Println("$", strings.Join(argv, " "))
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	time.Sleep(500 * time.Millisecond)
	if len(ax25.Interfaces("/")) > 0 && cfg.AX25 != nil {
		fmt.Println("Direwolf still holds the terminal, so the interface stays until it restarts: restarting it.")
		return releasePty(cfg.AX25.Rig)
	}
	return nil
}

func ax25Call(args []string) error {
	fs := flag.NewFlagSet("ax25 call", flag.ContinueOnError)
	port := fs.String("port", "", "axports port (default: hamstack's)")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) < 1 {
		return fmt.Errorf("%s", ax25Usage)
	}
	cfg, err := ax25Ready("axcall")
	if err != nil {
		return err
	}
	if *port == "" {
		*port = cfg.AX25.Port
	}
	if !ax25.ValidPortName(*port) {
		return fmt.Errorf("%q is not a valid port name", *port)
	}
	for _, c := range pos {
		if u := strings.ToUpper(c); !config.ValidCallsign(u) && !digiAlias.MatchString(u) {
			return fmt.Errorf("%q does not look like a callsign", c)
		}
	}
	if len(ax25.Interfaces("/")) == 0 {
		return fmt.Errorf("no AX.25 interface yet: hamstack ax25 attach")
	}
	path, _ := versions.WhichSbin("axcall")
	argv := append([]string{"axcall", *port}, upper(pos)...)
	return syscall.Exec(path, argv, os.Environ())
}

func upper(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = strings.ToUpper(s)
	}
	return out
}

func ax25Listen() error {
	if _, err := ax25Ready("axlisten"); err != nil {
		return err
	}
	// axlisten only knows the ports whose interface exists, and says "no AX.25 port data configured" otherwise.
	if len(ax25.Interfaces("/")) == 0 {
		return fmt.Errorf("no AX.25 interface yet: hamstack ax25 attach")
	}
	path, _ := versions.WhichSbin("axlisten")
	argv := []string{path, "-a", "-c", "-t"}
	if os.Geteuid() != 0 {
		sudo, err := exec.LookPath("sudo")
		if err != nil {
			return fmt.Errorf("axlisten needs root and sudo is not installed")
		}
		path, argv = sudo, append([]string{"sudo"}, argv...)
	}
	fmt.Println("$", strings.Join(argv, " "))
	return syscall.Exec(path, argv, os.Environ())
}

// ax25PortFrom builds the axports entry from the saved settings and the defaults: the station's callsign, the
// speed the rig's Direwolf runs at, 255-byte packets and a window of 2 (what suits Direwolf at 1200 baud).
func ax25PortFrom(cfg config.Config, rig config.Rig, a config.AX25) (ax25.Port, error) {
	call := a.Call
	if call == "" {
		st, ok := cfg.ActiveStation()
		if !ok {
			return ax25.Port{}, fmt.Errorf("no active station: give a callsign (--call CALL), or add a station")
		}
		call = st.Callsign
	}
	if !config.ValidCallsign(call) {
		return ax25.Port{}, fmt.Errorf("%q does not look like a callsign", call)
	}
	p := ax25.DefaultPort(call)
	if a.Port != "" {
		p.Name = a.Port
	}
	if !ax25.ValidPortName(p.Name) { // it is written into /etc/ax25/axports as root
		return ax25.Port{}, fmt.Errorf("%q is not a valid AX.25 port name (up to 8 letters, digits, - or _)", p.Name)
	}
	p.Speed = rig.Packet.ModemBaud()
	if a.Speed != 0 {
		p.Speed = a.Speed
	}
	if a.Paclen != 0 {
		p.Paclen = a.Paclen
	}
	if a.Window != 0 {
		p.Window = a.Window
	}
	switch {
	case p.Speed < 300 || p.Speed > 115200:
		return p, fmt.Errorf("speed %d is not sensible (300-115200 baud)", p.Speed)
	case p.Paclen < 1 || p.Paclen > 512:
		return p, fmt.Errorf("paclen %d is not sensible (1-512 bytes)", p.Paclen)
	case p.Window < 1 || p.Window > 7:
		return p, fmt.Errorf("window %d is not sensible (1-7 frames)", p.Window)
	}
	return p, nil
}

// askAX25Port offers each setting with its default and what it means; Enter keeps it.
func askAX25Port(a *asker, cfg config.Config, rig config.Rig, s *config.AX25) {
	def, _ := ax25PortFrom(cfg, rig, *s)
	fmt.Fprintln(a.out, "The AX.25 port: Enter keeps each value in [brackets].")
	fmt.Fprintln(a.out, "callsign: the port's own address on the air. An SSID (K0ABC-1) tells it apart from your other stations.")
	for {
		ans, ok := a.line(fmt.Sprintf("Callsign [%s]: ", def.Call))
		if !ok || ans == "" {
			break
		}
		if config.ValidCallsign(strings.ToUpper(ans)) {
			s.Call = strings.ToUpper(ans)
			break
		}
		fmt.Fprintln(a.out, "that does not look like a callsign (CALL or CALL-SSID)")
	}
	num := func(prompt string, cur int, lo, hi int, dst *int) {
		for {
			ans, ok := a.line(fmt.Sprintf("%s [%d]: ", prompt, cur))
			if !ok || ans == "" {
				return
			}
			if n, err := strconv.Atoi(ans); err == nil && n >= lo && n <= hi {
				*dst = n
				return
			}
			fmt.Fprintf(a.out, "a number from %d to %d, please\n", lo, hi)
		}
	}
	def, _ = ax25PortFrom(cfg, rig, *s)
	fmt.Fprintln(a.out, "speed: the on-air baud rate; it should match the modem (the rig's Direwolf runs at "+strconv.Itoa(rig.Packet.ModemBaud())+").")
	num("Speed", def.Speed, 300, 115200, &s.Speed)
	fmt.Fprintln(a.out, "paclen: the largest packet. Smaller is more robust on a noisy channel; 255 is usual.")
	num("Paclen", def.Paclen, 1, 512, &s.Paclen)
	fmt.Fprintln(a.out, "window: how many packets may be unacknowledged. 2 is usual for 1200 baud; more only on a clean, fast link.")
	num("Window", def.Window, 1, 7, &s.Window)
}

func ax25PortCmd(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.AX25 == nil {
		return fmt.Errorf("not set up: hamstack ax25 setup")
	}
	rig, ok := cfg.RigByIDValue(cfg.AX25.Rig)
	if !ok {
		return fmt.Errorf("rig %q no longer exists: hamstack ax25 setup", cfg.AX25.Rig)
	}
	if len(args) == 3 && args[0] == "set" {
		next := *cfg.AX25
		switch args[1] {
		case "call":
			next.Call = strings.ToUpper(args[2])
		case "speed", "paclen", "window":
			n, err := strconv.Atoi(args[2])
			if err != nil {
				return fmt.Errorf("%s needs a number, not %q", args[1], args[2])
			}
			switch args[1] {
			case "speed":
				next.Speed = n
			case "paclen":
				next.Paclen = n
			case "window":
				next.Window = n
			}
		default:
			return fmt.Errorf("unknown setting %q (call, speed, paclen, window)", args[1])
		}
		port, err := ax25PortFrom(cfg, rig, next)
		if err != nil {
			return err
		}
		old, _ := os.ReadFile(ax25.PortsFile)
		text, changed := ax25.Upsert(string(old), port)
		var steps []install.Step
		if changed {
			s, err := install.RootFileStep(install.HostEnv(), "Update the port in "+ax25.PortsFile+" (a copy of the old file is kept)", ax25.PortsFile, text)
			if err != nil {
				return err
			}
			steps = append(steps, s)
		}
		steps = append(steps, install.Step{Desc: "Remember the setting", Do: func() error {
			_, err := mutate(func(c *config.Config) (string, error) { c.AX25 = &next; return "", nil })
			return err
		}})
		if err := install.Run(steps, install.Options{}); err != nil {
			return err
		}
		fmt.Println("The tools read axports when they start; run: hamstack ax25 detach && hamstack ax25 attach to apply it to ax0.")
		return nil
	}
	if len(args) != 0 {
		return fmt.Errorf("usage: hamstack ax25 port [set call|speed|paclen|window VALUE]")
	}
	port, err := ax25PortFrom(cfg, rig, *cfg.AX25)
	if err != nil {
		return err
	}
	fmt.Printf("port %s for rig %s (%s)\n", port.Name, rig.ID, ax25.PortsFile)
	fmt.Printf("  call:    %s\n  speed:   %d baud\n  paclen:  %d bytes\n  window:  %d frames\n", port.Call, port.Speed, port.Paclen, port.Window)
	fmt.Printf("\nchange with: hamstack ax25 port set call|speed|paclen|window VALUE\n")
	return nil
}
