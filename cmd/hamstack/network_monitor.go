package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/netinfo"
)

// Native listeners cannot change their bind address in place. A small parent
// watches the current LAN and cleanly restarts its child when that address changes.
// systemd still owns the parent and its entire process group.
func watchNetwork(args []string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(signals)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	cfg := loadCfg()
	follow := false
	if len(args) > 1 && args[0] == "web" {
		follow = cfg.NetworkAccess("web", func() string {
			if cfg.Web != nil {
				return cfg.Web.EffectiveAccess()
			}
			return config.AccessLocal
		}()) == config.AccessNetwork
	}
	if len(args) > 1 && args[0] == "run" {
		if args[1] == "pat" {
			st, ok := cfg.ActiveStation()
			follow = ok && cfg.PatAccess(st) == config.AccessNetwork
		}
		if args[1] == "data2g" && len(args) > 2 {
			follow = cfg.NetworkAccess("data2g@"+args[2]+"/web", config.AccessAll) == config.AccessNetwork
		}
	}
	for {
		fingerprint := strings.Join(netinfo.LANAddrs(), ",")
		cmd := exec.Command(self, args...)
		cmd.Env = append(os.Environ(), "HAMSTACK_NETWORK_CHILD=1")
		cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin
		if err := cmd.Start(); err != nil {
			return err
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		stop := func() {
			_ = cmd.Process.Signal(syscall.SIGTERM)
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				_ = cmd.Process.Kill()
				<-done
			}
		}
		changed := false
		for !changed {
			select {
			case sig := <-signals:
				_ = sig
				stop()
				return nil
			case err := <-done:
				if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == exitRadioMissing {
					os.Exit(exitRadioMissing)
				}
				return err
			case <-ticker.C:
				if now := strings.Join(netinfo.LANAddrs(), ","); follow && now != fingerprint {
					fmt.Fprintln(os.Stderr, "hamstack: LAN address changed; restarting listener with the new address")
					stop()
					changed = true
				}
			}
		}
	}
}
