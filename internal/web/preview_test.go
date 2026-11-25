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
	server.sessions[hashToken("preview")] = time.Now().Add(time.Hour)
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
	return State{Site: "UI preview — simulated data", Station: "K0TEST", Grid: "EM10dg", Services: []Service{{Name: "rigctld@hf", What: "Rig control", State: "running", Enabled: true, Control: true}, {Name: "mercury@hf", What: "Mercury HF modem", State: "running", Enabled: true, Control: true}, {Name: "pat", What: "Winlink client", State: "stopped", Control: true}}, Apps: []App{{Name: "pat", Title: "Pat (Winlink)", Package: "pat", Installed: true, Running: true, Kind: "web", LinkPort: 8081, Scheme: "http"}, {Name: "wsjtx", Title: "WSJT-X", Package: "wsjtx-improved", Installed: true, Radio: true, Kind: "browser"}, {Name: "js8call", Title: "JS8Call", Package: "js8call", Installed: true, Kind: "browser"}, {Name: "fldigi", Title: "Fldigi", Package: "fldigi", Kind: "browser"}, {Name: "xastir", Title: "Xastir", Package: "xastir", Kind: "browser"}}, DesktopNote: "Preview only; no real desktop is exposed"}
}
func (previewBackend) ManagementState() any {
	return map[string]any{
		"Rigs":     []map[string]any{{"ID": "hf", "Radio": "Icom IC-7300 MK2", "Hardware": "present", "On": true, "Modem": "mercury", "Bands": "hf"}, {"ID": "packet", "Radio": "Digirig", "Hardware": "not detected", "On": false, "Modem": "direwolf", "NoCAT": true}},
		"Profiles": []map[string]string{{"ID": "ic7300mk2", "Name": "Icom IC-7300 MK2"}, {"ID": "digirig", "Name": "Digirig"}},
		"Sites":    []map[string]any{{"ID": "home", "Current": true, "Grid": "EM10dg", "Rigs": "hf, packet", "Station": "personal"}},
		"Stations": []map[string]any{{"ID": "personal", "Callsign": "K0TEST", "Active": true, "Access": "local", "Addr": "http://localhost:8080"}},
		"Programs": []map[string]any{
			{"Name": "pat", "Desc": "Winlink client", "Installed": "0.18.0", "Origin": "user install", "CanSource": true, "Suggestions": "mercury (installed), direwolf (installed), ardopcf"},
			{"Name": "mercury", "Desc": "HF modem", "Installed": "1.9.23", "Origin": "user install", "CanSource": true, "Rollback": "remove"},
			{"Name": "vnc", "Desc": "Individual apps on virtual displays", "Installed": "1.15.0", "Origin": "system package", "CanSource": true, "Package": "tigervnc-standalone-server", "Candidate": "1.15.0"},
			{"Name": "novnc", "Desc": "Browser VNC viewer", "Installed": "1.6.0", "Origin": "system package", "CanSource": false, "Package": "novnc", "Candidate": "1.6.0", "Held": true},
			{"Name": "ardopcf", "Desc": "ARDOP HF modem", "Category": "modem"},
			{"Name": "direwolf", "Desc": "soundcard packet TNC (VHF/UHF)", "Category": "modem", "Needs": "hamlib"},
			{"Name": "fldigi", "Desc": "digital modes (PSK, RTTY, Olivia...)", "Category": "digital"},
			{"Name": "wsjtx", "Desc": "WSJT-X: FT8, FT4, WSPR", "Category": "digital"},
			{"Name": "xastir", "Desc": "APRS client with maps", "Category": "aprs"},
			{"Name": "hamstack", "Desc": "Station manager", "Installed": "dev-7dc3b6", "Origin": "hamstack release", "Rollback": "self", "Previous": "dev-bf82dd9"},
		},
		"Categories": []map[string]string{{"ID": "winlink", "Title": "Winlink"}, {"ID": "modem", "Title": "Modems and rig control"}, {"ID": "digital", "Title": "Digital modes"}, {"ID": "aprs", "Title": "APRS and position"}},
		"Updates": map[string]any{"programs": []map[string]any{
			{"name": "pat", "installed": "0.18.0", "latest": "0.19.1", "status": "update available"},
			{"name": "mercury", "installed": "1.9.23", "latest": "1.9.24", "status": "update available"},
			{"name": "vnc", "installed": "1.15.0", "latest": "1.16.2", "status": "update available"},
			{"name": "novnc", "installed": "1.6.0", "latest": "1.6.1", "status": "update available"},
			{"name": "hamstack", "installed": "dev-7dc3b6", "latest": "0.2.0", "status": "development build; release available"},
		}},
		"Network":  map[string]any{"Rows": []map[string]any{{"Service": "web", "What": "HTTPS dashboard", "Port": 8443, "Who": "this computer only", "Target": "web", "Access": "local", "Native": true}, {"Service": "mercury@hf", "What": "HF modem ARQ", "Port": 8300, "Who": "ANY computer on your network", "Target": "mercury@hf/8300", "Access": "all", "Native": false}}},
		"Settings": []map[string]any{{"Key": "grid", "Label": "Grid square", "Value": "EM10dg", "Kind": "text", "Hint": "Maidenhead locator"}, {"Key": "web", "Label": "HTTPS dashboard", "Value": "on", "Kind": "exec", "Args": []string{"web", "off"}, "Hint": "Authenticated browser access"}},
	}
}
func (previewBackend) Manage(r ManagementRequest) (any, error) {
	if r.Action == "program.install" { // so the password dialog can be tried: the password is "preview"
		if r.Secret == "" {
			return nil, &SudoNeeded{User: "ham", Why: "Installing these needs administrator rights (sudo) for the system packages they use.", Commands: []string{"sudo pacman -S --noconfirm ardopcf"}, Terminal: "hamstack install ardopcf"}
		}
		if r.Secret != "preview" {
			return nil, &SudoRejected{Message: "That password was not accepted."}
		}
	}
	return "Simulated preview; no changes were made", nil
}
