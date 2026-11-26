package main

import (
	"strings"
	"testing"

	"github.com/ATCUSA/hamstack/internal/config"
)

// A journal as journalctl -o short prints it for the xara unit: systemd's lines, hamstack's and xara's notices, xara's
// trace (times in seconds) and the monitor's lines.
const xaraJournal = `Nov 26 01:09:20 radio systemd[1419]: Started hamstack-xara@hf.service - hamstack xara HF modem.
Nov 26 01:09:20 radio hamstack[100]: hamstack: rig hf: xara on port 8310 (audio in hamstack_hf, out plughw:CARD=CODEC,DEV=0)
Nov 26 01:09:20 radio hamstack[101]: xara 0.1.2: K0TEST 2300 Hz, cmd 127.0.0.1:8310 data 127.0.0.1:8311
Nov 26 01:09:20 radio hamstack[101]: — showing other stations' traffic (500, 2300 Hz) —
Nov 26 01:09:20 radio hamstack[101]: xara: RADIO MODE: transmitting into "plughw:CARD=CODEC,DEV=0"
Nov 26 01:09:20 radio hamstack[101]:      0.060 < BUSY ON
Nov 26 01:10:02 radio hamstack[101]: — heard K1ABC → K0EOC-10 (2300 Hz) —
Nov 26 01:10:40 radio hamstack[101]: [Winlink: 1834 bytes of compressed message data]
Nov 26 01:11:05 radio hamstack[101]: — K1ABC → K0EOC-10 ended (disconnect) —
Nov 26 01:11:06 radio hamstack[101]:     106.020 > Bw(Hz2300)
Nov 26 01:11:07 radio hamstack[101]: odd` + "\x1b[31m" + `colour from the air`

func TestHeardLinesKeepOnlyWhatTheMonitorHeard(t *testing.T) {
	got := heardLines(xaraJournal, 60)
	var texts []string
	for _, l := range got {
		texts = append(texts, l.Time+" "+l.Text)
	}
	want := []string{
		"01:09:20 — showing other stations' traffic (500, 2300 Hz) —",
		"01:10:02 — heard K1ABC → K0EOC-10 (2300 Hz) —",
		"01:10:40 [Winlink: 1834 bytes of compressed message data]",
		"01:11:05 — K1ABC → K0EOC-10 ended (disconnect) —",
	}
	if len(texts) != 5 || strings.Join(texts[:4], "\n") != strings.Join(want, "\n") {
		t.Fatalf("got:\n%s", strings.Join(texts, "\n"))
	}
	if strings.ContainsRune(texts[4], '\x1b') {
		t.Errorf("text from the air must be cleaned: %q", texts[4])
	}
	if last := heardLines(xaraJournal, 2); len(last) != 2 || !strings.Contains(last[0].Text, "ended") {
		t.Errorf("the last lines only: %+v", last)
	}
}

func TestRadioHeardOnlyForARigWithXara(t *testing.T) {
	old := xaraLogText
	xaraLogText = func(string) string { return xaraJournal }
	defer func() { xaraLogText = old }()
	if h := radioHeard(config.Rig{ID: "hf", Modem: config.Mercury}); h != nil {
		t.Errorf("no xara, no section: %+v", h)
	}
	if h := radioHeard(config.Rig{ID: "hf", Modem: config.Mercury, Alt: []string{config.Xara}, XaraMonitor: config.Set(false)}); h == nil || h.Monitor || len(h.Lines) != 0 {
		t.Errorf("monitor turned off: %+v", h)
	}
	if h := radioHeard(config.Rig{ID: "hf", Modem: config.Mercury, Alt: []string{config.Xara}}); h == nil || !h.Monitor || len(h.Lines) != 5 {
		t.Errorf("monitor left to its default (on): %+v", h)
	}
}
