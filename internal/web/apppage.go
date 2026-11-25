package web

import (
	"fmt"
	"net/http"
)

// A page for one app (/apps/NAME): what it is doing, its few settings that hamstack manages by name, and the
// recommended setup, which is shown as a preview before it is applied and can be undone. The settings are named
// fields only: a program's own config can hold passwords, and none of it is ever sent to the page whole.

// AppField is one setting the page can show and change.
type AppField struct {
	Key     string      `json:"key"`
	Label   string      `json:"label"`
	Kind    string      `json:"kind"` // text, select, toggle
	Value   string      `json:"value"`
	Hint    string      `json:"hint,omitempty"`
	Options [][2]string `json:"options,omitempty"` // for select: value, label
}

// AppChange is one setting the recommended setup would change (or did).
type AppChange struct {
	Path   string `json:"path"`
	Old    string `json:"old"`
	New    string `json:"new"`
	WasSet bool   `json:"wasSet"`
}

// AppSetup is an app's recommended setup: what it would change now, and whether a previous one can be undone.
type AppSetup struct {
	Title   string      `json:"title"`
	Summary string      `json:"summary"`
	Changes []AppChange `json:"changes"`
	CanUndo bool        `json:"canUndo"`
	CanDo   bool        `json:"canDo"`             // false when it cannot be applied now: Summary says why
	Restart string      `json:"restart,omitempty"` // the service to restart for it to take effect
}

// AppPageInfo is everything the page shows.
type AppPageInfo struct {
	Name      string     `json:"name"`
	Title     string     `json:"title"`
	Kind      string     `json:"kind"` // browser (a program shown through the browser), web (has its own web page)
	Installed bool       `json:"installed"`
	Package   string     `json:"package"`
	Version   string     `json:"version,omitempty"`
	Service   string     `json:"service,omitempty"` // for /api/service/NAME/VERB (services only)
	State     string     `json:"state"`
	Enabled   bool       `json:"enabled"`
	Radio     bool       `json:"radio"`
	LinkPort  int        `json:"linkPort,omitempty"`
	Scheme    string     `json:"scheme,omitempty"`
	LinkNote  string     `json:"linkNote,omitempty"`
	Log       string     `json:"log,omitempty"` // for /api/logs/NAME
	Fields    []AppField `json:"fields,omitempty"`
	Setup     *AppSetup  `json:"setup,omitempty"`
	Notes     []string   `json:"notes,omitempty"`
}

// AppPageSource builds an app's page.
type AppPageSource interface {
	AppPage(name string) (AppPageInfo, error)
}

func (s *Server) appPageView(w http.ResponseWriter, r *http.Request) {
	d := pageData{Title: "App", NoVNC: s.noVNCDir() != "", Name: r.PathValue("name")}
	for _, q := range pagesList {
		d.Nav = append(d.Nav, navItem{Href: q.Path, Label: q.Label, Current: q.Path == "/apps"})
	}
	render(w, "app.html", d)
}

func (s *Server) appPageData(w http.ResponseWriter, r *http.Request) {
	src, ok := s.be.(AppPageSource)
	if !ok {
		http.NotFound(w, r)
		return
	}
	info, err := src.AppPage(r.PathValue("name"))
	if err != nil {
		replyErr(w, http.StatusNotFound, fmt.Errorf("%w", err))
		return
	}
	writeJSON(w, http.StatusOK, info)
}
