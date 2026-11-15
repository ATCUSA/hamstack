package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

// TestBrowserPreview is an opt-in, localhost-only visual fixture. Its backend
// cannot change real services, credentials, packages or host configuration.
func TestBrowserPreview(t *testing.T) {
	if os.Getenv("HAMSTACK_WEB_PREVIEW") != "1" {
		t.Skip("set HAMSTACK_WEB_PREVIEW=1 for a simulated browser preview")
	}
	be := previewBackend{fakeBackend: &fakeBackend{}}
	server := New(be, Options{})
	server.sessions["preview"] = time.Now().Add(time.Hour)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.AddCookie(&http.Cookie{Name: cookieName, Value: "preview"})
		server.Handler().ServeHTTP(w, r)
	})
	ts := httptest.NewServer(handler)
	defer ts.Close()
	fmt.Println("BROWSER_PREVIEW_URL=" + ts.URL)
	time.Sleep(10 * time.Minute)
}

type previewBackend struct{ *fakeBackend }

func (previewBackend) State() State {
	return State{Site: "UI preview — simulated data", Station: "K0TEST", Grid: "FN31pr", Services: []Service{{Name: "rigctld@hf", What: "Rig control", State: "running", Enabled: true, Control: true}, {Name: "mercury@hf", What: "Mercury HF modem", State: "running", Enabled: true, Control: true}, {Name: "pat", What: "Winlink client", State: "stopped", Control: true}}, Apps: []App{{Name: "wsjtx", Title: "WSJT-X", Installed: true, Radio: true}, {Name: "js8call", Title: "JS8Call", Installed: true}, {Name: "fldigi", Title: "Fldigi"}}, DesktopNote: "Preview only; no real desktop is exposed"}
}
func (previewBackend) ManagementState() any {
	return map[string]any{
		"Rigs":     []map[string]any{{"ID": "hf", "Radio": "Icom IC-7300 MK2", "Hardware": "present", "On": true, "Modem": "mercury", "Bands": "hf"}, {"ID": "packet", "Radio": "Digirig", "Hardware": "not detected", "On": false, "Modem": "direwolf", "NoCAT": true}},
		"Profiles": []map[string]string{{"ID": "ic7300mk2", "Name": "Icom IC-7300 MK2"}, {"ID": "digirig", "Name": "Digirig"}},
		"Sites":    []map[string]any{{"ID": "home", "Current": true, "Grid": "FN31pr", "Rigs": "hf, packet", "Station": "personal"}},
		"Stations": []map[string]any{{"ID": "personal", "Callsign": "K0TEST", "Active": true, "Access": "local", "Addr": "http://localhost:8080"}},
		"Programs": []map[string]any{{"Name": "pat", "Desc": "Winlink client", "Installed": "0.18.0", "Origin": "user install", "Suggestions": "mercury (installed), direwolf (installed), ardopcf"}, {"Name": "vnc", "Desc": "Individual apps on virtual displays", "Installed": "1.15.0", "Origin": "system package"}},
		"Network":  map[string]any{"Rows": []map[string]any{{"Service": "web", "What": "HTTPS dashboard", "Port": 8443, "Who": "this computer only", "Target": "web", "Access": "local", "Native": true}, {"Service": "mercury@hf", "What": "HF modem ARQ", "Port": 8300, "Who": "ANY computer on your network", "Target": "mercury@hf/8300", "Access": "all", "Native": false}}},
		"Settings": []map[string]any{{"Key": "grid", "Label": "Grid square", "Value": "FN31pr", "Kind": "text", "Hint": "Maidenhead locator"}, {"Key": "web", "Label": "HTTPS dashboard", "Value": "on", "Kind": "exec", "Args": []string{"web", "off"}, "Hint": "Authenticated browser access"}},
	}
}
func (previewBackend) Manage(r ManagementRequest) (any, error) {
	return "Simulated preview; no changes were made", nil
}
