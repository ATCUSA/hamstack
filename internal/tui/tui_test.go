package tui

import (
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ATCUSA/hamstack/internal/doctor"
)

// fake is a Backend that records what it was asked to do.
type fake struct {
	calls []string
	fail  map[string]error

	needsSetup bool
	svcs       []Service
	rigs       []Rig
	profiles   []ProfileOpt
	sites      []Site
	hintID     string
	stations   []StationRow
	devices    []Device
	network    NetworkInfo
	hits       []RadioHit
	programs   []Program
	latest     map[string]string
	settings   []Setting
	results    []doctor.Result
}

func init() { tickInterval = time.Millisecond }

func (f *fake) rec(name string, args ...string) (string, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	if err := f.fail[name]; err != nil {
		return "", err
	}
	return name + " ok", nil
}

func (f *fake) Header() Header {
	return Header{Site: "home", Rigs: "hf", Station: "me (K0TEST)", Grid: "EM10dg"}
}
func (f *fake) NeedsSetup() bool    { return f.needsSetup }
func (f *fake) Services() []Service { return f.svcs }
func (f *fake) Control(verb, service string) error {
	_, err := f.rec("control", verb, service)
	return err
}
func (f *fake) UnitsInstall() (string, error)              { return f.rec("unitsinstall") }
func (f *fake) Rigs() []Rig                                { return f.rigs }
func (f *fake) Profiles() []ProfileOpt                     { return f.profiles }
func (f *fake) RigToggle(id string) (string, error)        { return f.rec("rigtoggle", id) }
func (f *fake) RigModem(id, modem string) (string, error)  { return f.rec("rigmodem", id, modem) }
func (f *fake) RigAdd(p, id, modem string) (string, error) { return f.rec("rigadd", p, id, modem) }
func (f *fake) RigRemove(id string) (string, error)        { return f.rec("rigremove", id) }
func (f *fake) Sites() []Site                              { return f.sites }
func (f *fake) SiteHint() (string, string, bool) {
	return f.hintID, "the radios of site " + f.hintID + " are plugged in", f.hintID != ""
}
func (f *fake) SiteUse(id string) (string, error)       { return f.rec("siteuse", id) }
func (f *fake) SiteAdd(id string) (string, error)       { return f.rec("siteadd", id) }
func (f *fake) SiteRemove(id string) (string, error)    { return f.rec("siteremove", id) }
func (f *fake) SiteGrid(id, g string) (string, error)   { return f.rec("sitegrid", id, g) }
func (f *fake) Stations() []StationRow                  { return f.stations }
func (f *fake) Use(id string) (string, error)           { return f.rec("use", id) }
func (f *fake) StationAdd(id, c string) (string, error) { return f.rec("stationadd", id, c) }
func (f *fake) StationImport() (string, error)          { return f.rec("stationimport") }
func (f *fake) StationRemove(id string) (string, error) { return f.rec("stationremove", id) }
func (f *fake) Devices() []Device                       { return f.devices }
func (f *fake) Network() NetworkInfo                    { return f.network }
func (f *fake) SearchRadios(q string) ([]RadioHit, error) {
	f.calls = append(f.calls, "search:"+q)
	var out []RadioHit
	for _, h := range f.hits {
		if q == "" || strings.Contains(strings.ToLower(h.Label), strings.ToLower(q)) {
			out = append(out, h)
		}
	}
	return out, nil
}
func (f *fake) RadioBaud(model int) (int, int, int) {
	if model == 4 {
		return 0, 0, 0
	}
	return 4800, 38400, 9600
}
func (f *fake) SaveRadio(model, baud int) (string, error) {
	f.calls = append(f.calls, fmt.Sprintf("save:%d:%d", model, baud))
	return fmt.Sprintf("radio-%d", model), nil
}
func (f *fake) AssignDevice(k, r string) (string, error) { return f.rec("assign", k, r) }
func (f *fake) Programs() []Program                      { return f.programs }
func (f *fake) Latest() (map[string]string, error) {
	f.calls = append(f.calls, "latest")
	return f.latest, f.fail["latest"]
}
func (f *fake) Settings() []Setting                    { return f.settings }
func (f *fake) SetSetting(k, v string) (string, error) { return f.rec("setting", k, v) }
func (f *fake) Doctor() []doctor.Result                { return f.results }
func (f *fake) Logs(s string, n int) (string, error)   { return "log line for " + s, nil }
func (f *fake) Command(args ...string) *exec.Cmd       { return exec.Command("true") }

func newFake() *fake {
	return &fake{
		fail: map[string]error{},
		svcs: []Service{{Name: "rigctld@hf", State: "inactive", Installed: true}, {Name: "mercury@hf", State: "active", Installed: true}, {Name: "pat", State: "inactive", Installed: true}},
		rigs: []Rig{
			{ID: "hf", Radio: "Icom IC-7300 MK2", Modem: "mercury", Bands: "hf", Ports: "rig 4532, modem 8300", Hardware: "present", On: true},
			{ID: "dig", Radio: "Digirig", Modem: "direwolf", Ports: "packet 8002", Hardware: "NOT DETECTED", On: true, NoCAT: true},
		},
		profiles: []ProfileOpt{{ID: "ic7300mk2", Name: "Icom IC-7300 MK2", HasCAT: true}, {ID: "digirig", Name: "Digirig", HasCAT: false}},
		sites:    []Site{{ID: "home", Rigs: "hf", Station: "me", Grid: "EM10dg", Radios: "1 of 1", Current: true}, {ID: "eoc", Rigs: "dig", Station: "eoc", Grid: "FN31pr", Radios: "0 of 1"}},
		stations: []StationRow{{ID: "me", Callsign: "K0TEST", Addr: "localhost:8080", Active: true}, {ID: "eoc", Callsign: "K0EOC", Addr: "localhost:8081"}},
		devices: []Device{
			{Kind: "serial", Key: "serial|/dev/ttyACM0", Label: "Icom IC-7300MK2 [0c26:0052]", Detail: "stable name: usb-Icom-if00", Group: "hub 7-1", Assigned: "hf"},
			{Kind: "sound", Key: "sound|Device", Label: "card 2 [Device] USB Audio Device", Group: "hub 7-1"},
		},
		programs: []Program{
			{Name: "hamlib", Desc: "rig control", Installed: "4.6.2", Origin: "system package", CanSource: true, UpdateHint: "sudo apt-get upgrade"},
			{Name: "pat", Desc: "Winlink client", Installed: "1.0.1", Origin: "user install", UserInstall: true},
			{Name: "mercury", Desc: "HF modem", Installed: "1.9.22", Origin: "user install", UserInstall: true},
			{Name: "direwolf", Desc: "packet TNC", Installed: "", CanSource: true},
		},
		latest: map[string]string{"hamlib": "4.7.2", "pat": "1.0.1", "mercury": "1.9.23", "direwolf": "1.8.1"},
		settings: []Setting{
			{Key: "pat", Label: "Pat (Winlink client)", Value: "on", Kind: SettingToggle},
			{Key: "grid", Label: "Grid square", Value: "EM10dg", Kind: SettingText},
			{Key: "linger", Label: "Start at boot", Value: "off", Kind: SettingExec, Args: []string{"linger", "on"}},
		},
		results: []doctor.Result{
			{Name: "programs", Level: doctor.OK, Detail: "fine"},
			{Name: "serial port permission", Level: doctor.Warn, Detail: "not in the uucp group", Fix: "hamstack groups fix"},
			{Name: "radio profile", Level: doctor.Fail, Detail: "none", Fix: "run: hamstack station add ID --callsign YOURCALL"},
			{Name: "Pat locator", Level: doctor.Warn, Detail: "differs", Fix: "update just that field (a backup is kept): hamstack pat locator"},
			{Name: "note", Level: doctor.Warn, Detail: "x"},
		},
	}
}

// settle feeds msg to the model and runs the commands that come back until nothing is left to do. Ticks
// are dropped (they would re-arm forever) and so are terminal hand-offs, which cannot run in a test.
func settle(m tea.Model, msg tea.Msg) tea.Model {
	m, cmd := m.Update(msg)
	return runCmd(m, cmd)
}

// runCmd runs a command and everything it leads to.
func runCmd(m tea.Model, cmd tea.Cmd) tea.Model {
	queue := []tea.Cmd{cmd}
	for steps := 0; len(queue) > 0 && steps < 200; steps++ {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		out := c()
		switch v := out.(type) {
		case nil:
		case tea.BatchMsg:
			queue = append(queue, v...)
		case tickMsg:
		default:
			var next tea.Cmd
			m, next = m.Update(out)
			queue = append(queue, next)
		}
	}
	return m
}

func k(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// start returns a model that has loaded its data, like one a moment after launch.
func start(t *testing.T, f *fake) tea.Model {
	t.Helper()
	m := New(f)
	m = settle(m, tea.WindowSizeMsg{Width: 110, Height: 34})
	return runCmd(m, m.Init())
}

func press(m tea.Model, keys ...string) tea.Model {
	for _, s := range keys {
		m = settle(m, k(s))
	}
	return m
}

func typeText(m tea.Model, s string) tea.Model {
	for _, r := range s {
		m = settle(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return m
}

func lastExec(m tea.Model) []string { return m.(model).lastExec }
func status(m tea.Model) string     { return m.(model).status }

func called(f *fake, want string) bool { return slices.Contains(f.calls, want) }

func TestTabsAndNavigation(t *testing.T) {
	f := newFake()
	m := start(t, f)
	for i, name := range tabNames {
		key := string(rune('1' + i))
		if i == 9 {
			key = "0"
		}
		m = press(m, key)
		if m.(model).tab != i {
			t.Fatalf("key %d should open %s", i+1, name)
		}
		if v := m.View(); !strings.Contains(v, "hamstack") {
			t.Errorf("%s: header missing", name)
		}
	}
	m = press(m, "1", "shift+tab")
	if m.(model).tab != nTabs-1 {
		t.Error("shift+tab from the first tab wraps to the last")
	}
	m = press(m, "tab")
	if m.(model).tab != 0 {
		t.Error("tab from the last tab wraps to the first")
	}
}

func TestTabBarFitsTheTerminal(t *testing.T) {
	m := New(newFake()).(model)
	for _, w := range []int{40, 60, 80, 100, 140} {
		m.w = w
		if got := lipgloss.Width(m.tabBar()); got > w {
			t.Errorf("width %d: tab bar is %d wide", w, got)
		}
	}
}

func TestServices(t *testing.T) {
	f := newFake()
	m := start(t, f)
	m = press(m, "enter", "down", "enter", "S", "X")
	want := []string{"control start rigctld@hf", "control stop mercury@hf", "control start all", "control stop all"}
	if !slices.Equal(f.calls, want) {
		t.Errorf("calls = %v, want %v", f.calls, want)
	}
}

func TestInstallServicesFromTheInterface(t *testing.T) {
	f := newFake()
	for i := range f.svcs {
		f.svcs[i].Installed, f.svcs[i].State = false, "not installed"
	}
	m := start(t, f)
	if v := m.View(); !strings.Contains(v, "press U to install them") {
		t.Errorf("a fresh setup must say how to install the services:\n%s", v)
	}
	m = press(m, "U")
	if !called(f, "unitsinstall ") {
		t.Errorf("U installs the services: %v", f.calls)
	}
}

func TestErrorShownAndStillUsable(t *testing.T) {
	f := newFake()
	f.fail["rigtoggle"] = errors.New("boom")
	m := start(t, f)
	m = press(m, "2", "enter")
	if !strings.Contains(m.View(), "boom") {
		t.Errorf("the error must be shown:\n%s", m.View())
	}
	f.fail["rigtoggle"] = nil
	m = press(m, "enter")
	if len(f.calls) != 2 {
		t.Errorf("the interface must not stay stuck after an error: %v", f.calls)
	}
}

func TestBusyBlocksKeysUntilTheOperationEnds(t *testing.T) {
	f := newFake()
	m := New(f)
	m = runCmd(m, m.Init())
	mm := m.(model)
	mm.busy = true
	if got, _ := mm.key(k("S")); len(f.calls) != 0 || got.status != "" {
		t.Errorf("a key while busy must do nothing: %v", f.calls)
	}
	if _, cmd := mm.key(k("3")); cmd != nil {
		_ = cmd
	}
}

func TestRigs(t *testing.T) {
	f := newFake()
	m := start(t, f)
	m = press(m, "2", "enter")
	if !called(f, "rigtoggle hf") {
		t.Errorf("enter toggles the rig: %v", f.calls)
	}
	m = press(m, "m") // hf has mercury, next is direwolf
	if !called(f, "rigmodem hf direwolf") {
		t.Errorf("m cycles the modem: %v", f.calls)
	}
	m = press(m, "down", "m") // dig is direwolf, next is none
	if !called(f, "rigmodem dig ") {
		t.Errorf("direwolf cycles to none: %v", f.calls)
	}
	f.rigs[1].Modem = ""
	m = runCmd(m, m.Init())
	m = press(m, "m") // none -> mercury, but dig has no CAT: skip to direwolf
	if !called(f, "rigmodem dig direwolf") {
		t.Errorf("a radio with no CAT cannot take Mercury, so the cycle skips it: %v", f.calls)
	}
	m = press(m, "i")
	if !slices.Equal(lastExec(m), []string{"rig", "identify", "dig"}) {
		t.Errorf("identify needs the terminal: %v", lastExec(m))
	}
}

func TestAddRigFlow(t *testing.T) {
	f := newFake()
	m := start(t, f)
	m = press(m, "2", "a")
	if m.(model).pr == nil || !strings.Contains(m.View(), "Which radio?") {
		t.Fatalf("a asks for the radio:\n%s", m.View())
	}
	m = press(m, "2") // Digirig, which has no CAT
	if !strings.Contains(m.View(), "Name this rig") {
		t.Fatalf("then a name:\n%s", m.View())
	}
	if !strings.Contains(m.View(), "digirig") {
		t.Errorf("the name is suggested from the radio:\n%s", m.View())
	}
	m = press(m, "enter")
	v := m.View()
	i := strings.Index(v, "Modem for digirig")
	if i < 0 || strings.Contains(v[i:], "mercury") { // the rig table above also says mercury, so look only at the prompt
		t.Errorf("Mercury is keyed through rigctld, so a radio with no CAT is not offered it:\n%s", v)
	}
	m = press(m, "2") // none, direwolf: 2 = direwolf
	if !called(f, "rigadd digirig digirig direwolf") {
		t.Errorf("calls = %v", f.calls)
	}
}

func TestAddRigSuggestsAFreeName(t *testing.T) {
	f := newFake()
	f.rigs[0].ID = "ic7300mk2" // the name the suggestion would use is taken
	m := start(t, f)
	m = press(m, "2", "a", "1")
	if !strings.Contains(m.View(), "ic7300mk2-2") {
		t.Errorf("a taken name must not be suggested:\n%s", m.View())
	}
}

func TestPromptCancelAndEmpty(t *testing.T) {
	f := newFake()
	m := start(t, f)
	m = press(m, "2", "a", "esc")
	if m.(model).pr != nil {
		t.Error("esc cancels a prompt")
	}
	m = press(m, "x") // confirm remove
	if m.(model).pr == nil {
		t.Fatal("x asks first")
	}
	m = press(m, "n")
	if m.(model).pr != nil || len(f.calls) != 0 {
		t.Errorf("anything but y cancels a confirmation: %v", f.calls)
	}
	m = press(m, "x", "y")
	if !called(f, "rigremove hf") {
		t.Errorf("y confirms: %v", f.calls)
	}
	m = press(m, "3", "a") // site id input
	m = press(m, "enter")  // nothing typed
	if m.(model).pr != nil || called(f, "siteadd ") {
		t.Error("an empty answer is a cancel")
	}
}

func TestSites(t *testing.T) {
	f := newFake()
	f.hintID = "eoc"
	m := start(t, f)
	m = press(m, "3")
	if !strings.Contains(m.View(), "Press h to switch to eoc") {
		t.Errorf("the hint is shown:\n%s", m.View())
	}
	m = press(m, "enter")
	if len(f.calls) != 0 || !strings.Contains(status(m), "already at home") {
		t.Errorf("switching to where you are does nothing: %v %q", f.calls, status(m))
	}
	m = press(m, "down", "enter")
	if !called(f, "siteuse eoc") {
		t.Errorf("enter switches: %v", f.calls)
	}
	m = press(m, "h", "y")
	if !called(f, "siteuse eoc") || len(f.calls) != 2 {
		t.Errorf("h goes to the matching site after confirming: %v", f.calls)
	}
	m = press(m, "a")
	m = typeText(m, "club")
	m = press(m, "enter")
	if !called(f, "siteadd club") {
		t.Errorf("a saves the current setup under the typed name: %v", f.calls)
	}
	m = press(m, "g")
	if !strings.Contains(m.View(), "FN31pr") {
		t.Errorf("the grid prompt starts from the current grid:\n%s", m.View())
	}
	m = press(m, "esc")
	f.hintID = ""
	m = runCmd(m, m.Init())
	m = press(m, "h")
	if !strings.Contains(status(m), "do not point to a different site") {
		t.Errorf("no hint, no switch: %q", status(m))
	}
}

func TestStations(t *testing.T) {
	f := newFake()
	m := start(t, f)
	m = press(m, "4", "down", "enter")
	if !called(f, "use eoc") {
		t.Errorf("enter switches: %v", f.calls)
	}
	m = press(m, "a")
	m = typeText(m, "K1ABC")
	m = press(m, "enter")
	if !strings.Contains(m.View(), "k1abc") {
		t.Errorf("the short name is suggested from the callsign:\n%s", m.View())
	}
	m = press(m, "enter")
	if !called(f, "stationadd k1abc K1ABC") {
		t.Errorf("calls = %v", f.calls)
	}
	m = press(m, "i", "y")
	if !called(f, "stationimport ") {
		t.Errorf("i imports after confirming: %v", f.calls)
	}
	m = press(m, "x", "y")
	if !called(f, "stationremove eoc") {
		t.Errorf("x removes after confirming: %v", f.calls)
	}
}

func TestNewDevicesAreFlaggedAndAssignable(t *testing.T) {
	f := newFake()
	m := start(t, f)
	m = press(m, "5")
	if strings.Contains(m.View(), "NEW") {
		t.Errorf("devices that were there at the start are not new:\n%s", m.View())
	}
	f.devices = append(f.devices, Device{Kind: "serial", Key: "serial|/dev/ttyUSB0", Label: "Silicon Labs CP2102N [10c4:ea60]"})
	m = runCmd(m, m.Init())
	if v := m.View(); !strings.Contains(v, "NEW") || !strings.Contains(status(m), "new device: Silicon Labs") {
		t.Errorf("a device plugged in while the interface is open is flagged:\n%s\n%q", v, status(m))
	}
	m = press(m, "1", "5")
	if strings.Contains(m.View(), "NEW") {
		t.Error("the flag clears once you have looked at it")
	}
	f.devices = f.devices[:2]
	m = runCmd(m, m.Init())
	m = press(m, "a", "2")
	if !called(f, "assign serial|/dev/ttyACM0 dig") {
		t.Errorf("a offers the rigs and assigns the chosen one: %v", f.calls)
	}
	m = press(m, "i", "1")
	if !slices.Equal(lastExec(m), []string{"rig", "identify", "hf"}) {
		t.Errorf("i hands the terminal over for the unplug and replug: %v", lastExec(m))
	}
}

func TestDevicesNeedARigFirst(t *testing.T) {
	f := newFake()
	f.rigs = nil
	m := start(t, f)
	m = press(m, "5", "a")
	if m.(model).pr != nil || !strings.Contains(status(m), "add a rig first") {
		t.Errorf("without a rig there is nothing to assign to: %q", status(m))
	}
}

func TestPrograms(t *testing.T) {
	f := newFake()
	m := start(t, f)
	m = press(m, "6")
	if !called(f, "latest") {
		t.Error("upstream versions are fetched when the tab opens")
	}
	v := m.View()
	for _, want := range []string{"update available", "up to date", "not installed", "4.7.2"} {
		if !strings.Contains(v, want) {
			t.Errorf("programs view lacks %q:\n%s", want, v)
		}
	}
	// hamlib is owned by the package manager: no terminal, just the way to update it.
	m = press(m, "i")
	if lastExec(m) != nil || !strings.Contains(status(m), "sudo apt-get upgrade") {
		t.Errorf("a package-manager program says how to update, and runs nothing: %v %q", lastExec(m), status(m))
	}
	m = press(m, "down", "i") // pat: up to date -> offer reinstall
	if !strings.Contains(m.View(), "up to date. Reinstall") {
		t.Errorf("%s", m.View())
	}
	m = press(m, "n", "down", "i", "y") // mercury: update available, user install
	if !slices.Equal(lastExec(m), []string{"update"}) {
		t.Errorf("a hamstack-installed program updates through 'update': %v", lastExec(m))
	}
	m = settle(m, execDoneMsg{})
	m = press(m, "down", "i", "y") // direwolf: not installed
	if !slices.Equal(lastExec(m), []string{"install", "direwolf"}) {
		t.Errorf("a missing program is installed in the terminal: %v", lastExec(m))
	}
	m = settle(m, execDoneMsg{})
	m = press(m, "s", "y")
	if !slices.Equal(lastExec(m), []string{"install", "direwolf", "--from-source"}) {
		t.Errorf("s builds from source: %v", lastExec(m))
	}
	m = settle(m, execDoneMsg{})
	m = press(m, "up", "up", "s")
	if !strings.Contains(status(m), "cannot be built from source") {
		t.Errorf("a program with no recipe cannot be built from source: %q", status(m))
	}
	m = press(m, "I", "y")
	if !slices.Equal(lastExec(m), []string{"install"}) {
		t.Errorf("I installs what the setup needs: %v", lastExec(m))
	}
}

func TestProgramStatus(t *testing.T) {
	m := New(newFake()).(model)
	p := Program{Name: "pat", Installed: "1.0.1"}
	if got := m.programStatus(p); got != "installed" {
		t.Errorf("before upstream is known: %s", got)
	}
	m.latest = map[string]string{"pat": "1.0.2"}
	if got := m.programStatus(p); got != "update available" {
		t.Errorf("1.0.1 < 1.0.2: %s", got)
	}
	m.latest = map[string]string{"pat": "1.0.1"}
	if got := m.programStatus(p); got != "up to date" {
		t.Errorf("%s", got)
	}
	m.latest = map[string]string{"pat": "1.0"}
	if got := m.programStatus(p); got != "up to date" {
		t.Errorf("1.0.1 is not older than 1.0: %s", got)
	}
	if got := m.programStatus(Program{Name: "x"}); got != "not installed" {
		t.Errorf("%s", got)
	}
	if got := m.programStatus(Program{Name: "x", Installed: "unknown"}); got != "version unknown" {
		t.Errorf("%s", got)
	}
}

func TestUpstreamCheckFailureIsShownNotFatal(t *testing.T) {
	f := newFake()
	f.fail["latest"] = errors.New("no network")
	f.latest = nil
	m := start(t, f)
	m = press(m, "6")
	v := m.View()
	if !strings.Contains(v, "could not check upstream versions: no network") || !strings.Contains(v, "hamlib") {
		t.Errorf("the programs still show, with the reason:\n%s", v)
	}
	f.fail["latest"] = nil
	f.latest = map[string]string{"hamlib": "4.7.2"}
	m = press(m, "r")
	if strings.Contains(m.View(), "could not check") {
		t.Error("r retries")
	}
}

func TestSettings(t *testing.T) {
	f := newFake()
	m := start(t, f)
	m = press(m, "7", "enter")
	if !called(f, "setting pat ") {
		t.Errorf("a toggle flips on enter: %v", f.calls)
	}
	m = press(m, "down", "enter")
	m = press(m, "esc") // text row opens an input
	m = press(m, "enter")
	m = typeText(m, "") // keep the current value
	m = press(m, "enter")
	if !called(f, "setting grid EM10dg") {
		t.Errorf("a text row asks for the value, starting from the current one: %v", f.calls)
	}
	m = press(m, "down", "enter")
	if !strings.Contains(m.View(), "[hamstack linger on]") {
		t.Errorf("a command row shows exactly what it will run:\n%s", m.View())
	}
	m = press(m, "y")
	if !slices.Equal(lastExec(m), []string{"linger", "on"}) {
		t.Errorf("%v", lastExec(m))
	}
}

func TestDoctorFix(t *testing.T) {
	f := newFake()
	m := start(t, f)
	m = press(m, "8")
	if len(m.(model).results) != 5 {
		t.Fatalf("results not loaded: %v", m.(model).results)
	}
	m = press(m, "down", "f")
	if !strings.Contains(m.View(), "[hamstack groups fix]") {
		t.Errorf("the fix command is shown before it runs:\n%s", m.View())
	}
	m = press(m, "y")
	if !slices.Equal(lastExec(m), []string{"groups", "fix"}) {
		t.Errorf("%v", lastExec(m))
	}
	m = settle(m, execDoneMsg{})
	m = press(m, "down", "f") // "station add ID --callsign YOURCALL" needs something filled in
	if lastExec(m) == nil || m.(model).pr != nil || !strings.Contains(status(m), "no command to run") {
		t.Errorf("a fix that needs input is not runnable: %q", status(m))
	}
	m = press(m, "down", "f", "y")
	if !slices.Equal(lastExec(m), []string{"pat", "locator"}) {
		t.Errorf("%v", lastExec(m))
	}
	m = settle(m, execDoneMsg{})
	m = press(m, "down", "f")
	if !strings.Contains(status(m), "nothing to fix") {
		t.Errorf("%q", status(m))
	}
}

func TestWelcomeAndGuidedSetup(t *testing.T) {
	f := newFake()
	f.needsSetup = true
	f.svcs, f.rigs, f.stations = nil, nil, nil
	m := start(t, f)
	v := m.View()
	if !strings.Contains(v, "Welcome to hamstack") || !strings.Contains(v, "guided setup") {
		t.Errorf("a fresh install is welcomed, not shown an empty table:\n%s", v)
	}
	m = press(m, "w")
	if !slices.Equal(lastExec(m), []string{"setup", "winlink"}) {
		t.Errorf("w runs the guided setup in the terminal: %v", lastExec(m))
	}
	m = settle(m, execDoneMsg{})
	f.needsSetup = false
	m = runCmd(m, m.Init())
	if strings.Contains(m.View(), "Welcome") {
		t.Error("the welcome goes away once something is set up")
	}
}

func TestTerminalHandOffReportsFailure(t *testing.T) {
	f := newFake()
	m := start(t, f)
	m = press(m, "w")
	m = settle(m, execDoneMsg{err: errors.New("exit status 1")})
	if !strings.Contains(status(m), "ended with an error") || m.(model).busy {
		t.Errorf("a failed command is reported and the interface is usable again: %q busy=%v", status(m), m.(model).busy)
	}
}

func TestLogs(t *testing.T) {
	f := newFake()
	m := start(t, f)
	m = press(m, "down", "l")
	if m.(model).tab != tLogs || !strings.Contains(m.View(), "log line for mercury@hf") {
		t.Errorf("l opens the logs of the selected service:\n%s", m.View())
	}
	m = press(m, "]")
	if !strings.Contains(m.View(), "log line for pat") {
		t.Errorf("] moves to the next service:\n%s", m.View())
	}
}

// tabKey is the key that opens tab i: 1 to 9, and 0 for the tenth.
func tabKey(i int) string {
	if i == 9 {
		return "0"
	}
	return string(rune('1' + i))
}

func TestEveryTabRendersAtAwkwardSizes(t *testing.T) {
	f := newFake()
	for _, size := range [][2]int{{0, 0}, {1, 1}, {3, 2}, {12, 4}, {20, 6}, {40, 10}, {80, 24}, {200, 60}} {
		m := New(f)
		m = settle(m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m = runCmd(m, m.Init())
		check := func(what string) {
			t.Helper()
			v := m.View() // must not panic
			for _, line := range strings.Split(v, "\n") {
				if lipgloss.Width(line) > size[0]+60 { // styling and wide runes aside, nothing may run far past the edge
					t.Errorf("%dx%d %s: a line is %d wide: %q", size[0], size[1], what, lipgloss.Width(line), line)
				}
			}
		}
		for i := range tabNames {
			m = press(m, tabKey(i))
			if m.(model).tab != i {
				t.Fatalf("%dx%d: key %s did not open %s", size[0], size[1], tabKey(i), tabNames[i])
			}
			check("tab " + tabNames[i])
			m = press(m, "down", "down", "end", "up", "home")
			check("tab " + tabNames[i] + " after moving")
		}
		// The three kinds of prompt, too: a list to choose from, a text input and a confirmation.
		m = press(m, "2", "a")
		check("the radio list")
		m = press(m, "enter")
		check("the name prompt")
		m = press(m, "esc", "x")
		check("the remove confirmation")
		m = press(m, "esc")
		if lipgloss.Width(m.(model).headerLine()) > max(10, size[0]) {
			t.Errorf("%dx%d: the header line is %d wide", size[0], size[1], lipgloss.Width(m.(model).headerLine()))
		}
	}
}

// Moving to another tab while the doctor checks run used to leave the interface deaf for good: busy was only
// cleared if the results arrived while the Doctor tab was still showing.
func TestLeavingDoctorWhileItChecksKeepsTheInterfaceUsable(t *testing.T) {
	f := newFake()
	m := start(t, f)
	next, cmd := m.Update(k("8")) // the checks start; their result has not arrived
	if mm := next.(model); !mm.checking || mm.busy || status(next) != checkingText {
		t.Fatalf("checking=%v busy=%v status=%q", mm.checking, mm.busy, status(next))
	}
	next, again := next.Update(k("r")) // r on the Doctor tab waits for the running checks
	if again != nil {
		t.Error("a second run started while the first was running")
	}
	next, _ = next.Update(k("1")) // off to Services while they run
	if status(next) != "" {
		t.Errorf("the checking note followed to another tab: %q", status(next))
	}
	next = runCmd(next, cmd) // now the results arrive
	if mm := next.(model); mm.checking || mm.busy || len(mm.results) == 0 {
		t.Fatalf("after the results: checking=%v busy=%v results=%d", mm.checking, mm.busy, len(mm.results))
	}
	next = press(next, "S")
	if !called(f, "control start all") {
		t.Errorf("keys on the other tab must work: %v", f.calls)
	}
	// And the usual case still clears the note.
	next = press(next, "8")
	if mm := next.(model); mm.checking || status(next) != "" {
		t.Errorf("checking=%v status=%q", mm.checking, status(next))
	}
}

func TestNoStyledNewlines(t *testing.T) {
	// lipgloss pads every line of styled text to the same width, so a newline inside styled text pushed the
	// first table row far to the right once. No table row or header may start with a long run of spaces.
	f := newFake()
	m := start(t, f)
	for i := range tabNames {
		m = press(m, tabKey(i))
		for _, line := range strings.Split(m.View(), "\n") {
			trimmed := strings.TrimLeft(line, " ")
			if trimmed != "" && len(line)-len(trimmed) > 12 && !strings.HasPrefix(trimmed, "fix:") && !strings.Contains(line, "stable name") && !strings.HasPrefix(trimmed, "[") {
				t.Errorf("tab %s: line indented far right: %q", tabNames[i], line)
			}
		}
	}
}

// A burst of typed or pasted characters arrives as one key message. The text input matched its string form
// against its key bindings, so the letters "home" moved the cursor instead of being typed.
func TestPromptInputTakesWordsThatAreAlsoKeyNames(t *testing.T) {
	for _, word := range []string{"home", "end", "left", "right", "enter", "delete", "tab", "esc", "up", "down", "backspace", "pgup", "ctrl+a", "k0test"} {
		f := newFake()
		m := start(t, f)
		m = press(m, "3", "a")
		m = settle(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(word)}) // one message, all the characters
		m = press(m, "enter")
		if want := "siteadd " + word; !called(f, want) {
			t.Errorf("typing %q as one burst: calls = %v, want %q", word, f.calls, want)
		}
	}
}

func TestPromptEditingKeysStillWork(t *testing.T) {
	f := newFake()
	m := start(t, f)
	m = press(m, "3", "a")
	m = typeText(m, "hoxme")
	m = settle(m, tea.KeyMsg{Type: tea.KeyLeft})
	m = settle(m, tea.KeyMsg{Type: tea.KeyLeft})
	m = settle(m, tea.KeyMsg{Type: tea.KeyBackspace}) // removes the x
	m = typeText(m, "-")
	m = settle(m, tea.KeyMsg{Type: tea.KeyEnd})
	m = typeText(m, "2")
	m = press(m, "enter")
	if !called(f, "siteadd ho-me2") {
		t.Errorf("arrow keys, backspace and End still edit the text, and typing inserts at the cursor: %v", f.calls)
	}
}

func TestUniqueID(t *testing.T) {
	for _, c := range []struct {
		base  string
		taken []string
		want  string
	}{
		{"ic7300mk2", nil, "ic7300mk2"},
		{"IC 7300 MK2", nil, "ic-7300-mk2"},
		{"hf", []string{"hf"}, "hf-2"},
		{"hf", []string{"hf", "hf-2", "hf-3"}, "hf-4"},
		{"???", nil, "rig"},
		{"K1ABC", []string{"k1abc"}, "k1abc-2"},
	} {
		if got := uniqueID(c.base, c.taken); got != c.want {
			t.Errorf("uniqueID(%q,%v) = %q, want %q", c.base, c.taken, got, c.want)
		}
	}
}

func TestNetworkTab(t *testing.T) {
	f := newFake()
	f.network = NetworkInfo{
		Rows: []NetRow{
			{Service: "rigctld@hf", What: "rig control (hamlib)", Port: 4532, Who: "this computer only"},
			{Service: "direwolf@pkt", What: "packet TNC: AGW", Port: 8000, Who: "ANY computer on your network", Exposed: true, Note: "no password"},
			{Service: "gpsd", What: "GPS daemon", Port: 2948, Who: "not running"},
		},
		Lines: []string{"This computer: 192.0.2.7", "AX.25 kernel stack: not in kernel 7.2 (hamstack packet listen|call work without it)"},
	}
	m := press(start(t, f), "0")
	if m.(model).tab != tNetwork {
		t.Fatalf("0 opens the Network tab, got tab %d", m.(model).tab)
	}
	v := m.View()
	for _, want := range []string{"WHO CAN CONNECT", "rigctld@hf", "4532", "ANY computer on your network", "no password", "192.0.2.7", "AX.25 kernel stack", "open to your whole network"} {
		if !strings.Contains(v, want) {
			t.Errorf("the Network tab lacks %q:\n%s", want, v)
		}
	}
	if m = press(m, "1"); m.(model).tab != tServices {
		t.Error("1 goes back to Services")
	}
	if m = press(m, "shift+tab"); m.(model).tab != tNetwork {
		t.Error("shift+tab from the first tab wraps to the last")
	}
	empty := press(start(t, newFake()), "0")
	if !strings.Contains(empty.View(), "no network ports to show") {
		t.Error("an empty setup says there is nothing to show")
	}
}

func TestAddRigBySearchingAllRadios(t *testing.T) {
	f := newFake()
	f.hits = []RadioHit{
		{Model: 2045, Label: "Elecraft KX3", Status: "Stable"},
		{Model: 3073, Label: "Icom IC-7300", Status: "Stable", Profile: "ic7300"},
		{Model: 4, Label: "FLRig", Status: "Stable"},
	}
	m := start(t, f)
	m = press(m, "2", "a")
	if !strings.Contains(m.View(), "Search every radio hamlib supports") {
		t.Fatalf("the radio menu offers the search:\n%s", m.View())
	}
	// Choose the last entry (the search), type a query, pick the first hit, answer the speed, name, modem.
	for i := 0; i < len(f.profiles); i++ {
		m = press(m, "down")
	}
	m = press(m, "enter")
	if !strings.Contains(m.View(), "Search radios") {
		t.Fatalf("asks what to search for:\n%s", m.View())
	}
	m = typeText(m, "kx3")
	m = press(m, "enter")
	if !called(f, "search:kx3") || !strings.Contains(m.View(), "Elecraft KX3") {
		t.Fatalf("results shown:\n%s", m.View())
	}
	m = press(m, "enter") // the KX3
	if !strings.Contains(m.View(), "4800..38400") {
		t.Fatalf("asks the serial speed with its range:\n%s", m.View())
	}
	m = press(m, "enter") // the default
	if !called(f, "save:2045:9600") {
		t.Errorf("saved a profile at the default speed: %v", f.calls)
	}
	if !strings.Contains(m.View(), "Name this rig") {
		t.Errorf("then names the rig:\n%s", m.View())
	}
}

func TestSearchRejectsABadSpeedAndNoMatch(t *testing.T) {
	f := newFake()
	f.hits = []RadioHit{{Model: 2045, Label: "Elecraft KX3", Status: "Stable"}}
	m := start(t, f)
	m = press(m, "2", "a")
	for i := 0; i < len(f.profiles); i++ {
		m = press(m, "down")
	}
	m = press(m, "enter")
	m = typeText(m, "zzz")
	m = press(m, "enter")
	if !strings.Contains(m.View(), "nothing matches") || !strings.Contains(m.View(), "Search radios") {
		t.Errorf("no match asks again:\n%s", m.View())
	}
	m = press(m, "esc")
	m = press(m, "a")
	for i := 0; i < len(f.profiles); i++ {
		m = press(m, "down")
	}
	m = press(m, "enter")
	m = typeText(m, "kx3")
	m = press(m, "enter", "enter")
	m = press(m, "ctrl+u")
	m = typeText(m, "999999")
	m = press(m, "enter")
	if !strings.Contains(m.View(), "not a speed this radio supports") || called(f, "save:2045:999999") {
		t.Errorf("a bad speed is refused:\n%s %v", m.View(), f.calls)
	}
}
