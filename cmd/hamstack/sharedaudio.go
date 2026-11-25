package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ATCUSA/hamstack/internal/audio"
	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/profile"
	"github.com/ATCUSA/hamstack/internal/usbdev"
)

// sharedAudioDir is where the rigs' shared-card configurations are written (hamstack's state folder).
func sharedAudioDir() string { return filepath.Join(stateDir(), "alsa") }

// cardOf is the ALSA card id in a device string resolveAudio returns ("plughw:CARD=CODEC,DEV=0" -> "CODEC").
func cardOf(dev string) string {
	_, rest, ok := strings.Cut(dev, "CARD=")
	if !ok {
		return ""
	}
	id, _, _ := strings.Cut(rest, ",")
	return id
}

// sharedAudio writes rig's shared-card configuration and returns its device and the environment entry that
// gives it to a program.
func sharedAudio(rig config.Rig, dev string) (device, env string, err error) {
	card := cardOf(dev)
	if card == "" {
		return "", "", fmt.Errorf("the sound card %q is not one hamstack can share", dev)
	}
	index := (rig.ArqPort - config.ArqPortBase) / config.ArqPortStep
	env, err = audio.WriteShared(sharedAudioDir(), rig.ID, audio.SharedConfig(rig.ID, rig.Name, card, index, audio.Caps(card)))
	if err != nil {
		return "", "", err
	}
	return audio.SharedDevice(rig.ID), env, nil
}

// modemAudio is the sound device a modem of rig should open: the card itself, or, with shared audio on, the rig's
// shared view of it (and this process's environment then points ALSA at its configuration, which the modem
// inherits). If the shared configuration cannot be written, the card is used directly, and the log says why.
func modemAudio(rig config.Rig, prof profile.Profile, snap usbdev.Snapshot) string {
	dev := resolveAudio(rig, prof, snap)
	if dev == "" || !rig.SharedAudio {
		return dev
	}
	shared, env, err := sharedAudio(rig, dev)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hamstack: rig %s: using the sound card directly, not shared: %v\n", rig.ID, err)
		return dev
	}
	k, v, _ := strings.Cut(env, "=")
	_ = os.Setenv(k, v)
	fmt.Fprintf(os.Stderr, "hamstack: rig %s: sound card %s shared as %s\n", rig.ID, cardOf(dev), shared)
	return shared
}

// cmdAudio: hamstack audio device RIG | test RIG [SECONDS]
func cmdAudio(args []string) error {
	usage := fmt.Errorf("usage: hamstack audio device RIG   (the shared device and the setting another program needs to use it)\n       hamstack audio test RIG [SECONDS]   (records from the shared device twice at once, to check sharing works)")
	if len(args) < 2 {
		return usage
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	rig, prof, err := findRig(cfg, args[1])
	if err != nil {
		return err
	}
	snap, _ := scanDevices()
	dev := resolveAudio(rig, prof, snap)
	if dev == "" {
		return fmt.Errorf("rig %s has no sound card (hamstack rig set %s audio CARD)", rig.ID, rig.ID)
	}
	shared, env, err := sharedAudio(rig, dev)
	if err != nil {
		return err
	}
	switch args[0] {
	case "device":
		fmt.Printf("device: %s\n%s\n", shared, env)
		if !rig.SharedAudio {
			fmt.Printf("(rig %s's modems still open the card directly: hamstack rig set %s shared-audio on)\n", rig.ID, rig.ID)
		}
		return nil
	case "test":
		seconds := 3
		if len(args) > 2 {
			if seconds, err = strconv.Atoi(args[2]); err != nil || seconds < 1 || seconds > 30 {
				return usage
			}
		}
		return sharedAudioTest(shared, env, cardOf(dev), seconds)
	}
	return usage
}

// sharedAudioTest records from the shared device with two programs at once, the way two modems would listen. It
// plays nothing: nothing reaches the radio.
func sharedAudioTest(device, env, card string, seconds int) error {
	if _, err := exec.LookPath("arecord"); err != nil {
		return fmt.Errorf("the test uses arecord, which is not installed (Debian: alsa-utils)")
	}
	if h := audio.Holders("hw:CARD=" + card + ",DEV=0"); len(h) > 0 {
		fmt.Printf("note: the card is open directly by %s; sharing cannot open it until that program lets go.\n", strings.Join(h, ", "))
	}
	fmt.Printf("Recording %d s from %s with two programs at once (nothing is played or sent)...\n", seconds, device)
	type result struct {
		err error
		out string
	}
	res := make([]result, 2)
	var wg sync.WaitGroup
	for i, rate := range []string{"12000", "48000"} { // ardopcf's rate and a monitor's
		wg.Add(1)
		go func(i int, rate string) {
			defer wg.Done()
			cmd := exec.Command("arecord", "-q", "-D", device, "-f", "S16_LE", "-c", "1", "-r", rate, "-d", strconv.Itoa(seconds), os.DevNull)
			cmd.Env = append(os.Environ(), env)
			var out bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &out
			res[i] = result{cmd.Run(), strings.TrimSpace(out.String())}
		}(i, rate)
		time.Sleep(200 * time.Millisecond) // the second opens while the first is recording
	}
	wg.Wait()
	ok := true
	for i, r := range res {
		if r.err != nil {
			ok = false
			fmt.Printf("  listener %d: failed: %v %s\n", i+1, r.err, r.out)
		} else {
			fmt.Printf("  listener %d: ok\n", i+1)
		}
	}
	if !ok {
		return fmt.Errorf("the card could not be shared")
	}
	fmt.Println("Both recorded at once: the card can be shared.")
	return nil
}
