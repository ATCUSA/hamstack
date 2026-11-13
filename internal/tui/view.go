package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/ATCUSA/hamstack/internal/doctor"
	"github.com/ATCUSA/hamstack/internal/versions"
)

var (
	title    = lipgloss.NewStyle().Bold(true)
	dim      = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "245", Dark: "243"})
	good     = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "28", Dark: "42"})
	warn     = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "130", Dark: "214"})
	bad      = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "160", Dark: "203"})
	selected = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "25", Dark: "81"})
	tabOn    = lipgloss.NewStyle().Bold(true).Underline(true)
)

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// trunc shortens s to w characters, ending with … when it was cut.
func trunc(s string, w int) string {
	if w <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	if w == 1 {
		return "…"
	}
	return string(r[:w-1]) + "…"
}

// col pads or cuts s to exactly w characters, keeping the last one free as a gap before the next column.
func col(s string, w int) string { return fmt.Sprintf("%-*s", w, trunc(s, w-1)) } // always leaves a gap

func (m model) View() string {
	var b strings.Builder
	b.WriteString(m.headerLine() + "\n")
	b.WriteString(m.tabBar() + "\n\n")

	lines, selLine := m.body()
	avail := max(4, m.h-8)
	shown := window(lines, selLine, avail)
	for i, l := range shown {
		shown[i] = ansi.Truncate(l, max(10, m.w-1), "…") // understands colour codes, unlike trunc
	}
	b.WriteString(strings.Join(shown, "\n"))
	b.WriteString("\n\n")

	if m.status != "" {
		st := good
		if m.statusErr {
			st = bad
		}
		b.WriteString(st.Render(trunc(m.status, max(10, m.w-1))) + "\n")
	}
	if m.pr != nil {
		b.WriteString(m.promptView())
	} else {
		b.WriteString(dim.Render(trunc(m.help(), max(10, m.w-1))))
	}
	return b.String()
}

func (m model) headerLine() string {
	parts := []string{title.Render("hamstack")}
	add := func(label, v string) {
		if v != "" {
			parts = append(parts, dim.Render(label+": ")+v)
		}
	}
	add("site", m.hdr.Site)
	add("rigs", m.hdr.Rigs)
	add("station", m.hdr.Station)
	add("grid", m.hdr.Grid)
	return ansi.Truncate(strings.Join(parts, "  "), max(10, m.w-1), "…") // counts what is seen, not the colour codes
}

// tabBar fits the tabs to the terminal: full names with numbers, then without numbers, then short names.
func (m model) tabBar() string {
	variants := []func(i int) string{
		func(i int) string { return fmt.Sprintf(" %d %s ", i+1, tabNames[i]) },
		func(i int) string { return " " + tabNames[i] + " " },
		func(i int) string { return fmt.Sprintf(" %d%s ", i+1, trunc(tabNames[i], 3)) },
	}
	var pick func(i int) string
	for _, v := range variants {
		total := 0
		for i := 0; i < nTabs; i++ {
			total += len([]rune(v(i)))
		}
		if total <= m.w {
			pick = v
			break
		}
	}
	if pick == nil { // too narrow for every tab: name the current one, and say there are more
		return tabOn.Render(trunc(fmt.Sprintf(" %s (%d/%d) ", tabNames[m.tab], m.tab+1, nTabs), max(1, m.w)))
	}
	var b strings.Builder
	for i := 0; i < nTabs; i++ {
		if i == m.tab {
			b.WriteString(tabOn.Render(pick(i)))
		} else {
			b.WriteString(dim.Render(pick(i)))
		}
	}
	return b.String()
}

// window returns the part of lines that fits avail rows and keeps selLine in view.
func window(lines []string, selLine, avail int) []string {
	if len(lines) <= avail {
		return lines
	}
	start := selLine - avail/2
	start = max(0, min(start, len(lines)-avail))
	return lines[start : start+avail]
}

func (m model) cursor(i int) string {
	if i == m.sel[m.tab] {
		return selected.Render("> ")
	}
	return "  "
}

// body returns the lines of the current tab and the index of the selected one.
func (m model) body() ([]string, int) {
	switch m.tab {
	case tServices:
		return m.servicesView()
	case tRigs:
		return m.rigsView()
	case tSites:
		return m.sitesView()
	case tStations:
		return m.stationsView()
	case tDevices:
		return m.devicesView()
	case tPrograms:
		return m.programsView()
	case tSettings:
		return m.settingsView()
	case tDoctor:
		return m.doctorView()
	case tLogs:
		return m.logsView(), 0
	case tNetwork:
		return m.networkView(), 0
	}
	return nil, 0
}

func (m model) networkView() []string {
	n := m.network
	if len(n.Rows) == 0 && len(n.Lines) == 0 {
		return []string{dim.Render("Nothing is set up yet, so there are no network ports to show.")}
	}
	var lines []string
	if len(n.Rows) > 0 {
		sw, ww := 7, 4
		for _, r := range n.Rows {
			sw, ww = max(sw, len(r.Service)), max(ww, len(r.What))
		}
		lines = append(lines, dim.Render(fmt.Sprintf("  %-*s  %-*s  %-5s  %-28s %s", sw, "SERVICE", ww, "WHAT", "PORT", "WHO CAN CONNECT", "NOTE")))
		exposed := false
		for _, r := range n.Rows {
			who := fmt.Sprintf("%-28s", r.Who)
			switch {
			case r.Exposed:
				who, exposed = warn.Render(who), true
			case r.Who == "not running":
				who = dim.Render(who)
			default:
				who = good.Render(who)
			}
			lines = append(lines, fmt.Sprintf("  %-*s  %-*s  %-5d  %s %s", sw, r.Service, ww, r.What, r.Port, who, dim.Render(r.Note)))
		}
		if exposed {
			lines = append(lines, "", warn.Render("  Orange rows are open to your whole network with no password: fine on a network you trust, otherwise use your firewall."))
		}
	}
	if len(n.Lines) > 0 {
		lines = append(lines, "")
		for _, l := range n.Lines {
			lines = append(lines, "  "+l)
		}
	}
	return lines
}

func empty(msg string) ([]string, int) { return []string{dim.Render(msg)}, 0 }

func (m model) servicesView() ([]string, int) {
	var lines []string
	if m.needsSetup {
		lines = append(lines,
			title.Render("Welcome to hamstack. Nothing is set up yet."),
			"",
			"  Press "+selected.Render(" Enter ")+" (or "+selected.Render("w")+") to start the guided setup.",
			"",
			"It asks a few questions (radio, callsign, grid square), installs what is missing, starts everything and checks it,",
			"right here in this terminal, showing every command before it runs. Or set things up piece by piece from the tabs above.",
			"")
	}
	if len(m.svcs) == 0 {
		if !m.needsSetup {
			lines = append(lines, dim.Render("No services: add a rig on the Rigs tab, or turn Pat on in Settings."))
		}
		return lines, 0
	}
	w := 9
	for _, s := range m.svcs {
		w = max(w, len(s.Name))
	}
	lines = append(lines, dim.Render(fmt.Sprintf("  %-*s %-14s %-9s %s", w, "SERVICE", "STATE", "AT LOGIN", "SINCE")))
	base := len(lines)
	for i, s := range m.svcs {
		stText := fmt.Sprintf("%-14s", s.State)
		switch s.State {
		case "active":
			stText = good.Render(stText)
		case "no radio":
			stText = warn.Render(stText)
		case "failed":
			stText = bad.Render(stText)
		case "not installed":
			stText = warn.Render(stText)
		default:
			stText = dim.Render(stText)
		}
		lines = append(lines, fmt.Sprintf("%s%-*s %s %-9s %s", m.cursor(i), w, s.Name, stText, orDash(s.Enabled), dim.Render(s.Since)))
	}
	if !m.svcs[0].Installed {
		lines = append(lines, "", warn.Render("The services are not installed yet: press U to install them (or w for the guided setup)."))
	}
	return lines, base + m.sel[tServices]
}

func (m model) rigsView() ([]string, int) {
	if len(m.rigs) == 0 {
		return empty("No rigs yet. A rig is one radio you own. Press a to add one (plug it in first to have it detected).")
	}
	lines := []string{dim.Render("  " + col("RIG", 12) + col("RADIO", 30) + col("MODEM", 10) + col("BANDS", 9) + col("PORTS", 24) + "STATE")}
	for i, r := range m.rigs {
		state := r.Hardware
		switch {
		case !r.On:
			state = dim.Render("off")
		case r.Hardware == "NOT DETECTED":
			state = warn.Render("not detected")
		case r.Hardware == "present":
			state = good.Render("on, present")
		default:
			state = "on"
		}
		lines = append(lines, m.cursor(i)+col(r.ID, 12)+col(r.Radio, 30)+col(orDash(r.Modem), 10)+col(orDash(r.Bands), 9)+col(r.Ports, 24)+state)
	}
	return lines, 1 + m.sel[tRigs]
}

func (m model) sitesView() ([]string, int) {
	if len(m.sites) == 0 {
		return empty("No sites yet. A site is a place you operate from (home, the EOC, the club). Press a to save what is set up now as one.")
	}
	lines := []string{dim.Render("  " + col("SITE", 12) + col("RIGS", 18) + col("STATION", 12) + col("GRID", 9) + col("RADIOS HERE", 13))}
	for i, s := range m.sites {
		mark := ""
		if s.Current {
			mark = good.Render("(here)")
		}
		lines = append(lines, m.cursor(i)+col(s.ID, 12)+col(orDash(s.Rigs), 18)+col(orDash(s.Station), 12)+col(orDash(s.Grid), 9)+col(s.Radios, 13)+mark)
	}
	if m.hintID != "" {
		lines = append(lines, "", warn.Render(m.hintWhy+". Press h to switch to "+m.hintID+"."))
	}
	return lines, 1 + m.sel[tSites]
}

func (m model) stationsView() ([]string, int) {
	if len(m.stations) == 0 {
		return empty("No stations yet. A station is a callsign with its own Pat config. Press a to add one, or i to import existing Pat configs.")
	}
	lines := []string{dim.Render("  " + col("STATION", 12) + col("CALLSIGN", 12) + col("WEB UI", 18) + "NAME")}
	for i, s := range m.stations {
		mark := ""
		if s.Active {
			mark = good.Render("  (active)")
		}
		lines = append(lines, m.cursor(i)+col(s.ID, 12)+col(s.Callsign, 12)+col(s.Addr, 18)+s.Name+mark)
	}
	return lines, 1 + m.sel[tStations]
}

func (m model) devicesView() ([]string, int) {
	if len(m.devices) == 0 {
		return empty("No USB serial ports or sound cards found. Plug your radio in: it appears here within a couple of seconds.")
	}
	var lines []string
	selLine, lastGroup := 0, "\x00"
	for i, d := range m.devices {
		if d.Group != lastGroup {
			head := "plugged in directly"
			if d.Group != "" {
				head = "sharing " + d.Group + " (probably one device)"
			}
			if len(lines) > 0 {
				lines = append(lines, "")
			}
			lines = append(lines, dim.Render(head))
			lastGroup = d.Group
		}
		tag := ""
		if m.fresh[d.Key] {
			tag = good.Render(" NEW")
		}
		use := ""
		if d.Assigned != "" {
			use = dim.Render("  -> rig " + d.Assigned)
		}
		if i == m.sel[tDevices] {
			selLine = len(lines)
		}
		lines = append(lines, m.cursor(i)+col(d.Kind, 7)+trunc(d.Label, 44)+tag+use)
		if d.Detail != "" {
			lines = append(lines, "          "+dim.Render(trunc(d.Detail, m.w-12)))
		}
	}
	return lines, selLine
}

// programStatus compares what is installed with the newest upstream release, once that is known.
func (m model) programStatus(p Program) string {
	switch {
	case p.Installed == "":
		return "not installed"
	case p.Installed == "unknown":
		return "version unknown"
	case m.latest == nil:
		return "installed"
	}
	latest, ok := m.latest[p.Name]
	if !ok || latest == "" {
		return "installed"
	}
	if versions.Compare(p.Installed, latest) < 0 {
		return "update available"
	}
	return "up to date"
}

func (m model) programsView() ([]string, int) {
	if len(m.programs) == 0 {
		return empty("loading…")
	}
	lines := []string{dim.Render("  " + col("PROGRAM", 10) + col("INSTALLED", 11) + col("LATEST", 10) + col("STATUS", 18) + col("INSTALLED VIA", 16) + "WHAT IT IS")}
	for i, p := range m.programs {
		latest := "-"
		switch {
		case m.latestLoading:
			latest = "…"
		case m.latest != nil:
			latest = orDash(m.latest[p.Name])
		case m.latestErr != "":
			latest = "?"
		}
		stText := col(m.programStatus(p), 18)
		switch m.programStatus(p) {
		case "up to date":
			stText = good.Render(stText)
		case "update available", "not installed":
			stText = warn.Render(stText)
		}
		lines = append(lines, m.cursor(i)+col(p.Name, 10)+col(orDash(p.Installed), 11)+col(latest, 10)+stText+col(orDash(p.Origin), 16)+p.Desc)
	}
	if m.latestErr != "" {
		lines = append(lines, "", warn.Render("could not check upstream versions: "+trunc(m.latestErr, m.w-40)+"  (r to retry)"))
	}
	return lines, 1 + m.sel[tPrograms]
}

func (m model) settingsView() ([]string, int) {
	if len(m.settings) == 0 {
		return empty("loading…")
	}
	var lines []string
	for i, s := range m.settings {
		val := s.Value
		if val == "" {
			val = dim.Render("(not set)")
		}
		switch s.Kind {
		case SettingToggle:
			if val == "on" {
				val = good.Render("on ")
			} else {
				val = dim.Render("off")
			}
		case SettingExec:
			val = warn.Render(val)
		}
		lines = append(lines, m.cursor(i)+col(s.Label, 28)+val+"  "+dim.Render(s.Hint))
	}
	return lines, m.sel[tSettings]
}

func (m model) doctorView() ([]string, int) {
	if m.results == nil {
		return empty("running checks…")
	}
	var lines []string
	selLine := 0
	for i, r := range m.results {
		mark := good.Render("ok  ")
		switch r.Level {
		case doctor.Warn:
			mark = warn.Render("warn")
		case doctor.Fail:
			mark = bad.Render("FAIL")
		}
		if i == m.sel[tDoctor] {
			selLine = len(lines)
		}
		lines = append(lines, fmt.Sprintf("%s[%s] %s %s", m.cursor(i), mark, col(r.Name, 27), trunc(r.Detail, max(10, m.w-42))))
		if r.Level != doctor.OK && r.Fix != "" && i == m.sel[tDoctor] {
			lines = append(lines, "      "+dim.Render("fix: "+trunc(r.Fix, max(10, m.w-12))))
		}
	}
	return lines, selLine
}

func (m model) logsView() []string {
	name := "-"
	if n := m.serviceNames(); len(n) > 0 {
		name = n[clamp(m.logSvc, len(n))]
	}
	lines := []string{dim.Render("logs: ") + selected.Render(name)}
	body := strings.Split(strings.TrimRight(m.logs, "\n"), "\n")
	room := max(3, m.h-9)
	if len(body) > room {
		body = body[len(body)-room:]
	}
	for _, l := range body {
		lines = append(lines, trunc(l, m.w-1))
	}
	return lines
}

func (m model) promptView() string {
	p := m.pr
	var b strings.Builder
	b.WriteString(selected.Render(p.title) + "\n")
	switch p.kind {
	case pInput:
		b.WriteString(p.input.View() + "\n" + dim.Render("enter accepts, esc cancels"))
	case pConfirm:
		b.WriteString(dim.Render("y yes, anything else cancels"))
	case pChoose:
		for i, o := range p.options {
			c := "  "
			if i == p.sel {
				c = selected.Render("> ")
			}
			b.WriteString(fmt.Sprintf("%s%d) %s\n", c, i+1, trunc(o, m.w-8)))
		}
		b.WriteString(dim.Render("up/down and enter, or the number; esc cancels"))
	}
	return b.String()
}

func (m model) help() string {
	base := "tab/1-9/0 switch · w guided setup · q quit"
	switch m.tab {
	case tServices:
		return "enter start/stop · r restart · e login on/off · l logs · S start all · X stop all · U install services · " + base
	case tRigs:
		return "enter on/off · m modem · d Direwolf settings · a add · i identify (unplug/replug) · x remove · " + base
	case tSites:
		return "enter switch · a save current setup as a site · g grid · h go to matching site · x remove · " + base
	case tStations:
		return "enter switch · a add · i import from Pat · x remove · " + base
	case tDevices:
		return "a use for a rig · i identify a rig's radio · " + base
	case tPrograms:
		return "i install/update · s build from source · I choose programs to install · r check upstream · " + base
	case tSettings:
		return "enter change · " + base
	case tDoctor:
		return "f run the fix · r re-check · " + base
	}
	return "[ ] change service · " + base
}
