package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// rigFake records what the panel asked of the radio. The methods are on fakeBackend so the ordinary test
// server has radio control; each test starts it afresh.
var rigFake struct {
	sets   []string
	tunes  []string
	aborts []string
	ctxErr bool
}

func (f *fakeBackend) RigStates() []RigState {
	swr := 1.4
	p := 0.2
	return []RigState{{ID: "ftdx10", Name: "FT-DX10", Online: true, Freq: 14074000, Mode: "PKTUSB", Modes: []string{"USB", "PKTUSB"},
		Power: &p, PowerMin: 0.05, PowerMax: 1, FullW: 100, SWR: &swr, CanTune: true, TunePower: 10}}
}
func (f *fakeBackend) RigSet(id string, s RigSetting) error {
	if id != "ftdx10" {
		return fmt.Errorf("no rig %q with radio control", id)
	}
	b, _ := json.Marshal(s)
	rigFake.sets = append(rigFake.sets, id+" "+string(b))
	return ValidateRigSetting(s, f.RigStates()[0])
}
func (f *fakeBackend) RigTune(ctx context.Context, id string) (TuneResult, error) {
	rigFake.tunes = append(rigFake.tunes, id)
	rigFake.ctxErr = ctx.Err() != nil
	swr := 1.3
	return TuneResult{Message: "Tuned: the highest SWR was 1.3.", SWR: &swr}, nil
}
func (f *fakeBackend) RigAbort(id string) error {
	rigFake.aborts = append(rigFake.aborts, id)
	return nil
}

func rigRequest(t *testing.T, ts interface{ Client() *http.Client }, c *http.Client, method, url, origin, body string, hdr bool) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if hdr {
		req.Header.Set("X-Hamstack", "1")
	}
	req.Header.Set("Content-Type", "application/json")
	r, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	return r.StatusCode, string(b)
}

func TestRigPanelEndpoints(t *testing.T) {
	rigFake.sets, rigFake.tunes, rigFake.aborts = nil, nil, nil
	ts, c := setup(t, &fakeBackend{})

	if code, _ := rigRequest(t, ts, c, "GET", ts.URL+"/api/rigs", "", "", false); code != http.StatusUnauthorized {
		t.Errorf("rigs without a login: %d", code)
	}
	for _, p := range []string{"/api/rig/ftdx10/set", "/api/rig/ftdx10/tune", "/api/rig/ftdx10/abort"} {
		if code, _ := rigRequest(t, ts, c, "POST", ts.URL+p, ts.URL, "{}", true); code != http.StatusUnauthorized {
			t.Errorf("%s without a login: %d", p, code)
		}
	}
	login(t, ts, c, "secret-pass")

	code, body := rigRequest(t, ts, c, "GET", ts.URL+"/api/rigs", "", "", false)
	var states []RigState
	if code != 200 || json.Unmarshal([]byte(body), &states) != nil || len(states) != 1 || states[0].Freq != 14074000 || states[0].SWR == nil {
		t.Fatalf("rigs: %d %s", code, body)
	}

	// Anything that changes the radio needs the dashboard's own header and the same origin.
	for name, tc := range map[string]struct {
		origin string
		hdr    bool
	}{"no header": {ts.URL, false}, "other site": {"https://evil.example", true}} {
		for _, p := range []string{"/api/rig/ftdx10/set", "/api/rig/ftdx10/tune", "/api/rig/ftdx10/abort"} {
			if code, _ := rigRequest(t, ts, c, "POST", ts.URL+p, tc.origin, `{"freq":7074000}`, tc.hdr); code != http.StatusForbidden {
				t.Errorf("%s, %s: %d, want 403", name, p, code)
			}
		}
	}
	if len(rigFake.sets)+len(rigFake.tunes)+len(rigFake.aborts) != 0 {
		t.Fatalf("a refused request reached the radio: %+v", rigFake)
	}

	if code, body := rigRequest(t, ts, c, "POST", ts.URL+"/api/rig/ftdx10/set", ts.URL, `{"freq":7074000,"mode":"PKTUSB"}`, true); code != 200 {
		t.Errorf("set: %d %s", code, body)
	}
	// What the radio cannot do is refused before it reaches the radio, with the reason.
	for body, want := range map[string]string{
		`{"mode":"AM"}`:       "has no",
		`{"power":1.5}`:       "power must be between",
		`{"freq":5}`:          "not a frequency",
		`{}`:                  "nothing to change",
		`{"freq":1,"x":true}`: "bad request",
	} {
		code, got := rigRequest(t, ts, c, "POST", ts.URL+"/api/rig/ftdx10/set", ts.URL, body, true)
		if code < 400 || !strings.Contains(got, want) {
			t.Errorf("set %s: %d %s, want an error saying %q", body, code, got, want)
		}
	}
	if code, body := rigRequest(t, ts, c, "POST", ts.URL+"/api/rig/nope/set", ts.URL, `{"freq":7074000}`, true); code != http.StatusBadGateway || !strings.Contains(body, "no rig") {
		t.Errorf("unknown rig: %d %s", code, body)
	}

	if code, body := rigRequest(t, ts, c, "POST", ts.URL+"/api/rig/ftdx10/tune", ts.URL, "", true); code != 200 || !strings.Contains(body, "Tuned") {
		t.Errorf("tune: %d %s", code, body)
	}
	if rigFake.ctxErr {
		t.Error("the tuning cycle must run under the request's context, which is alive while it runs")
	}
	if code, _ := rigRequest(t, ts, c, "POST", ts.URL+"/api/rig/ftdx10/abort", ts.URL, "", true); code != 200 || len(rigFake.aborts) != 1 {
		t.Errorf("abort: %d %v", code, rigFake.aborts)
	}

	if _, page := rigRequest(t, ts, c, "GET", ts.URL+"/", "", "", false); !strings.Contains(page, `id="rig-section"`) || !strings.Contains(page, "/api/rigs") {
		t.Error("the dashboard must carry the radio panel")
	}
}

func TestValidateRigSetting(t *testing.T) {
	st := RigState{Modes: []string{"USB", "PKTUSB"}, PowerMin: 0.05, PowerMax: 1}
	f := func(v int64) *int64 { return &v }
	p := func(v float64) *float64 { return &v }
	ok := []RigSetting{{Freq: f(14074000)}, {Mode: "USB"}, {Power: p(0.05)}, {Power: p(1)}, {Freq: f(7074000), Mode: "PKTUSB", Power: p(0.2)}, {Tuner: new(bool)}}
	for _, s := range ok {
		if err := ValidateRigSetting(s, st); err != nil {
			t.Errorf("%+v: %v", s, err)
		}
	}
	bad := map[string]RigSetting{"empty": {}, "mode": {Mode: "WFM"}, "low": {Power: p(0.01)}, "high": {Power: p(1.01)}, "freq low": {Freq: f(1000)}, "freq high": {Freq: f(9e12)}}
	for name, s := range bad {
		if ValidateRigSetting(s, st) == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	// A transmitting radio is not retuned, whatever else is true; the tuner switch is not part of that.
	tx := st
	tx.PTT = true
	for _, s := range []RigSetting{{Freq: f(14074000)}, {Mode: "USB"}, {Power: p(0.5)}} {
		if err := ValidateRigSetting(s, tx); err == nil || !strings.Contains(err.Error(), "transmitting") {
			t.Errorf("%+v while transmitting: %v", s, err)
		}
	}
}
