package tui

import (
	"fmt"
	"github.com/charmbracelet/x/ansi"
	"strings"
)

// A wider terminal makes room for an inspector without changing navigation.
func (m model) inspectorWidth() int {
	if m.w >= 110 && m.pr == nil && m.count() > 0 && m.tab != tLogs && m.tab != tNetwork {
		return 32
	}
	return 0
}

func (m model) contentBody() ([]string, int) {
	copy := m
	if m.inspectorWidth() > 0 {
		copy.w = m.w - 36
	}
	lines, sel := copy.body()
	if m.inspectorWidth() > 0 && (m.tab == tRigs || m.tab == tSites || m.tab == tStations || m.tab == tPrograms) && copy.w < 75 && len(lines) > m.count()+1 {
		lines = lines[:m.count()+1]
	}
	if m.inspectorWidth() > 0 {
		if m.tab == tSites && m.hintID != "" {
			lines = append(lines, "", warn.Render(m.hintWhy), warn.Render("Press h to switch to "+m.hintID))
		}
		if m.tab == tPrograms && m.latestErr != "" {
			lines = append(lines, "", warn.Render("could not check upstream versions: "+m.latestErr), dim.Render("r to retry"))
		}
	}
	return lines, sel
}

func (m model) details(width int) []string {
	lines := []string{section.Render("SELECTED ITEM"), ""}
	add := func(label, value string) {
		lines = append(lines, dim.Render(strings.ToUpper(label)), orDash(value), "")
	}
	i := clamp(m.cur(), m.count())
	switch m.tab {
	case tServices:
		x := m.svcs[i]
		add("Service", x.Name)
		add("State", x.State)
		add("At login", x.Enabled)
		add("Since", x.Since)
		add("Actions", "Enter start/stop\nr restart\nl view logs")
	case tRigs:
		x := m.rigs[i]
		add("Rig", x.ID)
		add("Radio", x.Radio)
		add("Hardware", x.Hardware)
		add("Modem", x.Modem)
		add("Bands", x.Bands)
		add("Ports", x.Ports)
	case tSites:
		x := m.sites[i]
		add("Site", x.ID)
		add("Rigs", x.Rigs)
		add("Station", x.Station)
		add("Grid", x.Grid)
		add("Radios present", x.Radios)
		if m.hintID != "" {
			add("Detected site", m.hintWhy+". Press h to switch to "+m.hintID+".")
		}
	case tStations:
		x := m.stations[i]
		add("Station", x.ID)
		add("Callsign", x.Callsign)
		add("Name", x.Name)
		add("Web interface", x.Addr)
		add("Access", x.Access)
	case tPrograms:
		x := m.programs[i]
		add("Program", x.Name)
		add("Status", m.programStatus(x))
		add("Installed", x.Installed)
		add("Latest", m.latest[x.Name])
		add("Source", x.Origin)
		add("About", x.Desc)
		if x.Needs != "" {
			add("Needs", x.Needs)
		}
		if x.Suggestions != "" {
			add("Suggested", x.Suggestions)
		}
		if x.OpenPath != "" {
			add("Launch", "o open application")
		}
	case tDevices:
		x := m.devices[i]
		add("Device", x.Label)
		add("Type", x.Kind)
		add("Assigned rig", x.Assigned)
		add("Details", x.Detail)
	case tSettings:
		x := m.settings[i]
		add("Setting", x.Label)
		add("Value", x.Value)
		add("About", x.Hint)
		add("Action", "Enter to change")
	case tDoctor:
		x := m.results[i]
		add("Check", x.Name)
		add("Result", x.Detail)
		add("Fix", x.Fix)
	case tNetwork:
		x := m.network.Rows[i]
		add("Service", x.Service)
		add("Purpose", x.What)
		add("Port", fmt.Sprint(x.Port))
		add("Reachable by", x.Who)
		add("Notes", x.Note)
		if x.URL != "" {
			add("Web interface", x.URL)
			add("Action", "Enter to open browser")
		}
	}
	var wrapped []string
	for _, line := range lines {
		wrapped = append(wrapped, strings.Split(ansi.Wrap(line, width, ""), "\n")...)
	}
	return wrapped
}

func fitLine(line string, width int) string {
	line = ansi.Truncate(line, width, "…")
	return line + strings.Repeat(" ", max(0, width-ansi.StringWidth(line)))
}
