package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func (f *fakeBackend) AppPage(name string) (AppPageInfo, error) {
	switch name {
	case "pat":
		return AppPageInfo{Name: "pat", Title: "Pat (Winlink)", Kind: "web", Installed: true, Package: "pat", Version: "1.0.1", Service: "pat", State: "running", Log: "pat",
			LinkPort: 8081, Scheme: "http",
			Fields: []AppField{{Key: "ardop.rig", Label: "ARDOP: radio", Kind: "select", Value: "", Options: [][2]string{{"", "none"}, {"ftdx10", "FT-DX10"}}},
				{Key: "ardop.ptt_ctrl", Label: "ARDOP: Pat keys the radio", Kind: "toggle", Value: "false"}},
			Setup: &AppSetup{Title: "Recommended setup", Summary: "2 settings would change", CanDo: true, CanUndo: true, Restart: "pat",
				Changes: []AppChange{{Path: "ardop.rig", Old: `""`, New: `"ftdx10"`, WasSet: true}, {Path: "hamlib_rigs.ftdx10.address", New: `"localhost:4533"`}}}}, nil
	case "fldigi":
		return AppPageInfo{Name: "fldigi", Title: "Fldigi", Kind: "browser", Package: "fldigi", State: "stopped"}, nil
	}
	return AppPageInfo{}, fmt.Errorf("no app %q", name)
}

func TestAppPageEndpoints(t *testing.T) {
	ts, c := setup(t, &fakeBackend{})
	if code, _ := rigRequest(t, ts, c, "GET", ts.URL+"/api/apps/pat", "", "", false); code != http.StatusUnauthorized {
		t.Errorf("the data without a login: %d", code)
	}
	if r, _ := c.Get(ts.URL + "/apps/pat"); r.StatusCode != http.StatusSeeOther {
		t.Errorf("the page without a login: %d", r.StatusCode)
	}
	login(t, ts, c, "secret-pass")

	code, body := rigRequest(t, ts, c, "GET", ts.URL+"/api/apps/pat", "", "", false)
	var info AppPageInfo
	if code != 200 || json.Unmarshal([]byte(body), &info) != nil || info.Setup == nil || len(info.Setup.Changes) != 2 || len(info.Fields) != 2 {
		t.Fatalf("pat: %d %s", code, body)
	}
	if code, body := rigRequest(t, ts, c, "GET", ts.URL+"/api/apps/nope", "", "", false); code != http.StatusNotFound || !strings.Contains(body, "no app") {
		t.Errorf("unknown app: %d %s", code, body)
	}

	r, err := c.Get(ts.URL + "/apps/pat")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(r.Body)
	r.Body.Close()
	page := string(b)
	if r.StatusCode != 200 || !strings.Contains(page, `const NAME = "pat";`) || !strings.Contains(page, `href="/apps" class="on" aria-current="page"`) {
		t.Errorf("the app page: %d (name in script, Apps marked in the navigation)", r.StatusCode)
	}
}

// The name comes from the address, and it ends up inside a script: it must arrive as a string, never as code.
func TestAppPageNameCannotBreakOutOfTheScript(t *testing.T) {
	ts, c := setup(t, &fakeBackend{})
	login(t, ts, c, "secret-pass")
	for _, name := range []string{`%22%3B%3C/script%3E%3Cscript%3Ealert(1)%3C/script%3E`, `%27%3Balert(1)//`, `x%3C!--`} {
		r, err := c.Get(ts.URL + "/apps/" + name)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(r.Body)
		r.Body.Close()
		page := string(b)
		if strings.Contains(page, "<script>alert(1)") || strings.Contains(page, `";</script>`) || strings.Contains(page, "';alert(1)") {
			t.Errorf("%s: the name reached the page as code:\n%s", name, page[strings.Index(page, "const NAME"):][:200])
		}
	}
}
