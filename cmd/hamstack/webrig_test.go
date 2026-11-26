package main

import (
	"bufio"
	"context"
	"fmt"
	"github.com/ATCUSA/hamstack/internal/rigctl"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/web"
)

// simRig is a small rigctld with a radio behind it: it keeps a frequency, a mode and a power, and a tuning
// cycle that transmits for a while. It writes down every command, so a test can see what reached the radio.
type simRig struct {
	ln net.Listener

	mu          sync.Mutex
	log         []string
	freq        int64
	mode        string
	power       float64
	tuner       bool
	txUntil     time.Time
	pttFail     bool          // the radio will not say whether it is transmitting
	pttBlind    bool          // it answers, but always "not transmitting"
	tuneFor     time.Duration // how long a tuning cycle transmits
	powerAtTune float64
}

func newSimRig(t *testing.T) *simRig {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	r := &simRig{ln: ln, freq: 14074000, mode: "PKTUSB", power: 0.5, tuner: true, tuneFor: 1200 * time.Millisecond}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go r.serve(c)
		}
	}()
	return r
}

func (r *simRig) port() int { return r.ln.Addr().(*net.TCPAddr).Port }

func (r *simRig) commands() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.log...)
}

func (r *simRig) serve(c net.Conn) {
	defer c.Close()
	in := bufio.NewReader(c)
	for {
		line, err := in.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.TrimSpace(strings.TrimPrefix(line, "+"))
		c.Write([]byte(r.handle(cmd)))
	}
}

func (r *simRig) handle(cmd string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !strings.HasPrefix(cmd, `\dump_caps`) {
		r.log = append(r.log, cmd)
	}
	tx := time.Now().Before(r.txUntil)
	f := strings.Fields(cmd)
	ok := func(echo string, data ...string) string {
		return echo + ":\n" + strings.Join(append(data, "RPRT 0"), "\n") + "\n"
	}
	switch {
	case cmd == `\dump_caps`:
		return "Caps dump for model 1042\nMode list: USB LSB PKTUSB CW\nGet functions: TUNER COMP\nSet functions: TUNER COMP\n" +
			"Get level: RFPOWER(0.050000..1.000000/0.010000) SWR(0.000000..5.000000/0.003922) STRENGTH(0..60/0) ALC(0.000000..1.000000/0.010000) RFPOWER_METER_WATTS(0.000000..100.000000/0.003922)\n" +
			"Set level: RFPOWER(0.050000..1.000000/0.010000)\nVFO Ops: TUNE XCHG\nRPRT 0\n"
	case f[0] == "f":
		return ok("get_freq", fmt.Sprintf("Frequency: %d", r.freq))
	case f[0] == "F":
		r.freq, _ = strconv.ParseInt(f[1], 10, 64)
		return ok("set_freq: " + f[1])
	case f[0] == "m":
		return ok("get_mode", "Mode: "+r.mode, "Passband: 3000")
	case f[0] == "M":
		r.mode = f[1]
		return ok("set_mode: " + f[1])
	case f[0] == "t":
		if r.pttFail {
			return "get_ptt:\nRPRT -9\n"
		}
		if r.pttBlind {
			return ok("get_ptt", "0")
		}
		return ok("get_ptt", map[bool]string{true: "1", false: "0"}[tx])
	case f[0] == "T":
		r.txUntil = time.Time{}
		return ok("set_ptt: " + f[1])
	case cmd == "l RFPOWER":
		return ok("get_level: RFPOWER", strconv.FormatFloat(r.power, 'f', -1, 64))
	case f[0] == "L" && f[1] == "RFPOWER":
		r.power, _ = strconv.ParseFloat(f[2], 64)
		return ok("set_level: " + cmd)
	case cmd == "l SWR":
		if tx {
			return ok("get_level: SWR", "1.4")
		}
		return ok("get_level: SWR", "1") // an FT-DX10 reads 1 at rest, not 0
	case cmd == "l STRENGTH":
		return ok("get_level: STRENGTH", "-30")
	case f[0] == "u" && f[1] == "TUNER":
		return ok("get_func: TUNER", map[bool]string{true: "1", false: "0"}[r.tuner])
	case f[0] == "U" && f[1] == "TUNER":
		r.tuner = f[2] == "1"
		return ok("set_func: " + cmd)
	case cmd == "G TUNE":
		r.powerAtTune = r.power
		r.txUntil = time.Now().Add(r.tuneFor)
		return ok("vfo_op: TUNE")
	}
	return "RPRT -11\n"
}

func simConfig(t *testing.T, r *simRig, tune bool) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	rigLinks.Lock()
	rigLinks.m = map[string]*rigLink{}
	rigLinks.Unlock()
	cfg := config.Config{Rigs: []config.Rig{{ID: "ftdx10", Name: "FT-DX10", Profile: "ftdx10", RigPort: r.port()}}, Web: &config.WebSettings{RigTune: tune}}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
}

func TestPanelReadsTheRadioThroughRigctld(t *testing.T) {
	r := newSimRig(t)
	simConfig(t, r, false)
	got := webBackend{}.RigStates()
	if len(got) != 1 {
		t.Fatalf("%+v", got)
	}
	s := got[0]
	if !s.Online || s.Freq != 14074000 || s.Mode != "PKTUSB" || s.Power == nil || *s.Power != 0.5 || s.Strength == nil || *s.Strength != -30 {
		t.Errorf("state: %+v", s)
	}
	if !s.CanTune || s.TuneOn || s.FullW != 100 || s.PowerMin != 0.05 || s.Tuner == nil || !*s.Tuner {
		t.Errorf("tune/limits: %+v", s)
	}
	if s.SWR != nil || s.ALC != nil {
		t.Errorf("transmit meters are read only while transmitting: %+v", s)
	}
	// A second look within a moment does not go back to the radio.
	n := len(r.commands())
	webBackend{}.RigStates()
	if len(r.commands()) != n {
		t.Errorf("a repeat reading within the cache time reached the radio: %v", r.commands()[n:])
	}
}

// The card shows every meter the radio has from the start, so the list comes from what hamlib can read and not
// from readings that only exist while transmitting.
func TestMeterKeysComeFromTheRadiosCapabilities(t *testing.T) {
	r := newSimRig(t)
	simConfig(t, r, false)
	got := webBackend{}.RigStates()[0].Meters
	if strings.Join(got, ",") != "s,p,swr,alc" {
		t.Errorf("meters: %v", got)
	}
}

func TestPanelReportsARigThatIsNotRunning(t *testing.T) {
	r := newSimRig(t)
	simConfig(t, r, false)
	r.ln.Close()
	got := webBackend{}.RigStates()
	if len(got) != 1 || got[0].Online || got[0].Error == "" || len(got[0].Services) == 0 || got[0].Services[0].Name != "rigctld@ftdx10" {
		t.Errorf("an offline rig must say why and carry its services, for the Start button: %+v", got)
	}
}

func TestOfflineReasonFollowsWhatRigControlIsDoing(t *testing.T) {
	svcs := func(state string) []web.RigService { return []web.RigService{{Name: "rigctld@x", State: state}} }
	for state, want := range map[string]string{
		"stopped": "Start it", "failed": "log", "no radio": "not found on USB", "not installed": "not installed", "starting": "starting",
	} {
		if got := offlineReason("raw", svcs(state)); !strings.Contains(got, want) {
			t.Errorf("%s: %q lacks %q", state, got, want)
		}
	}
	if got := offlineReason("the radio did not answer", svcs("running")); got != "the radio did not answer" {
		t.Errorf("a running rig control keeps the reason it was given: %q", got)
	}
}

func TestSetChangesFrequencyThenModeAndRefusesWhileTransmitting(t *testing.T) {
	r := newSimRig(t)
	simConfig(t, r, false)
	f := int64(7074000)
	p := 0.25
	if err := (webBackend{}).RigSet("ftdx10", web.RigSetting{Freq: &f, Mode: "USB", Power: &p}); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	if r.freq != 7074000 || r.mode != "USB" || r.power != 0.25 {
		t.Errorf("radio: %d %s %v", r.freq, r.mode, r.power)
	}
	r.mu.Unlock()
	cmds := strings.Join(r.commands(), "|")
	if strings.Index(cmds, "F 7074000") > strings.Index(cmds, "M USB") {
		t.Errorf("frequency must come before mode: %s", cmds)
	}
	r.mu.Lock()
	r.txUntil = time.Now().Add(time.Minute)
	r.mu.Unlock()
	f2 := int64(3573000)
	if err := (webBackend{}).RigSet("ftdx10", web.RigSetting{Freq: &f2}); err == nil || !strings.Contains(err.Error(), "transmitting") {
		t.Errorf("a transmitting radio must not be retuned: %v", err)
	}
	if err := (webBackend{}).RigSet("nope", web.RigSetting{Freq: &f2}); err == nil {
		t.Error("an unknown rig must be refused")
	}
}

func TestTuneRunsTheCycleAtLowPowerAndPutsThePowerBack(t *testing.T) {
	r := newSimRig(t)
	simConfig(t, r, true)
	res, err := webBackend{}.RigTune(context.Background(), "ftdx10")
	if err != nil {
		t.Fatal(err)
	}
	if res.SWR == nil || *res.SWR != 1.4 || !strings.Contains(res.Message, "Tuned") {
		t.Errorf("result: %+v", res)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.powerAtTune != 0.10 {
		t.Errorf("the cycle ran at power %v, want 0.10", r.powerAtTune)
	}
	if r.power != 0.5 {
		t.Errorf("the power must be put back: %v", r.power)
	}
}

// The FT-DX10 reads an SWR of 1 when it is not transmitting. A cycle must end when the carrier does, not
// run to the time limit because a resting reading looked like transmitting.
func TestTuneEndsWhenTheCarrierEndsEvenThoughTheRatioRestsAboveZero(t *testing.T) {
	r := newSimRig(t)
	simConfig(t, r, true)
	start := time.Now()
	res, err := webBackend{}.RigTune(context.Background(), "ftdx10")
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 6*time.Second {
		t.Errorf("took %s: the cycle waited for the time limit", d)
	}
	if res.SWR == nil || *res.SWR != 1.4 {
		t.Errorf("only readings while transmitting count: %+v", res)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.tuner {
		t.Error("a cycle that ended normally must leave the tuner in line")
	}
}

func TestTuneOnARadioThatNeverReportsTransmitting(t *testing.T) {
	r := newSimRig(t)
	r.pttBlind = true
	simConfig(t, r, true)
	start := time.Now()
	res, err := webBackend{}.RigTune(context.Background(), "ftdx10")
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 6*time.Second {
		t.Errorf("took %s", d)
	}
	if !strings.Contains(res.Message, "Tuned") || res.SWR == nil || *res.SWR != 1.4 {
		t.Errorf("the ratio rising above its resting value is the evidence: %+v", res)
	}
}

func TestTuneKeepsAPowerThatIsAlreadyLow(t *testing.T) {
	r := newSimRig(t)
	r.power = 0.05
	simConfig(t, r, true)
	if _, err := (webBackend{}).RigTune(context.Background(), "ftdx10"); err != nil {
		t.Fatal(err)
	}
	for _, c := range r.commands() {
		if strings.HasPrefix(c, "L RFPOWER") {
			t.Errorf("a power at or under the tuning power must not be touched: %v", r.commands())
		}
	}
}

func TestTuneIsRefusedUnlessSafe(t *testing.T) {
	t.Run("off unless the owner turned it on", func(t *testing.T) {
		r := newSimRig(t)
		simConfig(t, r, false)
		if _, err := (webBackend{}).RigTune(context.Background(), "ftdx10"); err == nil || !strings.Contains(err.Error(), "hamstack dashboard tune on") {
			t.Errorf("%v", err)
		}
		for _, c := range r.commands() {
			if c == "G TUNE" {
				t.Error("the radio must not be keyed")
			}
		}
	})
	t.Run("already transmitting", func(t *testing.T) {
		r := newSimRig(t)
		simConfig(t, r, true)
		r.txUntil = time.Now().Add(time.Minute)
		if _, err := (webBackend{}).RigTune(context.Background(), "ftdx10"); err == nil || !strings.Contains(err.Error(), "transmitting") {
			t.Errorf("%v", err)
		}
	})
	t.Run("cannot tell whether it is transmitting", func(t *testing.T) {
		r := newSimRig(t)
		r.pttFail = true
		simConfig(t, r, true)
		if _, err := (webBackend{}).RigTune(context.Background(), "ftdx10"); err == nil || !strings.Contains(err.Error(), "cannot tell") {
			t.Errorf("%v", err)
		}
		for _, c := range r.commands() {
			if c == "G TUNE" {
				t.Error("the radio must not be keyed when its state is unknown")
			}
		}
	})
	t.Run("only one at a time", func(t *testing.T) {
		r := newSimRig(t)
		simConfig(t, r, true)
		_, l, _ := rigByID(loadCfg(), "ftdx10")
		l.tuneMu.Lock()
		defer l.tuneMu.Unlock()
		if _, err := (webBackend{}).RigTune(context.Background(), "ftdx10"); err == nil || !strings.Contains(err.Error(), "already") {
			t.Errorf("%v", err)
		}
	})
}

func TestCancellingATuneDropsTheCarrierAndRestoresThePower(t *testing.T) {
	r := newSimRig(t)
	r.tuneFor = time.Minute
	simConfig(t, r, true)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(900 * time.Millisecond); cancel() }()
	_, err := webBackend{}.RigTune(ctx, "ftdx10")
	if err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("%v", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if time.Now().Before(r.txUntil) {
		t.Error("the carrier must be dropped")
	}
	if r.power != 0.5 {
		t.Errorf("the power must be put back: %v", r.power)
	}
	if r.tuner {
		t.Error("an emergency stop switches the tuner off, which is what ends a cycle the carrier-off does not")
	}
}

func TestATuneThatNeverEndsIsStopped(t *testing.T) {
	old := tuneMaxTime
	tuneMaxTime = 2 * time.Second
	t.Cleanup(func() { tuneMaxTime = old })
	r := newSimRig(t)
	r.tuneFor = time.Hour
	simConfig(t, r, true)
	start := time.Now()
	_, err := webBackend{}.RigTune(context.Background(), "ftdx10")
	if err == nil || !strings.Contains(err.Error(), "still transmitting") {
		t.Fatalf("%v", err)
	}
	if d := time.Since(start); d > tuneMaxTime+3*time.Second {
		t.Errorf("took %s", d)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if time.Now().Before(r.txUntil) || r.power != 0.5 {
		t.Errorf("carrier dropped and power restored: tx until %v, power %v", r.txUntil, r.power)
	}
}

// An antenna tuner always shows a high SWR while it searches. The verdict is where it settled.
func TestTuneResultJudgesTheFinalSWRNotThePeak(t *testing.T) {
	res := finishTune(nil, rigctl.Caps{}, 1.3, 25, true)
	if strings.Contains(res.Message, "Check the antenna") || !strings.Contains(res.Message, "settled at 1.3") || !strings.Contains(res.Message, "peaked at 25.0") {
		t.Fatalf("%s", res.Message)
	}
	if res.SWR == nil || *res.SWR != 1.3 {
		t.Fatal("the reported SWR is the final one")
	}
	bad := finishTune(nil, rigctl.Caps{}, 4.2, 25, true)
	if !strings.Contains(bad.Message, "Check the antenna") || !strings.Contains(bad.Message, "4.2") {
		t.Fatalf("%s", bad.Message)
	}
}

func TestSlowOptionalReadsArePausedNotRepeated(t *testing.T) {
	l := &rigLink{}
	l.slowRead("SWR", 100*time.Millisecond)
	if l.paused("SWR") {
		t.Fatal("a fast read must not be paused")
	}
	l.slowRead("func COMP", 2400*time.Millisecond) // an FT-DX10's COMP function, which stalls every program on the radio
	if !l.paused("func COMP") || l.paused("SWR") {
		t.Fatal("only the slow read is paused")
	}
	l.slow["func COMP"] = time.Now().Add(-time.Second)
	if l.paused("func COMP") {
		t.Fatal("the pause ends")
	}
}

// A tune lowers the power and only then marks the radio as tuning. A power change arriving in between must not
// put the power back up for the carrier.
func TestNoSettingChangesOnceATuneHasStarted(t *testing.T) {
	r := newSimRig(t)
	simConfig(t, r, true)
	_, l, _ := rigByID(loadCfg(), "ftdx10")
	l.tuneMu.Lock() // a tune has started; its "tuning" flag is not set yet
	defer l.tuneMu.Unlock()
	p := 1.0
	if err := (webBackend{}).RigSet("ftdx10", web.RigSetting{Power: &p}); err == nil || !strings.Contains(err.Error(), "tuning") {
		t.Fatalf("a power change during a tune: %v", err)
	}
	for _, c := range r.commands() {
		if strings.HasPrefix(c, "L RFPOWER") {
			t.Fatalf("the power was changed during a tune: %v", r.commands())
		}
	}
}

func TestSwitchingModemsChangesTheRigAndStartsTheNewOne(t *testing.T) {
	r := newSimRig(t)
	simConfig(t, r, false)
	cfg := loadCfg()
	rig, _ := cfg.RigByID("ftdx10")
	rig.Modem, rig.Alt, rig.ArqPort = config.Mercury, []string{config.Ardopcf}, config.ArqPortBase
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	var started []string
	old := startModemUnit
	startModemUnit = func(unit string) error { started = append(started, unit); return nil }
	defer func() { startModemUnit = old }()
	oldMissing := modemMissing
	modemMissing = func(kind string) string {
		if kind == config.Xara {
			return xaraWhere
		}
		return ""
	}
	defer func() { modemMissing = oldMissing }()

	// A modem that is not installed is refused before anything changes: the rig keeps the one it runs.
	cfg = loadCfg()
	rig, _ = cfg.RigByID("ftdx10")
	rig.Alt = []string{config.Ardopcf, config.Xara}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := switchRigModem("ftdx10", config.Xara); err == nil || !strings.Contains(err.Error(), "keeps mercury") || !strings.Contains(err.Error(), "NR5P/xara") {
		t.Fatalf("switching to a modem that is not installed: %v", err)
	}
	if c := loadCfg(); func() bool { g, _ := c.RigByID("ftdx10"); return g.Modem != config.Mercury }() || len(started) > 0 {
		t.Fatal("a refused switch must change nothing")
	}

	r.mu.Lock()
	r.txUntil = time.Now().Add(time.Minute)
	r.mu.Unlock()
	if _, err := switchRigModem("ftdx10", config.Ardopcf); err == nil || !strings.Contains(err.Error(), "transmitting") {
		t.Fatalf("a switch while the radio transmits would cut the over: %v", err)
	}
	if c := loadCfg(); func() bool { g, _ := c.RigByID("ftdx10"); return g.Modem != config.Mercury }() || len(started) > 0 {
		t.Fatal("nothing may change while it transmits")
	}
	r.mu.Lock()
	r.txUntil = time.Time{}
	r.mu.Unlock()
	if _, err := switchRigModem("ftdx10", config.Ardopcf); err != nil {
		t.Fatal(err)
	}
	after := loadCfg()
	got, _ := after.RigByID("ftdx10")
	if got.Modem != config.Ardopcf || len(got.Alt) != 2 || got.Alt[0] != config.Mercury {
		t.Fatalf("after the switch: %q %v", got.Modem, got.Alt)
	}
	if len(started) != 1 || started[0] != "hamstack-ardopcf@ftdx10.service" {
		t.Fatalf("the new modem must be started: %v", started)
	}
}
