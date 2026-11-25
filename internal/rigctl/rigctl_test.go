package rigctl

import (
	"bufio"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeRigctld answers like hamlib's rigctld in extended response mode. replies maps a command to its lines
// (the echo, the data) and the RPRT code.
type fakeRigctld struct {
	ln      net.Listener
	mu      sync.Mutex
	got     []string
	replies map[string]string
}

func newFake(t *testing.T, replies map[string]string) *fakeRigctld {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeRigctld{ln: ln, replies: replies}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				r := bufio.NewReader(conn)
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					cmd := strings.TrimSpace(strings.TrimPrefix(line, "+"))
					f.mu.Lock()
					f.got = append(f.got, cmd)
					reply, ok := f.replies[cmd]
					f.mu.Unlock()
					if !ok {
						reply = "RPRT -11\n"
					}
					conn.Write([]byte(reply))
				}
			}()
		}
	}()
	return f
}

func (f *fakeRigctld) commands() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.got)
}

func TestReadsAndWrites(t *testing.T) {
	f := newFake(t, map[string]string{
		"f":              "get_freq:\nFrequency: 14074000\nRPRT 0\n",
		"m":              "get_mode:\nMode: PKTUSB\nPassband: 3000\nRPRT 0\n",
		"l RFPOWER":      "get_level: RFPOWER\n0.2\nRPRT 0\n",
		"l STRENGTH":     "get_level: STRENGTH\n-54\nRPRT 0\n",
		"u TUNER":        "get_func: TUNER\n1\nRPRT 0\n",
		"u COMP":         "get_func: COMP\nRPRT -9\n",
		"t":              "get_ptt:\nPTT: 0\nRPRT 0\n",
		"F 7074000":      "set_freq: 7074000\nRPRT 0\n",
		"M PKTUSB 0":     "set_mode: PKTUSB 0\nRPRT 0\n",
		"L RFPOWER 0.25": "set_level: RFPOWER 0.25\nRPRT 0\n",
		"U TUNER 1":      "set_func: TUNER 1\nRPRT 0\n",
		"G TUNE":         "vfo_op: TUNE\nRPRT 0\n",
	})
	c := New(f.ln.Addr().String())
	defer c.Close()

	if hz, err := c.Freq(); err != nil || hz != 14074000 {
		t.Errorf("Freq: %d %v", hz, err)
	}
	if m, w, err := c.Mode(); err != nil || m != "PKTUSB" || w != 3000 {
		t.Errorf("Mode: %q %d %v", m, w, err)
	}
	if v, err := c.Level("RFPOWER"); err != nil || v != 0.2 {
		t.Errorf("RFPOWER: %v %v", v, err)
	}
	if v, err := c.Level("STRENGTH"); err != nil || v != -54 {
		t.Errorf("STRENGTH: %v %v", v, err)
	}
	if on, err := c.Func("TUNER"); err != nil || !on {
		t.Errorf("TUNER: %v %v", on, err)
	}
	if on, err := c.PTT(); err != nil || on {
		t.Errorf("PTT: %v %v", on, err)
	}
	// A radio that refuses a read is "not available", not a fault, and the connection stays usable.
	if _, err := c.Func("COMP"); !Unsupported(err) {
		t.Errorf("COMP: want an unsupported error, got %v", err)
	}
	for name, err := range map[string]error{
		"SetFreq":  c.SetFreq(7074000),
		"SetMode":  c.SetMode("PKTUSB", 0),
		"SetLevel": c.SetLevel("RFPOWER", 0.25),
		"SetFunc":  c.SetFunc("TUNER", true),
		"VFOOp":    c.VFOOp("TUNE"),
	} {
		if err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	want := []string{"f", "m", "l RFPOWER", "l STRENGTH", "u TUNER", "t", "u COMP", "F 7074000", "M PKTUSB 0", "L RFPOWER 0.25", "U TUNER 1", "G TUNE"}
	if got := f.commands(); !slices.Equal(got, want) {
		t.Errorf("commands sent:\n got %v\nwant %v", got, want)
	}
}

func TestCommandsCannotBeInjected(t *testing.T) {
	f := newFake(t, nil)
	c := New(f.ln.Addr().String())
	defer c.Close()
	for _, err := range []error{
		c.SetMode("USB\nT 1", 0), c.SetMode("", 0), c.SetFunc("TUNER 1\nT", true), c.VFOOp("TUNE; T 1"), c.SetLevel("RF POWER", 1),
		doErr(c, "f\nT 1"),
	} {
		if err == nil {
			t.Error("want an error for a command that is not one plain hamlib command")
		}
	}
	if got := f.commands(); len(got) != 0 {
		t.Errorf("nothing may reach the radio: %v", got)
	}
}

func doErr(c *Client, cmd string) error { _, err := c.Do(cmd); return err }

func TestReconnectsAfterRigctldRestarts(t *testing.T) {
	f := newFake(t, map[string]string{"f": "get_freq:\nFrequency: 7074000\nRPRT 0\n"})
	addr := f.ln.Addr().String()
	c := New(addr)
	defer c.Close()
	if _, err := c.Freq(); err != nil {
		t.Fatal(err)
	}
	f.ln.Close() // rigctld restarts: a new listener on the same port, and the old connection is dead
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Skip("could not take the port again:", err)
	}
	f2 := &fakeRigctld{ln: ln, replies: f.replies}
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		r.ReadString('\n')
		conn.Write([]byte(f2.replies["f"]))
		time.Sleep(200 * time.Millisecond)
	}()
	defer ln.Close()
	// the first connection's server side may still be alive for a moment; ask until the new one answers
	var hz int64
	for i := 0; i < 3 && hz == 0; i++ {
		hz, _ = c.Freq()
	}
	if hz != 7074000 {
		t.Errorf("did not recover: %d", hz)
	}
}

func TestParseCapsOfTheFTDX10(t *testing.T) {
	lines := []string{
		"Mode list: AM CW USB LSB RTTY FM CWR RTTYR PKTLSB PKTUSB FM-D FMN AMN PKTFMN",
		"Get functions: NB COMP VOX TONE TSQL FBKIN ANF NR APF MON MN LOCK RIT TUNER XIT ",
		"Set functions: NB COMP VOX TONE TSQL FBKIN ANF NR APF MON MN LOCK RIT TUNER XIT ",
		"Get level: PREAMP(0..20/10) AF(0.000000..1.000000/0.003922) RFPOWER(0.050000..1.000000/0.010000) SWR(0.000000..5.000000/0.003922) RFPOWER_METER_WATTS(0.000000..100.000000/0.003922) ",
		"Set level: PREAMP(0..20/10) RFPOWER(0.050000..1.000000/0.010000) MICGAIN(0.000000..1.000000/0.010000) ",
		"VFO Ops: CPY XCHG FROM_VFO TO_VFO UP DOWN BAND_UP BAND_DOWN TUNE TOGGLE ",
	}
	caps := ParseCaps(lines)
	if !slices.Contains(caps.Modes, "PKTUSB") || len(caps.Modes) != 14 {
		t.Errorf("modes: %v", caps.Modes)
	}
	if !caps.SetFuncs["TUNER"] || !caps.VFOOps["TUNE"] || !caps.GetLevels["SWR"] || !caps.GetLevels["RFPOWER_METER_WATTS"] || caps.SetLevels["SWR"] {
		t.Errorf("caps: %+v", caps)
	}
	if caps.FullWatts != 100 {
		t.Errorf("full power: %v W", caps.FullWatts)
	}
	if caps.RFPowerMin != 0.05 || caps.RFPowerMax != 1 {
		t.Errorf("power range: %v..%v", caps.RFPowerMin, caps.RFPowerMax)
	}
}
