// Package rigctl is a small client for hamlib's rigctld, the one program that talks to a radio's CAT port.
// It uses the extended response protocol (a "+" in front of each command), in which every reply ends in
// "RPRT n", so a reply can always be told from the next one even when a radio answers slowly or not at all.
//
// A Client holds one connection and runs one command at a time. rigctld serves each connection in turn, and
// the radio's serial line is slow (Mercury and the dashboard share it), so callers should ask for as little as
// they need.
package rigctl

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Hamlib's error codes that matter here.
const (
	ErrInvalid   = -1  // RIG_EINVAL: a bad argument, or a name the backend does not know
	ErrRejected  = -9  // RIG_ERJCTED: the radio refused the command
	ErrNotImpl   = -4  // RIG_ENIMPL
	ErrNotAvail  = -11 // RIG_ENAVAIL: not available, or not known yet
	ErrNotTarget = -6  // RIG_ETARGET: not for this VFO or backend; hamlib versions differ, and any of these means "no such thing"
)

// Error is a failure reported by rigctld itself, as opposed to a broken connection.
type Error struct {
	Cmd  string
	Code int
}

func (e *Error) Error() string { return fmt.Sprintf("rigctld: %s: RPRT %d", e.Cmd, e.Code) }

// Unsupported reports whether err means the radio or hamlib cannot do what was asked, which a caller should
// show as "not available" rather than as a fault.
func Unsupported(err error) bool {
	var e *Error
	if !errors.As(err, &e) {
		return false
	}
	switch e.Code {
	case ErrInvalid, ErrRejected, ErrNotImpl, ErrNotAvail, ErrNotTarget, -8:
		return true
	}
	return false
}

// Client is a connection to one rigctld.
type Client struct {
	Addr    string        // host:port
	Timeout time.Duration // for connecting, and for each reply; 3 seconds if zero

	mu   sync.Mutex
	conn net.Conn
	r    *bufio.Reader
	caps *Caps
}

// New makes a client. Nothing connects until the first command.
func New(addr string) *Client { return &Client{Addr: addr} }

func (c *Client) timeout() time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return 3 * time.Second
}

// Close drops the connection; the next command opens a new one.
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.drop()
}

func (c *Client) drop() {
	if c.conn != nil {
		c.conn.Close()
	}
	c.conn, c.r = nil, nil
}

// Do sends one command (as rigctl would take it: "f", "M USB 2400", "\get_rig_info") and returns the reply's
// lines without the echoed command and the closing RPRT line. A non-zero RPRT is an *Error.
func (c *Client) Do(cmd string) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.do(cmd)
}

func (c *Client) do(cmd string) ([]string, error) {
	if strings.ContainsAny(cmd, "\r\n") {
		return nil, fmt.Errorf("rigctl: a command is one line")
	}
	lines, err := c.try(cmd)
	if err != nil && isConn(err) {
		// A rigctld that was restarted leaves the old connection dead: once more, on a new one.
		c.drop()
		lines, err = c.try(cmd)
	}
	if err != nil && isConn(err) {
		c.drop()
	}
	return lines, err
}

func isConn(err error) bool {
	var e *Error
	return !errors.As(err, &e)
}

func (c *Client) try(cmd string) ([]string, error) {
	if c.conn == nil {
		conn, err := net.DialTimeout("tcp", c.Addr, c.timeout())
		if err != nil {
			return nil, err
		}
		c.conn, c.r = conn, bufio.NewReader(conn)
	}
	_ = c.conn.SetDeadline(time.Now().Add(c.timeout()))
	if _, err := fmt.Fprintf(c.conn, "+%s\n", cmd); err != nil {
		return nil, err
	}
	var lines []string
	for {
		line, err := c.r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		// A raw command's reply has no line end before its RPRT: "Reply: EX010417050;RPRT 0" (Hamlib 4.7, send_cmd).
		// Waiting for the RPRT on a line of its own would wait out the deadline on every one.
		if i := strings.LastIndex(line, "RPRT "); i > 0 {
			lines = append(lines, line[:i])
			line = line[i:]
		}
		if rest, ok := strings.CutPrefix(line, "RPRT "); ok {
			code, err := strconv.Atoi(strings.TrimSpace(rest))
			if err != nil {
				return nil, fmt.Errorf("rigctld: bad reply %q", line)
			}
			if code != 0 {
				return nil, &Error{Cmd: cmd, Code: code}
			}
			if len(lines) > 0 && isEcho(lines[0]) {
				lines = lines[1:]
			}
			return lines, nil
		}
		lines = append(lines, line)
	}
}

// isEcho reports whether a reply line is rigctld repeating the command it answers ("get_level: RFPOWER"):
// a lower-case function name and a colon. The data that follows is labelled in capitals ("Frequency: ...").
func isEcho(line string) bool {
	name, _, ok := strings.Cut(line, ":")
	if !ok || name == "" {
		return false
	}
	return strings.Trim(name, "abcdefghijklmnopqrstuvwxyz0123456789_") == ""
}

// value returns the last non-empty line of a reply, with a "Label: " prefix dropped, which is how rigctld
// presents a single value ("Frequency: 14074000" for f, a bare "0.2" for l).
func value(lines []string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		if s := strings.TrimSpace(lines[i]); s != "" {
			if _, v, ok := strings.Cut(s, ": "); ok {
				return strings.TrimSpace(v)
			}
			return s
		}
	}
	return ""
}

func parseFloat(lines []string) (float64, error) {
	s := value(lines)
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("rigctld: %q is not a number", s)
	}
	return f, nil
}

// Freq is the frequency in hertz.
func (c *Client) Freq() (int64, error) {
	lines, err := c.Do("f")
	if err != nil {
		return 0, err
	}
	f, err := parseFloat(lines)
	return int64(f), err
}

// SetFreq tunes the radio, in hertz.
func (c *Client) SetFreq(hz int64) error {
	_, err := c.Do("F " + strconv.FormatInt(hz, 10))
	return err
}

// Mode is the mode name and the passband width in hertz.
func (c *Client) Mode() (string, int, error) {
	lines, err := c.Do("m")
	if err != nil {
		return "", 0, err
	}
	var mode string
	var width int
	for _, l := range lines {
		if v, ok := strings.CutPrefix(l, "Mode: "); ok {
			mode = strings.TrimSpace(v)
		}
		if v, ok := strings.CutPrefix(l, "Passband: "); ok {
			width, _ = strconv.Atoi(strings.TrimSpace(v))
		}
	}
	if mode == "" && len(lines) > 0 { // a rigctld that does not label its answers: mode, then width
		mode = strings.TrimSpace(lines[0])
		if len(lines) > 1 {
			width, _ = strconv.Atoi(strings.TrimSpace(lines[1]))
		}
	}
	return mode, width, nil
}

// SetMode changes the mode. A width of 0 keeps the radio's own choice for it (hamlib's "RIG_PASSBAND_NOCHANGE"
// is -1, "normal" is 0).
func (c *Client) SetMode(mode string, width int) error {
	if !validWord(mode) {
		return fmt.Errorf("rigctl: bad mode %q", mode)
	}
	_, err := c.Do(fmt.Sprintf("M %s %d", mode, width))
	return err
}

// Level reads a level by hamlib's name (RFPOWER, SWR, STRENGTH...).
func (c *Client) Level(name string) (float64, error) {
	if !validWord(name) {
		return 0, fmt.Errorf("rigctl: bad level %q", name)
	}
	lines, err := c.Do("l " + name)
	if err != nil {
		return 0, err
	}
	return parseFloat(lines)
}

// SetLevel sets a level.
func (c *Client) SetLevel(name string, v float64) error {
	if !validWord(name) {
		return fmt.Errorf("rigctl: bad level %q", name)
	}
	_, err := c.Do(fmt.Sprintf("L %s %s", name, strconv.FormatFloat(v, 'f', -1, 64)))
	return err
}

// Func reads an on/off function (TUNER, COMP, NB...).
func (c *Client) Func(name string) (bool, error) {
	if !validWord(name) {
		return false, fmt.Errorf("rigctl: bad function %q", name)
	}
	lines, err := c.Do("u " + name)
	if err != nil {
		return false, err
	}
	f, err := parseFloat(lines)
	return f != 0, err
}

// SetFunc switches a function.
func (c *Client) SetFunc(name string, on bool) error {
	if !validWord(name) {
		return fmt.Errorf("rigctl: bad function %q", name)
	}
	n := 0
	if on {
		n = 1
	}
	_, err := c.Do(fmt.Sprintf("U %s %d", name, n))
	return err
}

// PTT reports whether the radio is transmitting.
func (c *Client) PTT() (bool, error) {
	lines, err := c.Do("t")
	if err != nil {
		return false, err
	}
	f, err := parseFloat(lines)
	return f != 0, err
}

// VFOOp does a VFO operation by hamlib's name (TUNE, BAND_UP, XCHG...).
func (c *Client) VFOOp(op string) error {
	if !validWord(op) {
		return fmt.Errorf("rigctl: bad operation %q", op)
	}
	_, err := c.Do("G " + op)
	return err
}

// validWord keeps anything but a hamlib name out of a command line.
func validWord(s string) bool {
	if s == "" || len(s) > 32 {
		return false
	}
	return strings.Trim(s, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-") == ""
}

// Caps is what the radio and hamlib can do, from rigctld's capability dump.
type Caps struct {
	Modes     []string
	GetLevels map[string]bool
	SetLevels map[string]bool
	GetFuncs  map[string]bool
	SetFuncs  map[string]bool
	VFOOps    map[string]bool
	// RFPowerMin and RFPowerMax are the limits of the RFPOWER level (0..1 of full power).
	RFPowerMin, RFPowerMax float64
	// FullWatts is the power at RFPOWER 1, when the radio's watt meter says what its range is; 0 if unknown.
	FullWatts float64
}

// Capabilities reads (once, then remembers) what the radio can do.
func (c *Client) Capabilities() (Caps, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.caps != nil {
		return *c.caps, nil
	}
	lines, err := c.do(`\dump_caps`)
	if err != nil {
		return Caps{}, err
	}
	caps := ParseCaps(lines)
	c.caps = &caps
	return caps, nil
}

// ParseCaps reads the lines of "\dump_caps" that say what the radio can do.
func ParseCaps(lines []string) Caps {
	caps := Caps{GetLevels: map[string]bool{}, SetLevels: map[string]bool{}, GetFuncs: map[string]bool{}, SetFuncs: map[string]bool{}, VFOOps: map[string]bool{}, RFPowerMin: 0, RFPowerMax: 1}
	for _, l := range lines {
		key, rest, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		rest = strings.TrimSpace(rest)
		switch strings.TrimSpace(key) {
		case "Mode list":
			caps.Modes = strings.Fields(rest)
		case "Get functions":
			addAll(caps.GetFuncs, strings.Fields(rest))
		case "Set functions":
			addAll(caps.SetFuncs, strings.Fields(rest))
		case "VFO Ops":
			addAll(caps.VFOOps, strings.Fields(rest))
		case "Get level":
			caps.parseLevels(caps.GetLevels, rest, false)
		case "Set level":
			caps.parseLevels(caps.SetLevels, rest, true)
		}
	}
	return caps
}

func addAll(m map[string]bool, names []string) {
	for _, n := range names {
		m[n] = true
	}
}

// parseLevels reads "RFPOWER(0.050000..1.000000/0.010000) SWR(...)": a name and its range and step.
func (c *Caps) parseLevels(into map[string]bool, s string, set bool) {
	for _, f := range strings.Fields(s) {
		name, rng, ok := strings.Cut(f, "(")
		if !ok {
			continue
		}
		into[name] = true
		if name == "RFPOWER_METER_WATTS" && !set {
			_, hi, _ := strings.Cut(strings.SplitN(rng, "/", 2)[0], "..")
			if v, err := strconv.ParseFloat(hi, 64); err == nil && v > 0 {
				c.FullWatts = v
			}
		}
		if name == "RFPOWER" && set {
			lo, hi, _ := strings.Cut(strings.TrimSuffix(strings.SplitN(rng, "/", 2)[0], ")"), "..")
			if v, err := strconv.ParseFloat(lo, 64); err == nil {
				c.RFPowerMin = v
			}
			if v, err := strconv.ParseFloat(hi, 64); err == nil && v > 0 {
				c.RFPowerMax = v
			}
		}
	}
}

// Abort drops the carrier from a second connection, so it works while the first is busy with a command. It is
// what a Stop button calls.
func (c *Client) Abort() error {
	conn, err := net.DialTimeout("tcp", c.Addr, c.timeout())
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(c.timeout()))
	if _, err := fmt.Fprint(conn, "+T 0\n"); err != nil {
		return err
	}
	r := bufio.NewReader(conn)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return err
		}
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "RPRT "); ok {
			if rest != "0" {
				return fmt.Errorf("rigctld: PTT off: RPRT %s", rest)
			}
			return nil
		}
	}
}
