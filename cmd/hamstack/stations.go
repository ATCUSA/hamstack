package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/patcfg"
	"github.com/ATCUSA/hamstack/internal/svc"
	"github.com/ATCUSA/hamstack/internal/units"
)

func cmdStations() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if len(cfg.Stations) == 0 {
		fmt.Println("No stations yet. Add one: hamstack station add ID --callsign YOURCALL")
		fmt.Println("or pick up existing Pat configs: hamstack station import-pat")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tCALLSIGN\tNAME\tWEB UI\tPAT CONFIG\t")
	for _, s := range cfg.Stations {
		mark := ""
		if s.ID == cfg.Active {
			mark = "(active)"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", s.ID, s.Callsign, s.Name, s.HTTPAddr, s.PatConfig, mark)
	}
	return tw.Flush()
}

func cmdStation(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: hamstack station add|remove|import-pat")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	switch args[0] {
	case "add":
		return stationAdd(cfg, args[1:])
	case "remove":
		if len(args) != 2 {
			return fmt.Errorf("usage: hamstack station remove ID")
		}
		if err := cfg.RemoveStation(args[1]); err != nil {
			return err
		}
		if err := config.Save(cfg); err != nil {
			return err
		}
		fmt.Printf("removed station %s (its Pat config and mailbox were not touched)\n", args[1])
		return nil
	case "import-pat":
		dir := ""
		if len(args) > 1 {
			dir = args[1]
		} else if home, err := os.UserHomeDir(); err == nil {
			dir = filepath.Join(home, ".config", "pat")
		}
		return stationImport(cfg, dir)
	}
	return fmt.Errorf("usage: hamstack station add|remove|import-pat")
}

func stationAdd(cfg config.Config, args []string) error {
	if len(args) == 0 || len(args[0]) == 0 || args[0][0] == '-' {
		return fmt.Errorf("usage: hamstack station add ID --callsign CALL [--name LABEL] [--pat-config FILE] [--addr HOST:PORT]")
	}
	id := args[0]
	fs := flag.NewFlagSet("station add", flag.ContinueOnError)
	call := fs.String("callsign", "", "callsign (required)")
	name := fs.String("name", "", "label")
	patConf := fs.String("pat-config", "", "Pat config file (default ~/.config/hamstack/pat/ID.json, created if missing)")
	addr := fs.String("addr", "", "Pat web UI address (default: next free localhost port from 8080)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *call == "" {
		return fmt.Errorf("--callsign is required")
	}
	st, created, err := addStation(&cfg, id, *call, *name, *patConf, *addr)
	if err != nil {
		return err
	}
	fmt.Printf("added station %s (%s), web UI %s\n", st.ID, st.Callsign, st.HTTPAddr)
	if created {
		fmt.Printf("created Pat config %s\nSet your Winlink password in Pat's web UI settings (or: pat --config %s configure). hamstack never stores it.\n", st.PatConfig, st.PatConfig)
	}
	if cfg.Active == st.ID {
		fmt.Println("it is the active station.")
	} else {
		fmt.Printf("switch to it with: hamstack use %s\n", st.ID)
	}
	return nil
}

// addStation validates and saves a new station, creating a starter Pat config if the file is missing.
func addStation(cfg *config.Config, id, call, name, patConf, addr string) (st config.Station, created bool, err error) {
	dir, err := config.Dir()
	if err != nil {
		return st, false, err
	}
	st = config.Station{ID: id, Name: name, Callsign: call, PatConfig: patConf, HTTPAddr: addr}
	if st.PatConfig == "" {
		st.PatConfig = filepath.Join(dir, "pat", id+".json")
	}
	if err := cfg.AddStation(st); err != nil {
		return st, false, err
	}
	st, _ = cfg.Find(id) // with the callsign and address filled in
	if _, err := os.Stat(st.PatConfig); os.IsNotExist(err) {
		opts := patcfg.Options{Callsign: st.Callsign, HTTPAddr: st.HTTPAddr, Grid: cfg.EffectiveGrid()}
		for _, r := range cfg.ActiveRigs() {
			if !r.NoCAT { // a rig with no CAT has no rigctld for Pat to talk to
				opts.Rigs = append(opts.Rigs, patcfg.RigRef{Name: r.ID, Port: r.RigPort})
			}
		}
		if hf, ok := cfg.PatHFRig(); ok {
			opts.HFRig, opts.HFArqPort = hf.ID, hf.ArqPort
		}
		if pk, ok := cfg.PatPacketRig(); ok {
			opts.Packet = true
			opts.AGWPort, _ = config.DirewolfPorts(pk)
			if !pk.NoCAT {
				opts.PacketRig = pk.ID
			}
		}
		b, err := patcfg.Minimal(opts)
		if err != nil {
			return st, false, err
		}
		if err := os.MkdirAll(filepath.Dir(st.PatConfig), 0o755); err != nil {
			return st, false, err
		}
		if err := os.WriteFile(st.PatConfig, b, 0o600); err != nil {
			return st, false, err
		}
		created = true
	}
	return st, created, config.Save(*cfg)
}

// importStations adds stations from the Pat configs found in dir, without printing or saving. It
// returns what it did, one line per config, and how many it added.
func importStations(cfg *config.Config, dir string) (notes []string, added int, err error) {
	found, err := patcfg.Scan(dir)
	if err != nil {
		return nil, 0, err
	}
	if len(found) == 0 {
		return nil, 0, fmt.Errorf("no Pat configs with a callsign found in %s", dir)
	}
	hadActive, defaultID := cfg.Active != "", ""
	for _, f := range found {
		if _, ok := cfg.Find(f.ID); ok {
			notes = append(notes, fmt.Sprintf("skip %s: a station with that id already exists", f.ID))
			continue
		}
		st := config.Station{ID: f.ID, Callsign: f.Callsign, PatConfig: f.File, HTTPAddr: f.HTTPAddr}
		if err := cfg.AddStation(st); err != nil {
			// Typically a web UI port clash or an unusable address: add it on a free port instead.
			st.HTTPAddr = cfg.NextAddr()
			if err2 := cfg.AddStation(st); err2 != nil {
				notes = append(notes, fmt.Sprintf("skip %s: %v", f.ID, err))
				continue
			}
			notes = append(notes, fmt.Sprintf("note: %s: %v; using %s (the Pat config file itself is unchanged)", f.ID, err, st.HTTPAddr))
		}
		notes = append(notes, fmt.Sprintf("imported %s (%s) from %s", f.ID, f.Callsign, f.File))
		added++
		if f.Default {
			defaultID = f.ID
		}
	}
	// With no station active before, prefer the one Pat itself uses by default.
	if added > 0 && !hadActive && defaultID != "" {
		_ = cfg.Use(defaultID)
	}
	return notes, added, nil
}

func stationImport(cfg config.Config, dir string) error {
	notes, added, err := importStations(&cfg, dir)
	for _, n := range notes {
		fmt.Println(n)
	}
	if err != nil || added == 0 {
		return err
	}
	return config.Save(cfg)
}

func cmdUse(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: hamstack use ID")
	}
	msgs, warns, err := useStation(args[0])
	for _, w := range warns {
		fmt.Fprintln(os.Stderr, "warning:", w)
	}
	for _, m := range msgs {
		fmt.Println(m)
	}
	return err
}

// useStation makes id the active station. If the services are installed it regenerates them
// and restarts whichever of mercury and pat are running. It prints nothing: it returns messages.
func useStation(id string) (msgs, warns []string, err error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, err
	}
	if err := cfg.Use(id); err != nil {
		return nil, nil, err
	}
	cfg.SyncSite()
	if err := config.Save(cfg); err != nil {
		return nil, nil, err
	}
	st, _ := cfg.ActiveStation()
	msgs = append(msgs, fmt.Sprintf("active station: %s (%s)", st.ID, st.Callsign))
	if installed, _ := units.Installed(); svc.Available() != nil || len(installed) == 0 {
		return append(msgs, "units not installed yet: run 'hamstack units install'"), nil, nil
	}
	p, warns, err := buildPlan()
	if err != nil {
		return msgs, warns, err
	}
	if _, err := writeUnits(p); err != nil {
		return msgs, warns, err
	}
	// The callsign is read when a service starts, so restart the ones that use it.
	for _, s := range units.ServicesFor(cfg) {
		if s.Kind == config.RigCtld {
			continue
		}
		if svc.Status(s.Unit()).Active == "active" {
			if err := svc.Run("restart", s.Unit()); err != nil {
				return msgs, warns, err
			}
			msgs = append(msgs, "restarted "+s.Name())
		}
	}
	return msgs, warns, nil
}
