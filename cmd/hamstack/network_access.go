package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"slices"
	"strconv"
	"strings"

	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/install"
	"github.com/ATCUSA/hamstack/internal/ledger"
	"github.com/ATCUSA/hamstack/internal/netaccess"
	"github.com/ATCUSA/hamstack/internal/netinfo"
	"github.com/ATCUSA/hamstack/internal/svc"
	"github.com/ATCUSA/hamstack/internal/units"
)

const firewallHelper = "/usr/local/libexec/hamstack-network-access.py"

type accessTarget struct {
	ID      string
	Service string
	What    string
	Port    int
	Mode    string
	Native  bool
}

func accessTargets(c config.Config) []accessTarget {
	var targets []accessTarget
	for _, row := range networkRows(c, nil) {
		id := row.Service + "/" + strconv.Itoa(row.Port)
		fallback := config.AccessAll
		native := false
		if row.Service == "pat" {
			id = "pat"
			native = true
			if st, ok := c.ActiveStation(); ok {
				fallback = st.EffectiveAccess()
			}
			if strings.Contains(row.What, "through the dashboard") { // the dashboard serves it: the dashboard's access
				id, fallback = "pat/relay", c.Web.EffectiveAccess()
			} else if c.PatRelayed() { // Pat's own port while the dashboard serves its page: this computer only
				targets = append(targets, accessTarget{id, row.Service, row.What, row.Port, config.AccessLocal, native})
				continue
			}
		}
		if row.Service == "web" {
			id = "web"
			native = true
			fallback = c.Web.EffectiveAccess()
		}
		if strings.HasPrefix(row.Service, "data2g@") && strings.Contains(row.What, "web UI") {
			id = row.Service + "/web"
			native = true
		}
		if strings.HasPrefix(row.Service, "ardopcf@") && strings.Contains(row.What, "screen") {
			id = row.Service + "/gui" // always this computer only: see firewallPolicy
		}
		if strings.HasPrefix(row.Service, "rigctld@") || row.Service == "gpsd" || row.Service == "flrig" { // flrig's can key the radio
			fallback = config.AccessLocal
		}
		if fallback == "" {
			fallback = config.AccessLocal
		}
		targets = append(targets, accessTarget{id, row.Service, row.What, row.Port, c.NetworkAccess(id, fallback), native})
	}
	return targets
}
func findAccessTarget(c config.Config, id string) (accessTarget, error) {
	for _, t := range accessTargets(c) {
		if t.ID == id {
			return t, nil
		}
	}
	return accessTarget{}, fmt.Errorf("unknown endpoint %q (hamstack network access)", id)
}

type firewallPort struct {
	Port int    `json:"port"`
	Mode string `json:"mode"`
}

func firewallPolicy(c config.Config) []firewallPort {
	out := []firewallPort{}
	// Restrict only endpoints explicitly configured. Untouched defaults retain their behavior.
	for _, t := range accessTargets(c) {
		if strings.HasSuffix(t.ID, "/gui") {
			// ardopcf's page (no password, buttons that send audio) is never let past this computer. Only while it is
			// turned on: a firewall installed before this port existed has not approved it, and must not start
			// refusing every policy change because of a page that is not running.
			if r, ok := c.RigByID(strings.TrimPrefix(t.Service, "ardopcf@")); ok && ardopScreenOn(*r) {
				out = append(out, firewallPort{t.Port, config.AccessLocal})
			}
			continue
		}
		if !t.Native && (c.Network[t.ID] != "" || func() bool { s, ok := c.Here(); return ok && s.Network[t.ID] != "" }()) {
			out = append(out, firewallPort{t.Port, t.Mode})
		}
	}
	return out
}
func applyFirewall(c config.Config) (string, error) {
	if !firewallAvailable() {
		return "", fmt.Errorf("firewall helper not installed: hamstack network firewall install (review and confirm its plan first)")
	}
	body, err := json.Marshal(firewallPolicy(c))
	if err != nil {
		return "", err
	}
	argv := []string{"/usr/bin/python3", firewallHelper, "update"}
	if os.Geteuid() != 0 {
		argv = append([]string{"sudo", "-n"}, argv...)
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin = bytes.NewReader(body)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("firewall was not applied: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return strings.TrimSpace(string(out)), nil
}
func setNetworkAccess(id, mode string, restart, firewall bool) (string, error) {
	c, err := config.Load()
	if err != nil {
		return "", err
	}
	target, err := findAccessTarget(c, id)
	if err != nil {
		return "", err
	}
	access, err := config.ParseAccess(mode)
	if err != nil {
		return "", err
	}
	if !target.Native && !firewall {
		return "", fmt.Errorf("%s needs firewall enforcement; review with hamstack network access %s %s --firewall", target.Service, id, access)
	}
	next := cloneConfig(c)
	if err := next.SetNetworkAccess(id, access); err != nil {
		return "", err
	}
	message := ""
	if !target.Native {
		message, err = applyFirewall(next)
		if err != nil {
			return "", err
		}
	}
	if err := config.Save(next); err != nil {
		return "", fmt.Errorf("network policy applied but saving configuration failed: %w", err)
	}
	note, err := applyUnits(c)
	if err != nil {
		return "", fmt.Errorf("preference saved; regenerate service files before restarting: %w", err)
	}
	if restart && svc.Available() == nil && target.Service != "flrig" { // FLRig is a program, not a service: the rule needs no restart
		ss, e := units.Resolve(next, target.Service)
		if e != nil {
			return "", e
		}
		if len(ss) > 0 && svc.Status(ss[0].Unit()).Active == "active" {
			if e := svc.Run("restart", ss[0].Unit()); e != nil {
				return "", fmt.Errorf("saved, but restart failed: %w", e)
			}
		}
	}
	if target.Native && !restart {
		message = "Saved; restart " + target.Service + " to apply"
	}
	return joinMsg("Access for "+id+": "+access, message, note), nil
}
func networkCommand(args []string) error {
	if len(args) == 0 {
		return cmdNetwork()
	}
	if args[0] == "firewall" {
		return firewallCommand(args[1:])
	}
	if args[0] != "access" {
		return fmt.Errorf("usage: hamstack network [access [TARGET local|network|all] [--restart] [--firewall] [--yes] | firewall install|apply]")
	}
	fs := flag.NewFlagSet("network access", flag.ContinueOnError)
	restart := fs.Bool("restart", false, "restart the affected service")
	firewall := fs.Bool("firewall", false, "confirm this endpoint uses firewall enforcement")
	yes := fs.Bool("yes", false, "confirm the shown change")
	pos, err := parseInterspersed(fs, args[1:])
	if err != nil {
		return err
	}
	c, err := config.Load()
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		for _, t := range accessTargets(c) {
			method := "native bind"
			if !t.Native {
				method = "requires firewall"
			}
			fmt.Printf("%-28s %-8s %-20s %s\n", t.ID, t.Mode, method, t.What)
		}
		return nil
	}
	if len(pos) == 1 {
		t, e := findAccessTarget(c, pos[0])
		if e != nil {
			return e
		}
		a := &asker{in: bufioStdin(), out: os.Stdout}
		mode, ok := a.line("Who can connect? local, network or all [" + t.Mode + "]: ")
		if !ok {
			return fmt.Errorf("cancelled")
		}
		if mode == "" {
			mode = t.Mode
		}
		pos = append(pos, mode)
		if !t.Native && !*firewall {
			*firewall = a.confirm("This endpoint needs firewall enforcement. Apply it?", false)
		}
		*restart = a.confirm("Restart the service if running?", true)
	}
	if len(pos) != 2 {
		return fmt.Errorf("give TARGET local|network|all")
	}
	t, err := findAccessTarget(c, pos[0])
	if err != nil {
		return err
	}
	fmt.Printf("Set %s (TCP %d) to %s. Restart: %t. Site: %s\n", t.Service, t.Port, pos[1], *restart, orDash(c.CurrentSite))
	if !t.Native {
		fmt.Println("Firewall restriction affects all listeners on this TCP port; loopback stays allowed for local radio clients. Network allows only detected local subnets and follows address changes.")
	}
	if !*yes && !(&asker{in: bufioStdin(), out: os.Stdout}).confirm("Apply this access change?", false) {
		return fmt.Errorf("cancelled")
	}
	msg, err := setNetworkAccess(pos[0], pos[1], *restart, *firewall)
	if msg != "" {
		fmt.Println(msg)
	}
	return err
}
func firewallCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: hamstack network firewall install [--yes] [--dry-run] | apply")
	}
	if args[0] == "apply" {
		c, err := config.Load()
		if err != nil {
			return err
		}
		msg, err := applyFirewall(c)
		fmt.Println(msg)
		return err
	}
	if args[0] != "install" {
		return fmt.Errorf("unknown firewall command")
	}
	fs := flag.NewFlagSet("firewall install", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "confirm installation")
	dry := fs.Bool("dry-run", false, "only show installation plan")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	steps, err := firewallSteps()
	if err != nil {
		return err
	}
	return install.Run(steps, install.Options{Yes: *yes, DryRun: *dry, Notice: "Installs nftables, a root-owned narrow helper, a LAN-address watcher, and a sudo rule allowing this user to update only their hamstack firewall table. Existing firewall tables are left intact; other firewalls can still block allowed traffic."})
}
func firewallSteps() ([]install.Step, error) {
	u, err := user.Current()
	if err != nil {
		return nil, err
	}
	if !sudoUser.MatchString(u.Username) {
		return nil, fmt.Errorf("unsupported username for sudoers rule")
	}
	env := install.HostEnv()
	var packages []string
	for _, dependency := range []struct{ tool, pkg string }{{"nft", "nftables"}, {"python3", "python3"}, {"ip", "iproute2"}} {
		installed := false
		for _, dir := range []string{"/usr/sbin", "/usr/bin", "/sbin", "/bin"} {
			if st, e := os.Stat(dir + "/" + dependency.tool); e == nil && !st.IsDir() && st.Mode()&0111 != 0 {
				installed = true
				break
			}
		}
		if installed {
			continue
		}
		pkg := dependency.pkg
		if env.Distro.Family == "arch" && pkg == "python3" {
			pkg = "python"
		}
		if env.Distro.Family == "fedora" && pkg == "iproute2" {
			pkg = "iproute"
		}
		packages = append(packages, pkg)
	}
	var steps []install.Step
	if len(packages) > 0 {
		steps, err = env.PkgSteps("Install missing firewall dependencies: "+strings.Join(packages, ", "), packages...)
		if err != nil {
			return nil, err
		}
	}

	ports := []int{}
	for _, target := range accessTargets(loadCfg()) {
		ports = append(ports, target.Port)
	}
	allowed, _ := json.Marshal(ports)
	files := []struct{ path, body string }{
		{"/etc/hamstack-network-access/" + u.Uid + ".json", string(allowed)},
		{firewallHelper, netaccess.Helper},
		{"/etc/systemd/system/hamstack-network-access.service", "[Unit]\nDescription=hamstack network access policy and LAN address watcher\nAfter=network.target\nBefore=network-online.target\n\n[Service]\nType=simple\nExecStart=/usr/bin/python3 " + firewallHelper + " watch\nRestart=on-failure\nUMask=0077\n\n[Install]\nWantedBy=multi-user.target\n"},
	}
	for _, f := range files {
		step, e := install.RootFileStepFor(env, ledger.Support, "firewall", "Install "+f.path, f.path, f.body)
		if e != nil {
			return nil, e
		}
		steps = append(steps, step)
	}
	rule := "/etc/sudoers.d/hamstack-network-" + u.Uid
	step, err := sudoersFileStep(env, "Install "+rule+" (checked with visudo first)", rule,
		u.Username+" ALL=(root) NOPASSWD: /usr/bin/python3 "+firewallHelper+" update\n", "firewall")
	if err != nil {
		return nil, err
	}
	steps = append(steps, step)
	for _, cmd := range [][]string{{"systemctl", "daemon-reload"}, {"systemctl", "enable", "--now", "hamstack-network-access.service"}} {
		argv, e := env.Priv(cmd...)
		if e != nil {
			return nil, e
		}
		steps = append(steps, install.Step{Desc: strings.Join(cmd, " "), Cmd: argv})
	}
	steps[len(steps)-1].Track = func() func() []ledger.Change {
		return func() []ledger.Change {
			return []ledger.Change{{Kind: ledger.Firewall, Role: ledger.Support, For: "firewall", User: u.Uid}}
		}
	}
	return install.For("firewall", steps), nil
}

func guidedNetworkAccess(a *asker) error {
	for _, target := range accessTargets(loadCfg()) {
		mode, ok := a.line(fmt.Sprintf("%s (%s): local, network or all [%s]: ", target.ID, target.What, target.Mode))
		if !ok {
			return nil
		}
		if mode == "" || mode == target.Mode {
			continue
		}
		firewall := false
		if !target.Native {
			fmt.Fprintln(a.out, "This app needs per-port firewall enforcement. Local clients remain allowed.")
			if !firewallAvailable() {
				fmt.Fprintln(a.out, "Install it first: hamstack network firewall install. Leaving this endpoint unchanged.")
				continue
			}
			firewall = a.confirm("Apply firewall rules for this port?", false)
			if !firewall {
				continue
			}
		}
		restart := a.confirm("Restart the affected running service to apply the change?", true)
		msg, err := setNetworkAccess(target.ID, mode, restart, firewall)
		if err != nil {
			return err
		}
		fmt.Fprintln(a.out, msg)
	}
	return nil
}

// firewallApproves reports whether the installed firewall helper may restrict port: it only accepts the ports
// approved when it was installed (a new one needs the install plan again).
func firewallApproves(port int) bool {
	b, err := os.ReadFile("/etc/hamstack-network-access/" + strconv.Itoa(os.Getuid()) + ".json")
	if err != nil {
		return false
	}
	var ports []int
	return json.Unmarshal(b, &ports) == nil && slices.Contains(ports, port)
}

// ardopGUIGuard says whether ardopcf's page may be started for rig now: only with the firewall holding its port to
// this computer, and the rule applied just before (it may never have been, or been lost with a reboot).
var ardopGUIGuard = func(cfg config.Config, rig config.Rig) error {
	port := config.ArdopGUIPort(rig)
	switch {
	case !firewallAvailable():
		return fmt.Errorf("the firewall helper is not installed, and the page would be open to the whole network: install it (Settings, Network, or: hamstack plan firewall)")
	case !firewallApproves(port):
		return fmt.Errorf("the firewall helper does not cover port %d yet: install it again to approve it (Settings, Network, or: hamstack plan firewall)", port)
	}
	if _, err := applyFirewall(cfg); err != nil {
		return err
	}
	return nil
}

func firewallAvailable() bool {
	return fileExists(firewallHelper) && fileExists("/etc/hamstack-network-access/"+strconv.Itoa(os.Getuid())+".json")
}

// accessRank orders the access modes from narrowest to widest.
var accessRank = map[string]int{config.AccessLocal: 0, config.AccessNetwork: 1, config.AccessAll: 2}

// effectiveReach is who can actually reach a port now: where its program listens (loopback, one address, every
// address), narrowed by a firewall rule that holds the port (enforced: "" when none does). "" when nothing listens.
// The access setting alone is not it: a modem's port with no rule reads "all" there, while xara listens on loopback.
func effectiveReach(open bool, scope netinfo.Scope, enforced string) string {
	if !open {
		return ""
	}
	reach := config.AccessAll
	switch scope {
	case netinfo.Local:
		return config.AccessLocal
	case netinfo.Address:
		reach = config.AccessNetwork
	}
	if r, ok := accessRank[enforced]; ok && r < accessRank[reach] {
		reach = enforced
	}
	return reach
}

// enforcedPorts are the ports the firewall holds now, with their mode: none without the firewall helper.
func enforcedPorts(c config.Config) map[int]string {
	out := map[int]string{}
	if !firewallAvailable() {
		return out
	}
	for _, p := range firewallPolicy(c) {
		out[p.Port] = p.Mode
	}
	return out
}
