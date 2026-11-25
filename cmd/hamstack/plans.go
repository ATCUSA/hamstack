package main

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"github.com/ATCUSA/hamstack/internal/install"
	"github.com/ATCUSA/hamstack/internal/web"
)

// Setup plans the dashboard shows and then runs. They used to run inside the dashboard with "sudo -n", which fails
// on any computer without a passwordless sudo rule, with sudo's reason lost in the service log: the page said only
// "exit status 1". Now the plan is shown from here, and running it is a job ("hamstack plan NAME --yes") that gets
// the administrator password through the dashboard's password dialog when sudo needs one, like an install.
type setupPlan struct {
	title  string // the job's title
	notice string
	steps  func() ([]install.Step, error)
}

var setupPlans = map[string]setupPlan{
	"firewall": {"Installing the firewall helper",
		"Installs a root-owned nftables helper, LAN watcher and narrow sudo permission. This changes access for whole TCP ports.",
		firewallSteps},
	"groups": {"Adding you to the serial and audio groups",
		"Adds missing serial and audio groups; a new login and service-manager session are needed afterward.",
		func() ([]install.Step, error) {
			rows := groupRows()
			return groupSteps(rows[0].info, rows[1].info, true)
		}},
	"remote": {"Installing the browser app components",
		"Installs browser VNC components. Existing installed components are kept.",
		func() ([]install.Step, error) { return remoteInstallSteps(false) }},
	"remote-desktop": {"Installing the desktop sharing components",
		"Installs browser VNC components. Existing installed components are kept.",
		func() ([]install.Step, error) { return remoteInstallSteps(true) }},
	"remote-console": {"Installing the browser console components",
		"Installs browser VNC components and the console terminal. Existing installed components are kept.",
		func() ([]install.Step, error) {
			steps, err := remoteInstallSteps(false)
			if err != nil {
				return nil, err
			}
			if _, e := desktopPath("xterm"); e != nil {
				extra, e := install.HostEnv().PkgSteps("Install the interactive browser console terminal", "xterm")
				if e != nil {
					return nil, e
				}
				steps = append(steps, extra...)
			}
			return steps, nil
		}},
}

func planNames() string {
	var names []string
	for n := range setupPlans {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, "|")
}

// cmdPlan runs one of the setup plans: hamstack plan NAME [--yes] [--dry-run].
func cmdPlan(args []string) error {
	usage := fmt.Errorf("usage: hamstack plan %s [--yes] [--dry-run]", planNames())
	if len(args) == 0 {
		return usage
	}
	p, ok := setupPlans[args[0]]
	if !ok {
		return usage
	}
	yes, dry := false, false
	for _, a := range args[1:] {
		switch a {
		case "--yes":
			yes = true
		case "--dry-run":
			dry = true
		default:
			return usage
		}
	}
	steps, err := p.steps()
	if err != nil {
		return err
	}
	return install.Run(steps, install.Options{Yes: yes, DryRun: dry, Notice: p.notice})
}

// webPlan shows a setup plan (dry), or runs it as a job once the person has confirmed it.
func webPlan(name string, r web.ManagementRequest, dry bool) (any, error) {
	p := setupPlans[name]
	steps, err := p.steps()
	if err != nil {
		return nil, err
	}
	if dry {
		var buf bytes.Buffer
		err := install.Run(steps, install.Options{DryRun: true, Out: &buf, Notice: p.notice})
		return buf.String(), err
	}
	if !r.Confirm {
		return nil, fmt.Errorf("review and confirm the plan")
	}
	if len(steps) == 0 {
		return "Nothing to do: it is all in place.", nil
	}
	spec := jobSpec{Title: p.title, Kind: "install", Cmds: [][]string{{"plan", name, "--yes"}}}
	if sudo := needsSudo(steps); len(sudo) > 0 && !sudoPasswordless() {
		if r.Secret == "" {
			return nil, needSudo(p.notice+" Parts of it run as root.", sudo, "hamstack plan "+name)
		}
		if err := verifySudoPassword(r.Secret); err != nil {
			return nil, err
		}
		spec.Secret = r.Secret
	}
	if err := jobGuard(strings.ToLower(p.title)); err != nil {
		return nil, err
	}
	id, err := startJob(spec)
	if err != nil {
		return nil, err
	}
	return map[string]string{"job": id}, nil
}
