package main

import (
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/profile"
	"github.com/ATCUSA/hamstack/internal/radiosetup"
	"github.com/ATCUSA/hamstack/internal/rigctl"
	"github.com/ATCUSA/hamstack/internal/web"
)

// The radio settings check: a rig's radio against the openRMS Rigs profile for what its modem does. Settings that are
// wrong and receive-side (AGC OFF clipped the receive audio on an FT-DX10) are fixed by themselves when the modem
// starts, and again after a band change, because the FT-DX10 keeps AGC per band: on the Pi it was AUTO on 40 and 20 m
// and still OFF on 80 m. Everything else is shown on the Radios page with a Fix button (owner's decision 2026-11-26).

// rigOperation is the profile operation for a modem.
func rigOperation(modem string) (op, note string) {
	switch modem {
	case config.Ardopcf:
		return "winlink-ardop", ""
	case config.Xara:
		return "winlink-vara", ""
	case config.Mercury, config.Data2G:
		return "winlink-vara", "the profile written for VARA HF, the closest there is for " + modem
	}
	return "", ""
}

// radioProfileFor is the profile for a rig as it runs now, if there is one.
func radioProfileFor(rig config.Rig) (radiosetup.Profile, string, bool) {
	if rig.NoCAT {
		return radiosetup.Profile{}, "", false
	}
	prof, err := profile.Find(userProfileDir(), rig.Profile)
	if err != nil || prof.Rig.Model == 0 {
		return radiosetup.Profile{}, "", false
	}
	op, note := rigOperation(rig.Modem)
	if op == "" {
		return radiosetup.Profile{}, "", false
	}
	p, ok := radiosetup.Find(prof.Rig.Model, op)
	return p, note, ok
}

// bandWatchEvery is how often the band is looked at while a modem runs.
var bandWatchEvery = 15 * time.Second

// radioClient is a rigctld connection for the check. A variable so a test can give a fake radio.
var radioClient = func(rig config.Rig) radiosetup.Radio {
	return bareRadio{rigctl.New("127.0.0.1:" + strconv.Itoa(rig.RigPort))}
}

// bareRadio gives the check rigctld's values without their labels: hamstack's client speaks rigctld's extended
// protocol, which labels some ("PTT: 0", "Frequency: 7102800", "Mode: PKTUSB", "Reply: EX010417050;") and not others
// ("0.5" for a level). Seen on a Raspberry Pi with Hamlib 4.7.2 and an FT-DX10.
type bareRadio struct{ c *rigctl.Client }

var labelRE = regexp.MustCompile(`^[A-Z][A-Za-z ]*: `)

func (b bareRadio) Do(cmd string) ([]string, error) {
	lines, err := b.c.Do(cmd)
	for i, l := range lines {
		lines[i] = labelRE.ReplaceAllString(strings.ReplaceAll(l, "\x00", ""), "")
	}
	return lines, err
}

// radioAutoFix fixes what the check fixes by itself, and says what it changed on w.
func radioAutoFix(rig config.Rig, r radiosetup.Radio, w io.Writer) {
	p, _, ok := radioProfileFor(rig)
	if !ok {
		return
	}
	for _, res := range radiosetup.Check(r, p, rig.RadioAutoTX) {
		if !res.Auto {
			continue
		}
		if err := radiosetup.Fix(r, res.Step); err != nil {
			fmt.Fprintf(w, "hamstack: rig %s: %s is %s, outside what %s needs, and could not be set: %v\n", rig.ID, res.Step.Name, orDash(res.NowFor), p.Title, err)
			continue
		}
		fmt.Fprintf(w, "hamstack: rig %s: %s was %s, outside what %s needs: set to %s (openRMS Rigs profile)\n", rig.ID, res.Step.Name, orDash(res.NowFor), p.Title, res.Want)
	}
}

// bandOf is the amateur band of a frequency in hertz, by its megahertz ("40m" for 7.1 MHz); "" outside the HF bands.
func bandOf(hz int64) string {
	mhz := float64(hz) / 1e6
	for _, b := range []struct {
		name     string
		low, top float64
	}{{"160m", 1.8, 2.0}, {"80m", 3.5, 4.0}, {"60m", 5.3, 5.41}, {"40m", 7.0, 7.3}, {"30m", 10.1, 10.15}, {"20m", 14.0, 14.35},
		{"17m", 18.068, 18.168}, {"15m", 21.0, 21.45}, {"12m", 24.89, 24.99}, {"10m", 28.0, 29.7}, {"6m", 50, 54}} {
		if mhz >= b.low && mhz <= b.top {
			return b.name
		}
	}
	return ""
}

// watchBands runs the check's fixes again whenever the radio changes band while it is not transmitting, until stop
// closes. The FT-DX10 keeps AGC per band, so a fix made on one band says nothing about the next.
func watchBands(rig config.Rig, r radiosetup.Radio, w io.Writer, every time.Duration, stop <-chan struct{}) {
	last := ""
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		l, err := r.Do("t")
		if err != nil || len(l) == 0 || strings.TrimSpace(strings.ReplaceAll(l[0], "\x00", "")) != "0" {
			continue // transmitting, or not readable now
		}
		f, err := r.Do("f")
		if err != nil || len(f) == 0 {
			continue
		}
		hz, _ := strconv.ParseInt(strings.TrimSpace(strings.ReplaceAll(f[0], "\x00", "")), 10, 64)
		b := bandOf(hz)
		if b == "" || b == last {
			continue
		}
		if last != "" {
			radioAutoFix(rig, r, w)
		}
		last = b
	}
}

// radioTransmitting says whether the rig is keyed now (or cannot be asked: then it is treated as keyed).
func radioTransmitting(r radiosetup.Radio) bool {
	l, err := r.Do("t")
	return err != nil || len(l) == 0 || strings.TrimSpace(strings.ReplaceAll(l[0], "\x00", "")) != "0"
}

// RadioSetup is the radio settings check for the Radios page.
func (webBackend) RadioSetup(id string) (web.SetupView, error) {
	cfg := loadCfg()
	rig, _, err := findRig(cfg, id)
	if err != nil {
		return web.SetupView{}, err
	}
	p, _, ok := radioProfileFor(rig)
	if !ok {
		return web.SetupView{}, fmt.Errorf("there is no openRMS Rigs profile for this radio and modem")
	}
	r := radioClient(rig)
	if radioTransmitting(r) {
		return web.SetupView{}, fmt.Errorf("the radio is transmitting (or rig control is not answering): check again when it has finished")
	}
	v := web.SetupView{Title: p.Title, Credit: "openRMS Rigs contributors, CC-BY-SA-4.0"}
	for _, res := range radiosetup.Check(r, p, rig.RadioAutoTX) {
		row := web.SetupRow{ID: res.Step.ID, Name: res.Step.Name, Now: res.NowFor, Want: res.Want, State: res.State, CanFix: res.CanFix,
			Auto: res.Auto, TX: res.Step.Safety == "tx", CheckOnly: res.Step.CheckOnly, Note: res.Step.Note, Confidence: res.Step.Confidence}
		if res.Step.Manual != nil {
			row.Menu = res.Step.Manual.Menu
		}
		v.Rows = append(v.Rows, row)
	}
	return v, nil
}

// radioFix sets one setting of a rig's radio to its profile's value (the Radios page's Fix button).
func radioFix(rigID, stepID string) (string, error) {
	cfg := loadCfg()
	rig, _, err := findRig(cfg, rigID)
	if err != nil {
		return "", err
	}
	p, _, ok := radioProfileFor(rig)
	if !ok {
		return "", fmt.Errorf("there is no openRMS Rigs profile for this radio and modem")
	}
	for _, s := range p.Steps {
		if s.ID != stepID {
			continue
		}
		r := radioClient(rig)
		if radioTransmitting(r) {
			return "", fmt.Errorf("the radio is transmitting: try again when it has finished")
		}
		if err := radiosetup.Fix(r, s); err != nil {
			return "", err
		}
		return fmt.Sprintf("%s set as %s has it", s.Name, p.Title), nil
	}
	return "", fmt.Errorf("%s has no setting %q", p.Title, stepID)
}
