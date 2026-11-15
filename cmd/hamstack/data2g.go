package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ATCUSA/hamstack/internal/audio"
	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/netinfo"
	"github.com/ATCUSA/hamstack/internal/profile"
	"github.com/ATCUSA/hamstack/internal/versions"
)

var d2gDeviceRE = regexp.MustCompile(`^\s*(\d+)\s+\d+ ch\s+(.+?)\s*$`)

// data2gDevice picks the number of the sound device Data2G should use from the output of
// "data2g host --list-audio-devices" (an "input:" list and an "output:" list, one numbered device per line).
// It takes the first device of the wanted kind whose name contains hint, ignoring the "Monitor of ..."
// entries a sound server adds for every output. ok is false if nothing matches.
func data2gDevice(listing, kind, hint string) (n int, ok bool) {
	if hint == "" {
		return 0, false
	}
	section := ""
	for _, line := range strings.Split(listing, "\n") {
		switch t := strings.TrimSpace(line); t {
		case "input:", "output:":
			section = strings.TrimSuffix(t, ":")
			continue
		}
		if section != kind {
			continue
		}
		m := d2gDeviceRE.FindStringSubmatch(line)
		if m == nil || strings.HasPrefix(m[2], "Monitor of ") || !strings.Contains(strings.ToLower(m[2]), strings.ToLower(hint)) {
			continue
		}
		n, _ = strconv.Atoi(m[1])
		return n, true
	}
	return 0, false
}

// data2gArgs is the command line of one rig's Data2G (after the "data2g" launcher). Data2G keys the radio
// through this rig's rigctld (Hamlib model 2, NET rigctl), like Mercury, so the CAT port has one owner. in
// and out are device numbers, or -1 to leave the choice to Data2G. With ui it runs the web front end
// (locked until a password is set with "data2g web --set-password"), otherwise the headless host.
func data2gArgs(cfg config.Config, rig config.Rig, callsign, settings string, in, out int, ui bool) []string {
	command, _, kiss, web := config.Data2GPorts(rig)
	argv := []string{"host"}
	if ui {
		argv = []string{"web", "--web-listen", config.BindHost(cfg.NetworkAccess("data2g@"+rig.ID+"/web", config.AccessAll), netinfo.LANAddrs()), "--web-port", strconv.Itoa(web)}
	}
	argv = append(argv, "--config", settings,
		"--command-port", strconv.Itoa(command), "--kiss-port", strconv.Itoa(kiss),
		"--rig-model", "2", "--rig-device", "localhost:"+strconv.Itoa(rig.RigPort), "--ptt-method", "cat")
	if callsign != "" {
		argv = append(argv, "--mycall", callsign)
	}
	if in >= 0 {
		argv = append(argv, "--input-device", strconv.Itoa(in))
	}
	if out >= 0 {
		argv = append(argv, "--output-device", strconv.Itoa(out))
	}
	return argv
}

// data2gSettingsFile is where one rig's Data2G keeps the settings made in its own GUI or web UI, the web
// password among them. The service starts with --config pointing here, and the password is set against the same file.
func data2gSettingsFile(rigID string) (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "rigs", rigID, "data2g.conf"), nil
}

// hasData2GPassword reports whether a settings file holds a web password (a "password=" line under [web]).
func hasData2GPassword(file string) bool {
	b, err := os.ReadFile(file)
	if err != nil {
		return false
	}
	section := ""
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = line
		} else if section == "[web]" && strings.HasPrefix(line, "password=") && len(line) > len("password=") {
			return true
		}
	}
	return false
}

// data2gRigs lists the rigs that use Data2G, whether or not they are switched on.
func data2gRigs(cfg config.Config) []config.Rig {
	var out []config.Rig
	for _, r := range cfg.Rigs {
		if r.Modem == config.Data2G {
			out = append(out, r)
		}
	}
	return out
}

// cmdData2G is "hamstack data2g password [RIG]".
func cmdData2G(args []string) error {
	if len(args) == 0 || args[0] != "password" || len(args) > 2 {
		return fmt.Errorf("usage: hamstack data2g password [RIG]   (sets the password of Data2G's web UI; it asks for it)")
	}
	rig := ""
	if len(args) == 2 {
		rig = args[1]
	}
	return data2gPassword(rig)
}

// data2gPassword asks for the web UI password of a Data2G rig and saves it in that rig's settings file, which
// the running service reads again by itself. Data2G does the asking (no echo, twice), so it needs a terminal.
func data2gPassword(rigID string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	rigs := data2gRigs(cfg)
	switch {
	case len(rigs) == 0:
		return fmt.Errorf("no rig uses Data2G (hamstack rig set RIG modem data2g)")
	case rigID == "" && len(rigs) > 1:
		var ids []string
		for _, r := range rigs {
			ids = append(ids, r.ID)
		}
		return fmt.Errorf("several rigs use Data2G (%s): say which one: hamstack data2g password RIG", strings.Join(ids, ", "))
	case rigID == "":
		rigID = rigs[0].ID
	}
	if !slices.ContainsFunc(rigs, func(r config.Rig) bool { return r.ID == rigID }) {
		return fmt.Errorf("rig %q does not use Data2G", rigID)
	}
	if !isTerminal() || !stdinIsTerminal() {
		return fmt.Errorf("the password is asked for on the terminal: run this in one")
	}
	path, err := versions.Which("data2g")
	if err != nil {
		return fmt.Errorf("data2g is not installed (run: hamstack install data2g)")
	}
	file, err := data2gSettingsFile(rigID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	fmt.Printf("Web UI password for Data2G on rig %s (the running service picks it up; no restart needed)\n", rigID)
	cmd := exec.Command(path, "web", "--config", file, "--set-password")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

func runData2G(cfg config.Config, rig config.Rig, prof profile.Profile) error {
	snap, _ := scanDevices()
	st, _ := cfg.ActiveStation()
	dir, err := config.Dir()
	if err != nil {
		return err
	}
	rigDir := filepath.Join(dir, "rigs", rig.ID)
	if err := os.MkdirAll(rigDir, 0o755); err != nil {
		return err
	}
	settings, err := data2gSettingsFile(rig.ID)
	if err != nil {
		return err
	}
	path, err := versions.Which("data2g")
	if err != nil {
		return fmt.Errorf("data2g is not installed (run: hamstack install data2g)")
	}
	// Data2G picks sound devices by number or by part of a name. The numbers change, so look them up now:
	// the card's descriptive name is what the sound server's device names are built from.
	in, out := -1, -1
	if dev := resolveAudio(rig, prof, snap); dev != "" {
		id := strings.TrimPrefix(dev, "plughw:CARD=")
		id, _, _ = strings.Cut(id, ",")
		name := audio.CardName(id)
		list, _ := exec.Command(path, "host", "--list-audio-devices").Output()
		i, iok := data2gDevice(string(list), "input", name)
		o, ook := data2gDevice(string(list), "output", name)
		if !iok || !ook {
			fmt.Fprintf(os.Stderr, "hamstack: rig %s: Data2G does not list a sound device for the card %q (%s): is the radio's sound card plugged in?\n", rig.ID, id, orDash(name))
			os.Exit(exitRadioMissing)
		}
		in, out = i, o
	}
	if err := waitPort(rig.RigPort, 15*time.Second); err != nil {
		return fmt.Errorf("rig %s: rig control is not running: %w", rig.ID, err)
	}
	argv := append([]string{"data2g"}, data2gArgs(cfg, rig, st.Callsign, settings, in, out, cfg.UI)...)
	fmt.Fprintf(os.Stderr, "hamstack: rig %s: %s\n", rig.ID, strings.Join(argv, " "))
	return syscall.Exec(path, argv, os.Environ())
}
