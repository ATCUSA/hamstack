package netinfo

import (
	"net"
	"testing"
)

const tcp4 = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:11C4 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 11111 1 0000000000000000 100 0 0 10 0
   1: 00000000:1F40 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 22222 1 0000000000000000 100 0 0 10 0
   2: 0100007F:9C40 0100007F:11C4 01 00000000:00000000 00:00000000 00000000  1000        0 33333 1 0000000000000000 100 0 0 10 0
   3: 0A3C0F01:1F41 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 44444 1 0000000000000000 100 0 0 10 0
`

const tcp6 = `  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000000000000000000001000000:11C4 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 55555 1 0000000000000000 100 0 0 10 0
   1: 00000000000000000000000000000000:2742 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 66666 1 0000000000000000 100 0 0 10 0
`

func TestParse(t *testing.T) {
	ls := Parse(tcp4)
	if len(ls) != 3 {
		t.Fatalf("got %d listeners (an established connection is not one): %+v", len(ls), ls)
	}
	want := map[int]struct {
		ip string
		sc Scope
	}{4548: {"127.0.0.1", Local}, 8000: {"0.0.0.0", Network}, 8001: {"1.15.60.10", Address}}
	for _, l := range ls {
		w, ok := want[l.Port]
		if !ok || l.IP.String() != w.ip || l.Scope() != w.sc {
			t.Errorf("listener %v scope %v: want %+v", l, l.Scope(), w)
		}
	}
	l6 := Parse(tcp6)
	if len(l6) != 2 || !l6[0].IP.Equal(net.IPv6loopback) || l6[0].Scope() != Local || l6[1].Port != 10050 || l6[1].Scope() != Network {
		t.Errorf("tcp6: %+v", l6)
	}
}

func TestFindPrefersTheWiderListener(t *testing.T) {
	ls := []Listener{{net.ParseIP("127.0.0.1"), 8000}, {net.IPv4zero, 8000}, {net.ParseIP("127.0.0.1"), 4532}}
	if l, ok := Find(ls, 8000); !ok || l.Scope() != Network {
		t.Errorf("8000: %+v %v", l, ok)
	}
	if l, ok := Find(ls, 4532); !ok || l.Scope() != Local {
		t.Errorf("4532: %+v %v", l, ok)
	}
	if _, ok := Find(ls, 9); ok {
		t.Error("nothing listens on 9")
	}
	// Every interface reaches further than one address, whichever comes first in the list: a port open on
	// 0.0.0.0 and on one LAN address used to be reported as "one address" and not flagged.
	lan := net.ParseIP("192.168.1.5")
	for _, ls := range [][]Listener{{{net.IPv4zero, 8000}, {lan, 8000}}, {{lan, 8000}, {net.IPv4zero, 8000}}} {
		if l, _ := Find(ls, 8000); l.Scope() != Network {
			t.Errorf("%v: got scope %v, want every interface", ls, l.Scope())
		}
	}
	if l, _ := Find([]Listener{{net.ParseIP("127.0.0.1"), 1}, {lan, 1}}, 1); l.Scope() != Address {
		t.Errorf("one address reaches further than loopback: %v", l.Scope())
	}
}

func TestParseIgnoresGarbage(t *testing.T) {
	if got := Parse("header\nnot a line\n   0: ZZZZ:11C4 x 0A\n   1: 0100007F:ZZ x 0A\n"); len(got) != 0 {
		t.Errorf("got %+v", got)
	}
	if got := Listeners(t.TempDir()); len(got) != 0 {
		t.Errorf("no /proc: %+v", got)
	}
}
