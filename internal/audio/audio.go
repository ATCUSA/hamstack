// Package audio finds ALSA sound cards.
package audio

import (
	"bufio"
	"os"
	"regexp"
	"strings"
)

var cardRE = regexp.MustCompile(`^\s*(\d+)\s+\[([^\]]+)\]:`)

// Cards returns the ALSA card ids (the bracketed names in /proc/asound/cards).
func Cards() []string { return cardsFrom("/proc/asound/cards") }

func cardsFrom(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var ids []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if m := cardRE.FindStringSubmatch(sc.Text()); m != nil {
			ids = append(ids, strings.TrimSpace(m[2]))
		}
	}
	return ids
}

// Resolve returns the ALSA device string for the first hint that matches an
// installed card, and whether one matched. If none matches it returns the
// device string for the first hint so a config can still be written.
func Resolve(hints []string) (device string, found bool) {
	return resolve(Cards(), hints)
}

func resolve(cards, hints []string) (string, bool) {
	for _, h := range hints {
		for _, c := range cards {
			if strings.EqualFold(c, h) {
				return "plughw:CARD=" + c + ",DEV=0", true
			}
		}
	}
	if len(hints) > 0 {
		return "plughw:CARD=" + hints[0] + ",DEV=0", false
	}
	return "", false
}
