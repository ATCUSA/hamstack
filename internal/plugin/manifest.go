// Package plugin defines hamstack's plugin manifest (DRAFT: the runtime that loads plugins is not built yet) and the
// validator that is its ground truth. docs/plugins/spec.md describes the fields; this code decides what is valid.
//
// A plugin is a separate program with its own localhost web server. hamstack installs it, runs it as a user service
// on a port it picks, and shows it in the dashboard behind the dashboard's login at /plugin/<id>/.
package plugin

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

const SchemaVersion = 1

type Manifest struct {
	Schema      int      `json:"schema"`
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Version     string   `json:"version"` // the version of this manifest's plugin at the time it was written; hamstack asks the program itself
	Description string   `json:"description"`
	Homepage    string   `json:"homepage,omitempty"`
	License     string   `json:"license,omitempty"`
	Category    string   `json:"category,omitempty"` // one of the catalog categories
	Install     Install  `json:"install"`
	Run         Run      `json:"run"`
	UI          UI       `json:"ui"`
	Needs       Needs    `json:"needs"`
	Config      []Config `json:"config,omitempty"`
}

// Install says where the program comes from. Only signed-by-checksum GitHub releases are supported.
type Install struct {
	GitHub    string   `json:"github"`                // owner/repo
	TagPrefix string   `json:"tag_prefix"`            // "v" for tags like v1.2.3
	Asset     string   `json:"asset"`                 // file name template: {tag} {os} {arch} {version}
	Checksums string   `json:"checksums"`             // file in the release holding sha256 sums, e.g. sha256sums.txt
	OS        []string `json:"os"`                    // linux only for now
	Arch      []string `json:"arch"`                  // amd64, arm64
	Raw       bool     `json:"raw_binary"`            // the asset is the executable itself (not an archive)
	Version   []string `json:"version_cmd,omitempty"` // arguments that print the version, e.g. ["-version"]
}

// Run says how hamstack starts it. base_path "arg": a command word carries {base_path}; "env": the program reads
// HAMSTACK_BASE_PATH, which hamstack always sets. Command words may use {bin} {listen} {port} {base_path} {data_dir} {config_dir} {rigctld}.
type Run struct {
	Command  []string          `json:"command"`
	Env      map[string]string `json:"env,omitempty"` // same placeholders; never put secrets here
	Health   string            `json:"health"`        // path that answers 200 when it is ready, relative to its own root
	StopWith string            `json:"stop_with"`     // "SIGTERM": it must release the radio (unkey) before it exits
	BasePath string            `json:"base_path"`     // how it learns where it is served: "arg", "env" or "none" (it cannot: then it needs its own port exposed)
	Restart  string            `json:"restart,omitempty"`
}

// UI is the plugin's page, shown inside the dashboard at /plugin/<id>/.
type UI struct {
	Title string `json:"title"`
	Path  string `json:"path"`  // starting path, "/" normally
	Embed string `json:"embed"` // "frame" (inside the dashboard) or "tab" (opens in its own tab)
	Auth  string `json:"auth"`  // "dashboard": hamstack's login protects it and the plugin needs none
	// Permissions the page asks the browser for, granted to its frame by the dashboard. Only "microphone" for now. It
	// needs the dashboard over HTTPS (browsers refuse the microphone on plain HTTP except on localhost).
	Permissions []string `json:"permissions,omitempty"`
}

// Needs are what the plugin is allowed to touch. The dashboard shows them before install.
type Needs struct {
	Rig       string `json:"rig"`       // "none", "rigctld" (talks to hamstack's rigctld as a client) or "serial" (opens the port itself: not allowed alongside hamstack's rig service)
	Audio     bool   `json:"audio"`     // opens the radio's sound card, exclusively: hamstack stops that rig's HF modem while the plugin runs
	Transmits bool   `json:"transmits"` // can key the transmitter: must unkey on SIGTERM and be listed as a modem for sound-card sharing
	Network   string `json:"network"`   // "loopback" (default) or "lan"
}

// Config is a setting hamstack shows on the plugin's page and passes as an environment variable.
type Config struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Type    string `json:"type"` // text, number, toggle
	Default string `json:"default,omitempty"`
	Env     string `json:"env"`
	Secret  bool   `json:"secret,omitempty"` // never shown again, never logged
}

var (
	idRe        = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}$`)
	placeholder = regexp.MustCompile(`\{([a-z_]+)\}`)
	knownPH     = map[string]bool{"bin": true, "listen": true, "port": true, "base_path": true, "data_dir": true, "config_dir": true, "rigctld": true}
	assetPH     = map[string]bool{"tag": true, "os": true, "arch": true, "version": true}
	repoRe      = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	envRe       = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
	reserved    = map[string]bool{"api": true, "vnc": true, "apps": true, "services": true, "updates": true, "settings": true, "login": true, "plugin": true, "hamstack": true, "pat": true}
	categories  = map[string]bool{"": true, "winlink": true, "modem": true, "digital": true, "aprs": true, "tools": true, "remote": true}
)

// Parse reads a manifest and refuses unknown fields: a misspelled key must not be silently ignored.
func Parse(b []byte) (*Manifest, error) {
	var m Manifest
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("manifest is not valid: %w", err)
	}
	return &m, nil
}

// Validate returns every problem it finds, each saying what to change. An empty list means valid.
func (m *Manifest) Validate() []string {
	var p []string
	bad := func(f string, a ...any) { p = append(p, fmt.Sprintf(f, a...)) }
	if m.Schema != SchemaVersion {
		bad("schema: must be %d (this hamstack understands schema %d)", SchemaVersion, SchemaVersion)
	}
	switch {
	case !idRe.MatchString(m.ID):
		bad("id: %q must be 2-31 characters: lowercase letters, digits and '-', starting with a letter", m.ID)
	case reserved[m.ID]:
		bad("id: %q is used by hamstack itself; pick another", m.ID)
	}
	if strings.TrimSpace(m.Name) == "" {
		bad("name: required (what people see)")
	}
	if strings.TrimSpace(m.Description) == "" || len(m.Description) > 200 {
		bad("description: required, one sentence of at most 200 characters")
	}
	if !categories[m.Category] {
		bad("category: %q is not one of winlink, modem, digital, aprs, tools, remote", m.Category)
	}
	// install
	in := m.Install
	if !repoRe.MatchString(in.GitHub) {
		bad("install.github: must be owner/repo")
	}
	if in.Asset == "" {
		bad("install.asset: required, e.g. \"name-{tag}-{os}-{arch}\"")
	}
	for _, x := range placeholder.FindAllStringSubmatch(in.Asset, -1) {
		if !assetPH[x[1]] {
			bad("install.asset: unknown placeholder {%s} (use {tag} {version} {os} {arch})", x[1])
		}
	}
	if in.Checksums == "" {
		bad("install.checksums: required: hamstack installs nothing it cannot verify (a sha256 file in the same release)")
	}
	if len(in.OS) == 0 || len(in.Arch) == 0 {
		bad("install.os and install.arch: list what you publish, e.g. [\"linux\"] and [\"amd64\",\"arm64\"]")
	}
	for _, o := range in.OS {
		if o != "linux" {
			bad("install.os: %q is not supported yet (linux only)", o)
		}
	}
	for _, a := range in.Arch {
		if a != "amd64" && a != "arm64" {
			bad("install.arch: %q is not supported (amd64, arm64)", a)
		}
	}
	// run
	r := m.Run
	if len(r.Command) == 0 || r.Command[0] != "{bin}" {
		bad("run.command: required and its first word must be \"{bin}\" (the installed program)")
	}
	uses := strings.Join(r.Command, " ")
	for _, v := range r.Env {
		uses += " " + v
	}
	for _, x := range placeholder.FindAllStringSubmatch(uses, -1) {
		if !knownPH[x[1]] {
			bad("run: unknown placeholder {%s} (use {bin} {listen} {port} {base_path} {data_dir} {config_dir} {rigctld})", x[1])
		}
	}
	if !strings.Contains(uses, "{listen}") && !strings.Contains(uses, "{port}") {
		bad("run: the plugin must be told where to listen with {listen} (127.0.0.1:PORT) or {port}: hamstack picks the port")
	}
	if !strings.HasPrefix(r.Health, "/") {
		bad("run.health: a path starting with '/' that answers 200 once the plugin is ready")
	}
	if r.StopWith != "SIGTERM" {
		bad("run.stop_with: must be \"SIGTERM\": the plugin must shut down cleanly on it")
	}
	switch r.BasePath {
	case "arg":
		if !strings.Contains(uses, "{base_path}") {
			bad("run.base_path is \"arg\" but no command word uses {base_path}: pass it, e.g. [\"-base-path\", \"{base_path}\"]")
		}
	case "env":
		// hamstack always sets HAMSTACK_BASE_PATH, so nothing needs mapping: the program reads that variable.
	case "none":
		bad("run.base_path \"none\": a plugin that cannot be served under /plugin/%s/ cannot be shown in the dashboard yet; support a base path (see docs/plugins/guide.md, Serving under a prefix)", m.ID)
	default:
		bad("run.base_path: \"arg\" or \"env\" (how the plugin is told its URL prefix)")
	}
	for k, v := range r.Env {
		if !envRe.MatchString(k) {
			bad("run.env: %q is not a valid variable name", k)
		}
		if strings.Contains(strings.ToLower(k), "password") || strings.Contains(strings.ToLower(k), "token") || strings.Contains(strings.ToLower(k), "secret") {
			bad("run.env: %s looks like a secret; secrets are config entries with \"secret\": true, never fixed values", k)
		}
		_ = v
	}
	if strings.Contains(uses, "{rigctld}") && m.Needs.Rig != "rigctld" {
		bad("run: {rigctld} is only available when needs.rig is \"rigctld\"")
	}
	// ui
	u := m.UI
	if strings.TrimSpace(u.Title) == "" {
		bad("ui.title: required")
	}
	if !strings.HasPrefix(u.Path, "/") {
		bad("ui.path: a path starting with '/'")
	}
	if u.Embed != "frame" && u.Embed != "tab" {
		bad("ui.embed: \"frame\" or \"tab\"")
	}
	if u.Auth != "dashboard" {
		bad("ui.auth: must be \"dashboard\": hamstack's login protects the plugin; a plugin must not run a second login of its own behind it")
	}
	for _, perm := range u.Permissions {
		if perm != "microphone" {
			bad("ui.permissions: %q is not supported (only \"microphone\")", perm)
		}
		if perm == "microphone" && !m.Needs.Audio {
			bad("ui.permissions \"microphone\": a plugin that records from the browser's microphone must declare needs.audio (it will play to or record from the radio's sound card)")
		}
	}
	// needs
	n := m.Needs
	switch n.Rig {
	case "none", "rigctld":
	case "serial":
		bad("needs.rig \"serial\": a plugin may not open the radio's serial port itself (hamstack's rig service owns it); use \"rigctld\" and connect as a client")
	default:
		bad("needs.rig: \"none\" or \"rigctld\"")
	}
	if n.Network != "" && n.Network != "loopback" && n.Network != "lan" {
		bad("needs.network: \"loopback\" or \"lan\"")
	}
	if n.Transmits && n.Rig == "none" && !n.Audio {
		bad("needs.transmits: a plugin that transmits must use the rig (\"rigctld\") or the sound card")
	}
	// config
	seen := map[string]bool{}
	for i, c := range m.Config {
		if c.Key == "" || seen[c.Key] {
			bad("config[%d].key: required and unique", i)
		}
		seen[c.Key] = true
		if c.Type != "text" && c.Type != "number" && c.Type != "toggle" {
			bad("config[%d].type: text, number or toggle", i)
		}
		if !envRe.MatchString(c.Env) {
			bad("config[%d].env: an UPPER_CASE variable name", i)
		}
		if c.Secret && c.Default != "" {
			bad("config[%d]: a secret cannot have a default", i)
		}
	}
	return p
}
