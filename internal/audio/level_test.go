package audio

import (
	"bytes"
	"encoding/binary"
	"math"
	"strings"
	"testing"
)

func pcm(samples ...int16) []byte {
	var b bytes.Buffer
	for _, s := range samples {
		_ = binary.Write(&b, binary.LittleEndian, s) // left
		_ = binary.Write(&b, binary.LittleEndian, int16(0))
	}
	return b.Bytes()
}

func TestLevelOfReceiveAudio(t *testing.T) {
	// a sine at a quarter of full scale: rms -15 dBFS, peak -12 dBFS, nothing clipped
	var s []int16
	for i := 0; i < 12000; i++ {
		s = append(s, int16(8192*math.Sin(2*math.Pi*float64(i)*1500/12000)))
	}
	l := levelOf(bytes.NewReader(pcm(s...)), 2)
	if math.Abs(l.RMS+15) > 0.5 || math.Abs(l.Peak+12) > 0.5 || l.Clipped != 0 {
		t.Fatalf("%+v", l)
	}
	if !strings.Contains(l.Advice(-25), "good") {
		t.Errorf("a level near the target is good: %q", l.Advice(-25))
	}
	// AGC off on the Pi: noise near full scale, clipping
	loud := levelOf(bytes.NewReader(pcm(32767, -32768, 30000, -31000)), 2)
	if loud.Clipped == 0 || !strings.Contains(loud.Advice(-25), "AGC") {
		t.Errorf("clipping must say what to check: %+v %q", loud, loud.Advice(-25))
	}
	quiet := levelOf(bytes.NewReader(pcm(10, -12, 8, -9)), 2)
	if !strings.Contains(quiet.Advice(-25), "low") {
		t.Errorf("a very low level must say so: %+v %q", quiet, quiet.Advice(-25))
	}
	if l := levelOf(bytes.NewReader(nil), 2); l.Samples != 0 || !strings.Contains(l.Advice(-25), "no audio") {
		t.Errorf("nothing recorded: %+v", l)
	}
}
