package safetext

import "testing"

func TestTextStripsWhatATerminalWouldAct(t *testing.T) {
	for in, want := range map[string]string{
		"a\x1b[2Jb\r\ncd\x00\x07e\tf\r":  "a[2Jb\ncde\tf\n", // escape, NUL and bell go; CR LF is one newline
		"one\rtwo\nthree\r\n\r\nfour":    "one\ntwo\nthree\n\nfour",
		"\x1b]0;owned\x07title":          "]0;ownedtitle",
		"8-bit CSI \x9b31m and \xc2\x9b": "8-bit CSI 31m and ", // the C1 escape, raw and as UTF-8
		"gr\xfc\xdfe":                    "grüße",              // Latin-1 from older software
		"Ä Ö Ü À – grüße 73":             "Ä Ö Ü À – grüße 73", // UTF-8 whose bytes fall in 0x80-0x9f stays whole
		"abc\u202edef\u200bghi\ufeff":    "abcdefghi",          // right-to-left override, zero-width space, BOM
		"line\u2028sep\u2029":            "linesep",            // Unicode line separators are not newlines we honour
		"\xff\xfe\x80":                   "ÿþ",
		"":                               "",
	} {
		if got := Text(in); got != want {
			t.Errorf("Text(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLineStaysOnOneLine(t *testing.T) {
	for in, want := range map[string]string{
		"u-blox 7\n\x1b[31mGPS\tGNSS\r": "u-blox 7[31mGPS GNSS",
		"/dev/ttyACM0":                  "/dev/ttyACM0",
		"3.25\x1b]2;x\x07":              "3.25]2;x",
	} {
		if got := Line(in); got != want {
			t.Errorf("Line(%q) = %q, want %q", in, got, want)
		}
	}
}
