package web

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// Every page carries the same navigation, marks its own entry, and brings what it needs.
func TestPagesRenderWithNavigation(t *testing.T) {
	ts, c := setup(t, &fakeBackend{})
	login(t, ts, c, "secret-pass")
	want := map[string][]string{
		"/":         {`id="rig-section"`, `id="glance"`},
		"/apps":     {`id="apps"`, `id="remote-checks"`},
		"/services": {`id="services"`},
		"/updates":  {`id="manage-nav"`, `"Updates,Programs,System,Maintenance"`},
		"/settings": {`id="manage-nav"`, `"Rigs,Sites,Stations,Apps,Devices,Settings,Network,Remote access,GPS,Doctor"`},
	}
	for path, needles := range want {
		r, err := c.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(r.Body)
		r.Body.Close()
		page := string(b)
		if r.StatusCode != http.StatusOK {
			t.Errorf("%s: %d %s", path, r.StatusCode, page)
			continue
		}
		for _, n := range needles {
			if !strings.Contains(page, n) {
				t.Errorf("%s lacks %s", path, n)
			}
		}
		for _, p := range pagesList {
			if !strings.Contains(page, `href="`+p.Path+`"`) {
				t.Errorf("%s: no link to %s", path, p.Path)
			}
		}
		if strings.Count(page, `aria-current="page"`) != 1 || !strings.Contains(page, `<a href="`+path+`" class="on" aria-current="page">`) {
			t.Errorf("%s must mark exactly its own navigation entry", path)
		}
	}
	if r, _ := c.Get(ts.URL + "/nope"); r.StatusCode != http.StatusNotFound && r.StatusCode != http.StatusSeeOther {
		t.Errorf("an unknown page: %d", r.StatusCode)
	}
}
