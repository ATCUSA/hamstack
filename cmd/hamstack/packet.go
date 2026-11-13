package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/ATCUSA/hamstack/internal/agw"
	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/svc"
	"github.com/ATCUSA/hamstack/internal/units"
)

const packetUsage = `usage:
  hamstack packet listen [--rig ID]                   watch every packet Direwolf hears (like axlisten)
  hamstack packet call CALL [VIA,VIA] [--rig ID]      connect to a station and chat (like axcall)

These talk to Direwolf over its AGW port, so they need no root, no kernel AX.25 and no ax25-tools, and they
work while Pat is connected too. (For the kernel's own AX.25 stack see: hamstack ax25)`

func cmdPacket(args []string) error {
	if len(args) == 0 {
		fmt.Println(packetUsage)
		return nil
	}
	switch args[0] {
	case "listen":
		return packetListen(args[1:])
	case "call":
		return packetCall(args[1:])
	case "help", "-h", "--help":
		fmt.Println(packetUsage)
		return nil
	}
	return fmt.Errorf("unknown packet command %q\n%s", args[0], packetUsage)
}

// agwTarget works out which Direwolf to talk to: the named rig's, or the first rig that has one.
func agwTarget(cfg config.Config, rigID, addrOverride string) (addr string, rig config.Rig, err error) {
	if addrOverride != "" {
		return addrOverride, config.Rig{}, nil
	}
	for _, r := range cfg.ActiveRigs() {
		if r.Modem != config.Direwolf || (rigID != "" && r.ID != rigID) {
			continue
		}
		agwPort, _ := config.DirewolfPorts(r)
		return fmt.Sprintf("localhost:%d", agwPort), r, nil
	}
	if rigID != "" {
		return "", rig, fmt.Errorf("rig %q has no Direwolf modem (see: hamstack rigs)", rigID)
	}
	return "", rig, fmt.Errorf("no rig uses Direwolf. Add one: hamstack rig add pkt --profile digirig --modem direwolf   (or: --addr HOST:PORT for a TNC elsewhere)")
}

func warnIfStopped(rig config.Rig) {
	if rig.ID == "" || svc.Available() != nil {
		return
	}
	u := (units.Service{Kind: config.Direwolf, Rig: rig.ID}).Unit()
	if svc.Status(u).Active != "active" {
		fmt.Fprintf(os.Stderr, "note: Direwolf for rig %s is not running: hamstack start direwolf@%s\n", rig.ID, rig.ID)
	}
}

func packetListen(args []string) error {
	fs := flag.NewFlagSet("packet listen", flag.ContinueOnError)
	rigID := fs.String("rig", "", "which rig's Direwolf (default: the first)")
	addr := fs.String("addr", "", "an AGW server elsewhere, HOST:PORT")
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}
	cfg, _ := config.Load()
	target, rig, err := agwTarget(cfg, *rigID, *addr)
	if err != nil {
		return err
	}
	warnIfStopped(rig)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	c, err := agw.Dial(ctx, target)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.Monitor(); err != nil {
		return err
	}
	fmt.Printf("Listening to %s. Ctrl+C to stop.\n", target)
	heard := 0
	for {
		f, err := c.Next()
		if err != nil {
			if ctx.Err() != nil {
				fmt.Printf("\n%d packets heard.\n", heard)
				return nil
			}
			return agw.ErrClosed
		}
		if !f.IsMonitor() {
			continue
		}
		heard++
		fmt.Printf("%s %s\n", time.Now().Format("15:04:05"), strings.ReplaceAll(f.Text(), "\n", "\n         "))
	}
}

func packetCall(args []string) error {
	fs := flag.NewFlagSet("packet call", flag.ContinueOnError)
	rigID := fs.String("rig", "", "which rig's Direwolf (default: the first)")
	addr := fs.String("addr", "", "an AGW server elsewhere, HOST:PORT")
	from := fs.String("from", "", "your callsign (default: the active station's)")
	radioPort := fs.Int("port", 0, "Direwolf radio port (0 is the first)")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) < 1 || len(pos) > 2 {
		return fmt.Errorf("%s", packetUsage)
	}
	to := strings.ToUpper(pos[0])
	if !validCallWithSSID(to) {
		return fmt.Errorf("%q does not look like a callsign", pos[0])
	}
	var via []string
	if len(pos) == 2 {
		if via, err = parseVia(pos[1]); err != nil {
			return err
		}
	}
	if *radioPort < 0 || *radioPort > 255 {
		return fmt.Errorf("--port %d is not a Direwolf radio port (0 is the first)", *radioPort)
	}
	cfg, _ := config.Load()
	me := strings.ToUpper(*from)
	if me == "" {
		st, ok := cfg.ActiveStation()
		if !ok {
			return fmt.Errorf("no active station: say who you are with --from CALL, or add one (hamstack station add)")
		}
		me = st.Callsign
	}
	if !validCallWithSSID(me) {
		return fmt.Errorf("%q does not look like a callsign", me)
	}
	target, rig, err := agwTarget(cfg, *rigID, *addr)
	if err != nil {
		return err
	}
	warnIfStopped(rig)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Not the signal's context: Ctrl+C must leave the connection open long enough to send the disconnect.
	c, err := agw.Dial(context.Background(), target)
	if err != nil {
		return err
	}
	defer c.Close()
	return callSession(ctx, c, os.Stdin, os.Stdout, byte(*radioPort), me, to, via)
}

// parseVia reads a comma-separated digipeater path: callsigns, or aliases such as WIDE1-1.
func parseVia(list string) ([]string, error) {
	var via []string
	for _, v := range strings.Split(list, ",") {
		v = strings.ToUpper(strings.TrimSpace(v))
		if !validCallWithSSID(v) && !digiAlias.MatchString(v) {
			return nil, fmt.Errorf("%q is not a digipeater callsign", v)
		}
		via = append(via, v)
	}
	if len(via) > 8 {
		return nil, fmt.Errorf("at most 8 digipeaters")
	}
	return via, nil
}

// digiAlias matches a digipeater alias that is not a callsign (WIDE1-1, RELAY, TEMP1-1, a regional SS2-2):
// anything that fits an AX.25 address, six letters or digits and an SSID.
var digiAlias = regexp.MustCompile(`^[A-Z0-9]{1,6}(-[0-9]{1,2})?$`)

// hangupWait is how long a call waits for the TNC to confirm a disconnect before leaving anyway.
var hangupWait = 2 * time.Second

// callSession makes a connected-mode call on c and relays lines of in to the other station and what it sends
// to out, until the other side disconnects, in ends, a line is "~.", or ctx ends (Ctrl+C). On every one of
// those it asks the TNC to disconnect and waits a moment for that to be confirmed, so the other station is
// not left holding a connection that only times out.
func callSession(ctx context.Context, c *agw.Conn, in io.Reader, out io.Writer, p byte, me, to string, via []string) error {
	if err := c.Register(me); err != nil {
		return err
	}
	if err := c.Connect(p, me, to, via); err != nil {
		return err
	}
	path := to
	if len(via) > 0 {
		path += " via " + strings.Join(via, ",")
	}
	fmt.Fprintf(out, "Calling %s as %s... (type a line and press Enter to send it; ~. on a line, or Ctrl+C, disconnects)\n", path, me)

	quit := make(chan struct{})
	defer close(quit)
	done := make(chan error, 1)
	go func() {
		for {
			f, err := c.Next()
			if err != nil {
				done <- err
				return
			}
			switch f.Kind {
			case 'C': // connected, or a failed attempt: the text says which
				fmt.Fprintln(out, f.Text())
			case 'D':
				fmt.Fprint(out, strings.ReplaceAll(f.Text(), "\n", "\r\n")+"\r\n")
			case 'd':
				fmt.Fprintln(out, f.Text())
				done <- nil
				return
			}
		}
	}()
	lines := make(chan string)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(in)
		for sc.Scan() {
			select {
			case lines <- sc.Text():
			case <-quit:
				return
			}
		}
	}()
	hangUp := func() error {
		if err := c.Disconnect(p, me, to); err != nil {
			return nil // the connection is already gone
		}
		select {
		case <-done: // the TNC confirmed it (or closed)
		case <-time.After(hangupWait):
		}
		return nil
	}
	for {
		select {
		case err := <-done:
			if err != nil {
				return agw.ErrClosed
			}
			return nil
		case <-ctx.Done():
			err := hangUp()
			fmt.Fprintln(out, "\nDisconnected.")
			return err
		case l, ok := <-lines:
			if !ok || l == "~." {
				return hangUp()
			}
			if err := c.SendData(p, me, to, []byte(l+"\r")); err != nil {
				return err
			}
		}
	}
}

func validCallWithSSID(s string) bool { return config.ValidCallsign(s) }
