package main

import (
	"regexp"
	"slices"
	"strings"

	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/safetext"
	"github.com/ATCUSA/hamstack/internal/svc"
	"github.com/ATCUSA/hamstack/internal/units"
	"github.com/ATCUSA/hamstack/internal/web"
)

// With --monitor, xara prints what it hears to its output as plain text: "— heard K1ABC → W1AW (2300 Hz) —", the text
// of a link ("[Winlink: N bytes of compressed message data]" for a Winlink exchange), "— K1ABC → W1AW ended (why) —",
// and the same for its own links ("— connected … —", "— disconnected —"). Its trace lines start with a time in
// seconds ("   12.345 > Connect ..."), its notices with "xara". Taken from xara's source (NR5P fork, rxtext.rs); a
// line it heard on the air has not been seen yet.

var xaraTraceRE = regexp.MustCompile(`^\s*\d+\.\d{3}\s`)

// heardKeep tells a line xara's monitor printed from xara's other output.
func heardKeep(text string) bool {
	t := strings.TrimSpace(text)
	switch {
	case t == "", xaraTraceRE.MatchString(text), strings.HasPrefix(t, "xara"), strings.HasPrefix(t, "hamstack:"):
		return false
	}
	return true
}

// heardLines picks the monitor's lines out of a unit's journal ("Nov 26 01:10:00 host hamstack[123]: text"), oldest
// first, the last max of them.
func heardLines(journal string, max int) []web.HeardLine {
	var out []web.HeardLine
	for _, l := range strings.Split(journal, "\n") {
		head, text, ok := strings.Cut(l, "]: ")
		if !ok || !strings.Contains(head, " hamstack[") { // systemd's own lines, and journalctl's notes
			continue
		}
		if !heardKeep(text) {
			continue
		}
		tm := ""
		if f := strings.Fields(head); len(f) >= 3 {
			tm = f[2]
		}
		out = append(out, web.HeardLine{Time: tm, Text: safetext.Line(text)})
	}
	if len(out) > max {
		out = out[len(out)-max:]
	}
	return out
}

// xaraLogText is the end of a rig's xara journal. A variable so a test need not read a journal.
var xaraLogText = func(rigID string) string {
	t, _ := svc.LogsText(units.Service{Kind: config.Xara, Rig: rigID}.Unit(), 600)
	return t
}

// radioHeard is the Radios page's "Heard on the air" for a rig with xara set up, nil for one without.
func radioHeard(rig config.Rig) *web.RadioHeard {
	if !slices.Contains(rig.HFModems(), config.Xara) {
		return nil
	}
	h := &web.RadioHeard{Monitor: rig.XaraMonitor, Lines: []web.HeardLine{}}
	h.Running = rig.Modem == config.Xara && svc.Status(units.Service{Kind: config.Xara, Rig: rig.ID}.Unit()).Active == "active"
	if rig.XaraMonitor {
		h.Lines = heardLines(xaraLogText(rig.ID), 60)
	}
	return h
}
