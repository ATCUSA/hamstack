package tui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ATCUSA/hamstack/internal/doctor"
)

// The tabs, in order.
const (
	tServices = iota
	tRigs
	tSites
	tStations
	tDevices
	tPrograms
	tSettings
	tDoctor
	tLogs
	tNetwork
	nTabs
)

var tabNames = [nTabs]string{"Services", "Rigs", "Sites", "Stations", "Devices", "Programs", "Settings", "Doctor", "Logs", "Network"}

type model struct {
	be      Backend
	tab     int
	w, h    int
	sel     [nTabs]int
	network NetworkInfo

	hdr        Header
	needsSetup bool
	svcs       []Service
	rigs       []Rig
	sites      []Site
	stations   []StationRow
	devices    []Device
	programs   []Program
	settings   []Setting
	results    []doctor.Result
	logs       string
	logSvc     int

	hintID, hintWhy string // a site whose radios are the ones plugged in

	seen, fresh map[string]bool // devices seen so far, and those that appeared since
	seenReady   bool

	latest        map[string]string
	latestTried   bool
	latestLoading bool
	latestErr     string

	status    string
	statusErr bool
	busy      bool // an action is running: keys other than tab switches wait
	checking  bool // the doctor checks are running (keys on the Doctor tab wait; the other tabs stay usable)
	pr        *prompt
	lastExec  []string // the last command handed to the terminal (also what tests look at)
}

// New returns the program model.
func New(b Backend) tea.Model {
	return model{be: b, w: 100, h: 30, seen: map[string]bool{}, fresh: map[string]bool{}}
}

// Messages.
type (
	refreshMsg struct {
		hdr             Header
		needsSetup      bool
		svcs            []Service
		rigs            []Rig
		sites           []Site
		stations        []StationRow
		devices         []Device
		network         NetworkInfo
		settings        []Setting
		hintID, hintWhy string
	}
	actionMsg struct {
		text string
		err  error
	}
	doctorMsg []doctor.Result
	logsMsg   struct {
		text string
		err  error
	}
	programsMsg []Program
	latestMsg   struct {
		v   map[string]string
		err error
	}
	execDoneMsg struct{ err error }
	tickMsg     time.Time
)

// tickInterval is how often the screen refreshes itself. It is a variable so tests need not wait.
var tickInterval = 2 * time.Second

func tick() tea.Cmd { return tea.Tick(tickInterval, func(t time.Time) tea.Msg { return tickMsg(t) }) }

func (m model) Init() tea.Cmd { return tea.Batch(m.refresh(), tick()) }

func (m model) refresh() tea.Cmd {
	be := m.be
	return func() tea.Msg {
		r := refreshMsg{hdr: be.Header(), needsSetup: be.NeedsSetup(), svcs: be.Services(), rigs: be.Rigs(), sites: be.Sites(),
			stations: be.Stations(), devices: be.Devices(), network: be.Network(), settings: be.Settings()}
		if id, why, ok := be.SiteHint(); ok {
			r.hintID, r.hintWhy = id, why
		}
		return r
	}
}

func (m model) loadDoctor() tea.Cmd {
	be := m.be
	return func() tea.Msg { return doctorMsg(be.Doctor()) }
}

func (m model) loadPrograms() tea.Cmd {
	be := m.be
	return func() tea.Msg { return programsMsg(be.Programs()) }
}

func (m model) loadLatest() tea.Cmd {
	be := m.be
	return func() tea.Msg {
		v, err := be.Latest()
		return latestMsg{v, err}
	}
}

func (m model) loadLogs() tea.Cmd {
	be, names := m.be, m.serviceNames()
	if len(names) == 0 {
		return nil
	}
	name := names[clamp(m.logSvc, len(names))]
	return func() tea.Msg {
		t, err := be.Logs(name, 200)
		return logsMsg{t, err}
	}
}

func (m model) serviceNames() []string {
	var out []string
	for _, s := range m.svcs {
		out = append(out, s.Name)
	}
	return out
}

func clamp(i, n int) int {
	if n == 0 {
		return 0
	}
	return max(0, min(i, n-1))
}

// count is the number of selectable rows on the current tab.
func (m model) count() int {
	switch m.tab {
	case tServices:
		return len(m.svcs)
	case tRigs:
		return len(m.rigs)
	case tSites:
		return len(m.sites)
	case tStations:
		return len(m.stations)
	case tDevices:
		return len(m.devices)
	case tPrograms:
		return len(m.programs)
	case tSettings:
		return len(m.settings)
	case tDoctor:
		return len(m.results)
	}
	return 0
}

func (m model) clampAll() model {
	for t := 0; t < nTabs; t++ {
		saved := m.tab
		m.tab = t
		m.sel[t] = clamp(m.sel[t], m.count())
		m.tab = saved
	}
	return m
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m2, cmd := m.update(msg)
	return m2, cmd
}

func (m model) update(msg tea.Msg) (model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case refreshMsg:
		m.hdr, m.needsSetup, m.svcs, m.rigs, m.sites = msg.hdr, msg.needsSetup, msg.svcs, msg.rigs, msg.sites
		m.stations, m.devices, m.settings = msg.stations, msg.devices, msg.settings
		m.network = msg.network
		m.hintID, m.hintWhy = msg.hintID, msg.hintWhy
		m = m.noteNewDevices().clampAll()
	case tickMsg:
		cmds := []tea.Cmd{m.refresh(), tick()}
		if m.tab == tLogs {
			cmds = append(cmds, m.loadLogs())
		}
		return m, tea.Batch(cmds...)
	case actionMsg:
		m.busy = false
		m.status, m.statusErr = msg.text, msg.err != nil
		if msg.err != nil {
			m.status = msg.err.Error()
		}
		return m, tea.Batch(m.refresh(), m.loadPrograms())
	case execDoneMsg:
		m.busy = false
		m.status, m.statusErr = "back from the terminal", false
		if msg.err != nil {
			m.status, m.statusErr = "the command ended with an error: "+msg.err.Error(), true
		}
		return m, tea.Batch(m.refresh(), m.loadPrograms(), m.loadDoctor())
	case doctorMsg:
		m.results = msg
		m.checking = false
		if m.status == checkingText {
			m.status = ""
		}
		m = m.clampAll()
	case programsMsg:
		m.programs = msg
		m = m.clampAll()
	case latestMsg:
		m.latestLoading = false
		m.latestErr = ""
		if msg.err != nil {
			m.latestErr = msg.err.Error()
		} else {
			m.latest = msg.v
		}
	case logsMsg:
		if msg.err != nil {
			m.logs = msg.err.Error()
		} else {
			m.logs = msg.text
		}
	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

// noteNewDevices flags devices that appeared after the interface started, so plugging a radio in is
// visible straight away.
func (m model) noteNewDevices() model {
	now := map[string]bool{}
	var added []string
	for _, d := range m.devices {
		now[d.Key] = true
		if m.seenReady && !m.seen[d.Key] {
			m.fresh[d.Key] = true
			added = append(added, d.Label)
		}
	}
	for k := range m.fresh {
		if !now[k] {
			delete(m.fresh, k) // unplugged again
		}
	}
	m.seen, m.seenReady = now, true
	if len(added) > 0 {
		m.status, m.statusErr = "new device: "+strings.Join(added, ", ")+" (see the Devices tab)", false
	}
	return m
}

// do runs an operation in the background and reports its message.
func (m model) do(label string, f func() (string, error)) (model, tea.Cmd) {
	m.busy, m.status, m.statusErr = true, label+"…", false
	return m, func() tea.Msg {
		text, err := f()
		return actionMsg{text, err}
	}
}

// exec hands the terminal to a hamstack command and comes back afterwards.
func (m model) exec(args ...string) (model, tea.Cmd) {
	m.lastExec = args
	m.busy = true
	m.status, m.statusErr = "", false
	return m, tea.ExecProcess(m.be.Command(args...), func(err error) tea.Msg { return execDoneMsg{err} })
}

func (m model) gotoTab(t int) (model, tea.Cmd) {
	if m.tab == tDevices && t != tDevices {
		m.fresh = map[string]bool{} // the "new" marks have been seen
	}
	m.tab = t
	if !m.busy {
		m.status, m.statusErr = "", false // a message about another tab's action is stale here
	}
	switch t {
	case tDoctor:
		return m.startChecks()
	case tLogs:
		return m, m.loadLogs()
	case tPrograms:
		cmds := []tea.Cmd{m.loadPrograms()}
		if !m.latestTried {
			m.latestTried, m.latestLoading = true, true
			cmds = append(cmds, m.loadLatest())
		}
		return m, tea.Batch(cmds...)
	}
	return m, nil
}

const checkingText = "checking…"

// startChecks runs the doctor checks in the background. It has its own flag rather than busy: the checks
// take a few seconds, the user may move to another tab meanwhile, and that tab must stay usable (busy used
// to stay set for good when the results arrived on another tab, and every key but the tab switches died).
func (m model) startChecks() (model, tea.Cmd) {
	m.checking = true
	if !m.busy {
		m.status, m.statusErr = checkingText, false
	}
	return m, m.loadDoctor()
}

// Run starts the interface.
func Run(b Backend) error {
	_, err := tea.NewProgram(New(b), tea.WithAltScreen()).Run()
	return err
}
