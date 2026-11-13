// Command hamstack sets up and manages amateur radio software stacks.
package main

import (
	"fmt"
	"os"
)

// version is set at build time (go build -ldflags "-X main.version=v1.2.3"); a plain build says "dev".
var version = "dev"

const usage = `hamstack - set up and manage amateur radio software (Winlink first)

Usage:
  hamstack version                   the version of hamstack itself
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
  hamstack rig add ID --profile P [--modem mercury] [--bands hf,vhf] [--cat DEVICE] [--audio CARD]
  hamstack rig remove|enable|disable|identify ID    identify = find its USB devices by unplug/replug
  hamstack rig set ID KEY VALUE      keys: name, profile, modem, bands, cat, audio
  hamstack pat on|off                run Pat or not
  hamstack pat locator               set Pat's locator to the grid square you are at (backup kept)
  hamstack pat sync                  wire Pat's config to your rigs and grid here: locator, rigs, HF modem, packet TNC
  hamstack profiles                  list supported radios/interfaces
  hamstack config [show]             show settings
  hamstack config set KEY VALUE      keys: profile (radio), ui (on|off), components (e.g. rigctld,mercury)
  hamstack stations                  list your stations (callsigns)
  hamstack station add ID --callsign CALL [--name LABEL] [--pat-config FILE] [--addr HOST:PORT]
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
  hamstack network                   every port the stack uses, who can reach it, addresses, AX.25 state
  hamstack direwolf [setup|set|show|edit]   configure a rig's Direwolf: modem speed, callsign, timing, digipeating, your own lines
  hamstack groups [fix [--yes]]      group memberships for serial and sound access (uucp/dialout, audio)
  hamstack doctor                    check everything is ready, with fixes
  hamstack status                    state of rigctld, mercury, pat
  hamstack start|stop|restart [SERVICE|all]
  hamstack enable|disable [SERVICE|all]   start at login on/off
  hamstack logs SERVICE [-f]
  hamstack linger [on|off]           start services at boot, not only at login (needs systemd user lingering)
  hamstack help

SERVICE is one of: rigctld, mercury, direwolf, gpsd, pat (default: all).
`

func main() {
	args := os.Args[1:]
	cmd := "help"
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	} else if isTerminal() {
		cmd = "tui" // bare `hamstack` in a terminal opens the interface
	}
	var err error
	switch cmd {
	case "version", "--version", "-v":
		fmt.Println("hamstack", version)
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
		err = cmdNetwork()
	case "direwolf":
		err = cmdDirewolf(args)
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
