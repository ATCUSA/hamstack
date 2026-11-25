package web

import (
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// JobInfo is a long-running operation (an update, a build, a system upgrade) as the pages show it: a title, how far
// it has got, and whether it worked. Its output is read separately, from where the page left off.
type JobInfo struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	Kind       string    `json:"kind"`  // install, self, system, launch
	State      string    `json:"state"` // running, done, failed, cancelled
	Error      string    `json:"error,omitempty"`
	Started    time.Time `json:"started"`
	Ended      time.Time `json:"ended,omitempty"`
	Cmd        int       `json:"cmd"`  // which command of the job is running (1-based)
	Cmds       int       `json:"cmds"` // how many it has
	Step       int       `json:"step"` // the step the current command says it is on, and of how many
	Steps      int       `json:"steps"`
	StepDesc   string    `json:"stepDesc,omitempty"`
	Cancelable bool      `json:"cancelable"`
	Restart    bool      `json:"restart,omitempty"` // what it did needs a restart to take effect (the dashboard was updated)
	Service    string    `json:"service,omitempty"` // the service whose log belongs with this job's terminal
}

// JobDetail is a job with the lines of its output from a given point.
type JobDetail struct {
	JobInfo
	Lines []string `json:"lines"`
	Next  int      `json:"next"` // ask for lines from here next time
}

// JobControl is the part of the backend that runs jobs. A backend without it has no jobs, and the pages say so.
type JobControl interface {
	Jobs() []JobInfo
	Job(id string, since int) (JobDetail, bool)
	JobCancel(id string) error
}

// LogSource reads the log of a service or app by the name the pages use ("pat", "rigctld@ftdx10", "vnc@wsjtx").
type LogSource interface {
	ServiceLogs(name string, lines int) (string, error)
}

// AppAutostart turns an app's start at login on or off.
type AppAutostart interface {
	SetAppAutostart(name string, on bool) error
}

func (s *Server) jobList(w http.ResponseWriter, r *http.Request) {
	jc, ok := s.be.(JobControl)
	if !ok {
		writeJSON(w, http.StatusOK, []JobInfo{})
		return
	}
	jobs := jc.Jobs()
	if jobs == nil {
		jobs = []JobInfo{}
	}
	writeJSON(w, http.StatusOK, jobs)
}

func (s *Server) jobGet(w http.ResponseWriter, r *http.Request) {
	jc, ok := s.be.(JobControl)
	if !ok {
		http.NotFound(w, r)
		return
	}
	since, _ := strconv.Atoi(r.URL.Query().Get("since"))
	d, found := jc.Job(r.PathValue("id"), max(since, 0))
	if !found {
		replyErr(w, http.StatusNotFound, fmt.Errorf("no such job"))
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) jobCancel(w http.ResponseWriter, r *http.Request) {
	jc, ok := s.be.(JobControl)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if err := jc.JobCancel(r.PathValue("id")); err != nil {
		replyErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) logsGet(w http.ResponseWriter, r *http.Request) {
	ls, ok := s.be.(LogSource)
	if !ok {
		http.NotFound(w, r)
		return
	}
	n, _ := strconv.Atoi(r.URL.Query().Get("lines"))
	if n <= 0 || n > 1000 {
		n = 300
	}
	text, err := ls.ServiceLogs(r.PathValue("name"), n)
	if err != nil {
		replyErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"text": text})
}

func (s *Server) appVerb(w http.ResponseWriter, r *http.Request) {
	name, verb := r.PathValue("name"), r.PathValue("verb")
	switch verb {
	case "enable", "disable":
		as, ok := s.be.(AppAutostart)
		if !ok {
			http.NotFound(w, r)
			return
		}
		reply(w, as.SetAppAutostart(name, verb == "enable"))
	case "start":
		reply(w, s.be.OpenApp(name))
	case "stop":
		reply(w, s.be.CloseApp(name))
	default:
		reply(w, fmt.Errorf("unknown action %q", verb))
	}
}
