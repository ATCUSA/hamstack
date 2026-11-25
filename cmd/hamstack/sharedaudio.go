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
	usage := fmt.Errorf("usage: hamstack audio device RIG   (the shared device and the setting another program needs to use it)\n       hamstack audio test RIG [SECONDS]   (two recordings and silent playback at once; the radio is not keyed)")
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

// sharedAudioTest opens the shared device the way modems do: two programs recording at once, and a third playing
// silence at the same time (a modem opens playback while its capture runs, and that is what failed on the Pi). The
// radio is not keyed, so nothing is transmitted.
func sharedAudioTest(device, env, card string, seconds int) error {
	for _, tool := range []string{"arecord", "aplay"} {
		if _, err := exec.LookPath(tool); err != nil {
			return fmt.Errorf("the test uses %s, which is not installed (Debian: alsa-utils)", tool)
		}
	}
	fmt.Printf("For %d s, %s is opened by two programs recording and a third playing silence, all at once (the radio is not keyed)...\n", seconds, device)
	type result struct {
		what string
		err  error
		out  string
	}
	n := strconv.Itoa(seconds)
	runs := []struct{ what, tool, rate, ch string }{
		{"listener 1 (12 kHz, like ardopcf)", "arecord", "12000", "1"},
		{"listener 2 (48 kHz, like a monitor)", "arecord", "48000", "1"},
		{"playback (silence, 12 kHz stereo, like a modem keying up)", "aplay", "12000", "2"},
	}
	res := make([]result, len(runs))
	var wg sync.WaitGroup
	for i, r := range runs {
		wg.Add(1)
		go func(i int, what, tool, rate, ch string) {
			defer wg.Done()
			file := os.DevNull
			if tool == "aplay" {
				file = "/dev/zero"
			}
			cmd := exec.Command(tool, "-q", "-D", device, "-f", "S16_LE", "-c", ch, "-r", rate, "-d", n, file)
			cmd.Env = append(os.Environ(), env)
			var out bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &out
			res[i] = result{what, cmd.Run(), strings.TrimSpace(out.String())}
		}(i, r.what, r.tool, r.rate, r.ch)
		time.Sleep(300 * time.Millisecond) // each opens while the ones before are running
	}
	wg.Wait()
	ok := true
	for _, r := range res {
		if r.err != nil {
			ok = false
			fmt.Printf("  %s: failed: %v %s\n", r.what, r.err, r.out)
		} else {
			fmt.Printf("  %s: ok\n", r.what)
		}
	}
	if !ok {
		// Who has the card: a program that opened it directly (a modem without shared audio, a sound server) keeps
		// the sharing layer out. (One that uses the shared device shows here too, and is not the problem.)
		if h := audio.Holders("hw:CARD=" + card + ",DEV=0"); len(h) > 0 {
			fmt.Printf("The card is open by %s. If that program opened it directly, sharing cannot use it until it lets go.\n", strings.Join(h, ", "))
		}
		return fmt.Errorf("the card could not be shared")
	}
	fmt.Println("All three at once: the card can be shared.")
	return nil
}
