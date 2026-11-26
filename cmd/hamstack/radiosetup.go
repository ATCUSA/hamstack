package main

import (
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ATCUSA/hamstack/internal/apps"
	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/profile"
	"github.com/ATCUSA/hamstack/internal/radiosetup"
	"github.com/ATCUSA/hamstack/internal/rigctl"
	"github.com/ATCUSA/hamstack/internal/svc"
	"github.com/ATCUSA/hamstack/internal/web"
)

// The radio settings check: a rig's radio against the openRMS Rigs profile for what its modem does. Settings that are
// wrong and receive-side (AGC OFF clipped the receive audio on an FT-DX10) are fixed by themselves when the modem
// starts, and again after a band change, because the FT-DX10 keeps AGC per band: on the Pi it was AUTO on 40 and 20 m
// and still OFF on 80 m. Everything else is shown on the Radios page with a Fix button (owner's decision 2026-11-26).

// Which profile: each program that uses the radio (a modem, WSJT-X, fldigi...) has a recommended profile
// (radiosetup.Recommended), and the owner can pick another one per program, or none, on the Radios page or with
// "hamstack rig set RIG radio-profile PROGRAM=PROFILE". The check and its fixes use the profile of the program that has
// the radio now.

// radioModel is the hamlib model of a rig's radio, 0 without rig control or a known model.
func radioModel(rig config.Rig) int {
	if rig.NoCAT {
		return 0
	}
	prof, err := profile.Find(userProfileDir(), rig.Profile)
	if err != nil {
		return 0
	}
	return prof.Rig.Model
}

// radioProfileForUse is the profile a program gets on a rig's radio: the owner's pick, else the recommended one. The
// note says why a profile written for something else is used; ok is false with none (or "none" picked).
func radioProfileForUse(rig config.Rig, use string) (radiosetup.Profile, string, bool) {
	model := radioModel(rig)
	if model == 0 || use == "" {
		return radiosetup.Profile{}, "", false
	}
	switch pick := rig.RadioProfiles[use]; pick {
	case "none":
		return radiosetup.Profile{}, "", false
	case "":
	default:
		if p, ok := radiosetup.ByID(pick); ok && p.HamlibModel == model {
			return p, "picked for " + useTitle(use), true
		}
		// A pick for another radio (the rig's radio was changed since) falls back to the recommended one.
	}
	p, ok := radiosetup.Recommended(model, use)
	if !ok {
		return radiosetup.Profile{}, "", false
	}
	u, _ := radiosetup.UseFor(use)
	return p, u.Closest, true
}

// setRadioProfile records the owner's profile for a program on a rig: a profile for the rig's radio, "none" (no check)
// or "default" (the recommended one).
func setRadioProfile(r *config.Rig, use, pick string) error {
	if _, ok := radiosetup.UseFor(use); !ok {
		var names []string
		for _, u := range radiosetup.Uses {
			names = append(names, u.Name)
		}
		return fmt.Errorf("%q has no radio settings profile: one of %s", use, strings.Join(names, ", "))
	}
	switch pick {
	case "default", "":
		delete(r.RadioProfiles, use)
		if len(r.RadioProfiles) == 0 {
			r.RadioProfiles = nil
		}
		return nil
	case "none":
	default:
		p, ok := radiosetup.ByID(pick)
		if !ok || p.HamlibModel != radioModel(*r) {
			var ids []string
			for _, q := range radiosetup.ForModel(radioModel(*r)) {
				ids = append(ids, q.ID)
			}
			if len(ids) == 0 {
				return fmt.Errorf("there are no radio settings profiles for rig %s's radio", r.ID)
			}
			return fmt.Errorf("%q is not a profile for rig %s's radio: one of %s", pick, r.ID, strings.Join(ids, ", "))
		}
	}
	if r.RadioProfiles == nil {
		r.RadioProfiles = map[string]string{}
	}
	r.RadioProfiles[use] = pick
	return nil
}

func useTitle(use string) string {
	if u, ok := radiosetup.UseFor(use); ok {
		return u.Title
	}
	return use
}

// radioAppNow is the radio program open now (WSJT-X, JS8Call, fldigi), from the dashboard or a menu entry: "" with none.
// A variable so a test need not run one.
var radioAppNow = func() string {
	for _, a := range apps.All {
		if !a.Radio {
			continue
		}
		if svc.Status(vncUnit(a.Name)).Active == "active" {
			return a.Name
		}
		if bin := a.Binary(); bin != "" && programRunning(bin) {
			return a.Name
		}
	}
	return ""
}

// rigUseNow is the program that has a rig's radio now: an open radio program (hamstack stops the modems for it),
// else the rig's modem.
func rigUseNow(rig config.Rig) string {
	if a := radioAppNow(); a != "" {
		if _, ok := radiosetup.UseFor(a); ok {
			return a
		}
	}
	if _, ok := radiosetup.UseFor(rig.Modem); ok {
		return rig.Modem
	}
	return ""
}

// rigUses are the programs that can have a rig's radio: its HF modems, and the radio programs installed.
func rigUses(rig config.Rig) []string {
	var out []string
	for _, m := range rig.HFModems() {
		if _, ok := radiosetup.UseFor(m); ok {
			out = append(out, m)
		}
	}
	for _, a := range []string{"wsjtx", "js8call", "fldigi"} {
		if appInstalled(a) {
			out = append(out, a)
		}
	}
	return out
}

// appInstalled says whether a program is installed. A variable so a test does not depend on the machine.
var appInstalled = func(name string) bool {
	a, ok := apps.Find(name)
	return ok && a.Path() != ""
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

// radioAutoFix fixes what the check fixes by itself for the program that has the radio, and says what it changed on w.
func radioAutoFix(rig config.Rig, use string, r radiosetup.Radio, w io.Writer) {
	radioCheck(rig, use, r, w, false)
}

// radioStartCheck is radioAutoFix when a program takes the radio: it also says when there was nothing to fix, so the
// log shows the check ran (silence looked the same as a check that never happened).
func radioStartCheck(rig config.Rig, use string, r radiosetup.Radio, w io.Writer) {
	radioCheck(rig, use, r, w, true)
}

func radioCheck(rig config.Rig, use string, r radiosetup.Radio, w io.Writer, sayNothing bool) {
	p, _, ok := radioProfileForUse(rig, use)
	if !ok {
		return
	}
	results := radiosetup.Check(r, p, rig.RadioAutoTX)
	auto := 0
	for _, res := range results {
		if res.Auto {
			auto++
		}
	}
	if sayNothing && auto == 0 {
		fmt.Fprintf(w, "hamstack: rig %s: radio settings checked against %s (%d settings read): nothing to fix by itself\n", rig.ID, p.Title, len(results))
	}
	for _, res := range results {
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

// radioAppWatch is the radio settings check for a radio program that is starting (WSJT-X, JS8Call, fldigi): the
// fixes it makes by itself, with the program's profile, and again after each band change while it runs. It runs beside
// the program, which starts without waiting for it; the function it returns stops the watch.
func radioAppWatch(cfg config.Config, app apps.App, w io.Writer) func() {
	rig, ok := seedRig(cfg)
	if !ok || rig.NoCAT {
		return func() {}
	}
	if _, _, ok := radioProfileForUse(rig, app.Name); !ok {
		return func() {}
	}
	stop := make(chan struct{})
	go func() {
		r := radioClient(rig)
		radioStartCheck(rig, app.Name, r, w)
		watchBands(rig, app.Name, r, w, bandWatchEvery, stop)
	}()
	var once sync.Once
	return func() { once.Do(func() { close(stop) }) }
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
func watchBands(rig config.Rig, use string, r radiosetup.Radio, w io.Writer, every time.Duration, stop <-chan struct{}) {
	// The band now is the one the start-up check just looked at: a change from it is what counts, however soon.
	last, _ := currentBand(r)
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		b, ok := currentBand(r)
		if !ok || b == last {
			continue
		}
		if last != "" {
			radioAutoFix(rig, use, r, w)
		}
		last = b
	}
}

// currentBand is the band the radio is on, when it is not transmitting and answers ("" outside the HF bands).
func currentBand(r radiosetup.Radio) (string, bool) {
	if radioTransmitting(r) {
		return "", false
	}
	f, err := r.Do("f")
	if err != nil || len(f) == 0 {
		return "", false
	}
	hz, err := strconv.ParseInt(strings.TrimSpace(strings.ReplaceAll(f[0], "\x00", "")), 10, 64)
	if err != nil {
		return "", false
	}
	b := bandOf(hz)
	return b, b != ""
}

// radioTransmitting says whether the rig is keyed now (or cannot be asked: then it is treated as keyed).
func radioTransmitting(r radiosetup.Radio) bool {
	l, err := r.Do("t")
	return err != nil || len(l) == 0 || strings.TrimSpace(strings.ReplaceAll(l[0], "\x00", "")) != "0"
}

// radioSetupRef is what the Radios page shows about the check before it is run: the profile in use, each program's
// profile, and every profile for the radio with the programs it is recommended for. nil for a radio with none.
func radioSetupRef(rig config.Rig) *web.SetupRef {
	model := radioModel(rig)
	all := radiosetup.ForModel(model)
	if model == 0 || len(all) == 0 {
		return nil
	}
	ref := &web.SetupRef{AutoTX: rig.RadioAutoTX}
	now := rigUseNow(rig)
	if p, note, ok := radioProfileForUse(rig, now); ok {
		ref.Title, ref.Note, ref.Profile, ref.Use = p.Title, note, p.ID, useTitle(now)
	}
	for _, u := range rigUses(rig) {
		row := web.SetupUse{Name: u, Title: useTitle(u), Now: u == now, Picked: rig.RadioProfiles[u]}
		if p, note, ok := radioProfileForUse(rig, u); ok {
			row.Profile, row.Note = p.ID, note
		}
		if p, ok := radiosetup.Recommended(model, u); ok {
			row.Recommended = p.ID
		}
		ref.Uses = append(ref.Uses, row)
	}
	for _, p := range all {
		ref.Profiles = append(ref.Profiles, web.SetupProfile{ID: p.ID, Title: p.Title, For: radiosetup.RecommendedFor(p)})
	}
	return ref
}

// RadioSetup is the radio settings check for the Radios page: against the profile in use, or another of the radio's
// profiles to look at (then nothing in it is offered to fix: Fix only ever sets the profile of what has the radio).
func (webBackend) RadioSetup(id, profileID string) (web.SetupView, error) {
	cfg := loadCfg()
	rig, _, err := findRig(cfg, id)
	if err != nil {
		return web.SetupView{}, err
	}
	inUse, _, inUseOK := radioProfileForUse(rig, rigUseNow(rig))
	p := inUse
	if profileID != "" && profileID != inUse.ID {
		q, ok := radiosetup.ByID(profileID)
		if !ok || q.HamlibModel != radioModel(rig) {
			return web.SetupView{}, fmt.Errorf("%q is not a profile for this radio", profileID)
		}
		p = q
	} else if !inUseOK {
		return web.SetupView{}, fmt.Errorf("no program that has a profile has the radio now: pick a profile to check against")
	}
	r := radioClient(rig)
	if radioTransmitting(r) {
		return web.SetupView{}, fmt.Errorf("the radio is transmitting (or rig control is not answering): check again when it has finished")
	}
	v := web.SetupView{Title: p.Title, Profile: p.ID, Credit: "openRMS Rigs contributors, CC-BY-SA-4.0"}
	fixable := inUseOK && p.ID == inUse.ID
	if !fixable {
		v.Note = "Only a look: Fix sets the profile of the program that has the radio. To use this one, pick it for that program above."
	}
	for _, res := range radiosetup.Check(r, p, rig.RadioAutoTX) {
		row := web.SetupRow{ID: res.Step.ID, Name: res.Step.Name, Now: res.NowFor, Want: res.Want, State: res.State, CanFix: res.CanFix && fixable,
			Auto: res.Auto && fixable, TX: res.Step.Safety == "tx", CheckOnly: res.Step.CheckOnly, Note: res.Step.Note, Confidence: res.Step.Confidence}
		if res.Step.Manual != nil {
			row.Menu = res.Step.Manual.Menu
		}
		v.Rows = append(v.Rows, row)
	}
	return v, nil
}

// radioFix sets one setting of a rig's radio to its profile's value (the Radios page's Fix button). The profile must be
// the one in use: a setting from another (the SSB profile's mic input, say) would undo what the modem needs to transmit.
func radioFix(rigID, profileID, stepID string) (string, error) {
	cfg := loadCfg()
	rig, _, err := findRig(cfg, rigID)
	if err != nil {
		return "", err
	}
	p, _, ok := radioProfileForUse(rig, rigUseNow(rig))
	if !ok {
		return "", fmt.Errorf("no program that has a profile has the radio now")
	}
	if profileID != "" && profileID != p.ID {
		return "", fmt.Errorf("Fix only sets the profile in use (%s): pick %s for that program first", p.Title, profileID)
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

// printRadioProfiles shows the profile each program gets on a rig, and every profile for its radio with the programs it
// is recommended for.
func printRadioProfiles(w io.Writer, rig config.Rig) error {
	ref := radioSetupRef(rig)
	if ref == nil {
		return fmt.Errorf("there are no radio settings profiles for rig %s's radio (openRMS Rigs has profiles for the FT-DX10, IC-7300 and IC-7300MK2 so far)", rig.ID)
	}
	fmt.Fprintf(w, "Rig %s: the profile each program gets (change: hamstack rig set %s radio-profile PROGRAM=PROFILE|default|none)\n", rig.ID, rig.ID)
	for _, u := range ref.Uses {
		prof := u.Profile
		switch {
		case u.Picked == "none":
			prof = "none (no check)"
		case u.Picked == "":
			prof += " (recommended)"
		}
		now := ""
		if u.Now {
			now = "  <- has the radio now"
		}
		fmt.Fprintf(w, "  %-8s %s%s\n", u.Title, prof, now)
	}
	fmt.Fprintln(w, "\nProfiles for this radio (openRMS Rigs contributors, CC-BY-SA-4.0):")
	for _, p := range ref.Profiles {
		line := fmt.Sprintf("  %-28s %s", p.ID, p.Title)
		if len(p.For) > 0 {
			line += "  [recommended for " + strings.Join(p.For, ", ") + "]"
		}
		fmt.Fprintln(w, line)
	}
	return nil
}
