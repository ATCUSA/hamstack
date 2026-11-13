// Package tui is hamstack's single-window interface. Everything that can be done on the command line
// can be done here: operations that need only a few answers happen inside it with prompts, and the ones
// that need a terminal or a password (installs, sudo, the guided setup) run in this same terminal and
// return to the interface when they finish.
package tui

import (
	"os/exec"

	"github.com/ATCUSA/hamstack/internal/doctor"
)

// Header is the line at the top: where you are and what is set up.
type Header struct{ Site, Rigs, Station, Grid string }

// Service is one managed service as the UI shows it.
type Service struct {
	Name      string
	State     string // active, inactive, failed, not installed...
	Enabled   string // enabled, disabled, -
	Since     string
	Installed bool
}

// Rig is one radio and how it is wired up.
type Rig struct {
	ID, Name, Radio, Modem, Bands, Ports string
	Hardware                             string // present, NOT DETECTED, off or -
	On, NoCAT                            bool
}

// ProfileOpt is a radio model that a rig can use.
type ProfileOpt struct {
	ID, Name string
	HasCAT   bool
}

// RadioHit is one result of searching hamlib's radio list.
type RadioHit struct {
	Model         int
	Label, Status string
	Profile       string // the id of the profile that already covers this model, if any
}

// Site is a place you operate from.
type Site struct {
	ID, Name, Rigs, Station, Grid string
	Radios                        string // "2 of 2" radios plugged in, or "-"
	Current                       bool
}

// StationRow is one station in the list.
type StationRow struct {
	ID, Callsign, Name, Addr string
	Active                   bool
}

// Device is a USB serial port or sound card.
type Device struct {
	Kind     string // "serial" or "sound"
	Key      string // identifies it between refreshes and to AssignDevice
	Label    string // what it is
	Detail   string // ids, stable name
	Group    string // the hub it shares with other devices ("" for one plugged in directly)
	Assigned string // the rig using it, if any
}

// Program is one piece of software hamstack manages.
type Program struct {
	Name, Desc, Installed, Origin, Latest, Status string
	CanSource                                     bool   // can be built from source with --from-source
	UserInstall                                   bool   // installed by hamstack in ~/.local, so hamstack can update it
	UpdateHint                                    string // how to update it if the package manager owns it
}

// Setting kinds.
const (
	SettingToggle = "toggle" // enter flips it
	SettingText   = "text"   // enter asks for a new value
	SettingExec   = "exec"   // enter runs a command in the terminal
)

// Setting is one row of the Settings tab.
type Setting struct {
	Key, Label, Value, Hint string
	Kind                    string
	Args                    []string // for SettingExec: the hamstack arguments to run
}

// Backend is what the UI needs from the rest of hamstack. Methods that change something return a short
// message to show, or an error.
type Backend interface {
	Header() Header
	NeedsSetup() bool // a fresh install: no rigs and no stations yet

	Services() []Service
	Control(verb, service string) error // start|stop|restart|enable|disable; "all" allowed
	UnitsInstall() (string, error)      // write the systemd units for the current setup

	Rigs() []Rig
	Profiles() []ProfileOpt
	SearchRadios(query string) ([]RadioHit, error) // every radio the installed hamlib supports, best matches first
	RadioBaud(model int) (min, max, def int)       // the serial speeds of a hamlib model; zeros for a network or no port
	SaveRadio(model, baud int) (string, error)     // a profile for a hamlib model, made if none exists; returns its id
	RigToggle(id string) (string, error)
	RigModem(id, modem string) (string, error)
	RigAdd(profile, id, modem string) (string, error)
	RigRemove(id string) (string, error)

	Sites() []Site
	SiteHint() (id, why string, ok bool)
	SiteUse(id string) (string, error)
	SiteAdd(id string) (string, error) // saves what is set up now
	SiteRemove(id string) (string, error)
	SiteGrid(id, grid string) (string, error)

	Stations() []StationRow
	Use(id string) (string, error)
	StationAdd(id, callsign string) (string, error)
	StationImport() (string, error)
	StationRemove(id string) (string, error)

	Devices() []Device
	Network() NetworkInfo
	AssignDevice(key, rigID string) (string, error)

	Programs() []Program
	Latest() (map[string]string, error) // upstream versions: needs the network

	Settings() []Setting
	SetSetting(key, value string) (string, error)

	Doctor() []doctor.Result
	Logs(service string, lines int) (string, error)

	// Command returns a hamstack command to run in the terminal. It waits for Enter when it finishes so
	// its output can be read before the interface comes back.
	Command(args ...string) *exec.Cmd
}

// NetRow is one network port of the stack and who can reach it.
type NetRow struct {
	Service, What string
	Port          int
	Who           string // "this computer only", "ANY computer on your network", "not running"
	Exposed       bool   // open to the whole network
	Note          string
}

// NetworkInfo is what the Network tab shows.
type NetworkInfo struct {
	Rows  []NetRow
	Lines []string // addresses and the AX.25 state, already worded
}
