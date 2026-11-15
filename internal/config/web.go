package config

import "fmt"

// The kinds of service that make up the web dashboard.
const (
	Web     = "web"     // the dashboard: one HTTPS server
	VNC     = "vnc"     // one desktop app on a virtual display, on demand: vnc@APP
	Desktop = "desktop" // the computer's own desktop, shared to the dashboard, on demand
)

// WebPort is the dashboard's default port.
const WebPort = 443

// WebSettings is the browser dashboard: service status and controls, links to the web UIs, and desktop apps
// (JS8Call, WSJT-X...) or the whole desktop in the browser through noVNC.
type WebSettings struct {
	Console bool   `json:"console,omitempty"` // opt-in authenticated browser terminal with this user's full permissions
	Access  string `json:"access,omitempty"`
	Port    int    `json:"port,omitempty"` // 0 means WebPort
	Cert    string `json:"cert,omitempty"` // TLS certificate (PEM, may hold the chain); empty: a self-signed one hamstack makes
	Key     string `json:"key,omitempty"`  // its private key
	Off     bool   `json:"off,omitempty"`  // kept in the config but not run
}

// RunsWeb reports whether the dashboard is part of the setup.
func (c Config) RunsWeb() bool { return c.Web != nil && !c.Web.Off }

// ListenPort is the dashboard's port.
func (w WebSettings) ListenPort() int {
	if w.Port > 0 {
		return w.Port
	}
	return WebPort
}

// SetWeb validates and saves the dashboard settings.
func (c *Config) SetWeb(w WebSettings) error {
	if w.Access != "" {
		a, err := ParseAccess(w.Access)
		if err != nil {
			return err
		}
		w.Access = a
	}
	if w.Port < 0 || w.Port > 65535 {
		return fmt.Errorf("port %d is out of range", w.Port)
	}
	if (w.Cert == "") != (w.Key == "") {
		return fmt.Errorf("give both a certificate and its key, or neither (hamstack then makes a self-signed one)")
	}
	c.Web = &w
	return nil
}

// EffectiveAccess preserves pre-access-setting dashboards, which listened on all interfaces.
func (w WebSettings) EffectiveAccess() string {
	if w.Access == "" {
		return AccessAll
	}
	return w.Access
}
