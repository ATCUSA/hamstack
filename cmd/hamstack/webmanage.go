package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ATCUSA/hamstack/internal/apps"
	"github.com/ATCUSA/hamstack/internal/catalog"
	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/distro"
	"github.com/ATCUSA/hamstack/internal/gps"
	"github.com/ATCUSA/hamstack/internal/install"
	"github.com/ATCUSA/hamstack/internal/svc"
	"github.com/ATCUSA/hamstack/internal/tui"
	"github.com/ATCUSA/hamstack/internal/units"
	"github.com/ATCUSA/hamstack/internal/updates"
	"github.com/ATCUSA/hamstack/internal/versions"
	"github.com/ATCUSA/hamstack/internal/web"
)

type managementState struct {
	Rigs        []tui.Rig
	Sites       []tui.Site
	Stations    []tui.StationRow
	Devices     []tui.Device
	Programs    []tui.Program
	Settings    []tui.Setting
	Profiles    []tui.ProfileOpt
	Network     tui.NetworkInfo
	GPS         *config.GPS
	Packet      map[string]config.PacketOpts
	Dashboard   *config.WebSettings
	Updates     updates.Snapshot
	Maintenance maintenanceState
	System      systemInfo
	Launchers   []string
	Categories  []catalogCategory // the catalog's groups, in listing order
}

type catalogCategory struct{ ID, Title string }

func (webBackend) ManagementState() any {
	b := backend{}
	cfg := loadCfg()
	packet := map[string]config.PacketOpts{}
	for _, rig := range cfg.Rigs {
		packet[rig.ID] = rig.Packet
	}
	return managementState{Rigs: b.Rigs(), Sites: b.Sites(), Stations: b.Stations(), Devices: b.Devices(), Programs: b.Programs(), Settings: b.Settings(), Profiles: b.Profiles(), Network: b.Network(), GPS: cfg.GPS, Packet: packet, Dashboard: cfg.Web, Updates: updates.Fresh(), Maintenance: maintenanceStatus(), System: gatherSystemInfo(), Launchers: installedLaunchers(), Categories: catalogCategories()}

}

// Every action is explicitly mapped to an existing backend operation. The API
// never accepts a command line, executable, or shell fragment from a browser.
func (webBackend) Manage(r web.ManagementRequest) (any, error) {
	b := backend{}
	switch r.Action {
	case "updates.self":
		if !r.Confirm {
			return nil, fmt.Errorf("confirm replacement of the hamstack executable")
		}
		return selfUpdateJob()
	case "rig.set":
		return mutate(func(c *config.Config) (string, error) {
			rig, ok := c.RigByID(r.ID)
			if !ok {
				return "", fmt.Errorf("unknown rig")
			}
			if err := setRigField(rig, r.Value, r.Extra); err != nil {
				return "", err
			}
			return "Rig saved; review pending restarts", nil
		})
	case "updates.check":
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		return updates.Check(ctx, true)
	case "maintenance.status":
		return maintenanceStatus(), nil
	case "maintenance.apply":
		if !r.Confirm {
			return nil, fmt.Errorf("confirm service interruptions")
		}
		if err := jobGuard("restarting services"); err != nil {
			return nil, err
		}
		state := maintenanceStatus()
		if state.Reload {
			if err := svc.Run("daemon-reload"); err != nil {
				return nil, err
			}
		}
		dash := false
		for _, name := range state.Services {
			if name == "web" {
				dash = true // last, and after this reply has gone out: it is the page asking
				continue
			}
			if err := b.Control("restart", name); err != nil {
				return nil, err
			}
		}
		if dash {
			go func() { time.Sleep(time.Second); _ = svc.Run("restart", units.Service{Kind: config.Web}.Unit()) }()
			return "Services restarted. The dashboard is restarting too: this page reloads when it is back.", nil
		}
		return "Affected services restarted", nil
	case "updates.rollback":
		if !r.Confirm {
			return nil, fmt.Errorf("confirm going back to the previous hamstack")
		}
		return "Went back to the previous hamstack", rollbackSelf()
	case "program.rollback":
		if !r.Confirm {
			return nil, fmt.Errorf("confirm removing hamstack's copy of %s", r.ID)
		}
		if _, ok := catalogPkg(r.ID); !ok {
			return nil, fmt.Errorf("no program %q", r.ID)
		}
		return rollbackProgram(r.ID)
	case "app.field.set":
		return appFieldSet(r)
	case "app.setup.apply":
		return appSetupApply(r)
	case "app.setup.undo":
		return appSetupUndo(r)
	case "launcher.install":
		self, err := os.Executable()
		if err != nil {
			return nil, err
		}
		written, err := installLaunchers(loadCfg(), self)
		return fmt.Sprintf("%d launchers are in the application menu on the radio computer", len(written)), err
	case "launcher.remove":
		n, err := removeLaunchers()
		return fmt.Sprintf("removed %d launchers", n), err
	case "dashboard.tune":
		if !r.Confirm {
			return nil, fmt.Errorf("confirm: the Tune button makes the radio transmit for a few seconds")
		}
		return mutateOnly(func(c *config.Config) (string, error) {
			if c.Web == nil {
				return "", fmt.Errorf("dashboard not configured")
			}
			c.Web.RigTune = r.Value == "on"
			return "Tuning from the dashboard is " + r.Value, nil
		})
	case "system.check":
		env := install.HostEnv()
		list, note, err := checkSystemUpdates(env, sudoAllowedNow())
		if err != nil {
			return nil, err
		}
		return fmt.Sprintf("%d package updates waiting%s", len(list), map[bool]string{true: ": " + strings.Join(list, ", "), false: ""}[len(list) > 0]) + map[bool]string{true: "\n(" + note + ")", false: ""}[note != ""], nil
	case "system.update":
		if !r.Confirm {
			return nil, fmt.Errorf("confirm the system update")
		}
		lastSystemCheck.Lock()
		lastSystemCheck.updates, lastSystemCheck.at = nil, time.Time{} // what was known is stale once it runs
		lastSystemCheck.Unlock()
		return systemUpdateJob(r.Secret)
	case "system.shutdown":
		if !r.Confirm || r.Extra != "SHUT DOWN" {
			return nil, fmt.Errorf("type SHUT DOWN and confirm: the computer powers off and cannot be turned back on from here")
		}
		if err := jobGuard("shutting down"); err != nil {
			return nil, err
		}
		if err := poweroffConfirmed(r.Secret); err != nil {
			return nil, err
		}
		return "Shutdown requested. The computer is powering off.", nil
	case "maintenance.reboot":
		if !r.Confirm || r.Extra != "REBOOT" {
			return nil, fmt.Errorf("type REBOOT and explicitly confirm: all users and radio sessions will be disconnected")
		}
		if err := jobGuard("restarting the computer"); err != nil {
			return nil, err
		}
		if err := rebootConfirmed(r.Secret); err != nil {
			return nil, err
		}
		return "Reboot requested. You will be disconnected.", nil
	case "groups.plan", "groups.fix":
		rows := groupRows()
		steps, err := groupSteps(rows[0].info, rows[1].info, true)
		if err != nil {
			return nil, err
		}
		return runWebSteps(steps, "Adds missing serial and audio groups; a new login and service-manager session are needed afterward.", r.Action == "groups.plan", r.Confirm)

	case "network.access":
		if !r.Confirm {
			return nil, fmt.Errorf("confirm the access change and any required firewall enforcement")
		}
		restart := strings.Contains(r.Extra, "restart")
		if r.ID == "web" && restart {
			msg, err := setNetworkAccess(r.ID, r.Value, false, strings.Contains(r.Extra, "firewall"))
			if err == nil {
				go func() { time.Sleep(time.Second); _ = b.Control("restart", "web") }()
			}
			return msg, err
		}
		return setNetworkAccess(r.ID, r.Value, restart, strings.Contains(r.Extra, "firewall"))
	case "network.firewall.plan", "network.firewall.install":
		steps, err := firewallSteps()
		if err != nil {
			return nil, err
		}
		return runWebSteps(steps, "Installs a root-owned nftables helper, LAN watcher and narrow sudo permission. This changes access for whole TCP ports.", r.Action == "network.firewall.plan", r.Confirm)
	case "dashboard.console":
		if !r.Confirm {
			return nil, fmt.Errorf("confirm full CLI/terminal access for all dashboard users")
		}
		msg, err := mutateOnly(func(c *config.Config) (string, error) {
			if c.Web == nil {
				return "", fmt.Errorf("dashboard not configured")
			}
			c.Web.Console = r.Value == "on"
			return "Browser console preference saved", nil
		})
		if err != nil {
			return nil, err
		}
		if r.Value != "on" {
			_ = svc.Run("stop", vncUnit("console"))
		}
		return msg, nil
	case "dashboard.disable":
		if !r.Confirm {
			return nil, fmt.Errorf("confirm disconnecting the dashboard and all browser VNC sessions")
		}
		if err := jobGuard("turning the dashboard off"); err != nil {
			return nil, err
		}
		msg, err := mutateOnly(func(c *config.Config) (string, error) {
			if c.Web != nil {
				c.Web.Off = true
			}
			return "Dashboard disabled. Re-enable with hamstack dashboard on", nil
		})
		if err == nil {
			go func() {
				time.Sleep(time.Second)
				for _, app := range apps.All {
					_ = svc.Run("stop", vncUnit(app.Name))
				}
				_ = svc.Run("stop", desktopUnit())
				_ = svc.Run("disable", "--now", units.Service{Kind: config.Web}.Unit())
			}()
		}
		return msg, err
	case "remote.check":
		return remoteChecks(), nil
	case "remote.plan", "remote.install":
		steps, err := remoteInstallSteps(r.Value == "desktop")
		if err == nil && r.Value == "console" {
			if _, e := desktopPath("xterm"); e != nil {
				extra, e := install.HostEnv().PkgSteps("Install the interactive browser console terminal", "xterm")
				if e != nil {
					return nil, e
				}
				steps = append(steps, extra...)
			}
		}
		if err != nil {
			return nil, err
		}
		return runWebSteps(steps, "Installs browser VNC components. Existing installed components are kept.", r.Action == "remote.plan", r.Confirm)

	case "dashboard.password":
		if len(r.Value) < 8 {
			return nil, fmt.Errorf("use at least 8 characters")
		}
		if r.Value != r.Extra {
			return nil, fmt.Errorf("password confirmation did not match")
		}
		hash, err := web.HashPassword(r.Value)
		if err != nil {
			return nil, err
		}
		file, err := webPasswordFile()
		if err != nil {
			return nil, err
		}
		return "Dashboard password changed", web.SavePasswordHash(file, hash)
	case "dashboard.tls":
		if !r.Confirm {
			return nil, fmt.Errorf("confirm certificate change")
		}
		return mutateOnly(func(c *config.Config) (string, error) {
			if c.Web == nil {
				return "", fmt.Errorf("dashboard is not configured")
			}
			w := *c.Web
			if r.Value != "" {
				if _, err := tls.LoadX509KeyPair(r.Value, r.Extra); err != nil {
					return "", err
				}
			}
			if r.Value != "" {
				r.Value, _ = filepath.Abs(r.Value)
				r.Extra, _ = filepath.Abs(r.Extra)
			}
			w.Cert, w.Key = r.Value, r.Extra
			if err := c.SetWeb(w); err != nil {
				return "", err
			}
			return "Certificate saved; restart the dashboard to switch certificate paths (renewals at the same paths reload automatically)", nil
		})
	case "dashboard.port":
		port, err := strconv.Atoi(r.Value)
		if err != nil || port < 1024 || port > 65535 {
			return nil, fmt.Errorf("choose an unprivileged port between 1024 and 65535")
		}
		return mutateOnly(func(c *config.Config) (string, error) {
			if c.Web == nil {
				return "", fmt.Errorf("dashboard not configured")
			}
			w := *c.Web
			w.Port = port
			if err := c.SetWeb(w); err != nil {
				return "", err
			}
			return "Dashboard port saved; restart web to apply", nil
		})
	case "gps.configure":
		if r.ID == "external" {
			return mutate(func(c *config.Config) (string, error) {
				return "GPS configured", c.SetGPS(config.GPS{Addr: gps.Addr(r.Value)})
			})
		}
		// Use the same USB checks and radio-port exclusions as the CLI.
		return "GPS configured", gpsUse([]string{r.Value})
	case "gps.toggle":
		return "GPS updated", gpsOnOff(r.Value == "off")
	case "gps.remove":
		if !r.Confirm {
			return nil, fmt.Errorf("confirm GPS removal")
		}
		return "GPS removed", gpsRemove()
	case "gps.grid":
		return "Grid updated from GPS", gpsGrid(nil)
	case "service.restart-web":
		if !r.Confirm {
			return nil, fmt.Errorf("confirm dashboard restart; the connection will briefly close")
		}
		if err := jobGuard("restarting the dashboard"); err != nil {
			return nil, err
		}
		go func() { time.Sleep(time.Second); _ = svc.Run("restart", units.Service{Kind: config.Web}.Unit()) }()
		return "Dashboard is restarting; reload after a few seconds", nil
	case "linger.set":
		return "Boot startup updated", svc.SetLinger(r.Value == "on")

	case "rig.add":
		return b.RigAdd(r.Value, r.ID, r.Extra)
	case "rig.toggle":
		return b.RigToggle(r.ID)
	case "rig.modem":
		return b.RigModem(r.ID, r.Value)
	case "rig.remove":
		if !r.Confirm {
			return nil, fmt.Errorf("confirm removal")
		}
		return b.RigRemove(r.ID)
	case "rig.search":
		return b.SearchRadios(r.Value)
	case "rig.profile":
		var model, baud int
		if _, err := fmt.Sscan(r.Value, &model); err != nil {
			return nil, err
		}
		if _, err := fmt.Sscan(r.Extra, &baud); err != nil {
			return nil, err
		}
		return b.SaveRadio(model, baud)
	case "site.add":
		return b.SiteAdd(r.ID)
	case "site.use":
		return b.SiteUse(r.ID)
	case "site.grid":
		return b.SiteGrid(r.ID, r.Value)
	case "site.remove":
		if !r.Confirm {
			return nil, fmt.Errorf("confirm removal")
		}
		return b.SiteRemove(r.ID)
	case "station.add":
		return b.StationAdd(r.ID, r.Value)
	case "station.use":
		return b.Use(r.ID)
	case "station.access":
		return b.StationAccess(r.ID, r.Value)
	case "station.import":
		return b.StationImport()
	case "station.remove":
		if !r.Confirm {
			return nil, fmt.Errorf("confirm removal")
		}
		return b.StationRemove(r.ID)
	case "device.assign":
		return b.AssignDevice(r.ID, r.Value)
	case "setting.set":
		return b.SetSetting(r.ID, r.Value)
	case "service.control":
		if r.ID == "web" {
			return nil, fmt.Errorf("use the confirmed dashboard restart/disable controls for this service")
		}
		if !slices.Contains([]string{"start", "stop", "restart", "enable", "disable"}, r.Value) {
			return nil, fmt.Errorf("invalid service action")
		}
		return "service updated", b.Control(r.Value, r.ID)
	case "service.logs":
		return b.Logs(r.ID, 200)
	case "units.install":
		return b.UnitsInstall()
	case "doctor.check":
		return b.Doctor(), nil
	case "doctor.fix":
		if !r.Confirm {
			return nil, fmt.Errorf("confirm fix")
		}
		for _, check := range b.Doctor() {
			if check.Name == r.ID {
				args, ok := tui.FixCommand(check.Fix)
				if !ok {
					return nil, fmt.Errorf("this fix requires terminal setup")
				}
				if slices.Equal(args, []string{"dashboard", "repair"}) {
					return dashboardRepairJob(r.Secret)
				}
				return runWebCommand(args)
			}
		}
		return nil, fmt.Errorf("check not found")
	case "program.latest":
		return b.Latest()
	case "program.plan":
		return webInstall(r)
	case "program.install":
		return installJob(r)
	case "program.bulk":
		return bulkJob(r)
	case "app.open":
		return "application opened", openApp(r.ID)
	case "app.close":
		return "application closed", webBackend{}.CloseApp(r.ID)
	case "desktop.open":
		return "desktop opened", webBackend{}.OpenDesktop()
	case "desktop.close":
		return "desktop stopped", webBackend{}.CloseDesktop()
	case "packet.settings":
		return mutateOnly(func(c *config.Config) (string, error) {
			rig, ok := c.RigByID(r.ID)
			if !ok {
				return "", fmt.Errorf("rig not found")
			}
			if err := setPacketField(&rig.Packet, r.Value, r.Extra); err != nil {
				return "", err
			}
			return "Packet settings saved; restart the service to apply", nil
		})
	}
	return nil, fmt.Errorf("unknown management action %q", r.Action)
}

func webInstall(r web.ManagementRequest) (any, error) {
	var names []string
	for _, name := range strings.Fields(strings.ReplaceAll(r.Value, ",", " ")) {
		if _, ok := catalog.ByName(name); !ok {
			return nil, fmt.Errorf("unknown program %q", name)
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("select at least one program")
	}
	requested := slices.Clone(names)
	names, _ = catalog.Closure(names)
	if conflicts := catalog.Conflicts(names); len(conflicts) > 0 {
		return nil, fmt.Errorf("conflicting selection: %v", conflicts)
	}
	src := map[string]bool{}
	if r.Extra == "source" {
		for _, n := range requested {
			if !canBuildFromSource(n) {
				return nil, fmt.Errorf("%s has no source recipe", n)
			}
			src[n] = true
		}
	}
	force := r.ID == "update"
	if force {
		for _, name := range requested {
			p, _ := catalog.ByName(name)
			in := versions.FindInstalled(p)
			// A package-manager program is updated by the package manager, unless a build from source was asked for:
			// that goes next to the package, under ~/.local.
			if in != nil && in.Origin == "system package" && r.Extra != "source" {
				return nil, fmt.Errorf("%s is distribution-managed: %s", name, systemUpdateHint(distro.Detect()))
			}
			if canBuildFromSource(name) {
				src[name] = true
			}
		}
	}
	if force {
		names = slices.DeleteFunc(names, func(name string) bool {
			if slices.Contains(requested, name) {
				return false
			}
			p, _ := catalog.ByName(name)
			return versions.FindInstalled(p) != nil
		})
	}
	steps, notice, err := buildInstallStepsSrc(names, force, src)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	// Package managers may need elevation. Never let sudo block an HTTP request
	// waiting for a password on a terminal: the plan and error remain reviewable.
	for i := range steps {
		if len(steps[i].Cmd) > 0 && steps[i].Cmd[0] == "sudo" {
			steps[i].Cmd = append([]string{"sudo", "-n"}, steps[i].Cmd[1:]...)
		}
	}
	dry := r.Action == "program.plan"
	if !dry && !r.Confirm {
		return nil, fmt.Errorf("review the plan and confirm installation")
	}
	err = install.Run(steps, install.Options{Yes: true, DryRun: dry, Out: &buf, In: strings.NewReader(""), Notice: notice})
	if err != nil {
		return nil, fmt.Errorf("%s\n%w", buf.String(), err)
	}
	return buf.String(), nil
}

func runWebCommand(args []string) (string, error) {
	// Fixes that require sudo or a terminal must be performed through their
	// explicit management forms or the local CLI, never an arbitrary shell.
	if len(args) == 0 {
		return "", fmt.Errorf("no fix command")
	}
	if len(args) == 2 && args[0] == "restart" {
		return "restarted", backend{}.Control("restart", args[1])
	}
	if len(args) == 2 && args[0] == "start" {
		return "started", backend{}.Control("start", args[1])
	}
	if slices.Equal(args, []string{"units", "install"}) {
		return backend{}.UnitsInstall()
	}
	return "", fmt.Errorf("this check needs an interactive local operation: hamstack %s", strings.Join(args, " "))
}

func runWebSteps(steps []install.Step, notice string, dry, confirm bool) (string, error) {
	if !dry && !confirm {
		return "", fmt.Errorf("review and confirm the installation plan")
	}
	for i := range steps {
		if len(steps[i].Cmd) > 0 && steps[i].Cmd[0] == "sudo" {
			steps[i].Cmd = append([]string{"sudo", "-n"}, steps[i].Cmd[1:]...)
		}
	}
	var buf bytes.Buffer
	err := install.Run(steps, install.Options{Yes: true, DryRun: dry, In: strings.NewReader(""), Out: &buf, Notice: notice})
	if err != nil {
		return "", fmt.Errorf("%s\n%w", buf.String(), err)
	}
	return buf.String(), nil
}

func catalogCategories() []catalogCategory {
	var out []catalogCategory
	for _, c := range catalog.Categories {
		out = append(out, catalogCategory{c.ID, c.Title})
	}
	return out
}
