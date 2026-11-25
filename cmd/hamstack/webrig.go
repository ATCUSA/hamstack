package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/rigctl"
	"github.com/ATCUSA/hamstack/internal/svc"
	"github.com/ATCUSA/hamstack/internal/units"
	"github.com/ATCUSA/hamstack/internal/web"
)

// The radio panel's backend: the dashboard talks to each rig through its rigctld, the one program that owns the
// radio's CAT port, so it can sit beside Mercury, Direwolf and the rest without taking the port from them.

const (
	rigCacheTTL = 700 * time.Millisecond // browsers asking more often than this share one reading
	tunePercent = 10                     // the power a tuning cycle runs at, in percent of full power
)

// Variables so a test need not wait out the real limits.
var (
	tuneMaxTime = 25 * time.Second // a tuning cycle that has not ended by now is stopped
	tuneNoTX    = 8 * time.Second  // how long to wait for a radio that never says it is transmitting
)

// rigLink is the dashboard's connection to one rig's rigctld.
type rigLink struct {
	c      *rigctl.Client
	tuneMu sync.Mutex // one tuning cycle at a time

	mu     sync.Mutex // guards what follows, and is held while the radio is read
	last   web.RigState
	at     time.Time
	n      int
	tuning bool
	slow   map[string]time.Time // optional reads paused until then (see slowRead)
}

var rigLinks = struct {
	sync.Mutex
	m map[string]*rigLink
}{m: map[string]*rigLink{}}

func rigLinkFor(r config.Rig) *rigLink {
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(r.RigPort))
	rigLinks.Lock()
	defer rigLinks.Unlock()
	if l := rigLinks.m[r.ID]; l != nil && l.c.Addr == addr {
		return l
	}
	l := &rigLink{c: rigctl.New(addr)}
	rigLinks.m[r.ID] = l
	return l
}

// panelRigs are the rigs the panel can show: switched on, and with CAT control.
func panelRigs(cfg config.Config) []config.Rig {
	var out []config.Rig
	for _, r := range cfg.ActiveRigs() {
		if !r.NoCAT && r.RigPort > 0 {
			out = append(out, r)
		}
	}
	return out
}

func rigByID(cfg config.Config, id string) (config.Rig, *rigLink, error) {
	for _, r := range panelRigs(cfg) {
		if r.ID == id {
			return r, rigLinkFor(r), nil
		}
	}
	return config.Rig{}, nil, fmt.Errorf("no rig %q with radio control", id)
}

func rigLabel(r config.Rig) string {
	if r.Name != "" {
		return r.Name
	}
	return r.ID
}

// rigProblem says in words why a rig could not be read.
func rigProblem(err error) string {
	var ne net.Error
	var oe *net.OpError
	switch {
	case errors.As(err, &oe) && oe.Op == "dial":
		return "rig control (rigctld) is not running, or the radio is not plugged in"
	case errors.As(err, &ne) && ne.Timeout():
		return "the radio did not answer"
	case rigctl.Unsupported(err):
		return "the radio refused: " + err.Error()
	}
	return err.Error()
}

func (webBackend) RigStates() []web.RigState {
	cfg := loadCfg()
	allow := cfg.Web != nil && cfg.Web.RigTune
	var out []web.RigState
	for _, r := range panelRigs(cfg) {
		st := rigLinkFor(r).snapshot(r, allow)
		st.Services = rigServices(r)
		if !st.Online {
			st.Error = offlineReason(st.Error, st.Services)
		}
		out = append(out, st)
	}
	return out
}

// rigServices are the services of a rig that a person may need to start or stop: its rig control, and its modem.
func rigServices(r config.Rig) []web.RigService {
	list := []units.Service{{Kind: config.RigCtld, Rig: r.ID}}
	if r.Modem != "" {
		list = append(list, units.Service{Kind: r.Modem, Rig: r.ID})
	}
	var out []web.RigService
	for _, s := range list {
		st := svc.Status(s.Unit())
		out = append(out, web.RigService{Name: s.Name(), What: serviceWhat(s), State: webState(st), Enabled: st.Enabled == "enabled"})
	}
	return out
}

// offlineReason says in words why a rig is offline, from what its rig control is doing: a stopped service and
// an unplugged radio need different things from the person looking at the card.
func offlineReason(tried string, services []web.RigService) string {
	if len(services) == 0 {
		return tried
	}
	switch services[0].State {
	case "stopped":
		return "Rig control is stopped. Start it to read and control the radio."
	case "failed":
		return "Rig control stopped with an error: look at its log, then restart it."
	case "no radio":
		return "Rig control is waiting for the radio, which is not found on USB. Check its cable and power, then restart rig control."
	case "not installed":
		return "Rig control is not installed (hamstack install hamlib)."
	case "starting":
		return "Rig control is starting…"
	}
	return tried
}

// snapshot reads the radio, unless it was read a moment ago. Only what a person can see is read, and the
// transmit meters only while the radio transmits: the CAT port is slow and Mercury is using it too.
func (l *rigLink) snapshot(r config.Rig, allowTune bool) web.RigState {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.tuning { // the tuning cycle keeps this up to date, and nothing else may talk to the radio meanwhile
		st := l.last
		st.TuneOn, st.Tuning = allowTune, true
		return st
	}
	if !l.at.IsZero() && time.Since(l.at) < rigCacheTTL {
		st := l.last
		st.TuneOn = allowTune
		return st
	}
	prev := l.last
	st := web.RigState{ID: r.ID, Name: rigLabel(r), TuneOn: allowTune, TunePower: tunePercent, PowerMax: 1}
	l.at = time.Now()
	hz, err := l.c.Freq()
	if err != nil {
		st.Error = rigProblem(err)
		l.last = st
		return st
	}
	st.Online, st.Freq = true, hz
	st.Mode, st.Width, _ = l.c.Mode()
	caps, cerr := l.c.Capabilities()
	if cerr == nil {
		st.Modes, st.CanTune = caps.Modes, caps.VFOOps["TUNE"]
		st.PowerMin, st.PowerMax = caps.RFPowerMin, caps.RFPowerMax
		st.FullW = caps.FullWatts
		st.Meters = meterKeys(caps)
	}
	if ptt, err := l.c.PTT(); err == nil {
		st.PTT = ptt
	}
	level := func(name string) *float64 {
		if cerr != nil || !caps.GetLevels[name] || l.paused(name) {
			return nil
		}
		t0 := time.Now()
		v, err := l.c.Level(name)
		l.slowRead(name, time.Since(t0))
		if err == nil {
			return &v
		}
		return nil
	}
	st.Power = level("RFPOWER")
	if st.PTT {
		st.SWR, st.ALC, st.Comp, st.PowerW = level("SWR"), level("ALC"), level("COMP_METER"), level("RFPOWER_METER_WATTS")
		st.Current = level("ID_METER")
	} else {
		st.Strength = level("STRENGTH")
	}
	// The supply voltage means something at rest, so it is read then too, but only now and then: it changes slowly.
	if st.PTT || l.n%5 == 0 {
		st.Voltage = level("VD_METER")
	} else {
		st.Voltage = prev.Voltage
	}
	if l.n%5 == 0 && cerr == nil && caps.GetFuncs["TUNER"] && !l.paused("func TUNER") { // changes rarely: read it now and then
		t0 := time.Now()
		on, err := l.c.Func("TUNER")
		l.slowRead("func TUNER", time.Since(t0))
		if err == nil {
			st.Tuner = &on
		}
	} else {
		st.Tuner = prev.Tuner
	}
	l.n++
	l.last = st
	return st
}

// A read the radio is slow to answer holds rigctld's lock for the whole time, so every program sharing the radio (Pat,
// the modem, other dashboards) stalls behind it. On an FT-DX10, reading the COMP function takes about 2.4 s. The
// readings the panel cannot do without (frequency, mode, PTT) are never paused; any optional one that takes longer
// than slowRead is skipped for pausedFor, and the panel shows its last value.
const (
	slowReadLimit = 800 * time.Millisecond
	pausedFor     = 10 * time.Minute
)

// slowRead records how long an optional read took, and pauses it if it was slow. l.mu is held by the caller.
func (l *rigLink) slowRead(name string, took time.Duration) {
	if took <= slowReadLimit {
		return
	}
	if l.slow == nil {
		l.slow = map[string]time.Time{}
	}
	l.slow[name] = time.Now().Add(pausedFor)
}

func (l *rigLink) paused(name string) bool {
	until, ok := l.slow[name]
	return ok && time.Now().Before(until)
}

// meterKeys lists the meters a radio has, from what hamlib says it can read, in the order the card shows them.
func meterKeys(caps rigctl.Caps) []string {
	var out []string
	for _, m := range []struct{ level, key string }{
		{"STRENGTH", "s"}, {"RFPOWER_METER_WATTS", "p"}, {"SWR", "swr"}, {"ALC", "alc"},
		{"COMP_METER", "c"}, {"ID_METER", "id"}, {"VD_METER", "vd"},
	} {
		if caps.GetLevels[m.level] {
			out = append(out, m.key)
		}
	}
	return out
}

func (l *rigLink) forget() {
	l.mu.Lock()
	l.at = time.Time{}
	l.mu.Unlock()
}

func (webBackend) RigSet(id string, set web.RigSetting) error {
	cfg := loadCfg()
	_, l, err := rigByID(cfg, id)
	if err != nil {
		return err
	}
	// The tune lock, not only the "tuning" flag: a tune lowers the power before it sets the flag, and a power change
	// in between would put the power back up for the carrier. Holding it also keeps a tune from starting mid-change.
	if !l.tuneMu.TryLock() {
		return fmt.Errorf("the radio is tuning: wait until it finishes")
	}
	defer l.tuneMu.Unlock()
	caps, err := l.c.Capabilities()
	if err != nil {
		return fmt.Errorf("%s", rigProblem(err))
	}
	st := web.RigState{Modes: caps.Modes, PowerMin: caps.RFPowerMin, PowerMax: caps.RFPowerMax}
	ptt, err := l.c.PTT()
	if err != nil {
		return fmt.Errorf("cannot tell whether the radio is transmitting, so nothing was changed: %s", rigProblem(err))
	}
	st.PTT = ptt
	if err := web.ValidateRigSetting(set, st); err != nil {
		return err
	}
	defer l.forget()
	// The frequency first and then the mode: a radio that remembers a mode for each band would otherwise
	// put its own back over the one asked for.
	if set.Freq != nil {
		if err := l.c.SetFreq(*set.Freq); err != nil {
			return fmt.Errorf("frequency: %s", rigProblem(err))
		}
	}
	if set.Mode != "" {
		if err := l.c.SetMode(set.Mode, 0); err != nil {
			return fmt.Errorf("mode: %s", rigProblem(err))
		}
	}
	if set.Power != nil {
		if err := l.c.SetLevel("RFPOWER", *set.Power); err != nil {
			return fmt.Errorf("power: %s", rigProblem(err))
		}
	}
	if set.Tuner != nil {
		if err := l.c.SetFunc("TUNER", *set.Tuner); err != nil {
			return fmt.Errorf("tuner: %s", rigProblem(err))
		}
	}
	return nil
}

func (l *rigLink) setTuning(on bool) {
	l.mu.Lock()
	l.tuning = on
	if !on {
		l.at = time.Time{}
	}
	l.mu.Unlock()
}

// note keeps the panel's meters moving while a tuning cycle owns the radio.
func (l *rigLink) note(ptt bool, swr float64) {
	l.mu.Lock()
	l.last.PTT = ptt
	if swr > 0 {
		l.last.SWR = &swr
	}
	l.mu.Unlock()
}

// RigTune runs the radio's antenna tuner. It transmits a carrier, so it is off unless the owner turned it on
// (hamstack dashboard tune on), and it refuses unless it can see the radio is not already transmitting and can turn
// the power down first. The power is put back whatever happens.
func (webBackend) RigTune(ctx context.Context, id string) (web.TuneResult, error) {
	var res web.TuneResult
	cfg := loadCfg()
	if cfg.Web == nil || !cfg.Web.RigTune {
		return res, fmt.Errorf("tuning from the browser is off: turn it on with 'hamstack dashboard tune on'")
	}
	_, l, err := rigByID(cfg, id)
	if err != nil {
		return res, err
	}
	if !l.tuneMu.TryLock() {
		return res, fmt.Errorf("the radio is already tuning, or a setting is being changed: try again in a moment")
	}
	defer l.tuneMu.Unlock()
	caps, err := l.c.Capabilities()
	if err != nil {
		return res, fmt.Errorf("%s", rigProblem(err))
	}
	if !caps.VFOOps["TUNE"] {
		return res, fmt.Errorf("this radio cannot start its tuner through rigctld")
	}
	ptt, err := l.c.PTT()
	if err != nil {
		return res, fmt.Errorf("cannot tell whether the radio is transmitting, so it was not tuned: %s", rigProblem(err))
	}
	if ptt {
		return res, fmt.Errorf("the radio is transmitting: not tuning")
	}
	cur, err := l.c.Level("RFPOWER")
	if err != nil {
		return res, fmt.Errorf("cannot read the power setting, so it was not tuned: %s", rigProblem(err))
	}
	want := min(max(float64(tunePercent)/100, caps.RFPowerMin), caps.RFPowerMax)
	lowered := false
	if cur > want+0.005 {
		if err := l.c.SetLevel("RFPOWER", want); err != nil {
			return res, fmt.Errorf("cannot lower the power for tuning, so it was not tuned: %s", rigProblem(err))
		}
		lowered = true
	}
	l.setTuning(true)
	defer l.setTuning(false)
	restore := func() {
		if !lowered {
			return
		}
		for range 3 {
			if l.c.SetLevel("RFPOWER", cur) == nil {
				return
			}
			time.Sleep(300 * time.Millisecond)
		}
		fmt.Fprintf(os.Stderr, "hamstack: rig %s: could not put the power back to %.0f%%: set it on the radio\n", id, cur*100)
	}
	defer restore()
	stop := func() { _ = l.c.Abort(); _ = l.c.SetFunc("TUNER", false) }

	if caps.SetFuncs["TUNER"] {
		_ = l.c.SetFunc("TUNER", true) // a tuner that is bypassed has nothing to tune
	}
	// A radio reports an SWR of 1 (and some watts) even when it is not transmitting, so a reading means nothing
	// by itself: the resting one is taken first, and only a reading above it counts as the carrier being on.
	var rest float64
	if caps.GetLevels["SWR"] {
		rest, _ = l.c.Level("SWR")
	}
	if err := l.c.VFOOp("TUNE"); err != nil {
		return res, fmt.Errorf("the radio would not start tuning: %s", rigProblem(err))
	}
	start := time.Now()
	sawTX, quiet := false, 0
	var worst, final float64 // worst: the peak while the tuner searched; final: the last reading, where it settled
	for {
		select {
		case <-ctx.Done():
			stop()
			return res, fmt.Errorf("tuning was cancelled and the carrier dropped")
		case <-time.After(400 * time.Millisecond):
		}
		el := time.Since(start)
		ptt, perr := l.c.PTT()
		var swr float64
		if caps.GetLevels["SWR"] {
			swr, _ = l.c.Level("SWR")
		}
		// The radio's own word is the evidence. A radio that does not report its transmit state is judged by its
		// ratio rising above the resting one.
		tx := (perr == nil && ptt) || (swr > rest+0.05)
		if tx {
			sawTX, quiet = true, 0
			worst = max(worst, swr)
			if swr > 0 {
				final = swr
			}
		} else if perr == nil {
			quiet++
		}
		if !tx {
			swr = 0
		}
		l.note(tx, swr)
		switch {
		case sawTX && quiet >= 2:
			return finishTune(l, caps, final, worst, true), nil
		case !sawTX && el >= tuneNoTX:
			return finishTune(l, caps, final, worst, false), nil
		case el >= tuneMaxTime:
			stop()
			return res, fmt.Errorf("the radio was still transmitting after %s: the carrier was dropped and the tuner switched off", tuneMaxTime)
		}
	}
}

// finishTune words the result by where the tuner settled (the final reading), not the peak: the SWR is always high
// while an antenna tuner searches, and says nothing about the result. The peak is mentioned only as context.
func finishTune(l *rigLink, caps rigctl.Caps, final, peak float64, sawTX bool) web.TuneResult {
	res := web.TuneResult{}
	if final > 0 {
		res.SWR = &final
	}
	ctx := ""
	if peak >= final*1.5 && peak-final >= 0.5 {
		ctx = fmt.Sprintf(" (it peaked at %.1f while the tuner searched, which is normal)", peak)
	}
	switch {
	case !sawTX:
		res.Message = "The radio never reported transmitting, so hamstack cannot say whether it tuned: look at the radio's TUNE indicator."
	case final <= 0:
		res.Message = "Tuning finished (the radio gave no SWR reading)."
	case final >= 3:
		res.Message = fmt.Sprintf("Tuning finished, but the SWR it settled at is %.1f%s: the tuner could not find a good match. Check the antenna and cable before transmitting.", final, ctx)
	case final >= 2:
		res.Message = fmt.Sprintf("Tuned: the SWR settled at %.1f%s. That works, but a lower SWR would be better.", final, ctx)
	default:
		res.Message = fmt.Sprintf("Tuned: the SWR settled at %.1f%s.", final, ctx)
	}
	if caps.GetFuncs["TUNER"] {
		if on, err := l.c.Func("TUNER"); err == nil && !on {
			res.Message += " The radio's tuner is now off."
		}
	}
	return res
}

// RigAbort drops the carrier, from another connection so it works while a tuning cycle is running.
func (webBackend) RigAbort(id string) error {
	_, l, err := rigByID(loadCfg(), id)
	if err != nil {
		return err
	}
	if err := l.c.Abort(); err != nil {
		return fmt.Errorf("%s", rigProblem(err))
	}
	l.mu.Lock()
	tuning := l.tuning
	l.mu.Unlock()
	if tuning {
		_ = l.c.SetFunc("TUNER", false) // what stops a cycle the carrier-off did not
	}
	return nil
}

// webTune is "hamstack dashboard tune on|off": whether the dashboard may key the transmitter to run the radio's tuner.
func webTune(args []string) error {
	if len(args) != 1 || (args[0] != "on" && args[0] != "off") {
		return fmt.Errorf("usage: hamstack dashboard tune on|off (on lets logged-in dashboard users run the radio's antenna tuner, which transmits for a few seconds)")
	}
	if args[0] == "on" && !(&asker{in: bufioStdin(), out: os.Stdout}).confirm(
		fmt.Sprintf("Allow logged-in dashboard users to key your transmitter briefly (a tuner cycle at about %d%% power) from the browser?", tunePercent), false) {
		return fmt.Errorf("cancelled")
	}
	msg, err := mutateOnly(func(c *config.Config) (string, error) {
		if c.Web == nil {
			return "", fmt.Errorf("the dashboard is not set up: hamstack dashboard on")
		}
		c.Web.RigTune = args[0] == "on"
		return "Browser tuning is " + args[0], nil
	})
	if msg != "" {
		fmt.Println(msg)
	}
	return err
}
