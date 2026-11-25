package main

import (
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ATCUSA/hamstack/internal/apps"
	"github.com/ATCUSA/hamstack/internal/catalog"
	"github.com/ATCUSA/hamstack/internal/install"
	"github.com/ATCUSA/hamstack/internal/svc"
	"github.com/ATCUSA/hamstack/internal/units"
	"github.com/ATCUSA/hamstack/internal/versions"
	"github.com/ATCUSA/hamstack/internal/web"
)

// The dashboard's side of jobs: turning "update these" into the commands a job runs, and refusing, before anything
// starts, what cannot work from a page (a step that needs a password) or would be cut off.

var passwordless struct {
	sync.Mutex
	at time.Time
	ok bool
}

// sudoPasswordless reports whether sudo will run without asking: a job from the dashboard cannot type one.
func sudoPasswordless() bool {
	if install.HostEnv().Root {
		return true
	}
	passwordless.Lock()
	defer passwordless.Unlock()
	if !passwordless.at.IsZero() && time.Since(passwordless.at) < 2*time.Minute {
		return passwordless.ok
	}
	passwordless.ok = exec.Command("sudo", "-n", "true").Run() == nil
	passwordless.at = time.Now()
	return passwordless.ok
}

// needsSudo lists the privileged commands of a plan.
func needsSudo(steps []install.Step) []string {
	var out []string
	for _, s := range steps {
		if len(s.Cmd) > 0 && s.Cmd[0] == "sudo" {
			out = append(out, strings.Join(s.Cmd, " "))
		}
	}
	return out
}

// splitNames reads a list of program names separated by commas or spaces, and checks them.
func splitNames(list string) ([]string, error) {
	var names []string
	for _, n := range strings.Fields(strings.ReplaceAll(list, ",", " ")) {
		if _, ok := catalog.ByName(n); !ok {
			return nil, fmt.Errorf("unknown program %q", n)
		}
		names = append(names, n)
	}
	return names, nil
}

// installJob starts one program's install, update or source build.
func installJob(r web.ManagementRequest) (any, error) {
	if !r.Confirm {
		return nil, fmt.Errorf("review the plan and confirm")
	}
	names, err := splitNames(r.Value)
	if err != nil || len(names) == 0 {
		return nil, fmt.Errorf("select at least one program")
	}
	update, source := r.ID == "update", r.Extra == "source"
	return startUpdates(update, names, nil, source, r.Secret)
}

// bulkJob starts the update of several programs at once: those in Value as hamstack would update them, those in
// Extra built from source.
func bulkJob(r web.ManagementRequest) (any, error) {
	if !r.Confirm {
		return nil, fmt.Errorf("confirm the update of the selected programs")
	}
	normal, err := splitNames(r.Value)
	if err != nil {
		return nil, err
	}
	src, err := splitNames(r.Extra)
	if err != nil {
		return nil, err
	}
	if len(normal)+len(src) == 0 {
		return nil, fmt.Errorf("select at least one program")
	}
	return startUpdates(true, normal, src, false, r.Secret)
}

// startUpdates plans, checks and starts the job. normal are installed or updated the usual way, srcNames are built
// from source (and sourceAll makes normal ones source builds too).
func startUpdates(update bool, normal, srcNames []string, sourceAll bool, secret string) (any, error) {
	var cmds [][]string
	var sudo []string
	plan := func(names []string, fromSource bool) error {
		src := map[string]bool{}
		for _, n := range names {
			if fromSource {
				if !canBuildFromSource(n) {
					return fmt.Errorf("%s has no source recipe in hamstack", n)
				}
				src[n] = true
			}
			if update && !fromSource {
				p, _ := catalog.ByName(n)
				if in := versions.FindInstalled(p); in != nil && in.Origin == "system package" {
					return fmt.Errorf("%s is managed by your package manager: it updates with the system (Updates, System), or build it from source", n)
				}
			}
		}
		steps, _, err := buildInstallStepsSrc(names, update, src)
		if err != nil {
			return err
		}
		sudo = append(sudo, needsSudo(steps)...)
		args := append([]string{"install"}, names...)
		args = append(args, "--yes")
		if update {
			args = append(args, "--force")
		}
		if fromSource {
			args = append(args, "--from-source")
		}
		cmds = append(cmds, args)
		return nil
	}
	if len(normal) > 0 {
		if sourceAll {
			for _, n := range normal { // each its own command: one failing build should not hide which
				if err := plan([]string{n}, true); err != nil {
					return nil, err
				}
			}
		} else if err := plan(normal, false); err != nil {
			return nil, err
		}
	}
	for _, n := range srcNames {
		if err := plan([]string{n}, true); err != nil {
			return nil, err
		}
	}
	needPassword := len(sudo) > 0 && !sudoPasswordless()
	if needPassword {
		if secret == "" {
			var terminal []string
			for _, c := range cmds {
				terminal = append(terminal, "hamstack "+strings.Join(c, " "))
			}
			return nil, needSudo("Installing or updating these needs administrator rights (sudo) for the system packages and settings they use.", sudo, terminal...)
		}
		if err := verifySudoPassword(secret); err != nil {
			return nil, err
		}
	}
	all := append(slices.Clone(normal), srcNames...)
	verb := "Installing "
	if update {
		verb = "Updating "
	}
	title := verb + strings.Join(all, ", ")
	if len(all) > 4 {
		title = fmt.Sprintf("%s%d programs", verb, len(all))
	}
	spec := jobSpec{Title: title, Kind: "install", Cmds: cmds, Cancelable: true}
	if needPassword {
		spec.Secret = secret
	}
	id, err := startJob(spec)
	if err != nil {
		return nil, err
	}
	return map[string]string{"job": id}, nil
}

func systemUpdateJob(secret string) (any, error) {
	spec := jobSpec{Title: "Updating the system", Kind: "system", Cmds: [][]string{{"system", "update", "--yes"}}}
	if !sudoAllowedNow() {
		steps, err := systemUpdateSteps(install.HostEnv())
		if err != nil {
			return nil, err
		}
		if secret == "" {
			return nil, needSudo("Updating the system's packages needs administrator rights (sudo).", needsSudo(steps),
				"hamstack system update", "or let the dashboard do this without asking: hamstack system allow")
		}
		if err := verifySudoPassword(secret); err != nil {
			return nil, err
		}
		spec.Secret = secret
	}
	id, err := startJob(spec)
	if err != nil {
		return nil, err
	}
	return map[string]string{"job": id}, nil
}

// dashboardRepairJob installs the packages the dashboard needs that are missing (what doctor's fix is). It asks for
// the sudo password the same way an update does.
func dashboardRepairJob(secret string) (any, error) {
	steps, err := remoteInstallSteps(false)
	if err != nil {
		return nil, err
	}
	if len(steps) == 0 {
		return "Everything the dashboard needs is installed.", nil
	}
	spec := jobSpec{Title: "Installing dashboard components", Kind: "install", Cmds: [][]string{{"dashboard", "repair", "--yes"}}, Cancelable: true}
	if sudo := needsSudo(steps); len(sudo) > 0 && !sudoPasswordless() {
		if secret == "" {
			return nil, needSudo("Installing the dashboard's missing packages needs administrator rights (sudo).", sudo, "hamstack dashboard repair")
		}
		if err := verifySudoPassword(secret); err != nil {
			return nil, err
		}
		spec.Secret = secret
	}
	id, err := startJob(spec)
	if err != nil {
		return nil, err
	}
	return map[string]string{"job": id}, nil
}

func selfUpdateJob() (any, error) {
	id, err := startJob(jobSpec{Title: "Updating hamstack", Kind: "self", Cmds: [][]string{{"updates", "self", "--yes"}}, Restart: true})
	if err != nil {
		return nil, err
	}
	return map[string]string{"job": id}, nil
}

// ServiceLogs reads a service's or app's log by the name the pages use.
func (webBackend) ServiceLogs(name string, lines int) (string, error) {
	unit, err := logUnit(name)
	if err != nil {
		return "", err
	}
	text, err := svc.LogsText(unit, lines)
	if strings.TrimSpace(text) == "" && err == nil {
		text = "(no log lines for " + name + " yet)"
	}
	return text, err
}

func logUnit(name string) (string, error) {
	if app, ok := strings.CutPrefix(name, "vnc@"); ok {
		if _, found := apps.Find(app); !found || !apps.ValidName(app) {
			return "", fmt.Errorf("no app %q", app)
		}
		return vncUnit(app), nil
	}
	if name == "desktop" {
		return desktopUnit(), nil
	}
	ss, err := units.Resolve(loadCfg(), name)
	if err != nil || len(ss) != 1 {
		return "", fmt.Errorf("no service %q", name)
	}
	return ss[0].Unit(), nil
}

// SetAppAutostart makes an app start when the user logs in, or not.
func (webBackend) SetAppAutostart(name string, on bool) error {
	if _, ok := apps.Find(name); !ok || !apps.ValidName(name) {
		return fmt.Errorf("no app %q", name)
	}
	if name == "console" {
		return fmt.Errorf("the browser console starts only when it is opened")
	}
	verb := "disable"
	if on {
		verb = "enable"
	}
	return svc.Run(verb, vncUnit(name))
}
