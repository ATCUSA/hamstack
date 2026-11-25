package main

import (
	"strings"
	"testing"
	"time"

	"github.com/ATCUSA/hamstack/internal/web"
)

// The job tests run /bin/sh where a real job would run hamstack, so what is checked is the runner: output,
// progress, stopping, one at a time, and what the child is given.
func jobTest(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	old := jobExecutable
	jobExecutable = func() (string, error) { return "/bin/sh", nil }
	jobTable.Lock()
	jobTable.list, jobTable.seq = nil, 0
	jobTable.Unlock()
	t.Cleanup(func() { jobExecutable = old })
}

func waitJob(t *testing.T, id string, d time.Duration) web.JobDetail {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		det, ok := webBackend{}.Job(id, 0)
		if !ok {
			t.Fatalf("job %s vanished", id)
		}
		if det.State != "running" {
			return det
		}
		if time.Now().After(deadline) {
			t.Fatalf("job still running after %s: %+v", d, det.JobInfo)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestJobCapturesEverythingAndReportsProgress(t *testing.T) {
	jobTest(t)
	id, err := startJob(jobSpec{Title: "Updating pat", Kind: "install", Cmds: [][]string{
		{"-c", "echo '[1/3] Download'; echo to-stderr >&2; echo '[2/3] Build'; echo '[3/3] Install'; echo installed: x"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	det := waitJob(t, id, 5*time.Second)
	if det.State != "done" || det.Error != "" {
		t.Fatalf("%+v", det.JobInfo)
	}
	text := strings.Join(det.Lines, "\n")
	for _, want := range []string{"$ hamstack -c ", "[1/3] Download", "to-stderr", "[3/3] Install", "installed: x", "Done."} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q:\n%s", want, text)
		}
	}
	if det.Step != 3 || det.Steps != 3 || det.StepDesc != "Install" {
		t.Errorf("progress: %d/%d %q", det.Step, det.Steps, det.StepDesc)
	}
	// Asking from where the page left off returns only what is new.
	more, _ := webBackend{}.Job(id, det.Next-2)
	if len(more.Lines) != 2 || more.Next != det.Next {
		t.Errorf("since: %v next %d (was %d)", more.Lines, more.Next, det.Next)
	}
	past, _ := webBackend{}.Job(id, det.Next+10)
	if len(past.Lines) != 0 {
		t.Errorf("past the end: %v", past.Lines)
	}
}

func TestJobStopsAtTheFirstFailure(t *testing.T) {
	jobTest(t)
	id, _ := startJob(jobSpec{Title: "Updating two", Kind: "install", Cmds: [][]string{{"-c", "echo first; exit 3"}, {"-c", "echo second-must-not-run"}}})
	det := waitJob(t, id, 5*time.Second)
	if det.State != "failed" || det.Error == "" {
		t.Fatalf("%+v", det.JobInfo)
	}
	text := strings.Join(det.Lines, "\n")
	if strings.Contains(text, "second-must-not-run") || !strings.Contains(text, "Failed:") {
		t.Errorf("a failed command ends the job:\n%s", text)
	}
	if det.Cmd != 1 || det.Cmds != 2 {
		t.Errorf("which command: %d of %d", det.Cmd, det.Cmds)
	}
}

func TestOnlyOneJobRunsAtATimeAndItProtectsWhatWouldCutItOff(t *testing.T) {
	jobTest(t)
	id, err := startJob(jobSpec{Title: "Updating the system", Kind: "system", Cmds: [][]string{{"-c", "sleep 30"}}})
	if err != nil {
		t.Fatal(err)
	}
	second := jobSpec{Title: "second", Kind: "install", Cmds: [][]string{{"-c", "true"}}}
	if _, err := startJob(second); err == nil || !strings.Contains(err.Error(), "one job at a time") {
		t.Errorf("a second job: %v", err)
	}
	err = jobGuard("restarting the dashboard")
	if err == nil || !strings.Contains(err.Error(), "Updating the system") {
		t.Errorf("the guard must name the running job: %v", err)
	}
	// A system update is not something to stop part way.
	if err := (webBackend{}).JobCancel(id); err == nil || !strings.Contains(err.Error(), "cannot be stopped") {
		t.Errorf("cancel of a job that cannot be stopped: %v", err)
	}
	// Tidy: this one is a test's, so end it.
	j := findJob(id)
	j.mu.Lock()
	j.info.Cancelable = true
	j.mu.Unlock()
	if err := (webBackend{}).JobCancel(id); err != nil {
		t.Fatal(err)
	}
	waitJob(t, id, 8*time.Second)
	if err := jobGuard("restarting the dashboard"); err != nil {
		t.Errorf("nothing is running now: %v", err)
	}
}

func TestStoppingAJobKillsTheWholeProcessGroup(t *testing.T) {
	jobTest(t)
	// The shell starts a child of its own; stopping must take that too, not just the shell.
	id, _ := startJob(jobSpec{Title: "Building x", Kind: "install", Cancelable: true, Cmds: [][]string{{"-c", "sleep 60 & echo child=$!; wait"}, {"-c", "echo not-reached"}}})
	var child string
	for i := 0; i < 100 && child == ""; i++ {
		det, _ := webBackend{}.Job(id, 0)
		for _, l := range det.Lines {
			if rest, ok := strings.CutPrefix(l, "child="); ok {
				child = rest
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if child == "" {
		t.Fatal("the job never reported its child")
	}
	start := time.Now()
	if err := (webBackend{}).JobCancel(id); err != nil {
		t.Fatal(err)
	}
	det := waitJob(t, id, 10*time.Second)
	if det.State != "cancelled" {
		t.Fatalf("%+v", det.JobInfo)
	}
	if strings.Contains(strings.Join(det.Lines, "\n"), "not-reached") {
		t.Error("the next command must not start after a stop")
	}
	if time.Since(start) > 8*time.Second {
		t.Errorf("stopping took %s", time.Since(start))
	}
	// the child is gone too (kill -0 fails for a process that no longer exists)
	for i := 0; i < 40; i++ {
		if !pidAlive(child) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Errorf("process %s from the job is still running", child)
}

func TestAJobGetsNoTerminalAndNoPasswordPrompt(t *testing.T) {
	jobTest(t)
	id, _ := startJob(jobSpec{Title: "env", Kind: "install", Cmds: [][]string{{"-c", "echo sudo_n=$HAMSTACK_SUDO_N; read x; echo read:[$x]; echo term=$TERM"}}})
	det := waitJob(t, id, 5*time.Second) // a read from a terminal would hang here
	text := strings.Join(det.Lines, "\n")
	for _, want := range []string{"sudo_n=1", "read:[]", "term=dumb"} {
		if !strings.Contains(text, want) {
			t.Errorf("lacks %q:\n%s", want, text)
		}
	}
}

func TestLineListKeepsTheLastPartOfAProgressLine(t *testing.T) {
	got := lineList([]byte("a\r\nprogress 10%\rprogress 60%\rprogress 100%\nlast\n"))
	if strings.Join(got, "|") != "a|progress 100%|last" {
		t.Errorf("%q", got)
	}
	if lineList(nil) != nil || lineList([]byte("")) != nil {
		t.Error("no output is no lines")
	}
}

func TestOldJobsAndTheirFilesAreForgotten(t *testing.T) {
	jobTest(t)
	for i := 0; i < keepJobs+3; i++ {
		id, err := startJob(jobSpec{Title: "n", Kind: "install", Cmds: [][]string{{"-c", "true"}}})
		if err != nil {
			t.Fatal(err)
		}
		waitJob(t, id, 5*time.Second)
	}
	if n := len(webBackend{}.Jobs()); n != keepJobs {
		t.Errorf("%d jobs kept, want %d", n, keepJobs)
	}
}

func TestAJobThatChangesProgramsRefreshesTheUpdateList(t *testing.T) {
	jobTest(t)
	calls := make(chan string, 4)
	old := afterProgramsChanged
	afterProgramsChanged = func() { calls <- "recheck" }
	defer func() { afterProgramsChanged = old }()
	for _, kind := range []string{"install", "browser"} {
		id, err := startJob(jobSpec{Title: kind, Kind: kind, Cmds: [][]string{{"-c", "true"}}})
		if err != nil {
			t.Fatal(err)
		}
		waitJob(t, id, 5*time.Second)
	}
	select {
	case <-calls:
	case <-time.After(2 * time.Second):
		t.Fatal("an install job ended without the update list being looked at again")
	}
	select {
	case <-calls:
		t.Fatal("a job that changes no program refreshed the update list")
	case <-time.After(300 * time.Millisecond):
	}
}
