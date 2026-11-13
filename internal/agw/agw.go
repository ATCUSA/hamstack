// Package agw is a small client for the AGWPE protocol that Direwolf serves (TCP, default port 8000).
//
// It gives hamstack a way to watch packet traffic and make a connected-mode call without the Linux
// kernel's AX.25 stack, which needs root, extra packages and a kernel that still has the module (recent
// kernels do not). Direwolf allows several AGW clients at once, so this works while Pat is connected.
package agw

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/ATCUSA/hamstack/internal/safetext"
)

const headerLen = 36

// Frame is one AGW frame.
type Frame struct {
	Port     byte
	Kind     byte // 'R' version, 'X' register callsign, 'm' monitor, 'C' connect, 'v' connect via, 'D' data, 'd' disconnect, ...
	PID      byte
	CallFrom string
	CallTo   string
	Data     []byte
}

func putCall(dst []byte, s string) {
	for i := range dst {
		dst[i] = 0
	}
	copy(dst, s)
}

func getCall(b []byte) string {
	if i := strings.IndexByte(string(b), 0); i >= 0 {
		b = b[:i]
	}
	return safetext.Line(strings.TrimSpace(string(b))) // a callsign from the wire is printed too
}

// Encode returns the frame as bytes on the wire.
func (f Frame) Encode() []byte {
	b := make([]byte, headerLen+len(f.Data))
	b[0] = f.Port
	b[4] = f.Kind
	b[6] = f.PID
	putCall(b[8:18], f.CallFrom)
	putCall(b[18:28], f.CallTo)
	binary.LittleEndian.PutUint32(b[28:32], uint32(len(f.Data)))
	copy(b[headerLen:], f.Data)
	return b
}

// maxData bounds a frame's data so a broken peer cannot make us allocate without limit.
const maxData = 1 << 20

// ReadFrame reads one frame.
func ReadFrame(r io.Reader) (Frame, error) {
	var h [headerLen]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return Frame{}, err
	}
	n := binary.LittleEndian.Uint32(h[28:32])
	if n > maxData {
		return Frame{}, fmt.Errorf("AGW frame of %d bytes: not an AGW server?", n)
	}
	f := Frame{Port: h[0], Kind: h[4], PID: h[6], CallFrom: getCall(h[8:18]), CallTo: getCall(h[18:28])}
	if n > 0 {
		f.Data = make([]byte, n)
		if _, err := io.ReadFull(r, f.Data); err != nil {
			return Frame{}, err
		}
	}
	return f, nil
}

// Conn is a connection to an AGW server.
type Conn struct {
	c    net.Conn
	r    *bufio.Reader
	mu   sync.Mutex
	stop func() bool // stops watching the context given to Dial
}

// Dial connects to an AGW server such as Direwolf's. The connection is closed when ctx ends, which
// unblocks a Next that is waiting for a frame; a caller that wants to say goodbye first (send a disconnect)
// should give Dial a context that outlives that, and call Close itself.
func Dial(ctx context.Context, addr string) (*Conn, error) {
	d := net.Dialer{Timeout: 3 * time.Second}
	c, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("cannot reach the packet TNC at %s: %w (is Direwolf running? hamstack start direwolf@RIG)", addr, err)
	}
	// AfterFunc rather than a goroutine waiting on ctx.Done(): with a context that never ends, that goroutine
	// would never end either, and Close can take this back.
	stop := context.AfterFunc(ctx, func() { c.Close() })
	return &Conn{c: c, r: bufio.NewReader(c), stop: stop}, nil
}

// Close closes the connection.
func (c *Conn) Close() error {
	if c.stop != nil {
		c.stop()
	}
	return c.c.Close()
}

// Send writes a frame.
func (c *Conn) Send(f Frame) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err := c.c.Write(f.Encode())
	return err
}

// Next reads the next frame.
func (c *Conn) Next() (Frame, error) { return ReadFrame(c.r) }

// Version asks the server to identify itself; it is a cheap check that something speaks AGW.
func (c *Conn) Version() error { return c.Send(Frame{Kind: 'R'}) }

// Monitor asks the server to send every frame it hears.
func (c *Conn) Monitor() error { return c.Send(Frame{Kind: 'm'}) }

// Register tells the server a callsign this client answers to (needed to make or accept a connection).
func (c *Conn) Register(call string) error { return c.Send(Frame{Kind: 'X', CallFrom: call}) }

// Connect asks for a connection to a station, optionally through digipeaters.
func (c *Conn) Connect(port byte, from, to string, via []string) error {
	if len(via) == 0 {
		return c.Send(Frame{Port: port, Kind: 'C', CallFrom: from, CallTo: to})
	}
	data := []byte{byte(len(via))}
	for _, v := range via {
		d := make([]byte, 10)
		putCall(d, v)
		data = append(data, d...)
	}
	return c.Send(Frame{Port: port, Kind: 'v', CallFrom: from, CallTo: to, Data: data})
}

// SendData sends text on an established connection.
func (c *Conn) SendData(port byte, from, to string, data []byte) error {
	return c.Send(Frame{Port: port, Kind: 'D', PID: 0xF0, CallFrom: from, CallTo: to, Data: data})
}

// Disconnect ends a connection.
func (c *Conn) Disconnect(port byte, from, to string) error {
	return c.Send(Frame{Port: port, Kind: 'd', CallFrom: from, CallTo: to})
}

// IsMonitor reports whether a frame is a monitored packet (heard on the air, or one we sent).
func (f Frame) IsMonitor() bool {
	switch f.Kind {
	case 'I', 'U', 'S', 'T':
		return true
	}
	return false
}

// Text returns the frame's data as printable text: a carriage return (packet's line ending), a line feed, or
// the pair, becomes one newline, and escape and other control characters are dropped, so a packet from the
// air cannot move the cursor or change the terminal. Text in UTF-8 is kept whole.
func (f Frame) Text() string {
	return strings.TrimRight(safetext.Text(string(f.Data)), "\n")
}

// ErrClosed is returned when the server closes the connection.
var ErrClosed = errors.New("the packet TNC closed the connection")
