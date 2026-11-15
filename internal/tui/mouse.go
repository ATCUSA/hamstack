package tui

import tea "github.com/charmbracelet/bubbletea"

// Mouse clicks select rows; actions still require a deliberate key press.
func (m model) mouse(ev tea.MouseMsg) (model, tea.Cmd) {
	if ev.Button == tea.MouseButtonWheelUp {
		return m.key(tea.KeyMsg{Type: tea.KeyUp})
	}
	if ev.Button == tea.MouseButtonWheelDown {
		return m.key(tea.KeyMsg{Type: tea.KeyDown})
	}
	if ev.Button != tea.MouseButtonLeft || ev.Action != tea.MouseActionPress || m.helpOpen {
		return m, nil
	}
	if m.pr != nil {
		if m.pr.kind == pChoose {
			limit := max(1, m.h-7)
			start := max(0, min(m.pr.sel-limit/2, max(0, len(m.pr.options)-limit)))
			// Prompts follow the visible body and optional status.
			reserve := 5 + stringsCount(m.promptView()) + 1
			if m.status != "" {
				reserve++
			}
			body := max(1, m.h-reserve)
			y := 3 + body
			if m.status != "" {
				y++
			}
			idx := start + ev.Y - y - 1
			if idx >= start && idx < min(len(m.pr.options), start+limit) {
				m.pr.sel = idx
			}
		}
		return m, nil
	}
	if ev.Y == 1 {
		// Derive the same tab widths as the rendered tab bar.
		variants := [][]int{}
		for mode := 0; mode < 3; mode++ {
			widths := []int{}
			total := 0
			for i := 0; i < nTabs; i++ {
				w := 0
				switch mode {
				case 0:
					w = 4 + len(tabNames[i])
				case 1:
					w = 2 + len(tabNames[i])
				case 2:
					w = 3 + min(3, len(tabNames[i]))
				}
				widths = append(widths, w)
				total += w
			}
			if total <= m.w {
				variants = append(variants, widths)
				break
			}
		}
		if len(variants) > 0 {
			x := 0
			for i, w := range variants[0] {
				if ev.X >= x && ev.X < x+w {
					return m.gotoTab(i)
				}
				x += w
			}
		}
		return m, nil
	}
	lines, sel := m.contentBody()
	avail := max(1, m.h-5)
	if m.status != "" {
		avail--
	}
	if m.inspectorWidth() > 0 && ev.X >= m.w-m.inspectorWidth()-3 {
		return m, nil
	}
	y := ev.Y - 3
	if y < 0 {
		return m, nil
	}
	offset := 0
	header := false
	if m.tab == tLogs {
		return m, nil
	}
	if len(lines) > avail && avail > 2 && (m.tab == tRigs || m.tab == tSites || m.tab == tStations || m.tab == tPrograms || m.tab == tNetwork || (m.tab == tServices && !m.needsSetup)) {
		header = true
		offset = max(0, min(max(0, sel-1)-(avail-1)/2, len(lines)-1-(avail-1)))
	} else if len(lines) > avail {
		offset = max(0, min(sel-avail/2, len(lines)-avail))
	}
	line := y + offset
	if header {
		if y == 0 {
			return m, nil
		}
		line = y + offset
	}
	for i := 0; i < m.count(); i++ {
		copy := m
		copy.sel[m.tab] = i
		_, row := copy.contentBody()
		if row == line {
			m.sel[m.tab] = i
			break
		}
	}
	return m, nil
}
func stringsCount(s string) int {
	n := 0
	for _, r := range s {
		if r == '\n' {
			n++
		}
	}
	return n
}
