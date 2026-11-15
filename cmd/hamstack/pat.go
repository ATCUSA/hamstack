package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/netinfo"
	"github.com/ATCUSA/hamstack/internal/versions"
)

// patArgs is Pat's command line for a station: its own config, log files in hamstack's state folder, and
// the web UI on listen.
func patArgs(st config.Station, listen, stateDir string) []string {
	return []string{"pat", "--config", st.PatConfig,
		"--log", filepath.Join(stateDir, "pat-"+st.ID+".log"),
		"--event-log", filepath.Join(stateDir, "pat-"+st.ID+"-events.json"),
		"http", "--addr", listen}
}

// runPat starts Pat for the active station. It works out the web UI address now, not when the unit was
// written: a station with "network" access listens on the computer's LAN address as it is at this moment, so a
// new network or DHCP lease (or a reboot onto another network) cannot leave Pat trying to bind an address the
// computer no longer has. Early at boot the network may not be up yet, so it waits a little for an address.
func runPat(cfg config.Config) error {
	st, ok := cfg.ActiveStation()
	if !ok {
		return fmt.Errorf("no active station (hamstack station add, or hamstack use ID)")
	}
	st.Access = cfg.NetworkAccess("pat", st.EffectiveAccess())
	path, err := versions.Which("pat")
	if err != nil {
		return fmt.Errorf("pat is not installed (run: hamstack install pat)")
	}
	if _, err := os.Stat(st.PatConfig); err != nil {
		return fmt.Errorf("station %s: Pat config %s not found", st.ID, st.PatConfig)
	}
	if err := os.MkdirAll(stateDir(), 0o755); err != nil {
		return err
	}
	listen, ok := st.ListenAddr(netinfo.LANAddrs())
	for i := 0; !ok && i < 30; i++ { // up to a minute for the network to come up
		time.Sleep(2 * time.Second)
		listen, ok = st.ListenAddr(netinfo.LANAddrs())
	}
	if !ok {
		fmt.Fprintf(os.Stderr, "hamstack: station %s is set to network access but this computer has no LAN address: Pat listens on %s only\n", st.ID, listen)
	}
	argv := patArgs(st, listen, stateDir())
	fmt.Fprintf(os.Stderr, "hamstack: %s\n", strings.Join(argv, " "))
	return syscall.Exec(path, argv, os.Environ())
}
