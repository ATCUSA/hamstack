// Package safetext makes text that came from outside hamstack safe to print on a terminal: packets heard on
// the air, strings a gpsd on another machine reports, the names a USB device gives itself. Printed as they
// are, such strings could carry escape sequences that move the cursor, retitle the window or hide text.
package safetext

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Line returns s with everything removed that is not a printable character: control characters (escape,
// newlines and carriage returns included; a tab becomes a space), and Unicode's invisible formatting
// characters, which can reorder or hide what is shown. It is for values shown within one line.
func Line(s string) string {
	return clean(s, false)
}

// Text is Line for text of several lines: a carriage return, a line feed, or the pair, becomes one newline,
// and tabs are kept.
func Text(s string) string {
	return clean(s, true)
}

func clean(s string, multiline bool) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && n == 1 {
			// Not UTF-8. Older packet software sends Latin-1, so a lone high byte is shown as that character;
			// the C1 control range (0x80-0x9f, which holds an 8-bit escape) is dropped.
			if c := s[i]; c >= 0xa0 {
				b.WriteRune(rune(c))
			}
			i++
			continue
		}
		i += n
		switch {
		case r == '\r' || r == '\n':
			if multiline {
				if r == '\r' && i < len(s) && s[i] == '\n' {
					i++
				}
				b.WriteByte('\n')
			}
		case r == '\t':
			if multiline {
				b.WriteByte('\t')
			} else {
				b.WriteByte(' ')
			}
		case unicode.IsPrint(r):
			b.WriteRune(r)
		}
	}
	return b.String()
}
