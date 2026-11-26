package main

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/rigctl"
)

// agcRadio is an FT-DX10 as far as the band watch needs: PTT, frequency and AGC kept per band, and the profile's other
// settings answered as they were on the Pi.
type agcRadio struct {
	mu   sync.Mutex
	freq string
	agc  map[string]string // band -> AGC
	ptt  string
	sets []string
}

func (a *agcRadio) tune(f string) { a.mu.Lock(); a.freq = f; a.mu.Unlock() }

func (a *agcRadio) state() (map[string]string, []string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	agc := map[string]string{}
	for k, v := range a.agc {
		agc[k] = v
	}
	return agc, append([]string(nil), a.sets...)
}

func (a *agcRadio) Do(cmd string) ([]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p := strings.Fields(cmd)
	band := bandOf(func() int64 { var n int64; fmt.Sscan(a.freq, &n); return n }())
	switch p[0] {
	case "t":
		return []string{a.ptt}, nil
	case "f":
		return []string{a.freq}, nil
	case "m":
		return []string{"PKTUSB", "2400"}, nil
	case "l":
		if p[1] == "AGC" {
			return []string{a.agc[band]}, nil
		}
		return []string{"0.5"}, nil
	case "L":
		a.sets = append(a.sets, cmd+" on "+band)
		a.agc[band] = p[2]
		return nil, nil
	case "u":
		return []string{"0"}, nil
	case "w":
		c := strings.TrimSuffix(p[1], ";")
		return []string{c + map[string]string{"EX010415": "1", "EX010416": "1", "EX010417": "050", "EX010408": "1500", "EX010413": "050", "EX010414": "1"}[c] + ";"}, nil
	}
	return nil, fmt.Errorf("RPRT -1")
}

func TestBandChangeFixesAGCOnTheNewBand(t *testing.T) {
	if _, _, ok := radioProfileForUse(config.Rig{ID: "hf", Profile: "ftdx10", Modem: config.Xara, RigPort: 4533}, config.Xara); !ok {
		t.Skip("the ftdx10 profile is not found here")
	}
	rig := config.Rig{ID: "hf", Profile: "ftdx10", Modem: config.Xara, RigPort: 4533}
	r := &agcRadio{freq: "7102800", agc: map[string]string{"40m": "6", "80m": "0"}, ptt: "0"}
	var out strings.Builder
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() { watchBands(rig, config.Xara, r, &out, 5*time.Millisecond, stop); close(done) }()
	time.Sleep(30 * time.Millisecond)
	r.tune("3585000") // to 80 m, where AGC is OFF
	time.Sleep(60 * time.Millisecond)
	close(stop)
	<-done
	agc, sets := r.state()
	if agc["80m"] != "6" || len(sets) != 1 || !strings.Contains(out.String(), "AGC was OFF") {
		t.Fatalf("AGC on 80 m: %v; sets %v; said %q", agc, sets, out.String())
	}
	if agc["40m"] != "6" {
		t.Error("40 m was already right and must not be touched")
	}
}

func TestBandWatchLeavesATransmittingRadioAlone(t *testing.T) {
	rig := config.Rig{ID: "hf", Profile: "ftdx10", Modem: config.Xara, RigPort: 4533}
	r := &agcRadio{freq: "7102800", agc: map[string]string{"40m": "6", "80m": "0"}, ptt: "1"}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() { watchBands(rig, config.Xara, r, &strings.Builder{}, 5*time.Millisecond, stop); close(done) }()
	time.Sleep(20 * time.Millisecond)
	r.tune("3585000")
	time.Sleep(40 * time.Millisecond)
	close(stop)
	<-done
	if _, sets := r.state(); len(sets) != 0 {
		t.Fatalf("nothing is set while the radio transmits: %v", sets)
	}
}

func TestProfilePerProgram(t *testing.T) {
	rig := config.Rig{ID: "hf", Profile: "ftdx10", Modem: config.Mercury, RigPort: 4533}
	if radioModel(rig) == 0 {
		t.Skip("the ftdx10 profile is not found here")
	}
	id := func(r config.Rig, use string) string {
		p, _, ok := radioProfileForUse(r, use)
		if !ok {
			return "-"
		}
		return p.ID
	}
	if got := id(rig, config.Mercury); got != "yaesu-ftdx10/winlink-vara" {
		t.Errorf("Mercury gets the VARA profile by default: %s", got)
	}
	if _, note, _ := radioProfileForUse(rig, config.Mercury); note == "" {
		t.Error("Mercury's profile must say it is only the closest")
	}
	if got := id(rig, "wsjtx"); got != "yaesu-ftdx10/ft8" {
		t.Errorf("WSJT-X: %s", got)
	}
	rig.RadioProfiles = map[string]string{"wsjtx": "yaesu-ftdx10/psk31", config.Mercury: "none", "fldigi": "icom-ic7300/psk31"}
	for use, want := range map[string]string{"wsjtx": "yaesu-ftdx10/psk31", config.Mercury: "-", "fldigi": "yaesu-ftdx10/psk31", config.Direwolf: "-"} {
		if got := id(rig, use); got != want {
			t.Errorf("%s: %s, want %s (a pick for another radio falls back to the recommended one)", use, got, want)
		}
	}
	if got := id(config.Rig{ID: "x", Profile: "ftdx10", NoCAT: true}, "wsjtx"); got != "-" {
		t.Errorf("no rig control, no check: %s", got)
	}
}

func TestTheProgramWithTheRadioChoosesTheProfile(t *testing.T) {
	old := radioAppNow
	defer func() { radioAppNow = old }()
	rig := config.Rig{ID: "hf", Profile: "ftdx10", Modem: config.Data2G}
	radioAppNow = func() string { return "" }
	if got := rigUseNow(rig); got != config.Data2G {
		t.Errorf("the modem has the radio: %q", got)
	}
	radioAppNow = func() string { return "wsjtx" }
	if got := rigUseNow(rig); got != "wsjtx" {
		t.Errorf("WSJT-X is open, so it has the radio (hamstack stopped the modem for it): %q", got)
	}
	radioAppNow = func() string { return "" }
	if got := rigUseNow(config.Rig{ID: "p", Modem: config.Direwolf}); got != "" {
		t.Errorf("Direwolf has no profile: %q", got)
	}
}

// Against a rigctld speaking the extended protocol as Hamlib 4.7.2 did on the Pi: labelled values, and a raw command's
// RPRT on the same line as its reply.
func TestBareRadioReadsRealRigctldReplies(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	replies := map[string]string{
		"+t":           "get_ptt:\nPTT: 0\nRPRT 0\n",
		"+f":           "get_freq:\nFrequency: 7102800\nRPRT 0\n",
		"+m":           "get_mode:\nMode: PKTUSB\nPassband: 2400\nRPRT 0\n",
		"+l AGC":       "get_level: AGC\n6\nRPRT 0\n",
		"+w EX010417;": "send_cmd: EX010417;\nReply: EX010417050;RPRT 0\n",
	}
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		r := bufio.NewReader(c)
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			_, _ = c.Write([]byte(replies[strings.TrimSpace(line)]))
		}
	}()
	r := bareRadio{rigctl.New(l.Addr().String())}
	for cmd, want := range map[string]string{"t": "0", "f": "7102800", "m": "PKTUSB", "l AGC": "6", "w EX010417;": "EX010417050;"} {
		got, err := r.Do(cmd)
		if err != nil || len(got) == 0 || got[0] != want {
			t.Errorf("%s: %q %v, want %q first", cmd, got, err, want)
		}
	}
}

// A band change before the watch's first look still counts: on the Pi the radio went to 80 m four seconds after the
// modem started, and the first look 15 seconds in took 80 m for the starting band.
func TestBandChangeSoonAfterStartCounts(t *testing.T) {
	if _, _, ok := radioProfileForUse(config.Rig{ID: "hf", Profile: "ftdx10", Modem: config.Xara, RigPort: 4533}, config.Xara); !ok {
		t.Skip("the ftdx10 profile is not found here")
	}
	rig := config.Rig{ID: "hf", Profile: "ftdx10", Modem: config.Xara, RigPort: 4533}
	r := &agcRadio{freq: "7102800", agc: map[string]string{"40m": "6", "80m": "0"}, ptt: "0"}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() { watchBands(rig, config.Xara, r, &strings.Builder{}, 40*time.Millisecond, stop); close(done) }()
	time.Sleep(5 * time.Millisecond)
	r.tune("3585000") // before the first look
	time.Sleep(100 * time.Millisecond)
	close(stop)
	<-done
	if agc, _ := r.state(); agc["80m"] != "6" {
		t.Fatalf("AGC on 80 m: %v", agc)
	}
}

// A band change while the start-up check runs still counts: the watch starts from the band read before the check, so
// a radio already on another band when the watch begins is fixed there (it was taken for the starting band before).
func TestBandChangeDuringTheStartCheckCounts(t *testing.T) {
	rig := config.Rig{ID: "hf", Profile: "ftdx10", Modem: config.Xara, RigPort: 4533}
	if radioModel(rig) == 0 {
		t.Skip("the ftdx10 profile is not found here")
	}
	r := &agcRadio{freq: "7102800", agc: map[string]string{"20m": "6", "40m": "0"}, ptt: "0"} // on 40 m already
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		watchBandsFrom(rig, config.Xara, r, &strings.Builder{}, 5*time.Millisecond, stop, "20m")
		close(done)
	}()
	deadline := time.Now().Add(time.Second)
	for agc, _ := r.state(); agc["40m"] != "6" && time.Now().Before(deadline); agc, _ = r.state() {
		time.Sleep(5 * time.Millisecond)
	}
	close(stop)
	<-done
	if agc, _ := r.state(); agc["40m"] != "6" {
		t.Fatalf("AGC on 40 m: %v", agc)
	}
}
