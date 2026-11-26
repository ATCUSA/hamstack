package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ATCUSA/hamstack/internal/apps"
	"github.com/ATCUSA/hamstack/internal/catalog"
	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/ledger"
	"github.com/ATCUSA/hamstack/internal/patcfg"
	"github.com/ATCUSA/hamstack/internal/svc"
	"github.com/ATCUSA/hamstack/internal/units"
	"github.com/ATCUSA/hamstack/internal/versions"
	"github.com/ATCUSA/hamstack/internal/web"
)

// Each app's own page. A program is described by a small piece of Go here (what its fields are, what its recommended
// setup does), not by a generic schema: the apps differ in kind, and what they share is the page that shows them.

var versionCache struct {
	sync.Mutex
	m map[string]struct {
		v  string
		at time.Time
	}
}

// cachedVersion is a program's version, looked up at most every five minutes: finding it runs the program.
func cachedVersion(pkg string) string {
	versionCache.Lock()
	defer versionCache.Unlock()
	if e, ok := versionCache.m[pkg]; ok && time.Since(e.at) < 5*time.Minute {
		return e.v
	}
	v := ""
	if p, ok := catalog.ByName(pkg); ok {
		if in := versions.FindInstalled(p); in != nil {
			v = in.Version
		}
	}
	if versionCache.m == nil {
		versionCache.m = map[string]struct {
			v  string
			at time.Time
		}{}
	}
	versionCache.m[pkg] = struct {
		v  string
		at time.Time
	}{v, time.Now()}
	return v
}

// patField describes one of Pat's settings the page can change by name.
type patField struct{ Key, Label, Kind, Hint string }

var patFields = []patField{
	{"ardop.rig", "ARDOP: radio", "rig", "The rig Pat tunes for ARDOP, through rigctld. Empty: Pat cannot tune the radio for ARDOP."},
	{"ardop.addr", "ARDOP: modem address", "addr", "Where the ARDOP modem listens, as host:port. With ardopcf set up on a rig, the recommended setup points this at it."},
	{"ardop.ptt_ctrl", "ARDOP: Pat keys the radio", "toggle", "On: Pat keys the transmitter through rigctld. Off: the ARDOP modem does it."},
	{"varahf.rig", "VARA HF / Mercury: radio", "rig", "The rig Pat tunes for VARA HF, which is how Pat reaches Mercury."},
	{"varahf.addr", "VARA HF / Mercury: modem address", "addr", "Mercury's port; the recommended setup sets it from the rig's modem."},
	{"varahf.ptt_ctrl", "VARA HF / Mercury: Pat keys the radio", "toggle", "On: Pat keys the transmitter through rigctld."},
}

func patFieldFor(key string) (patField, bool) {
	for _, f := range patFields {
		if f.Key == key {
			return f, true
		}
	}
	return patField{}, false
}

var hostPort = regexp.MustCompile(`^[A-Za-z0-9.-]+:[0-9]{1,5}$`)

func (webBackend) AppPage(name string) (web.AppPageInfo, error) {
	cfg := loadCfg()
	if name == "pat" {
		return patPage(cfg), nil
	}
	a, ok := apps.Find(name)
	if !ok || a.Name == "console" || !apps.ValidName(name) {
		return web.AppPageInfo{}, fmt.Errorf("no app %q", name)
	}
	return browserAppPage(cfg, a), nil
}

func rigOptions(cfg config.Config) [][2]string {
	opts := [][2]string{{"", "none"}}
	for _, r := range cfg.ActiveRigs() {
		if !r.NoCAT {
			opts = append(opts, [2]string{r.ID, rigLabel(r)})
		}
	}
	return opts
}

func patPage(cfg config.Config) web.AppPageInfo {
	info := web.AppPageInfo{Name: "pat", Title: "Pat (Winlink)", Kind: "web", Package: "pat", Service: "pat", Log: "pat"}
	if _, err := versions.Which("pat"); err == nil {
		info.Installed = true
		info.Version = cachedVersion("pat")
	}
	st := svc.Status(units.Service{Kind: config.Pat}.Unit())
	info.State, info.Enabled = webState(st), st.Enabled == "enabled"
	station, ok := cfg.ActiveStation()
	if !ok {
		info.Notes = append(info.Notes, "No station is active: add one in Settings, Stations.")
		return info
	}
	if cfg.NetworkAccess("pat", station.EffectiveAccess()) == config.AccessLocal {
		info.LinkNote = "Pat's web page is available on the radio computer only. To reach it from other computers, change who can connect (Settings, Stations)."
	} else if _, p, err := net.SplitHostPort(station.HTTPAddr); err == nil {
		info.LinkPort, _ = strconv.Atoi(p)
		info.Scheme = "http"
	}
	if !fileExists(station.PatConfig) {
		info.Notes = append(info.Notes, "Pat's settings file for station "+station.ID+" does not exist yet; it is made the first time Pat is set up.")
		return info
	}
	keys := make([]string, len(patFields))
	for i, f := range patFields {
		keys[i] = f.Key
	}
	vals, err := patcfg.Fields(station.PatConfig, keys)
	if err != nil {
		info.Notes = append(info.Notes, "Pat's settings could not be read: "+err.Error())
		return info
	}
	for _, f := range patFields {
		fld := web.AppField{Key: f.Key, Label: f.Label, Kind: f.Kind, Value: vals[f.Key], Hint: f.Hint}
		switch f.Kind {
		case "rig":
			fld.Kind, fld.Options = "select", rigOptions(cfg)
		case "addr":
			fld.Kind = "text"
		}
		info.Fields = append(info.Fields, fld)
	}
	setup := &web.AppSetup{Title: "Recommended setup", Restart: "pat", CanDo: true}
	changes, err := patcfg.Plan(station.PatConfig, patWiring(cfg))
	switch {
	case err != nil:
		setup.CanDo, setup.Summary = false, "Pat's settings could not be read: "+err.Error()
	case len(changes) == 0:
		setup.Summary = "Pat's settings already match your rigs and grid."
	default:
		setup.Summary = fmt.Sprintf("%d settings would change to connect Pat to your rigs. Nothing else in Pat's settings is touched, and the passwords never leave the file.", len(changes))
		for _, c := range changes {
			setup.Changes = append(setup.Changes, web.AppChange{Path: c.Path, Old: c.Old, New: c.New, WasSet: c.WasSet})
		}
	}
	setup.CanUndo = len(loadPatUndo(station.ID)) > 0
	info.Setup = setup
	if _, ok := cfg.PatHFRig(); !ok {
		info.Notes = append(info.Notes, "No rig has a VARA-style HF modem (Mercury, Data2G or xara), so Pat's VARA HF entry points at nothing. Set a modem in Settings, Rigs.")
	}
	if _, ok := ardopRig(cfg); ok {
		info.Notes = append(info.Notes, "hamstack runs ardopcf for ARDOP. Recommended setup points Pat's ARDOP modem address at it and has Pat key the radio through rigctld (ardopcf cannot do that itself).")
	} else {
		info.Notes = append(info.Notes, "ARDOP needs an ARDOP modem (ardopcf) listening where its modem address says. hamstack can run it as a rig's modem: Settings, Rigs, modem ardopcf (install it first from Apps, Available to install).")
	}
	return info
}

// ---- the undo record: what the recommended setup changed, so only that can be put back ----

func patUndoPath(id string) string { return filepath.Join(stateDir(), "pat-undo-"+id+".json") }

func loadPatUndo(id string) []patcfg.Change {
	b, err := os.ReadFile(patUndoPath(id))
	if err != nil {
		return nil
	}
	var out []patcfg.Change
	_ = json.Unmarshal(b, &out)
	return out
}

// mergePatUndo keeps what each setting was before hamstack first changed it, and what hamstack last set it to.
func mergePatUndo(old, now []patcfg.Change) []patcfg.Change {
	out := append([]patcfg.Change(nil), old...)
	for _, c := range now {
		found := false
		for i := range out {
			if out[i].Path == c.Path {
				out[i].New, found = c.New, true
			}
		}
		if !found {
			out = append(out, c)
		}
	}
	return out
}

func savePatUndo(id string, changes []patcfg.Change) error {
	if err := os.MkdirAll(stateDir(), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(changes, "", "  ")
	return writeFileAtomic(patUndoPath(id), b, 0o600)
}

// patApply wires the active station's Pat config to the rigs and grid, and records what it changed.
func patApply(cfg config.Config) (config.Station, []patcfg.Change, string, error) {
	st, ok := cfg.ActiveStation()
	if !ok {
		return st, nil, "", fmt.Errorf("no active station")
	}
	backup, changes, err := patcfg.Apply(st.PatConfig, patWiring(cfg))
	if err != nil {
		return st, nil, "", err
	}
	if len(changes) > 0 {
		if err := savePatUndo(st.ID, mergePatUndo(loadPatUndo(st.ID), changes)); err != nil {
			return st, changes, backup, fmt.Errorf("Pat's settings were changed but the undo record could not be saved: %w", err)
		}
	}
	return st, changes, backup, nil
}

// ---- actions ----

func appFieldSet(r web.ManagementRequest) (any, error) {
	if r.ID != "pat" {
		return nil, fmt.Errorf("%s has no settings hamstack manages by name", r.ID)
	}
	f, ok := patFieldFor(r.Value)
	if !ok {
		return nil, fmt.Errorf("%q is not a setting hamstack manages", r.Value)
	}
	cfg := loadCfg()
	st, ok := cfg.ActiveStation()
	if !ok {
		return nil, fmt.Errorf("no active station")
	}
	var v any
	switch f.Kind {
	case "toggle":
		if r.Extra != "true" && r.Extra != "false" {
			return nil, fmt.Errorf("%s is on or off", f.Label)
		}
		v = r.Extra == "true"
	case "rig":
		if r.Extra != "" {
			found := false
			for _, o := range rigOptions(cfg) {
				found = found || o[0] == r.Extra
			}
			if !found {
				return nil, fmt.Errorf("%q is not one of your rigs with radio control", r.Extra)
			}
		}
		v = r.Extra
	case "addr":
		if r.Extra != "" {
			if !hostPort.MatchString(r.Extra) {
				return nil, fmt.Errorf("%q is not an address like localhost:8515", r.Extra)
			}
			p := r.Extra[strings.LastIndex(r.Extra, ":")+1:]
			if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
				return nil, fmt.Errorf("port %q is out of range", p)
			}
		}
		v = r.Extra
	}
	if err := patcfg.SetField(st.PatConfig, f.Key, v); err != nil {
		return nil, err
	}
	return f.Label + " saved. Restart Pat for it to take effect.", nil
}

func appSetupApply(r web.ManagementRequest) (any, error) {
	if !r.Confirm {
		return nil, fmt.Errorf("confirm applying the recommended setup")
	}
	cfg := loadCfg()
	if r.ID == "pat" {
		st, changes, backup, err := patApply(cfg)
		if err != nil {
			return nil, err
		}
		if len(changes) == 0 {
			return "Pat's settings already matched.", nil
		}
		return fmt.Sprintf("Applied: %d settings changed in Pat's config for station %s (the original is kept as %s). Restart Pat for it to take effect.", len(changes), st.ID, backup), nil
	}
	a, ok := apps.Find(r.ID)
	if !ok || !apps.ValidName(r.ID) {
		return nil, fmt.Errorf("no app %q", r.ID)
	}
	note, err := seedRadioApp(a, cfg)
	if err != nil {
		return nil, err
	}
	if note == "" {
		return nil, fmt.Errorf("nothing was changed: it already has a radio set up, or no rig control is running")
	}
	return note, nil
}

func appSetupUndo(r web.ManagementRequest) (any, error) {
	if !r.Confirm {
		return nil, fmt.Errorf("confirm undoing the recommended setup")
	}
	cfg := loadCfg()
	if r.ID == "pat" {
		st, ok := cfg.ActiveStation()
		if !ok {
			return nil, fmt.Errorf("no active station")
		}
		changes := loadPatUndo(st.ID)
		if len(changes) == 0 {
			return nil, fmt.Errorf("there is nothing to undo")
		}
		n, err := patcfg.Revert(st.PatConfig, changes)
		if err != nil {
			return nil, err
		}
		_ = os.Remove(patUndoPath(st.ID))
		return fmt.Sprintf("Put back %d settings (%d were left because they have been changed since). Restart Pat for it to take effect.", n, len(changes)-n), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	a, ok := apps.Find(r.ID)
	if !ok {
		return nil, fmt.Errorf("no app %q", r.ID)
	}
	path := seedPath(a, home)
	all, _ := ledger.Load(home)
	for _, c := range all {
		if c.Kind == ledger.AppConfig && c.Path == path {
			msg, err := revertAppConfig(c)
			if err != nil {
				return nil, err
			}
			_ = ledger.Drop(home, func(x ledger.Change) bool { return x.Kind == ledger.AppConfig && x.Path == path })
			if msg == "" {
				msg = "the radio keys had been changed since: they are left as they are"
			}
			return a.Title + ": " + msg, nil
		}
	}
	return nil, fmt.Errorf("hamstack has no record of setting up %s's radio", a.Title)
}

// seedPath is the settings file hamstack's radio setup writes for a program.
func seedPath(a apps.App, home string) string {
	switch a.Name {
	case "wsjtx":
		return filepath.Join(home, ".config", "WSJT-X.ini")
	case "js8call":
		return filepath.Join(home, ".config", "JS8Call.ini")
	case "fldigi":
		return filepath.Join(home, ".fldigi", "fldigi_def.xml")
	}
	return ""
}

func browserAppPage(cfg config.Config, a apps.App) web.AppPageInfo {
	info := web.AppPageInfo{Name: a.Name, Title: a.Title, Kind: "browser", Package: a.Package, Radio: a.Radio, Log: "vnc@" + a.Name}
	if a.Path() != "" {
		info.Installed = true
		info.Version = cachedVersion(a.Package)
	}
	st := svc.Status(vncUnit(a.Name))
	info.State, info.Enabled = webState(st), st.Enabled == "enabled"
	info.Notes = append(info.Notes, "Its own settings are in the program's own menus: open it with Open or Show.")
	if a.Radio {
		info.Notes = append(info.Notes, "It uses the radio's sound card: the HF modem stops while it is open, and starts again when it closes.")
	}
	home, err := os.UserHomeDir()
	if err != nil || seedPath(a, home) == "" {
		return info
	}
	info.Setup = radioSetupPreview(cfg, a, home)
	return info
}

// radioSetupPreview says what pointing a program at the rig would change, without changing it.
func radioSetupPreview(cfg config.Config, a apps.App, home string) *web.AppSetup {
	s := &web.AppSetup{Title: "Recommended setup: its radio"}
	path := seedPath(a, home)
	all, _ := ledger.Load(home)
	for _, c := range all {
		if c.Kind == ledger.AppConfig && c.Path == path {
			s.CanUndo = true
		}
	}
	rig, ok := seedRig(cfg)
	if !ok {
		s.Summary = "No rig control is running, so there is nothing to point it at. Start rig control first (Services)."
		return s
	}
	st, _ := cfg.ActiveStation()
	port := fmt.Sprintf("127.0.0.1:%d", rig.RigPort)
	add := func(path, old, new string) {
		s.Changes = append(s.Changes, web.AppChange{Path: path, Old: old, New: new, WasSet: old != ""})
	}
	if a.Name == "fldigi" {
		if fileExists(path) {
			s.Summary = "It already has settings of its own: hamstack leaves them as they are. Set the radio in its Configure menu (Rig control, Hamlib: NET rigctl, " + port + ")."
			return s
		}
		add("CHKUSEHAMLIBIS", "", "1")
		add("HAMRIGMODEL", "", "2 (NET rigctl)")
		add("HAMRIGDEVICE", "", port)
		add("HAMLIBCMDPTT", "", "1")
	} else {
		text := ""
		if b, err := os.ReadFile(path); err == nil {
			text = string(b)
		}
		if r, _ := iniGet(text, "Configuration", "Rig"); r != "" && r != "None" {
			s.Summary = "It already has a radio chosen (" + r + "): hamstack leaves it as it is."
			return s
		}
		for _, k := range []string{"CATNetworkPort", "CATSerialPort"} {
			if v, _ := iniGet(text, "Configuration", k); v != "" {
				s.Summary = "Its radio is set up another way (" + k + "): hamstack leaves it as it is."
				return s
			}
		}
		add("Rig", "", "Hamlib NET rigctl")
		add("CATNetworkPort", "", port)
		add("PTTMethod", "", "CAT")
	}
	if st.Callsign != "" {
		add("callsign (only if empty)", "", st.Callsign)
	}
	s.CanDo = true
	s.Summary = fmt.Sprintf("Would point it at %s (rigctld at %s), with PTT by CAT. A program that already has a radio is never changed, and Undo puts back only the radio keys.", rigLabel(rig), port)
	return s
}

// ardopRig is the rig whose modem is ardopcf, if any.
func ardopRig(cfg config.Config) (config.Rig, bool) {
	for _, r := range cfg.ActiveRigs() {
		if slices.Contains(r.HFModems(), config.Ardopcf) {
			return r, true
		}
	}
	return config.Rig{}, false
}
