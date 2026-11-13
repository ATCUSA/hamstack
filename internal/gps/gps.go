// Package gps reads a position from gpsd and turns it into a Maidenhead grid square.
//
// gpsd (https://gpsd.io) owns the GPS receiver and shares it with any number of programs over a small
// TCP protocol: a client sends ?WATCH and receives one JSON object per line. hamstack only ever reads.
package gps

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"strings"
	"time"

	"github.com/ATCUSA/hamstack/internal/safetext"
)

// DefaultPort is the port hamstack's own gpsd service listens on. gpsd's standard port is 2947; using
// a different one keeps hamstack from fighting a system-wide gpsd that may already be running there.
const DefaultPort = 2948

// Grid converts a position to a Maidenhead locator of 4, 6 or 8 characters (EM10, EM10dg, EM10dg54).
// The locator is truncated, not rounded: that is how grid squares are defined.
func Grid(lat, lon float64, chars int) (string, error) {
	if math.IsNaN(lat) || math.IsNaN(lon) || lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		return "", fmt.Errorf("position %.5f, %.5f is outside the Earth", lat, lon)
	}
	if chars != 4 && chars != 6 && chars != 8 {
		return "", fmt.Errorf("a grid square has 4, 6 or 8 characters, not %d", chars)
	}
	// Work in whole cells of the finest size (a tenth of a sub-square), so no rounding error creeps in
	// between the levels. The poles and the antimeridian belong to the last cell, not one past it.
	const subs = 24 * 10 // finest cells per degree of latitude; longitude has half as many per degree
	x := int(math.Floor((lon + 180) * float64(subs) / 2))
	y := int(math.Floor((lat + 90) * float64(subs)))
	x = min(x, 360*subs/2-1)
	y = min(y, 180*subs-1)
	const upper, lower = "ABCDEFGHIJKLMNOPQRSTUVWX", "abcdefghijklmnopqrstuvwx"
	// x counts cells of 1/120 degree of longitude; y counts cells of 1/240 degree of latitude.
	// Each level divides by: 10 (extended), 24 (subsquare), 10 (square), 18 (field).
	ex, ey := x%10, y%10
	x, y = x/10, y/10
	sx, sy := x%24, y%24
	x, y = x/24, y/24
	qx, qy := x%10, y%10
	x, y = x/10, y/10
	out := []byte{upper[x], upper[y], byte('0' + qx), byte('0' + qy)}
	if chars >= 6 {
		out = append(out, lower[sx], lower[sy])
	}
	if chars == 8 {
		out = append(out, byte('0'+ex), byte('0'+ey))
	}
	return string(out), nil
}

// Status is what gpsd reported.
type Status struct {
	Version  string   // gpsd's release
	Devices  []string // receivers gpsd has open (their paths)
	Mode     int      // 0 unknown, 1 no fix, 2 two-dimensional fix, 3 three-dimensional
	HasPos   bool     // Lat and Lon are valid
	Lat, Lon float64
	Alt      float64 // metres, with a 3D fix
	Time     string  // the receiver's UTC time of the fix
	SatsUsed int     // satellites in the solution
	SatsSeen int
	gotSky   bool
}

// Fix reports whether there is a position to use.
func (s Status) Fix() bool { return s.HasPos && s.Mode >= 2 }

// ModeName describes Mode in words.
func (s Status) ModeName() string {
	switch s.Mode {
	case 1:
		return "no fix"
	case 2:
		return "2D fix"
	case 3:
		return "3D fix"
	}
	return "unknown"
}

// ErrNoFix means gpsd answered but the receiver has no position yet.
var ErrNoFix = errors.New("the GPS receiver has no fix yet")

// Query connects to gpsd at addr and watches until it has a position, or, when needFix is false, until it
// has also seen one position report and one satellite report. If the time runs out it returns what it
// saw so far; the error is ErrNoFix when gpsd was reachable but the receiver had no position.
func Query(ctx context.Context, addr string, needFix bool) (Status, error) {
	var st Status
	d := net.Dialer{Timeout: 3 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return st, fmt.Errorf("cannot reach gpsd at %s: %w", addr, err)
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	// Closing the connection when the context ends unblocks the read below.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-done:
		}
	}()
	if _, err := fmt.Fprint(conn, "?WATCH={\"enable\":true,\"json\":true}\n"); err != nil {
		return st, err
	}
	sawTPV := false
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		if st.apply(sc.Bytes()) == "TPV" {
			sawTPV = true
		}
		switch {
		case needFix && st.Fix():
			return st, nil
		case !needFix && sawTPV && st.gotSky:
			return st, nil
		}
	}
	// The connection ended or the time ran out.
	switch {
	case st.Fix():
		return st, nil
	case st.Version == "" && len(st.Devices) == 0 && !sawTPV:
		return st, fmt.Errorf("gpsd at %s did not answer", addr)
	case needFix:
		return st, ErrNoFix
	}
	return st, nil
}

// apply folds one line of gpsd's output into the status and returns its class.
func (s *Status) apply(line []byte) string {
	var m struct {
		Class   string   `json:"class"`
		Release string   `json:"release"`
		Mode    int      `json:"mode"`
		Lat     *float64 `json:"lat"`
		Lon     *float64 `json:"lon"`
		Alt     *float64 `json:"altMSL"`
		AltOld  *float64 `json:"alt"`
		Time    string   `json:"time"`
		Devices []struct {
			Path string `json:"path"`
		} `json:"devices"`
		Sats []struct {
			Used bool `json:"used"`
		} `json:"satellites"`
	}
	if json.Unmarshal(line, &m) != nil {
		return ""
	}
	switch m.Class {
	// The strings are printed, and a gpsd on another machine is not ours to trust with the terminal.
	case "VERSION":
		s.Version = safetext.Line(m.Release)
	case "DEVICES":
		s.Devices = s.Devices[:0]
		for _, d := range m.Devices {
			s.Devices = append(s.Devices, safetext.Line(d.Path))
		}
	case "TPV":
		s.Mode = m.Mode
		s.Time = safetext.Line(m.Time)
		s.HasPos = m.Lat != nil && m.Lon != nil && m.Mode >= 2
		if s.HasPos {
			s.Lat, s.Lon = *m.Lat, *m.Lon
			switch {
			case m.Alt != nil:
				s.Alt = *m.Alt
			case m.AltOld != nil:
				s.Alt = *m.AltOld
			}
		}
	case "SKY":
		s.gotSky = true
		s.SatsSeen = len(m.Sats)
		s.SatsUsed = 0
		for _, sat := range m.Sats {
			if sat.Used {
				s.SatsUsed++
			}
		}
	}
	return m.Class
}

// Describe is a one-line summary of a position, "46.73240 N, 117.00020 W".
func Describe(lat, lon float64) string {
	ns, ew := "N", "E"
	if lat < 0 {
		ns = "S"
	}
	if lon < 0 {
		ew = "W"
	}
	return fmt.Sprintf("%.5f %s, %.5f %s", math.Abs(lat), ns, math.Abs(lon), ew)
}

// Addr completes an address typed by the user: "host" gets gpsd's standard port, ":2948" means this
// machine, and a bare IPv6 address is bracketed ("::1" becomes "[::1]:2947").
func Addr(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if ip := net.ParseIP(strings.Trim(s, "[]")); ip != nil { // an address with no port, IPv6 included
		return net.JoinHostPort(ip.String(), "2947")
	}
	switch {
	case strings.HasPrefix(s, ":"):
		return "localhost" + s
	case strings.Contains(s, ":"):
		return s
	}
	return s + ":2947"
}
