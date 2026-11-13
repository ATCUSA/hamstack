package agw

import (
	"bytes"
	"context"
	"net"
	"runtime"
	"testing"
	"time"
)

func TestFrameRoundTrip(t *testing.T) {
	in := Frame{Port: 1, Kind: 'D', PID: 0xF0, CallFrom: "N0CALL-1", CallTo: "K0TEST", Data: []byte("hello\r")}
	b := in.Encode()
	if len(b) != 36+6 || b[4] != 'D' || b[28] != 6 {
		t.Fatalf("bad encoding: % x", b)
	}
	out, err := ReadFrame(bytes.NewReader(b))
	if err != nil || out.CallFrom != "N0CALL-1" || out.CallTo != "K0TEST" || string(out.Data) != "hello\r" || out.Port != 1 || out.PID != 0xF0 {
		t.Errorf("got %+v, %v", out, err)
	}
}

func TestReadFrameRefusesHugeData(t *testing.T) {
	b := Frame{Kind: 'D'}.Encode()
	b[31] = 0x7f // 2 GB
	if _, err := ReadFrame(bytes.NewReader(b)); err == nil {
		t.Error("a frame claiming 2 GB must be refused")
	}
	if _, err := ReadFrame(bytes.NewReader(b[:10])); err == nil {
		t.Error("a short header must be an error")
	}
}

func TestTextIsSafeToPrint(t *testing.T) {
	f := Frame{Data: []byte("a\x1b[2Jb\r\ncd\x00\x07e\tf\r")}
	if got := f.Text(); got != "a[2Jb\ncde\tf" { // CR LF is one line ending, not a blank line
		t.Errorf("got %q", got)
	}
	// UTF-8 text survives whole (bytes 0x80-0x9f inside a character used to be dropped, mangling it), while
	// the 8-bit escape and a callsign field with an escape in it do not get through.
	f = Frame{Data: []byte("Gr\xc3\x9c\xc3\x9fe \x9b31m\xc2\x9b")}
	if got := f.Text(); got != "GrÜße 31m" {
		t.Errorf("got %q", got)
	}
	if got := getCall([]byte("K0\x1b[2J\x00junk")); got != "K0[2J" {
		t.Errorf("callsign field: %q", got)
	}
}

// Dial with a context that never ends must not leave a goroutine behind once the connection is closed.
func TestDialLeavesNoGoroutineBehind(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	defer l.Close()
	go func() {
		for {
			s, err := l.Accept()
			if err != nil {
				return
			}
			s.Close()
		}
	}()
	before := runtime.NumGoroutine()
	for i := 0; i < 20; i++ {
		c, err := Dial(context.Background(), l.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		c.Close()
	}
	time.Sleep(50 * time.Millisecond)
	if after := runtime.NumGoroutine(); after > before+5 {
		t.Errorf("goroutines grew from %d to %d over 20 dials", before, after)
	}
}

func TestContextEndUnblocksNext(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	defer l.Close()
	go func() {
		if s, err := l.Accept(); err == nil {
			defer s.Close()
			time.Sleep(2 * time.Second) // says nothing
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	c, err := Dial(ctx, l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	done := make(chan error, 1)
	go func() { _, err := c.Next(); done <- err }()
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Error("Next returned a frame from a closed connection")
		}
	case <-time.After(time.Second):
		t.Fatal("Next stayed blocked after the context ended")
	}
}

func TestConnectViaEncoding(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	c := &Conn{c: a}
	go c.Connect(0, "N0CALL", "K0TEST", []string{"WIDE1-1", "WIDE2-1"})
	f, err := ReadFrame(b)
	if err != nil || f.Kind != 'v' || f.CallTo != "K0TEST" || len(f.Data) != 21 || f.Data[0] != 2 || getCall(f.Data[1:11]) != "WIDE1-1" || getCall(f.Data[11:21]) != "WIDE2-1" {
		t.Errorf("%+v, %v", f, err)
	}
}

func TestDialMonitorsAFakeServer(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	defer l.Close()
	go func() {
		s, err := l.Accept()
		if err != nil {
			return
		}
		defer s.Close()
		f, _ := ReadFrame(s)
		if f.Kind != 'm' {
			return
		}
		s.Write(Frame{Kind: 'U', CallFrom: "K0TEST", CallTo: "CQ", Data: []byte(" 1:Fm K0TEST To CQ <UI pid=F0 Len=2 >\rhi\r")}.Encode())
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, err := Dial(ctx, l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Monitor(); err != nil {
		t.Fatal(err)
	}
	f, err := c.Next()
	if err != nil || !f.IsMonitor() || f.CallFrom != "K0TEST" {
		t.Errorf("%+v, %v", f, err)
	}
	if _, err := Dial(ctx, "127.0.0.1:1"); err == nil {
		t.Error("a closed port must be an error")
	}
}
