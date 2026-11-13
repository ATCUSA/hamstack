package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/units"
)

const direwolfUsage = `usage:
  hamstack direwolf [show] [RIG]        the settings of a rig's Direwolf, and the configuration it will run
  hamstack direwolf setup [RIG]         guided: modem speed, callsign, key-up timing, digipeating
  hamstack direwolf set RIG KEY VALUE   keys: baud (300 1200 2400 4800 9600), call (CALL-SSID, or "" for the station's),
                                        txdelay, txtail, persist, slottime (0-255, 0 = Direwolf's default), digipeat (on|off)
  hamstack direwolf edit [RIG]          your own extra configuration lines (appended last, so they win), in $EDITOR

Direwolf is restarted by: hamstack restart direwolf@RIG`

func cmdDirewolf(args []string) error {
	if len(args) == 0 {
		return direwolfShow("")
	}
	switch args[0] {
	case "show":
		return direwolfShow(optArg(args, 1))
	case "setup":
		return direwolfSetup(optArg(args, 1))
	case "set":
		if len(args) != 4 {
			return fmt.Errorf("%s", direwolfUsage)
		}
		return direwolfSet(args[1], args[2], args[3])
	case "edit":
		return direwolfEdit(optArg(args, 1))
	case "help", "-h", "--help":
		fmt.Println(direwolfUsage)
		return nil
	}
	// A bare rig id: hamstack direwolf pkt
	return direwolfShow(args[0])
}

func optArg(args []string, i int) string {
	if i < len(args) {
		return args[i]
	}
	return ""
}

// direwolfRig finds the rig to configure: the one named, or the only (or first) rig with Direwolf.
func direwolfRig(cfg config.Config, id string) (config.Rig, error) {
	if id != "" {
		r, ok := cfg.RigByIDValue(id)
		if !ok {
			return r, fmt.Errorf("no rig %q (see: hamstack rigs)", id)
		}
		if r.Modem != config.Direwolf {
			return r, fmt.Errorf("rig %s does not use Direwolf (hamstack rig set %s modem direwolf)", id, id)
		}
		return r, nil
	}
	for _, r := range cfg.Rigs {
		if r.Modem == config.Direwolf {
			return r, nil
		}
	}
	return config.Rig{}, fmt.Errorf("no rig uses Direwolf: add one with hamstack rig add pkt --profile digirig --modem direwolf")
}

func extraPath(rigID string) string {
	d, err := config.Dir()
	if err != nil {
		return ""
	}
	return filepath.Join(d, "rigs", rigID, "direwolf.extra")
}

func describeOpts(o config.PacketOpts, station string) []string {
	dflt := func(v int, d string) string {
		if v == 0 {
			return d + " (Direwolf's default)"
		}
		return strconv.Itoa(v)
	}
	call := o.Call
	if call == "" {
		call = station + " (the station's)"
	}
	return []string{
		fmt.Sprintf("modem speed:  %d baud%s", o.ModemBaud(), map[bool]string{true: " (the default)", false: ""}[o.Baud == 0]),
		"callsign:     " + call,
		"txdelay:      " + dflt(o.TxDelay, "30") + "  (key-up delay, 10 ms units)",
		"txtail:       " + dflt(o.TxTail, "10") + "  (hold after the packet, 10 ms units)",
		"persist:      " + dflt(o.Persist, "63") + "  (how eagerly to transmit on a busy channel)",
		"slottime:     " + dflt(o.SlotTime, "10") + "  (10 ms units)",
		"digipeat:     " + map[bool]string{true: "ON: repeats other stations' WIDE packets", false: "off"}[o.Digipeat],
	}
}

func direwolfShow(id string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	rig, err := direwolfRig(cfg, id)
	if err != nil {
		return err
	}
	st, _ := cfg.ActiveStation()
	fmt.Printf("Direwolf for rig %s\n", rig.ID)
	for _, l := range describeOpts(rig.Packet, st.Callsign) {
		fmt.Println("  " + l)
	}
	if b, err := os.ReadFile(extraPath(rig.ID)); err == nil && strings.TrimSpace(string(b)) != "" {
		fmt.Printf("  your own lines: %d (%s)\n", len(strings.Split(strings.TrimSpace(string(b)), "\n")), extraPath(rig.ID))
	}
	_, prof, err := findRig(cfg, rig.ID)
	if err != nil {
		return err
	}
	snap := snapshotOrEmpty()
	ptt, _, perr := direwolfPTTLine(rig, prof, snap)
	if perr != nil {
		ptt = "# (" + perr.Error() + ")"
	}
	extra, _ := os.ReadFile(extraPath(rig.ID))
	fmt.Printf("\nIt will run this configuration (written each time the service starts):\n\n%s", units.DirewolfConfigWith(rig, prof, st, resolveAudio(rig, prof, snap), ptt, string(extra)))
	return nil
}

// setPacketField changes one setting, validating it.
func setPacketField(o *config.PacketOpts, key, value string) error {
	num := func() (int, error) {
		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return 0, fmt.Errorf("%s needs a number, not %q", key, value)
		}
		return n, nil
	}
	n := 0
	var err error
	switch key {
	case "baud", "txdelay", "txtail", "persist", "slottime":
		if n, err = num(); err != nil {
			return err
		}
	}
	next := *o
	switch key {
	case "baud":
		next.Baud = n
	case "call":
		next.Call = strings.ToUpper(strings.TrimSpace(value))
	case "txdelay":
		next.TxDelay = n
	case "txtail":
		next.TxTail = n
	case "persist":
		next.Persist = n
	case "slottime":
		next.SlotTime = n
	case "digipeat":
		switch strings.ToLower(value) {
		case "on", "true", "yes", "1":
			next.Digipeat = true
		case "off", "false", "no", "0":
			next.Digipeat = false
		default:
			return fmt.Errorf("digipeat must be on or off")
		}
	default:
		return fmt.Errorf("unknown setting %q (baud, call, txdelay, txtail, persist, slottime, digipeat)", key)
	}
	if err := next.Validate(); err != nil {
		return err
	}
	*o = next
	return nil
}

const digipeatNotice = "Digipeating makes this station transmit other stations' packets, unattended. It needs a callsign you are licensed for, " +
	"an SSID of its own, and a good location; a badly placed or misconfigured digipeater harms a whole area's APRS network."

func direwolfSet(rigID, key, value string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	rig, err := direwolfRig(cfg, rigID)
	if err != nil {
		return err
	}
	r, _ := cfg.RigByID(rig.ID)
	if err := setPacketField(&r.Packet, key, value); err != nil {
		return err
	}
	if err := config.Save(cfg); err != nil {
		return err
	}
	fmt.Printf("rig %s: %s set to %s\n", rig.ID, key, orDash(value))
	if key == "digipeat" && r.Packet.Digipeat {
		fmt.Println("note: " + digipeatNotice)
	}
	fmt.Printf("Restart Direwolf to use it: hamstack restart direwolf@%s\n", rig.ID)
	return nil
}

// direwolfSetup asks the questions. Every question has a default that keeps what is there, so pressing Enter
// through them changes nothing.
func direwolfSetup(rigID string) error {
	a := &asker{in: bufio.NewReader(os.Stdin), out: os.Stdout}
	return direwolfSetupWith(a, rigID)
}

func direwolfSetupWith(a *asker, rigID string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	rig, err := direwolfRig(cfg, rigID)
	if err != nil {
		return err
	}
	st, _ := cfg.ActiveStation()
	r, _ := cfg.RigByID(rig.ID)
	o := r.Packet
	fmt.Fprintf(a.out, "Direwolf setup for rig %s. Enter keeps the value shown in [brackets].\n\n", rig.ID)

	ask := func(prompt string, cur int, def int, lo, hi int) int {
		shown := cur
		if shown == 0 {
			shown = def
		}
		for {
			ans, ok := a.line(fmt.Sprintf("%s [%d]: ", prompt, shown))
			if !ok || ans == "" {
				return cur
			}
			n, err := strconv.Atoi(ans)
			if err == nil && n >= lo && n <= hi {
				if n == def {
					return 0 // Direwolf's own default: leave it unset
				}
				return n
			}
			fmt.Fprintf(a.out, "a number from %d to %d, please\n", lo, hi)
		}
	}

	fmt.Fprintln(a.out, "Modem speed: 1200 baud is standard VHF/UHF packet and APRS (144.390 MHz in North America). 9600 is for")
	fmt.Fprintln(a.out, "radios with a data port and a matching network. 300 is HF packet. Everyone you talk to must use the same.")
	for {
		ans, ok := a.line(fmt.Sprintf("Modem speed (%v) [%d]: ", config.Bauds, o.ModemBaud()))
		if !ok || ans == "" {
			break
		}
		if n, err := strconv.Atoi(ans); err == nil {
			next := o
			next.Baud = n
			if next.Baud == 1200 {
				next.Baud = 0
			}
			if next.Validate() == nil {
				o = next
				break
			}
		}
		fmt.Fprintf(a.out, "one of %v, please\n", config.Bauds)
	}

	fmt.Fprintf(a.out, "\nCallsign: your station's is %s. A packet station often uses an SSID so it can be told from your other stations\n", orDash(st.Callsign))
	fmt.Fprintln(a.out, "(for example -1 for a TNC, -10 for an iGate, -15 for a digipeater). Blank keeps the station's callsign.")
	for {
		ans, ok := a.line(fmt.Sprintf("Callsign [%s]: ", orDefault(o.Call, st.Callsign)))
		if !ok || ans == "" {
			break
		}
		if ans == "-" {
			o.Call = ""
			break
		}
		next := o
		next.Call = strings.ToUpper(ans)
		if next.Validate() == nil {
			o = next
			break
		}
		fmt.Fprintln(a.out, "that does not look like a callsign (CALL or CALL-SSID); a single - clears it")
	}

	if a.confirm("\nAdjust the transmit timing (txdelay, txtail, persist, slottime)? The defaults suit most radios.", false) {
		fmt.Fprintln(a.out, "txdelay is how long the radio is keyed before the data starts, in 10 ms units: a radio that cuts off the start of")
		fmt.Fprintln(a.out, "packets (slow to switch to transmit) needs more, a fast one can use less.")
		o.TxDelay = ask("txdelay", o.TxDelay, 30, 0, 255)
		o.TxTail = ask("txtail", o.TxTail, 10, 0, 255)
		o.Persist = ask("persist", o.Persist, 63, 0, 255)
		o.SlotTime = ask("slottime", o.SlotTime, 10, 0, 255)
	}

	if a.confirm("\nRun as an APRS digipeater (repeat other stations' WIDE packets)?", o.Digipeat) {
		fmt.Fprintln(a.out, digipeatNotice)
		o.Digipeat = a.confirm("Turn digipeating on?", false)
	} else {
		o.Digipeat = false
	}
	if err := o.Validate(); err != nil {
		return err
	}
	// The questions can take minutes: save onto the configuration as it is now, not the copy read before them.
	if _, err := mutateOnly(func(c *config.Config) (string, error) {
		now, ok := c.RigByID(rig.ID)
		if !ok {
			return "", fmt.Errorf("rig %s was removed in the meantime", rig.ID)
		}
		now.Packet = o
		return "", nil
	}); err != nil {
		return err
	}
	fmt.Fprintf(a.out, "\nSaved.\n")
	for _, l := range describeOpts(o, st.Callsign) {
		fmt.Fprintln(a.out, "  "+l)
	}
	fmt.Fprintf(a.out, "\nFor anything else (beacons, an iGate, more channels) add Direwolf lines of your own: hamstack direwolf edit %s\n", rig.ID)
	fmt.Fprintf(a.out, "Restart Direwolf to use it: hamstack restart direwolf@%s\n", rig.ID)
	return nil
}

func orDefault(v, d string) string {
	if v != "" {
		return v
	}
	return d
}

// direwolfEdit opens the rig's extra-lines file in $EDITOR, creating it with a short explanation.
func direwolfEdit(rigID string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	rig, err := direwolfRig(cfg, rigID)
	if err != nil {
		return err
	}
	path := extraPath(rig.ID)
	if _, err := os.Stat(path); err != nil {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		const intro = "# Your own Direwolf configuration lines for this rig. They are added at the end of the generated\n" +
			"# configuration every time the service starts, so they can override it. See Direwolf's documentation (man direwolf,\n" +
			"# or the User Guide) for PBEACON, IGSERVER, FILTER and the rest. Lines you add run unattended: check them.\n"
		if err := os.WriteFile(path, []byte(intro), 0o644); err != nil {
			return err
		}
	}
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		fmt.Printf("Set $EDITOR, or edit the file yourself:\n  %s\n", path)
		return nil
	}
	parts := strings.Fields(editor)
	cmd := exec.Command(parts[0], append(parts[1:], path)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	fmt.Printf("Saved %s. Restart Direwolf to use it: hamstack restart direwolf@%s\n", path, rig.ID)
	return nil
}
