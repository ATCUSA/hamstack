// Command hamstack sets up and manages amateur radio software stacks.
package main

import (
	"fmt"
	"os"

	"github.com/ATCUSA/hamstack/internal/install"
	"github.com/ATCUSA/hamstack/internal/updates"
)

// version remains compatible with the repository release Taskfile.
var version = "dev"

func init() {
	if version != "dev" {
		updates.Version = version
	}
}

const usage = `hamstack - set up and manage amateur radio software (Winlink first)

Usage:
  hamstack version                   build identity
  hamstack updates                   shared installed-app and hamstack release inventory
  hamstack updates self [--dry-run] [--yes]   verified self-update with rollback binary
  hamstack maintenance               review pending restarts, login changes and confirmed reboot
  hamstack versions [--offline]      installed vs latest upstream versions
  hamstack tui                       the single-window interface (also: run hamstack with no arguments)
  hamstack setup winlink [--components LIST] [--radio ID] [--callsign CALL] [--ui on|off] [--skip-install] [--yes] [--dry-run]
                                     guided setup: radio, station, install, services, check, start
  hamstack install [PROGRAM...|all] [--list] [--dry-run] [--yes] [--force]   (no names: choose from the list)
  hamstack install PROGRAM --from-source   build the newest release from source (installs compilers; slow)
                                     install what is missing (shows every command first)
  hamstack update [--dry-run] [--yes]   update what hamstack installed; tells you how for the rest
  hamstack sites                     your places (home, EOC, club) and which radios are plugged in at each
  hamstack site add ID [--rigs a,b] [--station S] [--grid G]   save a place (defaults: what is set up now)
  hamstack site use ID               switch: its rigs on, others off, its station and grid, services restarted
  hamstack site here [--switch]      which site do the plugged-in radios match?
  hamstack site set|remove ...
  hamstack rigs                      your radios and how each is wired up
  hamstack rig add ID --profile P [--modem mercury|data2g|ardopcf|direwolf] [--bands hf,vhf] [--cat DEVICE] [--audio CARD]
  hamstack rig remove|enable|disable|identify ID    identify = find its USB devices by unplug/replug
  hamstack rig set ID KEY VALUE      keys: name, profile, modem, bands, baud, cat, audio
  hamstack pat on|off                run Pat or not
  hamstack pat locator               set Pat's locator to the grid square you are at (backup kept)
  hamstack pat sync [--dry-run]      wire Pat's config to your rigs and grid here (locator, rigs, ARDOP rig, HF modem, packet TNC); --dry-run shows what would change
  hamstack profiles                  list supported radios/interfaces
  hamstack config [show]             show settings
  hamstack dashboard on [--desktop] [--access local|network|all] [--cert FILE --key FILE]
  hamstack dashboard check                  browser, virtual-display and desktop compatibility
  hamstack dashboard repair                 install any dashboard packages that are missing (doctor offers this)
  hamstack dashboard console on|off         optional browser terminal with full user permissions
  hamstack dashboard tune on|off            let the dashboard's radio panel run the radio's antenna tuner (it transmits briefly; off by default)
  hamstack dashboard [on|off|password]     the browser dashboard: services, links, apps and the desktop in the browser
  hamstack app list|open APP|close APP   desktop programs (WSJT-X, JS8Call...) on their own display, for the dashboard
  hamstack data2g password [RIG]     set the password of Data2G's web UI (it asks; also: config set data2g-password)
  hamstack config set KEY VALUE      keys: profile (radio), ui (on|off), components (e.g. rigctld,mercury or rigctld,data2g)
  hamstack stations                  list your stations (callsigns)
  hamstack station add ID --callsign CALL [--name LABEL] [--pat-config FILE] [--addr HOST:PORT] [--access local|network|all]
  hamstack station access ID [local|network|all]   who can reach the station's Pat web UI (default local)
  hamstack station import-pat [DIR]  add stations from existing Pat configs (default ~/.config/pat)
  hamstack station remove ID
  hamstack use ID                    switch the active station
  hamstack units install             write Mercury config and systemd user units
  hamstack units show                print what install would write
  hamstack units remove              remove the unit files
  hamstack devices [watch]           USB serial ports and sound cards, grouped; 'watch' shows plug/unplug live
  hamstack gps [status]              GPS: is gpsd running, does the receiver have a fix
  hamstack gps use [DEVICE|--addr HOST:PORT]   set up a GPS receiver (or an existing gpsd)
  hamstack gps grid [--save]         read a fix and turn it into your grid square
  hamstack gps on|off|remove
  hamstack packet listen|call        watch packet traffic / call a station through Direwolf (no root, no kernel AX.25)
  hamstack ax25 [setup|attach|call|listen]   the Linux kernel AX.25 stack: axports, kissattach, axcall, axlisten
  hamstack network access [TARGET local|network|all] [--restart] [--firewall] [--yes]
  hamstack network firewall install [--dry-run] [--yes]   reviewed firewall helper and LAN watcher
  hamstack network                   every port the stack uses, who can reach it, addresses, AX.25 state
  hamstack direwolf [setup|set|show|edit]   configure a rig's Direwolf: modem speed, callsign, timing, digipeating, your own lines
  hamstack groups [fix [--yes]]      group memberships for serial and sound access (uucp/dialout, audio)
  hamstack doctor                    check everything is ready, with fixes
  hamstack status                    state of rigctld, mercury, pat
  hamstack start|stop|restart [SERVICE|all]
  hamstack enable|disable [SERVICE|all]   start at login on/off
  hamstack logs SERVICE [-f]
  hamstack linger [on|off]           start services at boot, not only at login (needs systemd user lingering)
  hamstack system [status|check|update|reboot|shutdown|allow|disallow]
                                     the operating system: updates, restart, power off (shutdown warns first);
                                     'allow' lets the dashboard do the same without a password (a narrow sudoers rule)
  hamstack plugin validate FILE             check a plugin manifest (draft spec, docs/plugins/)
  hamstack launcher [install|remove|list]   menu entries for the dashboard, the terminal interface and each installed program
  hamstack launch PROGRAM [--debug]  start a program with its radio set up; --debug shows what it prints (opens a terminal)
  hamstack uninstall [--keep-apps|--remove-apps] [--dry-run] [--yes]
                                     remove hamstack and undo the system changes it made; asks whether to keep
                                     the ham radio programs (their mail, logs and contacts are never deleted)
  hamstack help

SERVICE is one of: rigctld, mercury, data2g, ardopcf, direwolf, gpsd, pat (default: all).
`

func main() {
	// First, before anything can start a program: a dashboard job may have passed the sudo password on descriptor 3.
	// Take it and close the descriptor so that nothing this process starts inherits it (install/sudo.go).
	if err := takeJobPassword(); err != nil {
		fmt.Fprintln(os.Stderr, "hamstack:", err)
		os.Exit(1)
	}
	defer install.ForgetSudoPassword()
	args := os.Args[1:]
	cmd := "help"
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	} else if isTerminal() {
		cmd = "tui" // bare `hamstack` in a terminal opens the interface
	}
	var err error
	switch cmd {
	case "--version", "version":
		fmt.Println("hamstack", updates.Current())
	case "maintenance":
		err = offerMaintenance(&asker{in: bufioStdin(), out: os.Stdout}, false)
	case "updates":
		err = cmdUpdates(args)
	case "versions":
		offline := len(args) > 0 && args[0] == "--offline"
		os.Exit(runVersions(offline))
	case "tui":
		err = cmdTUI()
	case "setup":
		err = cmdSetup(args)
	case "install":
		err = cmdInstall(args)
	case "update":
		err = cmdUpdate(args)
	case "sites":
		err = cmdSites()
	case "site":
		err = cmdSite(args)
	case "rigs":
		err = cmdRigs()
	case "rig":
		err = cmdRig(args)
	case "pat":
		err = cmdPat(args)
	case "run": // executed by the generated systemd units
		err = cmdRun(args)
	case "profiles":
		err = cmdProfiles()
	case "config":
		err = cmdConfig(args)
	case "wait-port": // used by the generated Mercury unit
		err = cmdWaitPort(args)
	case "stations":
		err = cmdStations()
	case "station":
		err = cmdStation(args)
	case "use":
		err = cmdUse(args)
	case "units":
		err = cmdUnits(args)
	case "devices":
		err = cmdDevices(args)
	case "gps":
		err = cmdGPS(args)
	case "packet":
		err = cmdPacket(args)
	case "ax25":
		err = cmdAX25(args)
	case "network":
		err = networkCommand(args)
	case "direwolf":
		err = cmdDirewolf(args)
	case "data2g":
		err = cmdData2G(args)
	case "web", "dashboard": // "dashboard" is the name people use; "web" is kept
		err = cmdWeb(args)
	case "app":
		err = cmdApp(args)
	case "groups":
		err = cmdGroups(args)
	case "doctor":
		err = cmdDoctor()
	case "status":
		err = cmdStatus()
	case "start", "stop", "restart", "enable", "disable":
		err = cmdControl(cmd, args)
	case "linger":
		err = cmdLinger(args)
	case "uninstall":
		err = cmdUninstall(args)
	case "system":
		err = cmdSystem(args)
	case "plugin":
		err = cmdPlugin(args)
	case "plan": // the setup plans the dashboard runs as jobs
		err = cmdPlan(args)
	case "launcher":
		err = cmdLauncher(args)
	case "launch":
		err = cmdLaunch(args)
	case "logs":
		err = cmdLogs(args)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "hamstack: unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "hamstack: %v\n", err)
		os.Exit(1)
	}
}

// takeJobPassword reads the password a dashboard job passed on descriptor 3, if it passed one, and closes it.
func takeJobPassword() error {
	if os.Getenv(install.SudoPasswordEnv) != "fd" {
		return nil
	}
	_ = os.Unsetenv(install.SudoPasswordEnv)
	return install.ReadSudoPassword(os.NewFile(install.SudoPasswordFD, "sudo-password"))
}
