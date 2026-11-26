package main

import (
	"fmt"
	"os"

	"github.com/ATCUSA/hamstack/internal/apps"
	"github.com/ATCUSA/hamstack/internal/audio"
	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/ledger"
)

// WSJT-X Improved's Qt 6 build reaches the sound card through PipeWire and stores the devices by PipeWire's names
// (SoundInName, SoundOutName in its [Configuration]). Its "TCI audio" setting (TCIAudio=true) sends the audio over the
// network to a TCI radio instead, an SDR protocol: with a radio that does not speak it, WSJT-X keys the transmitter and
// sends nothing. On the Pi it read 5 W and ALC 0 on every over, and no station heard it, until TCI audio was turned off and
// the output set to the radio's card (then 52 W and 328 reports within four minutes). The recommended setup checks both.
// Only WSJT-X: JS8Call's build here is Qt 5, whose device names were not checked.

// soundEdit is one sound setting the recommended setup would change.
type soundEdit struct{ Key, Old, New string }

// soundEdits are the changes that would give a WSJT-X settings file the radio's sound card, in and out, and turn TCI
// audio off. Nothing when it already has them.
func soundEdits(text, in, out string) []soundEdit {
	var out2 []soundEdit
	if v, _ := iniGet(text, "Configuration", "TCIAudio"); v == "true" {
		out2 = append(out2, soundEdit{"TCIAudio", v, "false"})
	}
	if v, _ := iniGet(text, "Configuration", "SoundInName"); in != "" && v != in {
		out2 = append(out2, soundEdit{"SoundInName", v, in})
	}
	if v, _ := iniGet(text, "Configuration", "SoundOutName"); out != "" && v != out {
		out2 = append(out2, soundEdit{"SoundOutName", v, out})
	}
	return out2
}

// emptyOnly keeps the edits that fill a setting nobody has set: what hamstack does by itself when a program starts. A
// device someone chose, and TCI audio (right for a TCI radio), are left for the recommended setup's button.
func emptyOnly(edits []soundEdit) []soundEdit {
	var out []soundEdit
	for _, e := range edits {
		if e.Old == "" && e.Key != "TCIAudio" {
			out = append(out, e)
		}
	}
	return out
}

// rigSoundNames are PipeWire's names for the first active rig's sound card, input and output. ok is false without
// PipeWire, or when PipeWire does not offer the card both ways.
var rigSoundNames = func(cfg config.Config) (rig, in, out string, ok bool) {
	cards := radioCards(cfg)
	if len(cards) == 0 {
		return "", "", "", false
	}
	b, err := pipeWire.dump()
	if err != nil {
		return "", "", "", false
	}
	pc, err := audio.PipeWireCard(b, cards[0].Index)
	if err != nil || pc.SourceName == "" || pc.SinkName == "" {
		return "", "", "", false
	}
	return cards[0].Rig, pc.SourceName, pc.SinkName, true
}

// seedSound applies the sound part of the recommended setup to WSJT-X, records it for Undo and uninstall, and says
// what it did ("" when there was nothing to do). It is only run when the person applies the recommended setup: the
// program's sound devices are the person's choice, so nothing changes them by itself.
func seedSound(a apps.App, cfg config.Config) (string, error) { return seedSoundAs(a, cfg, false) }

// seedSoundAs is seedSound; with auto (a program about to start) it only fills sound settings that are empty.
func seedSoundAs(a apps.App, cfg config.Config, auto bool) (string, error) {
	if a.Name != "wsjtx" {
		return "", nil
	}
	rig, in, out, ok := rigSoundNames(cfg)
	if !ok {
		return "", nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	path := seedPath(a, home)
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	edits := soundEdits(string(old), in, out)
	if auto {
		edits = emptyOnly(edits)
	}
	if len(edits) == 0 {
		return "", nil
	}
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
		if bak := path + ".hamstack-bak"; !fileExists(bak) {
			if err := os.WriteFile(bak, old, mode); err != nil {
				return "", err
			}
		}
	}
	var set [][2]string
	var led []ledger.Edit
	for _, e := range edits {
		set = append(set, [2]string{e.Key, e.New})
		led = append(led, ledger.Edit{Key: e.Key, Old: e.Old, New: e.New})
	}
	text := iniSet(string(old), "Configuration", set)
	if err := writeFileAtomic(path, []byte(text), mode); err != nil {
		return "", err
	}
	note := fmt.Sprintf("%s: sound set to %s's card (in %s, out %s)", a.Title, rig, in, out)
	if err := ledger.Add(home, ledger.Change{Kind: ledger.AppConfig, Role: ledger.Support, For: a.Name, Path: path, Edits: led, Sum: sumOf([]byte(text))}); err != nil {
		note += fmt.Sprintf(" (could not record this for Undo: %v)", err)
	}
	if auto {
		return note + " (nothing was set; change it in the program's own settings if you like)", nil
	}
	return note + ". Close and open it again for the change to take effect.", nil
}

// programRunning reports whether a program of this name runs now (its process name, as /proc/PID/comm has it). A
// variable so a test need not start one.
var programRunning = func(comm string) bool {
	ents, _ := os.ReadDir("/proc")
	for _, e := range ents {
		if b, err := os.ReadFile("/proc/" + e.Name() + "/comm"); err == nil && string(b) == comm+"\n" {
			return true
		}
	}
	return false
}
