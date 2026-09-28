// Package textutil holds small string helpers shared across ycode.
package textutil

import "strings"

// Truncate shortens s to at most n characters, counting characters rather than
// bytes, and appends an ellipsis when it had to cut.
//
// The distinction is not cosmetic. These strings are file paths, test output,
// diffs and model answers, all of which routinely contain non-ASCII: an arrow
// in a compiler message, a box-drawing character in test output, a
// non-ASCII filename on Windows, an em dash in a sentence. Slicing by bytes
// cuts such a character in half and yields invalid UTF-8, which a terminal
// renders as a replacement glyph and which a model is handed as mojibake. The
// repo brief that goes into every prompt went through exactly this path.
//
// n is measured in characters, so the result is never longer than n plus the
// ellipsis. A non-positive n returns just the ellipsis, and n below the length
// returns s untouched.
func Truncate(s string, n int) string {
	if n <= 0 {
		return "…"
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// TruncateBytes is Truncate with a byte budget instead of a character one,
// for the few places that genuinely care about payload size rather than how
// the text reads. It still cuts on a character boundary.
func TruncateBytes(s string, max int) string {
	if max <= 0 {
		return "…"
	}
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8Start(s[cut]) {
		cut--
	}
	return strings.ToValidUTF8(s[:cut], "") + "…"
}

// utf8Start reports whether b begins a UTF-8 sequence, i.e. whether the byte
// before it ended a character.
func utf8Start(b byte) bool { return b&0xC0 != 0x80 }
