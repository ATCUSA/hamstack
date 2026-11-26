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
	if _, _, ok := radioProfileFor(config.Rig{ID: "hf", Profile: "ftdx10", Modem: config.Xara, RigPort: 4533}); !ok {
		t.Skip("the ftdx10 profile is not found here")
	}
	rig := config.Rig{ID: "hf", Profile: "ftdx10", Modem: config.Xara, RigPort: 4533}
	r := &agcRadio{freq: "7102800", agc: map[string]string{"40m": "6", "80m": "0"}, ptt: "0"}
	var out strings.Builder
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() { watchBands(rig, r, &out, 5*time.Millisecond, stop); close(done) }()
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
	go func() { watchBands(rig, r, &strings.Builder{}, 5*time.Millisecond, stop); close(done) }()
	time.Sleep(20 * time.Millisecond)
	r.tune("3585000")
	time.Sleep(40 * time.Millisecond)
	close(stop)
	<-done
	if _, sets := r.state(); len(sets) != 0 {
		t.Fatalf("nothing is set while the radio transmits: %v", sets)
	}
}

func TestRigOperation(t *testing.T) {
	for m, want := range map[string]string{config.Ardopcf: "winlink-ardop", config.Xara: "winlink-vara", config.Mercury: "winlink-vara", config.Direwolf: ""} {
		if op, _ := rigOperation(m); op != want {
			t.Errorf("%s: %q", m, op)
		}
	}
	if _, note := rigOperation(config.Mercury); note == "" {
		t.Error("Mercury gets the VARA profile, and must say it is only the closest")
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
