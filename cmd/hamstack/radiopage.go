package main

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/ATCUSA/hamstack/internal/audio"
	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/netinfo"
	"github.com/ATCUSA/hamstack/internal/patcfg"
	"github.com/ATCUSA/hamstack/internal/svc"
	"github.com/ATCUSA/hamstack/internal/units"
	"github.com/ATCUSA/hamstack/internal/versions"
	"github.com/ATCUSA/hamstack/internal/web"
)

// rxTarget is the receive level (band noise, RMS) a data modem works well with: room above it for strong stations.
const rxTarget = -25.0

// RadioPage gathers everything about one rig for its page.
func (webBackend) RadioPage(id string) (web.RadioPage, error) {
	cfg := loadCfg()
	if len(cfg.Rigs) == 0 {
		return web.RadioPage{}, fmt.Errorf("no radio is set up yet (Settings, Rigs, or: hamstack setup)")
	}
	if id == "" {
		id = cfg.Rigs[0].ID
	}
	rig, prof, err := findRig(cfg, id)
	if err != nil {
		return web.RadioPage{}, err
	}
	p := web.RadioPage{ID: rig.ID, Name: orDash(rig.Name), Model: prof.Name}
	if rig.Name == "" {
		p.Name = rig.ID
	}
	for _, r := range cfg.Rigs {
		p.Rigs = append(p.Rigs, web.RadioRef{ID: r.ID, Name: r.Name, Off: r.Off})
	}
	p.Modems = radioModems(cfg, rig)
	for _, m := range config.HFModemKinds {
		if rig.NoCAT || slices.Contains(rig.HFModems(), m) || !config.IsHFModem(rig.Modem) {
			continue
		}
		if _, err := versions.Which(m); err == nil {
			p.Add = append(p.Add, m)
		}
	}
	snap, _ := scanDevices()
	dev := resolveAudio(rig, prof, snap)
	p.Sound = web.RadioSound{Card: cardOf(dev), Device: dev, Shared: rig.SharedAudio}
	switch {
	case dev == "":
		p.Sound.Note = "no sound card found for this radio: set it in Settings, Rigs (or: hamstack rig set " + rig.ID + " audio CARD)"
	case rig.SharedAudio:
		p.Sound.Device = audio.SharedDevice(rig.ID) + " (listening, shared) and " + dev + " (transmitting)"
	}
	p.Pat = radioPat(cfg)
	p.Ports = radioPorts(cfg, rig)
	p.Heard = radioHeard(rig)
	p.Setup = radioSetupRef(rig)
	return p, nil
}

func radioModems(cfg config.Config, rig config.Rig) []web.RadioModem {
	var out []web.RadioModem
	kinds := rig.HFModems()
	if rig.Modem != "" && !slices.Contains(kinds, rig.Modem) {
		kinds = append([]string{rig.Modem}, kinds...) // direwolf
	}
	for _, k := range kinds {
		s := units.Service{Kind: k, Rig: rig.ID}
		m := web.RadioModem{Kind: k, Unit: s.Name(), What: serviceWhat(s), State: "standby"}
		if k == rig.Modem {
			m.State = webState(svc.Status(s.Unit()))
			m.Running = m.State == "running"
		} else {
			m.Note = "switch to it to use it: " + orDash(rig.Modem) + " stops"
		}
		switch k {
		case config.Mercury:
			if cfg.UI {
				_, _, _, ui := config.MercuryPorts(rig)
				m.Link, m.LinkLabel = ":"+strconv.Itoa(ui), "Open web UI"
			}
		case config.Data2G:
			if cfg.Data2GPageOn() {
				_, _, _, w := config.Data2GPorts(rig)
				m.Link, m.LinkLabel = ":"+strconv.Itoa(w), "Open web UI"
				if cfg.Data2GRelayed() {
					m.Link = "https::" + strconv.Itoa(config.Data2GPagePort(rig)) // served by the dashboard, over HTTPS
				}
			}
			m.Password = "none"
			if f, err := data2gSettingsFile(rig.ID); err == nil && hasData2GPassword(f) {
				m.Password = "set"
			}
		case config.Xara:
			switch {
			case !rig.XaraScreenOn():
				m.ScreenOff = true
			case m.Running:
				m.Link, m.LinkLabel = "/xara/"+rig.ID+"/", "Open screen"
			default:
				m.Note = strings.TrimPrefix(m.Note+"; its screen shows here while it runs", "; ")
			}
		case config.Ardopcf:
			switch {
			case !ardopScreenOn(rig):
				m.ScreenOff = true
			case m.Running:
				m.Link, m.LinkLabel = "/ardop/"+rig.ID+"/", "Open screen"
			default:
				m.Note = strings.TrimPrefix(m.Note+"; its screen shows here while it runs", "; ")
			}
		}
		if !m.Running && m.Link != "" && !strings.HasPrefix(m.Link, "/") {
			m.Link = "" // a modem's own web UI is only there while it runs
		}
		out = append(out, m)
	}
	return out
}

func radioPat(cfg config.Config) web.RadioPat {
	st, ok := cfg.ActiveStation()
	if !ok || !cfg.PatEnabled() {
		return web.RadioPat{Note: "Pat is not set up"}
	}
	p := web.RadioPat{Station: st.Callsign}
	changes, err := patcfg.Plan(st.PatConfig, patWiring(cfg))
	if err != nil {
		p.Note = "Pat's config could not be read: " + err.Error()
		return p
	}
	for _, c := range changes {
		p.Changes = append(p.Changes, web.AppChange{Path: c.Path, Old: c.Old, New: c.New, WasSet: c.WasSet})
	}
	return p
}

func radioPorts(cfg config.Config, rig config.Rig) []web.RadioPort {
	modes := map[string]string{}
	for _, t := range accessTargets(cfg) {
		modes[t.Service+"/"+strconv.Itoa(t.Port)] = t.Mode
	}
	var out []web.RadioPort
	for _, row := range networkRows(cfg, netinfo.Listeners("/")) {
		if !strings.HasSuffix(row.Service, "@"+rig.ID) {
			continue
		}
		out = append(out, web.RadioPort{What: row.What, Port: row.Port, Access: modes[row.Service+"/"+strconv.Itoa(row.Port)], Open: row.Open, Note: row.Note})
	}
	return out
}

// RadioLevel measures the rig's receive audio: through the shared card while a modem runs (it keeps running), or
// from the card itself when nothing holds it.
func (webBackend) RadioLevel(id string) (web.RadioLevel, error) {
	cfg := loadCfg()
	rig, prof, err := findRig(cfg, id)
	if err != nil {
		return web.RadioLevel{}, err
	}
	snap, _ := scanDevices()
	dev := resolveAudio(rig, prof, snap)
	if dev == "" {
		return web.RadioLevel{}, fmt.Errorf("no sound card found for this radio")
	}
	device, env := dev, ""
	if rig.SharedAudio {
		if device, env, err = sharedAudio(rig, dev); err != nil {
			return web.RadioLevel{}, err
		}
	} else if h := audio.Holders("hw:CARD=" + cardOf(dev) + ",DEV=0"); len(h) > 0 {
		return web.RadioLevel{}, fmt.Errorf("the sound card is in use by %s: turn on shared listening (Sound, above) to measure while the modem runs", strings.Join(h, ", "))
	}
	l, err := audio.Measure(device, env, 3)
	if err != nil {
		return web.RadioLevel{}, err
	}
	advice := l.Advice(rxTarget)
	return web.RadioLevel{RMS: l.RMS, Peak: l.Peak, Clipped: l.Clipped, Target: rxTarget, Advice: advice, Good: advice == "good for a data modem"}, nil
}
