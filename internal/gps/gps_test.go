package gps

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

func TestGrid(t *testing.T) {
	cases := []struct {
		lat, lon float64
		chars    int
		want     string
	}{
		{41.7147, -72.7272, 6, "FN31pr"},   // Newington, Connecticut
		{41.7147, -72.7272, 4, "FN31"},     //
		{41.7147, -72.7272, 8, "FN31pr21"}, //
		{-33.8568, 151.2153, 6, "QF56od"},  // Sydney Opera House
		{0, 0, 6, "JJ00aa"},                // Null Island
		{90, 180, 6, "RR99xx"},             // the far corner belongs to the last square
		{-90, -180, 6, "AA00aa"},           //
		{51.5, -0.0001, 4, "IO91"},         // just west of the Greenwich meridian
		{51.5, 0.0001, 4, "JO01"},          // just east: the next square over
	}
	for _, c := range cases {
		got, err := Grid(c.lat, c.lon, c.chars)
		if err != nil || got != c.want {
			t.Errorf("Grid(%v, %v, %d) = %q, %v; want %q", c.lat, c.lon, c.chars, got, err, c.want)
		}
	}
}

func TestGridRejectsNonsense(t *testing.T) {
	for _, c := range [][3]float64{{91, 0, 6}, {0, 181, 6}, {0, 0, 5}} {
		if g, err := Grid(c[0], c[1], int(c[2])); err == nil {
			t.Errorf("Grid(%v) = %q, want an error", c, g)
		}
	}
}

// fakeGPSD serves lines to the first client, after reading its ?WATCH command.
func fakeGPSD(t *testing.T, lines ...string) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		fmt.Fprintln(c, `{"class":"VERSION","release":"3.25"}`)
		cmd, _ := bufio.NewReader(c).ReadString('\n')
		if !strings.HasPrefix(cmd, "?WATCH=") {
			return
		}
		for _, ln := range lines {
			fmt.Fprintln(c, ln)
		}
		time.Sleep(2 * time.Second) // stay open, like gpsd does
	}()
	return l.Addr().String()
}

func TestQueryFix(t *testing.T) {
	addr := fakeGPSD(t,
		`{"class":"DEVICES","devices":[{"path":"/dev/ttyACM0"}]}`,
		`{"class":"TPV","mode":1}`,
		`{"class":"SKY","satellites":[{"used":true},{"used":false},{"used":true}]}`,
		`not json at all`,
		`{"class":"TPV","mode":3,"lat":41.7147,"lon":-72.7272,"altMSL":12.5,"time":"2026-11-12T10:00:00Z"}`,
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	st, err := Query(ctx, addr, true)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Fix() || st.Mode != 3 || st.Lat != 41.7147 || st.Lon != -72.7272 || st.Alt != 12.5 {
		t.Errorf("status = %+v", st)
	}
	if st.Version != "3.25" || len(st.Devices) != 1 || st.Devices[0] != "/dev/ttyACM0" || st.SatsUsed != 2 || st.SatsSeen != 3 {
		t.Errorf("status = %+v", st)
	}
	if g, _ := Grid(st.Lat, st.Lon, 6); g != "FN31pr" {
		t.Errorf("grid = %s", g)
	}
}

func TestQueryNoFix(t *testing.T) {
	addr := fakeGPSD(t, `{"class":"DEVICES","devices":[{"path":"/dev/ttyACM0"}]}`, `{"class":"TPV","mode":1}`)
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()
	st, err := Query(ctx, addr, true)
	if !errors.Is(err, ErrNoFix) {
		t.Fatalf("err = %v, want ErrNoFix", err)
	}
	if st.Fix() || st.Mode != 1 || len(st.Devices) != 1 {
		t.Errorf("status = %+v", st)
	}
}

func TestQueryStatusWithoutFix(t *testing.T) {
	addr := fakeGPSD(t, `{"class":"TPV","mode":1}`, `{"class":"SKY","satellites":[]}`)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	st, err := Query(ctx, addr, false)
	if err != nil || st.Mode != 1 || st.ModeName() != "no fix" {
		t.Fatalf("st = %+v, err = %v", st, err)
	}
	if time.Since(start) > time.Second {
		t.Errorf("a status query waited for a fix that never came")
	}
}

func TestQueryUnreachable(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := l.Addr().String()
	l.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := Query(ctx, addr, true); err == nil || !strings.Contains(err.Error(), "cannot reach gpsd") {
		t.Errorf("err = %v", err)
	}
}

func TestAddr(t *testing.T) {
	for in, want := range map[string]string{"": "", "pi": "pi:2947", "pi:2948": "pi:2948", ":2948": "localhost:2948",
		"192.168.1.5": "192.168.1.5:2947", "::1": "[::1]:2947", "[::1]": "[::1]:2947", "[fe80::1]:2948": "[fe80::1]:2948", " pi ": "pi:2947"} {
		if got := Addr(in); got != want {
			t.Errorf("Addr(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDescribe(t *testing.T) {
	if got := Describe(-33.5, -70.25); got != "33.50000 S, 70.25000 W" {
		t.Errorf("got %q", got)
	}
}

// What gpsd reports is printed; a gpsd on another machine must not be able to send the terminal escape sequences.
func TestStatusStringsAreSafeToPrint(t *testing.T) {
	var st Status
	st.apply([]byte(`{"class":"VERSION","release":"3.25\u001b]0;owned\u0007"}`))
	st.apply([]byte(`{"class":"DEVICES","devices":[{"path":"/dev/ttyACM0\u001b[2J\n"}]}`))
	st.apply([]byte(`{"class":"TPV","mode":3,"lat":46.7,"lon":-117.0,"time":"2026-01-02T03:04:05Z\u009b31m"}`))
	if st.Version != "3.25]0;owned" || len(st.Devices) != 1 || st.Devices[0] != "/dev/ttyACM0[2J" || st.Time != "2026-01-02T03:04:05Z31m" {
		t.Errorf("%+v", st)
	}
	if !st.Fix() {
		t.Error("the fix itself must still be read")
	}
}
