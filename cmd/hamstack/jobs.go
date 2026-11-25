package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ATCUSA/hamstack/internal/install"
	"github.com/ATCUSA/hamstack/internal/web"
)

// Jobs: the long operations the dashboard starts (an update, a build from source, a system upgrade).
//
// Each runs as a child "hamstack ..." process, the same command a person would type, with everything it prints
// (and everything the programs it starts print) going to one file. That is what lets the page show a real terminal,
// lets a job be stopped as a whole (it has its own process group), and means a page reload does not lose the output.
// One job runs at a time: they change the same packages and the same ~/.local.

type job struct {
	mu   sync.Mutex
	info web.JobInfo
	path string
	pgid int // process group of the running command; 0 between commands
	stop bool
}

var jobTable struct {
	sync.Mutex
	seq  int
	list []*job // newest last
}

const keepJobs = 20

// jobExecutable is the program a job runs: this one. A variable so a test can run something harmless instead.
var jobExecutable = os.Executable

func jobsDir() string { return filepath.Join(stateDir(), "jobs") }

// jobBusy says which job is running, if one is.
func jobBusy() (string, bool) {
	jobTable.Lock()
	defer jobTable.Unlock()
	for _, j := range jobTable.list {
		j.mu.Lock()
		running, title := j.info.State == "running", j.info.Title
		j.mu.Unlock()
		if running {
			return title, true
		}
	}
	return "", false
}

// jobGuard refuses what would cut a running job off: restarting the dashboard (the job is its child), rebooting or
// shutting down, a second update. Killing a package manager part way leaves the system half-upgraded.
func jobGuard(what string) error {
	if title, busy := jobBusy(); busy {
		return fmt.Errorf("%s would interrupt %q, which is still running: wait for it to finish (Updates, Activity)", what, title)
	}
	return nil
}

// jobSpec describes a job to start.
type jobSpec struct {
	Title, Kind, Service string
	Cmds                 [][]string // hamstack arguments, run one after the other; the job stops at the first failure
	Cancelable           bool
	Restart              bool   // when it succeeds, what it did needs a restart to take effect
	Secret               string // the administrator password the person gave for this job, if it needs one
}

func startJob(spec jobSpec) (string, error) {
	if len(spec.Cmds) == 0 {
		return "", fmt.Errorf("nothing to run")
	}
	self, err := jobExecutable()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(jobsDir(), 0o755); err != nil {
		return "", err
	}
	jobTable.Lock()
	for _, j := range jobTable.list {
		j.mu.Lock()
		running, title := j.info.State == "running", j.info.Title
		j.mu.Unlock()
		if running {
			jobTable.Unlock()
			return "", fmt.Errorf("%q is still running: one job at a time", title)
		}
	}
	jobTable.seq++
	id := fmt.Sprintf("j%s-%d", time.Now().Format("0102-150405"), jobTable.seq)
	j := &job{path: filepath.Join(jobsDir(), id+".log"), info: web.JobInfo{
		ID: id, Title: spec.Title, Kind: spec.Kind, State: "running", Started: time.Now(), Cmds: len(spec.Cmds),
		Cancelable: spec.Cancelable, Restart: spec.Restart, Service: spec.Service}}
	jobTable.list = append(jobTable.list, j)
	for len(jobTable.list) > keepJobs {
		_ = os.Remove(jobTable.list[0].path)
		jobTable.list = jobTable.list[1:]
	}
	jobTable.Unlock()
	f, err := os.OpenFile(j.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		j.finish("failed", err.Error())
		return "", err
	}
	var secret []byte
	if spec.Secret != "" {
		if err := install.CheckSudoPassword(spec.Secret); err != nil {
			f.Close()
			j.finish("failed", err.Error())
			return "", err
		}
		secret = []byte(spec.Secret)
	}
	go j.run(self, spec, f, secret)
	return id, nil
}

func (j *job) finish(state, errText string) {
	j.mu.Lock()
	j.info.State, j.info.Error, j.info.Ended, j.info.Cancelable = state, errText, time.Now(), false
	if state != "done" {
		j.info.Restart = false
	}
	j.mu.Unlock()
}

func (j *job) run(self string, spec jobSpec, out *os.File, secret []byte) {
	defer out.Close()
	defer clear(secret)
	null, _ := os.Open(os.DevNull)
	if null != nil {
		defer null.Close()
	}
	for i, argv := range spec.Cmds {
		j.mu.Lock()
		stopped := j.stop
		j.info.Cmd = i + 1
		j.mu.Unlock()
		if stopped {
			j.finish("cancelled", "stopped before it finished")
			return
		}
		fmt.Fprintf(out, "$ hamstack %s\n", strings.Join(argv, " "))
		cmd := exec.Command(self, argv...)
		// No terminal and nobody to type a password: sudo must fail at once with its reason, and a program that
		// asks a question must see the end of its input.
		cmd.Env = append(os.Environ(), "HAMSTACK_SUDO_N=1", "TERM=dumb", "NO_COLOR=1", "DEBIAN_FRONTEND=noninteractive")
		var pwSock *os.File
		if secret != nil {
			// The password goes to the command on a socket of its own as descriptor 3, which it reads and closes
			// before it starts anything (main). A socket, not a pipe: a pipe could be reopened through /proc.
			child, err := passwordSocket(secret)
			if err != nil {
				fmt.Fprintf(out, "\nFailed: %v\n", err)
				j.finish("failed", err.Error())
				return
			}
			pwSock = child
			cmd.ExtraFiles = []*os.File{child}
			cmd.Env = append(cmd.Env, install.SudoPasswordEnv+"=fd") // checked before HAMSTACK_SUDO_N
		}
		cmd.Stdin, cmd.Stdout, cmd.Stderr = null, out, out
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.WaitDelay = 5 * time.Second // a daemon started by a package script may hold the output open for ever
		// The pid is copied under the lock once Start has set it: JobCancel must not read cmd.Process while
		// Start is still writing it. A stop that came in while it was starting is acted on here.
		err := cmd.Start()
		if pwSock != nil {
			pwSock.Close() // the command has its own copy now
		}
		if err == nil {
			j.mu.Lock()
			j.pgid = cmd.Process.Pid
			if j.stop {
				killGroup(j.pgid)
			}
			j.mu.Unlock()
			err = cmd.Wait()
		}
		j.mu.Lock()
		stopped = j.stop
		j.pgid = 0
		j.mu.Unlock()
		switch {
		case stopped:
			fmt.Fprintln(out, "\nStopped.")
			j.finish("cancelled", "stopped before it finished")
			return
		case err != nil:
			fmt.Fprintf(out, "\nFailed: %v\n", err)
			j.finish("failed", err.Error())
			return
		}
	}
	fmt.Fprintln(out, "\nDone.")
	j.finish("done", "")
}

var stepLine = regexp.MustCompile(`^\[(\d+)/(\d+)\] (.*)$`)

// lineList splits a job's output into lines. A carriage return inside a line (progress bars) keeps the last part.
func lineList(b []byte) []string {
	text := strings.ReplaceAll(string(b), "\r\n", "\n")
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	for i, l := range lines {
		if k := strings.LastIndex(l, "\r"); k >= 0 {
			lines[i] = l[k+1:]
		}
	}
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	return lines
}

const maxJobRead = 2 << 20

func readJobOutput(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	if fi, err := f.Stat(); err == nil && fi.Size() > maxJobRead {
		_, _ = f.Seek(fi.Size()-maxJobRead, 0)
	}
	b := make([]byte, 0, 64<<10)
	buf := make([]byte, 64<<10)
	for {
		n, err := f.Read(buf)
		b = append(b, buf[:n]...)
		if err != nil {
			break
		}
	}
	return lineList(b)
}

// progress finds the step the running command says it is on, from the "[3/7] ..." lines install and the system
// update print before each step.
func progress(lines []string) (step, steps int, desc string) {
	for i := len(lines) - 1; i >= 0; i-- {
		if m := stepLine.FindStringSubmatch(lines[i]); m != nil {
			step, _ = strconv.Atoi(m[1])
			steps, _ = strconv.Atoi(m[2])
			return step, steps, m[3]
		}
	}
	return 0, 0, ""
}

func (j *job) snapshot(lines []string) web.JobInfo {
	j.mu.Lock()
	info := j.info
	j.mu.Unlock()
	if info.State == "running" {
		// Within the running command only: an earlier command's last step is not this one's.
		cur := lines
		for i := len(lines) - 1; i >= 0; i-- {
			if strings.HasPrefix(lines[i], "$ hamstack ") {
				cur = lines[i:]
				break
			}
		}
		info.Step, info.Steps, info.StepDesc = progress(cur)
	} else {
		info.Step, info.Steps, info.StepDesc = progress(lines)
	}
	return info
}

func (webBackend) Jobs() []web.JobInfo {
	jobTable.Lock()
	list := append([]*job(nil), jobTable.list...)
	jobTable.Unlock()
	sort.SliceStable(list, func(a, b int) bool { return list[a].info.Started.After(list[b].info.Started) })
	var out []web.JobInfo
	for _, j := range list {
		out = append(out, j.snapshot(readJobOutput(j.path)))
	}
	return out
}

func findJob(id string) *job {
	jobTable.Lock()
	defer jobTable.Unlock()
	for _, j := range jobTable.list {
		if j.info.ID == id {
			return j
		}
	}
	return nil
}

func (webBackend) Job(id string, since int) (web.JobDetail, bool) {
	j := findJob(id)
	if j == nil {
		return web.JobDetail{}, false
	}
	lines := readJobOutput(j.path)
	d := web.JobDetail{JobInfo: j.snapshot(lines), Next: len(lines)}
	if since < len(lines) {
		d.Lines = lines[since:]
	}
	if d.Lines == nil {
		d.Lines = []string{}
	}
	return d, true
}

// JobCancel stops a job: the whole process group gets SIGTERM, and SIGKILL if it has not gone in a few seconds.
func (webBackend) JobCancel(id string) error {
	j := findJob(id)
	if j == nil {
		return fmt.Errorf("no such job")
	}
	j.mu.Lock()
	pgid, cancelable, running := j.pgid, j.info.Cancelable, j.info.State == "running"
	if running && cancelable {
		j.stop = true
	}
	j.mu.Unlock()
	switch {
	case !running:
		return fmt.Errorf("it has already finished")
	case !cancelable:
		return fmt.Errorf("this job cannot be stopped part way: stopping a package manager mid-way leaves the system half-updated")
	case pgid == 0:
		return nil // between commands or still starting: run stops it
	}
	killGroup(pgid)
	return nil
}

// killGroup sends the process group SIGTERM, and SIGKILL if it has not gone in a few seconds.
func killGroup(pgid int) {
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	go func() {
		time.Sleep(5 * time.Second)
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}()
}

// passwordSocket returns one end of a new socket pair with the password written into it and the other end closed,
// so the reader gets the password and then the end of input.
func passwordSocket(secret []byte) (*os.File, error) {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("could not hand the password to the job: %w", err)
	}
	ours, theirs := os.NewFile(uintptr(fds[0]), "sudo-password"), os.NewFile(uintptr(fds[1]), "sudo-password")
	_, err = ours.Write(secret)
	ours.Close()
	if err != nil {
		theirs.Close()
		return nil, fmt.Errorf("could not hand the password to the job: %w", err)
	}
	return theirs, nil
}
