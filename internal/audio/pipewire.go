package audio

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// PipeWire's session manager (WirePlumber) works out which directions of a sound card it can offer when it sets the
// card up. On a Raspberry Pi 5 with a Yaesu FT-DX10's USB codec it was once found offering the card for listening
// only: no output profile at all, until WirePlumber was restarted. Why is not established. A modem holding the card's
// output while WirePlumber starts was the first guess, but holding it with aplay through a restart did not
// reproduce it. Programs that reach the radio through PipeWire (WSJT-X Improved's Qt 6 build, JS8Call) then find no
// output: WSJT-X quietly falls back to "TCI audio" for both directions and hears nothing.

// PWCard is what PipeWire offers of one ALSA card.
type PWCard struct {
	Managed bool // PipeWire has the card as a device at all (false: no PipeWire, or the card is left to ALSA)
	Sink    bool // it offers the card's output (transmit audio)
	Source  bool // it offers the card's input (receive audio)
	// SinkName and SourceName are PipeWire's names for them (node.name), which programs such as WSJT-X store as their
	// sound devices.
	SinkName, SourceName string
}

// Missing names the directions PipeWire has lost, "" when it offers both (or does not manage the card).
func (c PWCard) Missing() string {
	switch {
	case !c.Managed || (c.Sink && c.Source):
		return ""
	case !c.Sink && !c.Source:
		return "input and output"
	case !c.Sink:
		return "output"
	}
	return "input"
}

// PipeWireCard reads pw-dump's output for what PipeWire offers of ALSA card index card.
func PipeWireCard(dump []byte, card int) (PWCard, error) {
	var objs []struct {
		Type string `json:"type"`
		Info struct {
			Props map[string]any `json:"props"`
		} `json:"info"`
	}
	if err := json.Unmarshal(dump, &objs); err != nil {
		return PWCard{}, fmt.Errorf("reading pw-dump: %w", err)
	}
	var c PWCard
	for _, o := range objs {
		p := o.Info.Props
		if p == nil {
			continue
		}
		switch p["media.class"] {
		case "Audio/Device":
			if n, ok := intProp(p, "api.alsa.card"); ok && n == card {
				c.Managed = true
			}
		case "Audio/Sink", "Audio/Source":
			n, ok := intProp(p, "api.alsa.pcm.card")
			if !ok {
				n, ok = intProp(p, "alsa.card")
			}
			if !ok || n != card {
				continue
			}
			name, _ := p["node.name"].(string)
			if p["media.class"] == "Audio/Sink" {
				c.Sink, c.SinkName = true, name
			} else {
				c.Source, c.SourceName = true, name
			}
		}
	}
	return c, nil
}

// intProp reads a property PipeWire writes as a number or, in some versions, as a string.
func intProp(p map[string]any, key string) (int, bool) {
	switch v := p[key].(type) {
	case float64:
		return int(v), true
	case string:
		n, err := strconv.Atoi(v)
		return n, err == nil
	}
	return 0, false
}

// CardIndex is the ALSA index of a card named by its id ("CODEC"), or "" if there is no such card.
func CardIndex(id string) string {
	if _, err := strconv.Atoi(id); err == nil {
		return id
	}
	return cardIndex("/proc/asound/cards", id)
}
