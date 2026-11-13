package tui

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m model) key(k tea.KeyMsg) (model, tea.Cmd) {
	if m.pr != nil {
		return m.promptKey(k)
	}
	s := k.String()
	switch s {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "tab", "right":
		return m.gotoTab((m.tab + 1) % nTabs)
	case "shift+tab", "left":
		return m.gotoTab((m.tab + nTabs - 1) % nTabs)
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		return m.gotoTab(int(s[0] - '1'))
	case "0":
		return m.gotoTab(tNetwork)
	}
	if m.busy || (m.checking && m.tab == tDoctor) {
		return m, nil
	}
	if s == "w" || (s == "enter" && m.needsSetup && m.tab == tServices) { // on a fresh install Enter starts the setup
		return m.exec("setup", "winlink")
	}
	if m2, moved := m.nav(s); moved {
		return m2, nil
	}
	switch m.tab {
	case tServices:
		return m.keysServices(s)
	case tRigs:
		return m.keysRigs(s)
	case tSites:
		return m.keysSites(s)
	case tStations:
		return m.keysStations(s)
	case tDevices:
		return m.keysDevices(s)
	case tPrograms:
		return m.keysPrograms(s)
	case tSettings:
		return m.keysSettings(s)
	case tDoctor:
		return m.keysDoctor(s)
	case tLogs:
		return m.keysLogs(s)
	}
	return m, nil
}

// nav moves the selection on the current tab.
func (m model) nav(s string) (model, bool) {
	n := m.count()
	switch s {
	case "up", "k":
		m.sel[m.tab] = clamp(m.sel[m.tab]-1, n)
	case "down", "j":
		m.sel[m.tab] = clamp(m.sel[m.tab]+1, n)
	case "pgup":
		m.sel[m.tab] = clamp(m.sel[m.tab]-5, n)
	case "pgdown":
		m.sel[m.tab] = clamp(m.sel[m.tab]+5, n)
	case "home":
		m.sel[m.tab] = 0
	case "end":
		m.sel[m.tab] = clamp(n-1, n)
	default:
		return m, false
	}
	return m, true
}

func (m model) cur() int { return m.sel[m.tab] }

// ---- Services ----

func (m model) keysServices(s string) (model, tea.Cmd) {
	do := func(verb, name, label string) (model, tea.Cmd) {
		return m.do(label, func() (string, error) {
			if err := m.be.Control(verb, name); err != nil {
				return "", err
			}
			return label + ": done", nil
		})
	}
	var cur Service
	if len(m.svcs) > 0 {
		cur = m.svcs[m.cur()]
	}
	switch s {
	case "U":
		return m.do("installing the services", func() (string, error) { return m.be.UnitsInstall() })
	case "S":
		return do("start", "all", "starting all")
	case "X":
		return do("stop", "all", "stopping all")
	case "enter", " ":
		if cur.Name == "" {
			return m, nil
		}
		if cur.State == "active" {
			return do("stop", cur.Name, "stopping "+cur.Name)
		}
		return do("start", cur.Name, "starting "+cur.Name)
	case "r":
		if cur.Name != "" {
			return do("restart", cur.Name, "restarting "+cur.Name)
		}
	case "e":
		if cur.Name != "" {
			if cur.Enabled == "enabled" {
				return do("disable", cur.Name, "disabling "+cur.Name+" at login")
			}
			return do("enable", cur.Name, "enabling "+cur.Name+" at login")
		}
	case "l":
		if cur.Name != "" {
			m.logSvc = m.cur()
			return m.gotoTab(tLogs)
		}
	}
	return m, nil
}

// ---- Rigs ----

var modemChoices = []struct{ label, value string }{
	{"none (rig control only)", ""},
	{"mercury (HF modem)", "mercury"},
	{"direwolf (VHF/UHF packet)", "direwolf"},
}

func (m model) keysRigs(s string) (model, tea.Cmd) {
	switch s {
	case "a":
		return m.addRig(), nil
	}
	if len(m.rigs) == 0 {
		return m, nil
	}
	r := m.rigs[m.cur()]
	switch s {
	case "enter", " ":
		return m.do("switching "+r.ID, func() (string, error) { return m.be.RigToggle(r.ID) })
	case "m":
		next := map[string]string{"": "mercury", "mercury": "direwolf", "direwolf": ""}[r.Modem]
		if next == "mercury" && r.NoCAT {
			next = "direwolf" // mercury needs CAT control
		}
		label := next
		if label == "" {
			label = "no modem"
		}
		return m.do("setting "+r.ID+" to "+label, func() (string, error) { return m.be.RigModem(r.ID, next) })
	case "i":
		return m.exec("rig", "identify", r.ID)
	case "d":
		if r.Modem != "direwolf" {
			m.status, m.statusErr = r.ID+" does not use Direwolf (m changes the modem)", true
			return m, nil
		}
		return m.exec("direwolf", "setup", r.ID)
	case "x":
		return m.askConfirm("Remove rig "+r.ID+" (its services are stopped)? y/n", func(m model) (model, tea.Cmd) {
			return m.do("removing "+r.ID, func() (string, error) { return m.be.RigRemove(r.ID) })
		}), nil
	}
	return m, nil
}

var idChars = regexp.MustCompile(`[^a-z0-9_-]+`)

// uniqueID suggests an id from base that is not in taken.
func uniqueID(base string, taken []string) string {
	id := strings.Trim(idChars.ReplaceAllString(strings.ToLower(base), "-"), "-")
	if id == "" {
		id = "rig"
	}
	cand := id
	for n := 2; contains(taken, cand); n++ {
		cand = fmt.Sprintf("%s-%d", id, n)
	}
	return cand
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// addRig asks for the radio model, a name and a modem, then adds the rig (detecting its USB devices).
func (m model) addRig() model {
	profiles := m.be.Profiles()
	if len(profiles) == 0 {
		m.status, m.statusErr = "no radio profiles are available", true
		return m
	}
	opts := make([]string, 0, len(profiles)+1)
	for _, p := range profiles {
		opts = append(opts, p.Name+"  ["+p.ID+"]")
	}
	opts = append(opts, "Search every radio hamlib supports…")
	var taken []string
	for _, r := range m.rigs {
		taken = append(taken, r.ID)
	}
	return m.askChoose("Which radio?", opts, func(m model, _ string, pi int) (model, tea.Cmd) {
		if pi == len(profiles) {
			return m.searchRadio("", func(m model, p ProfileOpt) (model, tea.Cmd) { return m.nameRig(p, taken) }), nil
		}
		return m.nameRig(profiles[pi], taken)
	})
}

// nameRig asks for the rig's name and modem once a radio is chosen, then adds it.
func (m model) nameRig(p ProfileOpt, taken []string) (model, tea.Cmd) {
	m = m.askInput("Name this rig (letters, digits, - and _):", uniqueID(p.ID, taken), func(m model, id string, _ int) (model, tea.Cmd) {
		var labels, values []string
		for _, mc := range modemChoices {
			if mc.value == "mercury" && !p.HasCAT {
				continue // Mercury is keyed through rigctld, which needs CAT control
			}
			labels, values = append(labels, mc.label), append(values, mc.value)
		}
		m = m.askChoose("Modem for "+id+":", labels, func(m model, _ string, mi int) (model, tea.Cmd) {
			return m.do("adding rig "+id, func() (string, error) { return m.be.RigAdd(p.ID, id, values[mi]) })
		})
		return m, nil
	})
	return m, nil
}

// searchRadio asks for part of a radio's name, lists the best matches, and calls done with a profile for the
// one chosen (made on the spot, after asking the serial speed, if hamstack has none for it).
func (m model) searchRadio(initial string, done func(model, ProfileOpt) (model, tea.Cmd)) model {
	return m.askInput("Search radios (part of a name, like ic-7100 or kx3; blank lists them all):", initial, func(m model, q string, _ int) (model, tea.Cmd) {
		hits, err := m.be.SearchRadios(q)
		if err != nil {
			m.status, m.statusErr = err.Error(), true
			return m, nil
		}
		if len(hits) == 0 {
			m.status, m.statusErr = "nothing matches "+q+": try fewer letters, or just the maker", true
			return m.searchRadio(q, done), nil
		}
		const show = 14
		shown := hits[:min(show, len(hits))]
		opts := make([]string, 0, len(shown)+1)
		for _, h := range shown {
			opts = append(opts, fmt.Sprintf("%s  [hamlib %d, %s]", h.Label, h.Model, h.Status))
		}
		more := ""
		if len(hits) > len(shown) {
			more = fmt.Sprintf(" (%d more: refine the search)", len(hits)-len(shown))
		}
		opts = append(opts, "Search again…"+more)
		m = m.askChoose(fmt.Sprintf("%d match%s:", len(hits), plural(len(hits))), opts, func(m model, _ string, hi int) (model, tea.Cmd) {
			if hi == len(shown) {
				return m.searchRadio(q, done), nil
			}
			h := shown[hi]
			finish := func(m model, baud int) (model, tea.Cmd) {
				id, err := m.be.SaveRadio(h.Model, baud)
				if err != nil {
					m.status, m.statusErr = err.Error(), true
					return m, nil
				}
				return done(m, ProfileOpt{ID: id, Name: h.Label, HasCAT: h.Model > 0})
			}
			if h.Profile != "" {
				return finish(m, 0) // a profile already exists: SaveRadio returns it
			}
			lo, hi2, def := m.be.RadioBaud(h.Model)
			if hi2 == 0 {
				return finish(m, 0) // no serial speed: a network radio such as FLRig
			}
			m = m.askInput(fmt.Sprintf("Serial speed: the radio's CAT rate (%d..%d):", lo, hi2), strconv.Itoa(def), func(m model, text string, _ int) (model, tea.Cmd) {
				n, err := strconv.Atoi(strings.TrimSpace(text))
				if err != nil || n < lo || n > hi2 {
					m.status, m.statusErr = fmt.Sprintf("%q is not a speed this radio supports (%d..%d)", text, lo, hi2), true
					return m, nil
				}
				return finish(m, n)
			})
			return m, nil
		})
		return m, nil
	})
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "es"
}

// ---- Sites ----

func (m model) keysSites(s string) (model, tea.Cmd) {
	switch s {
	case "a":
		return m.askInput("Save what is set up now as a site named:", "", func(m model, id string, _ int) (model, tea.Cmd) {
			return m.do("saving site "+id, func() (string, error) { return m.be.SiteAdd(id) })
		}), nil
	case "h":
		if m.hintID == "" {
			m.status, m.statusErr = "the plugged-in radios do not point to a different site", false
			return m, nil
		}
		id := m.hintID
		return m.askConfirm(m.hintWhy+". Switch to "+id+"? y/n", func(m model) (model, tea.Cmd) {
			return m.do("switching to "+id, func() (string, error) { return m.be.SiteUse(id) })
		}), nil
	}
	if len(m.sites) == 0 {
		return m, nil
	}
	st := m.sites[m.cur()]
	switch s {
	case "enter", " ":
		if st.Current {
			m.status, m.statusErr = "you are already at "+st.ID, false
			return m, nil
		}
		return m.do("switching to "+st.ID, func() (string, error) { return m.be.SiteUse(st.ID) })
	case "g":
		return m.askInput("Grid square for "+st.ID+" (e.g. EM10dg):", st.Grid, func(m model, g string, _ int) (model, tea.Cmd) {
			return m.do("setting the grid of "+st.ID, func() (string, error) { return m.be.SiteGrid(st.ID, g) })
		}), nil
	case "x":
		return m.askConfirm("Remove site "+st.ID+" (your rigs and stations are kept)? y/n", func(m model) (model, tea.Cmd) {
			return m.do("removing "+st.ID, func() (string, error) { return m.be.SiteRemove(st.ID) })
		}), nil
	}
	return m, nil
}

// ---- Stations ----

func (m model) keysStations(s string) (model, tea.Cmd) {
	switch s {
	case "a":
		return m.askInput("Callsign for the new station:", "", func(m model, call string, _ int) (model, tea.Cmd) {
			var taken []string
			for _, st := range m.stations {
				taken = append(taken, st.ID)
			}
			m = m.askInput("Short name for it (letters, digits, - and _):", uniqueID(call, taken), func(m model, id string, _ int) (model, tea.Cmd) {
				return m.do("adding station "+id, func() (string, error) { return m.be.StationAdd(id, call) })
			})
			return m, nil
		}), nil
	case "i":
		return m.askConfirm("Import stations from your existing Pat configs (the files are not changed)? y/n", func(m model) (model, tea.Cmd) {
			return m.do("importing stations", func() (string, error) { return m.be.StationImport() })
		}), nil
	}
	if len(m.stations) == 0 {
		return m, nil
	}
	st := m.stations[m.cur()]
	switch s {
	case "enter", " ":
		return m.do("switching to "+st.ID, func() (string, error) { return m.be.Use(st.ID) })
	case "x":
		return m.askConfirm("Remove station "+st.ID+" (its Pat config and mailbox are kept)? y/n", func(m model) (model, tea.Cmd) {
			return m.do("removing "+st.ID, func() (string, error) { return m.be.StationRemove(st.ID) })
		}), nil
	}
	return m, nil
}

// ---- Devices ----

func (m model) keysDevices(s string) (model, tea.Cmd) {
	if len(m.devices) == 0 {
		return m, nil
	}
	d := m.devices[m.cur()]
	switch s {
	case "a":
		if len(m.rigs) == 0 {
			m.status, m.statusErr = "add a rig first (Rigs tab, a)", true
			return m, nil
		}
		opts := make([]string, len(m.rigs))
		for i, r := range m.rigs {
			opts[i] = r.ID + "  (" + r.Radio + ")"
		}
		rigs := m.rigs
		return m.askChoose("Use "+d.Label+" for which rig?", opts, func(m model, _ string, i int) (model, tea.Cmd) {
			return m.do("assigning to "+rigs[i].ID, func() (string, error) { return m.be.AssignDevice(d.Key, rigs[i].ID) })
		}), nil
	case "i":
		if len(m.rigs) == 0 {
			m.status, m.statusErr = "add a rig first (Rigs tab, a)", true
			return m, nil
		}
		opts := make([]string, len(m.rigs))
		for i, r := range m.rigs {
			opts[i] = r.ID + "  (" + r.Radio + ")"
		}
		rigs := m.rigs
		return m.askChoose("Identify which rig's radio (you will unplug and replug it)?", opts, func(m model, _ string, i int) (model, tea.Cmd) {
			return m.exec("rig", "identify", rigs[i].ID)
		}), nil
	}
	return m, nil
}

// ---- Programs ----

func (m model) keysPrograms(s string) (model, tea.Cmd) {
	switch s {
	case "r":
		m.latestTried, m.latestLoading = true, true
		return m, tea.Batch(m.loadPrograms(), m.loadLatest())
	case "I":
		return m.exec("install") // in a terminal this shows the list of programs to choose from
	}
	if len(m.programs) == 0 {
		return m, nil
	}
	p := m.programs[m.cur()]
	switch s {
	case "i", "enter":
		switch {
		case p.Installed == "":
			return m.confirmExec("Install "+p.Name+"?", "install", p.Name), nil
		case m.programStatus(p) == "update available" && p.UserInstall:
			return m.confirmExec("Update "+p.Name+" (and anything else hamstack installed)?", "update"), nil
		case m.programStatus(p) == "update available":
			m.status, m.statusErr = p.Name+" is managed by your package manager: "+p.UpdateHint, false
			return m, nil
		}
		return m.confirmExec(p.Name+" is up to date. Reinstall it anyway?", "install", p.Name, "--force"), nil
	case "s":
		if !p.CanSource {
			m.status, m.statusErr = p.Name+" cannot be built from source by hamstack (it has no source recipe, or is always built that way)", true
			return m, nil
		}
		return m.confirmExec("Build "+p.Name+" from source (installs compilers; slow)?", "install", p.Name, "--from-source"), nil
	}
	return m, nil
}

// confirmExec asks, showing the exact command, and then runs a hamstack command in the terminal.
func (m model) confirmExec(question string, args ...string) model {
	return m.askConfirm(question+"  [hamstack "+strings.Join(args, " ")+"]  y/n", func(m model) (model, tea.Cmd) {
		return m.exec(args...)
	})
}

// ---- Settings ----

func (m model) keysSettings(s string) (model, tea.Cmd) {
	if len(m.settings) == 0 || (s != "enter" && s != " ") {
		return m, nil
	}
	st := m.settings[m.cur()]
	switch st.Kind {
	case SettingToggle:
		return m.do("changing "+st.Label, func() (string, error) { return m.be.SetSetting(st.Key, "") })
	case SettingText:
		return m.askInput(st.Label+":", st.Value, func(m model, v string, _ int) (model, tea.Cmd) {
			return m.do("saving "+st.Label, func() (string, error) { return m.be.SetSetting(st.Key, v) })
		}), nil
	case SettingExec:
		return m.confirmExec("Run this in the terminal?", st.Args...), nil
	}
	return m, nil
}

// ---- Doctor ----

func (m model) keysDoctor(s string) (model, tea.Cmd) {
	switch s {
	case "r":
		return m.startChecks()
	case "f":
		if len(m.results) == 0 {
			return m, nil
		}
		r := m.results[m.cur()]
		if r.Fix == "" {
			m.status, m.statusErr = "that check has nothing to fix", false
			return m, nil
		}
		args, ok := FixCommand(r.Fix)
		if !ok {
			m.status, m.statusErr = "no command to run for this one: "+r.Fix, false
			return m, nil
		}
		return m.confirmExec("Fix: "+r.Name+"?", args...), nil
	}
	return m, nil
}

// ---- Logs ----

func (m model) keysLogs(s string) (model, tea.Cmd) {
	if s == "[" || s == "]" {
		if n := len(m.svcs); n > 0 {
			d := 1
			if s == "[" {
				d = n - 1
			}
			m.logSvc = (clamp(m.logSvc, n) + d) % n
		}
		return m, m.loadLogs()
	}
	return m, nil
}
