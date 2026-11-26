package audio

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"strconv"
)

// Level is how loud a radio's receive audio is, from the channel that carries it (the left one on the radios
// hamstack knows: the right is silent).
type Level struct {
	RMS, Peak float64 // dBFS
	Clipped   int     // samples at or near full scale
	Samples   int
}

// Advice says, in an operator's words, whether the level suits a data modem and what to change if not. target is
// the RMS to aim for on band noise (about -25 dBFS).
func (l Level) Advice(target float64) string {
	switch {
	case l.Samples == 0:
		return "no audio was recorded: is the radio's sound card the right one, and is nothing else holding it?"
	case l.Clipped > 0 || l.Peak > -1:
		return "too loud: the audio reaches full scale, so a modem cannot decode a station on top of it. Check that the radio's AGC is on, then lower its USB or DATA output level"
	case l.RMS < target-20:
		return "very low: weak stations may be lost in it. Raise the radio's USB or DATA output level (and check the radio is on the right antenna and not muted)"
	case l.RMS > target+10:
		return "loud: there is little room before full scale. Lower the radio's USB or DATA output level a little"
	}
	return "good for a data modem"
}

// levelOf reads 16-bit little-endian PCM with the given number of channels and measures the first channel.
func levelOf(r io.Reader, channels int) Level {
	br := bufio.NewReader(r)
	frame := make([]byte, 2*channels)
	var sum float64
	var peak, n, clipped int
	for {
		if _, err := io.ReadFull(br, frame); err != nil {
			break
		}
		v := int(int16(binary.LittleEndian.Uint16(frame)))
		if v < 0 {
			v = -v
		}
		sum += float64(v) * float64(v)
		if v > peak {
			peak = v
		}
		if v >= 32000 {
			clipped++
		}
		n++
	}
	if n == 0 {
		return Level{RMS: -120, Peak: -120}
	}
	db := func(x float64) float64 { return 20 * math.Log10(math.Max(x, 1)/32768) }
	return Level{RMS: db(math.Sqrt(sum / float64(n))), Peak: db(float64(peak)), Clipped: clipped, Samples: n}
}

// Measure records seconds of a radio's receive audio from an ALSA device (the shared one, so a modem keeps running)
// with arecord and measures it. env is an extra environment entry ("" for none), such as the shared card's.
func Measure(device, env string, seconds int) (Level, error) {
	if _, err := exec.LookPath("arecord"); err != nil {
		return Level{}, fmt.Errorf("measuring the level uses arecord, which is not installed (Debian: alsa-utils)")
	}
	cmd := exec.Command("arecord", "-q", "-D", device, "-f", "S16_LE", "-r", "12000", "-c", "2", "-d", strconv.Itoa(seconds), "-t", "raw")
	cmd.Env = os.Environ()
	if env != "" {
		cmd.Env = append(cmd.Env, env)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return Level{}, err
	}
	var errBuf limitedBuffer
	cmd.Stderr = &errBuf
	if err := cmd.Start(); err != nil {
		return Level{}, err
	}
	l := levelOf(out, 2)
	if err := cmd.Wait(); err != nil && l.Samples == 0 {
		return l, fmt.Errorf("could not record from %s: %s", device, errBuf.String())
	}
	return l, nil
}

type limitedBuffer struct{ b []byte }

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if len(l.b) < 2048 {
		l.b = append(l.b, p...)
	}
	return len(p), nil
}
func (l *limitedBuffer) String() string { return string(l.b) }
