package main

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/ATCUSA/hamstack/internal/ax25"
	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/netinfo"
)

// netRow is one network endpoint of the stack.
type netRow struct {
	Service string // "rigctld@hf"
	What    string
	Port    int
	Open    bool          // something is listening now
	Scope   netinfo.Scope // how far it reaches (only meaningful when Open)
	Note    string
}

// networkRows lists every port the configured stack uses, with what is really listening on it.
func networkRows(cfg config.Config, ls []netinfo.Listener) []netRow {
	var rows []netRow
	add := func(service, what string, port int, note string) {
		r := netRow{Service: service, What: what, Port: port, Note: note}
		if l, ok := netinfo.Find(ls, port); ok {
			r.Open, r.Scope = true, l.Scope()
		}
		rows = append(rows, r)
	}
	if cfg.RunsGPSD() {
		add("gpsd", "GPS daemon", cfg.GPS.ListenPort(), "")
	}
	for _, rig := range cfg.ActiveRigs() {
		if !rig.NoCAT {
			add("rigctld@"+rig.ID, "rig control (hamlib)", rig.RigPort, "")
		}
		switch rig.Modem {
		case config.Mercury:
			arq, data, bcast, ui := config.MercuryPorts(rig)
			add("mercury@"+rig.ID, "HF modem: ARQ control (VARA-style)", arq, "Pat connects here")
			add("mercury@"+rig.ID, "HF modem: ARQ data", data, "")
			add("mercury@"+rig.ID, "HF modem: broadcast", bcast, "")
			if cfg.UI {
				add("mercury@"+rig.ID, "HF modem: web UI", ui, "no password")
			}
		case config.Direwolf:
			agwPort, kiss := config.DirewolfPorts(rig)
			add("direwolf@"+rig.ID, "packet TNC: AGW", agwPort, "Pat and 'hamstack packet' connect here; no password")
			if kiss > 0 {
				add("direwolf@"+rig.ID, "packet TNC: KISS", kiss, "no password")
			}
		}
	}
	if cfg.PatEnabled() {
		if st, ok := cfg.ActiveStation(); ok {
			if i := strings.LastIndex(st.HTTPAddr, ":"); i >= 0 {
				var p int
				if _, err := fmt.Sscanf(st.HTTPAddr[i+1:], "%d", &p); err == nil {
					add("pat", "Winlink web UI ("+st.Callsign+")", p, "")
				}
			}
		}
	}
	return rows
}

func scopeText(r netRow) string {
	if !r.Open {
		return "not running"
	}
	switch r.Scope {
	case netinfo.Local:
		return "this computer only"
	case netinfo.Network:
		return "ANY computer on your network"
	}
	return "one address"
}

func cmdNetwork() error {
	cfg, _ := config.Load()
	ls := netinfo.Listeners("/")
	rows := networkRows(cfg, ls)
	if len(rows) == 0 {
		fmt.Println("Nothing is set up yet, so there are no network ports to show (hamstack setup winlink).")
	} else {
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "SERVICE\tWHAT\tPORT\tWHO CAN CONNECT\tNOTE")
		exposed := false
		for _, r := range rows {
			fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\n", r.Service, r.What, r.Port, scopeText(r), r.Note)
			if r.Open && r.Scope == netinfo.Network {
				exposed = true
			}
		}
		tw.Flush()
		if ips := netinfo.LANAddrs(); len(ips) > 0 {
			fmt.Printf("\nThis computer: %s\n", strings.Join(ips, ", "))
		}
		if exposed {
			fmt.Println("\nSomething above is open to your whole network and has no password. That is fine on a network you trust;\n" +
				"otherwise block it with your firewall (hamstack cannot see your firewall rules) or turn it off\n" +
				"(Mercury's web UI: hamstack config set ui off).")
		}
	}

	fmt.Println("\nAX.25 (the Linux kernel's packet stack):")
	k, release := ax25.Kernel("/")
	fmt.Printf("  kernel:      %s (%s)\n", k, orDash(release))
	if k == ax25.KernelMissing {
		fmt.Println("  not usable on this kernel; 'hamstack packet listen|call' works without it (hamstack ax25 explains)")
		return nil
	}
	if b, err := os.ReadFile(ax25.PortsFile); err == nil {
		for _, p := range ax25.ParsePorts(string(b)) {
			fmt.Printf("  axports:     %s  %s  %d baud\n", p.Name, p.Call, p.Speed)
		}
	} else {
		fmt.Printf("  axports:     none (%s missing)\n", ax25.PortsFile)
	}
	if ifs := ax25.Interfaces("/"); len(ifs) > 0 {
		for _, i := range ifs {
			fmt.Printf("  interface:   %s\n", i.Name)
		}
	} else {
		fmt.Println("  interface:   none (hamstack ax25 attach)")
	}
	return nil
}
