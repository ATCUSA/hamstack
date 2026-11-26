package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ATCUSA/hamstack/internal/config"
)

// resetDefaults is the full reset: a copy of the config first, then every setting back to its default and every access
// rule back to this computer only (config.ResetToDefaults), the units rewritten, the firewall told and the menu entries
// put back. The owner chose this scope (2026-11-26): rigs, stations, sites and the dashboard password stay.
func resetDefaults() (string, error) {
	cfg := loadCfg()
	changes := cfg.ResetChanges()
	if len(changes) == 0 {
		return "Every setting is at its default, and every access rule lets this computer only in: nothing to reset.", nil
	}
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	src := filepath.Join(dir, "config.json")
	backup := filepath.Join(dir, "config.json.before-reset-"+time.Now().Format("20060102-150405"))
	if b, err := os.ReadFile(src); err == nil {
		if err := os.WriteFile(backup, b, 0o600); err != nil {
			return "", fmt.Errorf("could not keep a copy of the config before the reset, so nothing was changed: %w", err)
		}
	} else {
		backup = ""
	}
	msg, err := mutate(func(c *config.Config) (string, error) {
		c.ResetToDefaults()
		return fmt.Sprintf("Reset %d things to their defaults", len(changes)), nil
	})
	if err != nil {
		return msg, err
	}
	notes := []string{msg}
	if backup != "" {
		notes = append(notes, "the config before the reset is kept in "+backup)
	}
	if firewallAvailable() {
		if _, err := applyFirewall(loadCfg()); err != nil {
			notes = append(notes, "the firewall was not updated: "+err.Error())
		}
	}
	var b strings.Builder
	refreshLaunchers(loadCfg(), &b)
	notes = append(notes, "restart the services to apply it (Updates, Maintenance; or hamstack restart)")
	return strings.Join(notes, "; "), nil
}

// cmdConfigReset shows what a full reset changes and asks before doing it.
func cmdConfigReset(args []string) error {
	yes := len(args) > 0 && args[0] == "--yes"
	changes := loadCfg().ResetChanges()
	if len(changes) == 0 {
		msg, err := resetDefaults()
		fmt.Println(msg)
		return err
	}
	fmt.Println("A full reset changes these, and keeps your rigs, stations, sites, grid and dashboard password:")
	for _, c := range changes {
		fmt.Println("  " + c)
	}
	if !yes {
		if !stdinIsTerminal() {
			return fmt.Errorf("add --yes to reset without a terminal to ask on")
		}
		if !(&asker{in: bufioStdin(), out: os.Stdout}).confirm("Reset them?", false) {
			fmt.Println("nothing changed")
			return nil
		}
	}
	msg, err := resetDefaults()
	fmt.Println(msg)
	return err
}
