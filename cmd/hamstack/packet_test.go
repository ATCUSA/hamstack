package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ATCUSA/hamstack/internal/agw"
)

// fakeTNC is an AGW server that records the frames a client sends and answers the way Direwolf does.
type fakeTNC struct {
	l      net.Listener
	mu     sync.Mutex
	got    []agw.Frame
	closed chan struct{}
}

func newFakeTNC(t *testing.T, onFrame func(f agw.Frame, reply func(agw.Frame)) (hangUp bool)) *fakeTNC {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tnc := &fakeTNC{l: l, closed: make(chan struct{})}
	t.Cleanup(func() { l.Close() })
	go func() {
		defer close(tnc.closed)
		s, err := l.Accept()
		if err != nil {
			return
		}
		defer s.Close()
		for {
			f, err := agw.ReadFrame(s)
			if err != nil {
				return
			}
			tnc.mu.Lock()
			tnc.got = append(tnc.got, f)
			tnc.mu.Unlock()
			if onFrame(f, func(r agw.Frame) { s.Write(r.Encode()) }) {
				return
			}
		}
	}()
	return tnc
}

func (f *fakeTNC) kinds() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var b strings.Builder
	for _, fr := range f.got {
		b.WriteByte(fr.Kind)
	}
	return b.String()
}

func dialTNC(t *testing.T, tnc *fakeTNC) *agw.Conn {
	t.Helper()
	c, err := agw.Dial(context.Background(), tnc.l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// direwolfLike answers a connect with a connected notice and a disconnect with its confirmation.
func direwolfLike(f agw.Frame, reply func(agw.Frame)) bool {
	switch f.Kind {
	case 'C', 'v':
		reply(agw.Frame{Kind: 'C', CallFrom: f.CallTo, CallTo: f.CallFrom, Data: []byte("*** CONNECTED With Station " + f.CallTo + "\r")})
		reply(agw.Frame{Kind: 'D', CallFrom: f.CallTo, CallTo: f.CallFrom, Data: []byte("Welcome \x1b]0;owned\x07to the BBS\r")})
	case 'd':
		reply(agw.Frame{Kind: 'd', CallFrom: f.CallTo, CallTo: f.CallFrom, Data: []byte("*** DISCONNECTED From Station " + f.CallTo + "\r")})
	}
	return false
}

func TestCallSendsLinesAndHangsUpCleanly(t *testing.T) {
	tnc := newFakeTNC(t, direwolfLike)
	var out bytes.Buffer
	err := callSession(context.Background(), dialTNC(t, tnc), strings.NewReader("hello\n~.\nnever sent\n"), &out, 0, "N0CALL", "K0TEST", nil)
	if err != nil {
		t.Fatal(err)
	}
	if k := tnc.kinds(); k != "XCDd" {
		t.Errorf("frames sent: %q, want register, connect, data, disconnect", k)
	}
	if d := tnc.got[2]; string(d.Data) != "hello\r" || d.CallFrom != "N0CALL" || d.CallTo != "K0TEST" || d.PID != 0xF0 {
		t.Errorf("data frame %+v", d)
	}
	for _, want := range []string{"Calling K0TEST as N0CALL", "*** CONNECTED With Station K0TEST", "Welcome ]0;ownedto the BBS", "*** DISCONNECTED"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	if strings.ContainsAny(out.String(), "\x1b\x07") {
		t.Errorf("an escape sequence from the air reached the terminal: %q", out.String())
	}
}

// Ctrl+C used to close the connection before the disconnect could be sent (and could end with a "use of
// closed network connection" error): the other station was left connected until its own timers gave up.
func TestCallInterruptedStillSendsTheDisconnect(t *testing.T) {
	tnc := newFakeTNC(t, direwolfLike)
	ctx, cancel := context.WithCancel(context.Background())
	in, inW := io.Pipe() // a keyboard nobody is typing on
	defer inW.Close()
	var out bytes.Buffer
	res := make(chan error, 1)
	go func() {
		res <- callSession(ctx, dialTNC(t, tnc), in, &out, 0, "N0CALL", "K0TEST", []string{"WIDE1-1"})
	}()
	deadline := time.Now().Add(2 * time.Second)
	for tnc.kinds() != "Xv" && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-res:
		if err != nil {
			t.Errorf("an interrupted call must end quietly, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the call did not end after the interrupt")
	}
	if k := tnc.kinds(); k != "Xvd" {
		t.Errorf("frames sent: %q, want register, connect via, disconnect", k)
	}
	if !strings.Contains(out.String(), "Disconnected.") || !strings.Contains(out.String(), "K0TEST via WIDE1-1") {
		t.Errorf("output:\n%s", out.String())
	}
}

func TestCallEndsWhenTheTNCGoesAway(t *testing.T) {
	tnc := newFakeTNC(t, func(f agw.Frame, _ func(agw.Frame)) bool { return f.Kind == 'C' }) // drops the client
	in, inW := io.Pipe()
	defer inW.Close()
	err := callSession(context.Background(), dialTNC(t, tnc), in, io.Discard, 0, "N0CALL", "K0TEST", nil)
	if !errors.Is(err, agw.ErrClosed) {
		t.Errorf("got %v, want %v", err, agw.ErrClosed)
	}
}

// A TNC that never confirms the disconnect must not hang the call.
func TestCallDoesNotWaitForeverForTheDisconnect(t *testing.T) {
	old := hangupWait
	hangupWait = 50 * time.Millisecond
	defer func() { hangupWait = old }()
	tnc := newFakeTNC(t, func(agw.Frame, func(agw.Frame)) bool { return false }) // says nothing at all
	start := time.Now()
	if err := callSession(context.Background(), dialTNC(t, tnc), strings.NewReader(""), io.Discard, 0, "N0CALL", "K0TEST", nil); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > time.Second {
		t.Errorf("took %s", time.Since(start))
	}
	if k := tnc.kinds(); k != "XCd" {
		t.Errorf("frames sent: %q", k)
	}
}

func TestParseVia(t *testing.T) {
	if v, err := parseVia("wide1-1, WIDE2-1,k0eoc-7"); err != nil || strings.Join(v, " ") != "WIDE1-1 WIDE2-1 K0EOC-7" {
		t.Errorf("%v %v", v, err)
	}
	for _, bad := range []string{"WIDE1-1;rm", "TOOLONGALIAS", "", "K0TEST-123", "a,b,c,d,e,f,g,h,i"} {
		if v, err := parseVia(bad); err == nil {
			t.Errorf("%q accepted as %v", bad, v)
		}
	}
}
