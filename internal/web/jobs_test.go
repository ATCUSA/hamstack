package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// What the pages are shown of jobs, logs and apps comes from these; the fake keeps what it was asked.
var jobFake struct {
	cancelled []string
	autostart []string
	opened    []string
}

func (f *fakeBackend) Jobs() []JobInfo {
	return []JobInfo{{ID: "j2", Title: "Updating pat", Kind: "install", State: "running", Started: time.Now(), Cmd: 1, Cmds: 2, Step: 3, Steps: 7, StepDesc: "Build", Cancelable: true},
		{ID: "j1", Title: "Updating the system", Kind: "system", State: "done", Started: time.Now().Add(-time.Hour), Ended: time.Now().Add(-50 * time.Minute)}}
}
func (f *fakeBackend) Job(id string, since int) (JobDetail, bool) {
	if id != "j2" {
		return JobDetail{}, false
	}
	lines := []string{"$ hamstack install pat --yes", "[1/7] Download", "[2/7] Verify", "[3/7] Build"}
	d := JobDetail{JobInfo: f.Jobs()[0], Lines: []string{}, Next: len(lines)}
	if since < len(lines) {
		d.Lines = lines[since:]
	}
	return d, true
}
func (f *fakeBackend) JobCancel(id string) error {
	if id != "j2" {
		return fmt.Errorf("no such job")
	}
	jobFake.cancelled = append(jobFake.cancelled, id)
	return nil
}
func (f *fakeBackend) ServiceLogs(name string, lines int) (string, error) {
	if name == "nope" {
		return "", fmt.Errorf("no service %q", name)
	}
	return fmt.Sprintf("Nov 15 13:19 radio hamstack[1]: %s ready (%d lines asked)\n", name, lines), nil
}
func (f *fakeBackend) SetAppAutostart(name string, on bool) error {
	jobFake.autostart = append(jobFake.autostart, fmt.Sprintf("%s=%v", name, on))
	return nil
}

func TestJobAndLogEndpoints(t *testing.T) {
	jobFake.cancelled, jobFake.autostart = nil, nil
	ts, c := setup(t, &fakeBackend{})
	for _, p := range []string{"/api/jobs", "/api/jobs/j2", "/api/logs/pat"} {
		if code, _ := rigRequest(t, ts, c, "GET", ts.URL+p, "", "", false); code != http.StatusUnauthorized {
			t.Errorf("%s without a login: %d", p, code)
		}
	}
	login(t, ts, c, "secret-pass")

	code, body := rigRequest(t, ts, c, "GET", ts.URL+"/api/jobs", "", "", false)
	var jobs []JobInfo
	if code != 200 || json.Unmarshal([]byte(body), &jobs) != nil || len(jobs) != 2 || jobs[0].Step != 3 || !jobs[0].Cancelable {
		t.Fatalf("jobs: %d %s", code, body)
	}
	code, body = rigRequest(t, ts, c, "GET", ts.URL+"/api/jobs/j2?since=2", "", "", false)
	var d JobDetail
	if code != 200 || json.Unmarshal([]byte(body), &d) != nil || len(d.Lines) != 2 || d.Next != 4 || d.Lines[1] != "[3/7] Build" {
		t.Fatalf("job from 2: %d %s", code, body)
	}
	if code, _ := rigRequest(t, ts, c, "GET", ts.URL+"/api/jobs/zzz", "", "", false); code != http.StatusNotFound {
		t.Errorf("an unknown job: %d", code)
	}
	if code, body := rigRequest(t, ts, c, "GET", ts.URL+"/api/jobs/j2?since=-5", "", "", false); code != 200 || !strings.Contains(body, "Download") {
		t.Errorf("a negative offset counts as the start: %d %s", code, body)
	}

	// Stopping a job needs the dashboard's own header and the same origin, like every change.
	for _, tc := range []struct {
		origin string
		hdr    bool
	}{{ts.URL, false}, {"https://evil.example", true}} {
		if code, _ := rigRequest(t, ts, c, "POST", ts.URL+"/api/jobs/j2/cancel", tc.origin, "", tc.hdr); code != http.StatusForbidden {
			t.Errorf("cancel with origin %q header %v: %d", tc.origin, tc.hdr, code)
		}
	}
	if len(jobFake.cancelled) != 0 {
		t.Fatalf("a refused request reached the backend: %v", jobFake.cancelled)
	}
	if code, _ := rigRequest(t, ts, c, "POST", ts.URL+"/api/jobs/j2/cancel", ts.URL, "", true); code != 200 || len(jobFake.cancelled) != 1 {
		t.Errorf("cancel: %d %v", code, jobFake.cancelled)
	}
	if code, body := rigRequest(t, ts, c, "POST", ts.URL+"/api/jobs/zzz/cancel", ts.URL, "", true); code != http.StatusBadRequest || !strings.Contains(body, "no such job") {
		t.Errorf("cancel of an unknown job: %d %s", code, body)
	}

	code, body = rigRequest(t, ts, c, "GET", ts.URL+"/api/logs/rigctld@ftdx10?lines=50", "", "", false)
	if code != 200 || !strings.Contains(body, "rigctld@ftdx10 ready (50 lines asked)") {
		t.Errorf("logs: %d %s", code, body)
	}
	if code, body := rigRequest(t, ts, c, "GET", ts.URL+"/api/logs/pat?lines=99999", "", "", false); code != 200 || !strings.Contains(body, "300 lines asked") {
		t.Errorf("an absurd line count is capped to the default: %d %s", code, body)
	}
	if code, body := rigRequest(t, ts, c, "GET", ts.URL+"/api/logs/nope", "", "", false); code != http.StatusBadRequest || !strings.Contains(body, `no service`) {
		t.Errorf("an unknown service: %d %s", code, body)
	}
}

func TestAppVerbs(t *testing.T) {
	jobFake.autostart = nil
	ts, c := setup(t, &fakeBackend{})
	login(t, ts, c, "secret-pass")
	for _, tc := range []struct {
		path string
		want int
	}{{"/api/app/fldigi/enable", 200}, {"/api/app/fldigi/disable", 200}, {"/api/app/fldigi/start", 200}, {"/api/app/fldigi/stop", 200}, {"/api/app/fldigi/close", 200}, {"/api/app/fldigi/frobnicate", 400}} {
		if code, _ := rigRequest(t, ts, c, "POST", ts.URL+tc.path, ts.URL, "", true); code != tc.want {
			t.Errorf("%s: %d, want %d", tc.path, code, tc.want)
		}
	}
	if strings.Join(jobFake.autostart, ",") != "fldigi=true,fldigi=false" {
		t.Errorf("autostart calls: %v", jobFake.autostart)
	}
	if code, _ := rigRequest(t, ts, c, "POST", ts.URL+"/api/app/fldigi/enable", ts.URL, "", false); code != http.StatusForbidden {
		t.Errorf("without the header: %d", code)
	}
}
