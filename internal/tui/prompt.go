package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

const (
	pInput = iota
	pConfirm
	pChoose
)

// prompt is a question shown at the bottom of the screen: free text, yes or no, or a pick from a list.
// While one is open it gets every key.
type prompt struct {
	kind    int
	title   string
	input   textinput.Model
	options []string
	sel     int
	done    func(m model, text string, idx int) (model, tea.Cmd)
}

func (m model) askInput(title, initial string, done func(model, string, int) (model, tea.Cmd)) model {
	ti := textinput.New()
	ti.SetValue(initial)
	ti.CharLimit = 64
	ti.Width = 32
	ti.Focus()
	m.pr = &prompt{kind: pInput, title: title, input: ti, done: done}
	return m
}

func (m model) askConfirm(title string, yes func(model) (model, tea.Cmd)) model {
	m.pr = &prompt{kind: pConfirm, title: title, done: func(m model, _ string, _ int) (model, tea.Cmd) { return yes(m) }}
	return m
}

func (m model) askChoose(title string, opts []string, done func(model, string, int) (model, tea.Cmd)) model {
	m.pr = &prompt{kind: pChoose, title: title, options: opts, done: done}
	return m
}

// runeIs reports whether k is exactly the single typed character c.
func runeIs(k tea.KeyMsg, c string) bool { return k.Type == tea.KeyRunes && string(k.Runes) == c }

// promptKey handles a key while a prompt is open. It goes by the kind of key, never by its text: a
// burst of typed or pasted characters spelling "esc" or "enter" is text, not that key.
func (m model) promptKey(k tea.KeyMsg) (model, tea.Cmd) {
	p := m.pr
	if k.Type == tea.KeyEsc || k.Type == tea.KeyCtrlC {
		m.pr = nil
		return m, nil
	}
	switch p.kind {
	case pInput:
		if k.Type == tea.KeyEnter {
			text := strings.TrimSpace(p.input.Value())
			m.pr = nil
			if text == "" {
				return m, nil // nothing entered is a cancel
			}
			return p.done(m, text, 0)
		}
		if k.Type == tea.KeyRunes {
			// Typed or pasted characters are text, whatever they spell. The text input would otherwise match
			// them against its key bindings by name, so a burst of the letters h-o-m-e acts as the Home key
			// and inserts nothing (and likewise "end" or "left").
			runes := []rune(p.input.Value())
			pos := min(max(p.input.Position(), 0), len(runes))
			ins := k.Runes
			p.input.SetValue(string(runes[:pos]) + string(ins) + string(runes[pos:]))
			p.input.SetCursor(pos + len(ins))
			return m, nil
		}
		var cmd tea.Cmd
		p.input, cmd = p.input.Update(k) // editing keys: backspace, arrows, delete...
		return m, cmd
	case pConfirm:
		m.pr = nil
		if runeIs(k, "y") || runeIs(k, "Y") {
			return p.done(m, "", 0)
		}
		return m, nil
	case pChoose:
		switch {
		case k.Type == tea.KeyUp || runeIs(k, "k"):
			p.sel = clamp(p.sel-1, len(p.options))
		case k.Type == tea.KeyDown || runeIs(k, "j"):
			p.sel = clamp(p.sel+1, len(p.options))
		case k.Type == tea.KeyEnter:
			m.pr = nil
			if len(p.options) == 0 {
				return m, nil
			}
			return p.done(m, p.options[p.sel], p.sel)
		case k.Type == tea.KeyRunes && len(k.Runes) == 1 && k.Runes[0] >= '1' && k.Runes[0] <= '9' && int(k.Runes[0]-'1') < len(p.options):
			i := int(k.Runes[0] - '1')
			m.pr = nil
			return p.done(m, p.options[i], i)
		}
	}
	return m, nil
}
