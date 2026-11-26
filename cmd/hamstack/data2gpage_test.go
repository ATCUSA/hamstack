package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/doctor"
)

func listenOf(argv []string) string {
	if i := slices.Index(argv, "--web-listen"); i >= 0 && i+1 < len(argv) {
		return argv[i+1]
	}
	return ""
}

// With the dashboard running, Data2G's page is served through it, so Data2G listens on this computer only even when its
// network setting says "anyone"; without the dashboard it keeps the setting it always had.
func TestData2GPageListensLocallyWhenTheDashboardServesIt(t *testing.T) {
	rig := config.Rig{ID: "hf", RigPort: 4533, ArqPort: 8310, Modem: config.Data2G}
	cfg := config.Config{UI: true, Rigs: []config.Rig{rig}, Web: &config.WebSettings{}}
	if got := listenOf(data2gArgs(cfg, rig, "K0TEST", "s.conf", -1, -1, true)); got != "127.0.0.1" {
		t.Errorf("relayed: listens on %q", got)
	}
	if p := config.Data2GPagePort(rig); p != 9401 {
		t.Errorf("page port %d, want 9401 (8401 + 1000)", p)
	}
	cfg.Web = nil
	if got := listenOf(data2gArgs(cfg, rig, "K0TEST", "s.conf", -1, -1, true)); got == "127.0.0.1" || got == "" {
		t.Errorf("without the dashboard the network setting stays: %q", got)
	}
}

// A password Data2G would mangle (a line break ends it on its standard input) is refused before Data2G runs.
func TestData2GPasswordIsCheckedFirst(t *testing.T) {
	old := data2gPasswordRun
	defer func() { data2gPasswordRun = old }()
	var gotPW string
	data2gPasswordRun = func(_, _, pw string) error { gotPW = pw; return nil }
	for _, bad := range []string{"short", "has a\nline break", "has\rreturn!"} {
		if _, err := data2gSetPassword("hf", bad); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
	if gotPW != "" {
		t.Fatal("a refused password must not reach Data2G")
	}
}

func TestDoctorOnData2GsPage(t *testing.T) {
	cfg := config.Config{UI: true, Web: &config.WebSettings{}, Rigs: []config.Rig{{ID: "hf", Profile: "dummy", Modem: config.Data2G, RigPort: 4533, ArqPort: 8300}}}
	run := func(pw bool) doctor.Result {
		in := doctor.Inputs{Cfg: cfg, Sys: doctor.System{
			LookPath: func(string) (string, error) { return "/bin/x", nil }, Glob: func(string) ([]string, error) { return nil, nil },
			Cards: func() []string { return nil }, Access: func(string) error { return nil }, Group: func(string) string { return "" },
			Listening: func(int) bool { return false }, ServiceActive: func(string) bool { return true }, FileExists: func(string) bool { return true },
			UnitsState: func() (bool, bool) { return true, true }, Data2GPassword: func() bool { return pw }}}
		for _, r := range doctor.Run(in) {
			if r.Name == "Data2G web UI" {
				return r
			}
		}
		return doctor.Result{}
	}
	if r := run(true); r.Level != doctor.OK || !strings.Contains(r.Detail, "through the dashboard") {
		t.Errorf("relayed, with a password: %+v", r)
	}
	if r := run(false); r.Level != doctor.Warn || !strings.Contains(r.Fix, "Set page password") {
		t.Errorf("relayed, locked: %+v", r)
	}
	cfg.Web = nil
	if r := run(true); r.Level != doctor.Warn || !strings.Contains(r.Fix, "hamstack dashboard on") {
		t.Errorf("without the dashboard: %+v", r)
	}
}
