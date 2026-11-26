package main

import (
	"encoding/json"
	"fmt"
	"github.com/ATCUSA/hamstack/internal/config"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/ATCUSA/hamstack/internal/svc"
	"github.com/ATCUSA/hamstack/internal/units"
)

type maintenanceState struct {
	Services []string `json:"services"`
	Reload   bool     `json:"reload"`
	Session  bool     `json:"session"`
	Reboot   bool     `json:"reboot"`
	Reasons  []string `json:"reasons"`
}

func maintenanceStatus() maintenanceState {
	var state maintenanceState
	pending := pendingRestarts()
	for name, stamp := range pending {
		if currentInvocation(name) == stamp {
			state.Services = append(state.Services, name)
		}
	}
	slices.Sort(state.Services)
	for _, r := range groupRows() {
		if !r.info.Exists || !r.info.Member {
			continue
		}
		if !r.info.Session || (r.info.ManagerKnown && !r.info.Manager) {
			state.Session = true
			state.Reasons = append(state.Reasons, r.info.Name+": log out and back in; the user service manager may also need restarting")
		}
	}
	for _, s := range units.ServicesFor(loadCfg()) {
		out, err := exec.Command("systemctl", "--user", "show", s.Unit(), "-p", "NeedDaemonReload", "-p", "ActiveState", "-p", "MainPID").Output()
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(out), "\n") {
			if value, ok := strings.CutPrefix(line, "MainPID="); ok && processNeedsRestart(value, 0) && !slices.Contains(state.Services, s.Name()) {
				state.Services = append(state.Services, s.Name())
			}
		}
		if strings.Contains(string(out), "NeedDaemonReload=yes") {
			state.Reload = true
		}
	}
	for _, path := range []string{"/run/reboot-required", "/var/run/reboot-required"} {
		if fileExists(path) {
			state.Reboot = true
			state.Reasons = append(state.Reasons, "The operating system reports that a reboot is required")
			break
		}
	}

	slices.Sort(state.Services)
	return state
}
func offerMaintenance(a *asker, noninteractive bool) error {
	state := maintenanceStatus()
	for _, name := range state.Services {
		fmt.Fprintln(a.out, "Restart needed:", name)
	}
	for _, reason := range state.Reasons {
		fmt.Fprintln(a.out, reason)
	}
	if noninteractive || !stdinIsTerminal() {
		if state.Session || state.Reboot || state.Reload {
			fmt.Fprintln(a.out, "Review actions with: hamstack maintenance")
		}
		return nil
	}
	if state.Reload && a.confirm("Reload the user service manager now?", true) {
		if err := svc.Run("daemon-reload"); err != nil {
			return err
		}
	}
	if len(state.Services) > 0 && a.confirm("Restart the affected services now? This interrupts any active radio sessions.", false) {
		for _, name := range state.Services {
			if err := (backend{}).Control("restart", name); err != nil {
				return err
			}
		}
	}

	if state.Session {
		fmt.Fprintln(a.out, "Restarting the user service manager ends this user's sessions and services. Logging out completely and back in is the usual way to apply group changes.")
	}
	if state.Reboot || state.Session {
		if a.confirm("WARNING: reboot disconnects all users and stops radio sessions. Reboot this computer now?", false) {
			return rebootConfirmed("")
		}
	}
	return nil
}
func rebootConfirmed(secret string) error { return powerAction("reboot", secret) }

var pendingMu sync.Mutex

func pendingPath() string { return filepath.Join(stateDir(), "pending-restarts.json") }
func pendingRestarts() map[string]string {
	pendingMu.Lock()
	defer pendingMu.Unlock()
	return readPending()
}
func readPending() map[string]string {
	out := map[string]string{}
	b, err := os.ReadFile(pendingPath())
	if err == nil {
		_ = json.Unmarshal(b, &out)
	}
	return out
}
func currentInvocation(name string) string {
	ss, err := units.Resolve(loadCfg(), name)
	if err != nil || len(ss) == 0 {
		return ""
	}
	out, err := exec.Command("systemctl", "--user", "show", ss[0].Unit(), "-p", "InvocationID", "--value").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// rigForRigctld is the part of a rig rig control starts from (runRigctld: the radio's model, its CAT device and speed,
// its port): a change anywhere else needs no restart of rig control. Kept to what it reads, so a new modem switch or
// radio-check choice never marks it.
func rigForRigctld(r config.Rig) config.Rig {
	return config.Rig{ID: r.ID, Profile: r.Profile, CAT: r.CAT, CATID: r.CATID, Baud: r.Baud, RigPort: r.RigPort, NoCAT: r.NoCAT, Off: r.Off}
}

// rigsForPat are the rigs as Pat's wiring sees them (patWiring: rig control's port, the modems and their ports).
func rigsForPat(rigs []config.Rig) []config.Rig {
	out := make([]config.Rig, len(rigs))
	for i, r := range rigs {
		out[i] = config.Rig{ID: r.ID, Modem: r.Modem, Alt: r.Alt, RigPort: r.RigPort, ArqPort: r.ArqPort, NoCAT: r.NoCAT, Off: r.Off}
	}
	return out
}

func changedServices(before, after config.Config) []string {
	var changed []string
	for _, service := range units.ServicesFor(after) {
		dirty := false
		switch service.Kind {
		case config.Web:
			if before.Web == nil || after.Web == nil {
				dirty = before.Web != after.Web
			} else {
				a, b := *before.Web, *after.Web
				a.Console, b.Console = false, false
				a.RigTune, b.RigTune = false, false // read each time it is used: nothing to restart
				dirty = a != b
			}
		case config.GPSD:
			dirty = !reflect.DeepEqual(before.GPS, after.GPS)
		case config.Pat:
			dirty = before.Active != after.Active || !reflect.DeepEqual(before.Stations, after.Stations) || !reflect.DeepEqual(rigsForPat(before.Rigs), rigsForPat(after.Rigs)) || before.EffectiveGrid() != after.EffectiveGrid()
		case config.RigCtld, config.Mercury, config.Data2G, config.Ardopcf, config.Xara, config.Direwolf:
			a, aok := before.RigByIDValue(service.Rig)
			b, bok := after.RigByIDValue(service.Rig)
			if service.Kind == config.RigCtld {
				a, b = rigForRigctld(a), rigForRigctld(b)
			}
			dirty = aok != bok || !reflect.DeepEqual(a, b)
			if service.Kind != config.RigCtld {
				a, _ := before.ActiveStation()
				b, _ := after.ActiveStation()
				dirty = dirty || a.Callsign != b.Callsign || before.UI != after.UI
			}
		}
		for _, target := range accessTargets(after) {
			if target.Service != service.Name() {
				continue
			}
			for _, old := range accessTargets(before) {
				if old.ID == target.ID && old.Mode != target.Mode {
					dirty = true
				}
			}
		}
		if dirty {
			changed = append(changed, service.Name())
		}
	}
	return changed
}
func recordRestarts(before, after config.Config) error {
	names := changedServices(before, after)
	if len(names) == 0 {
		return nil
	}
	pendingMu.Lock()
	defer pendingMu.Unlock()
	pending := readPending()
	for _, name := range names {
		stamp := currentInvocation(name)
		if stamp != "" {
			pending[name] = stamp
		}
	}
	if len(pending) == 0 {
		return nil
	}
	if err := os.MkdirAll(stateDir(), 0755); err != nil {
		return err
	}
	b, _ := json.Marshal(pending)
	tmp, err := os.CreateTemp(stateDir(), ".pending-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), pendingPath())
}

func processNeedsRestart(pid string, depth int) bool {
	n, err := strconv.Atoi(pid)
	if err != nil || n < 1 || depth > 4 {
		return false
	}
	path, err := os.Readlink("/proc/" + pid + "/exe")
	if err == nil && strings.HasSuffix(path, " (deleted)") {
		return true
	}
	children, _ := os.ReadFile("/proc/" + pid + "/task/" + pid + "/children")
	for i, child := range strings.Fields(string(children)) {
		if i >= 200 {
			break
		}
		if processNeedsRestart(child, depth+1) {
			return true
		}
	}
	return false
}
