package textutil

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateLeavesShortTextAlone(t *testing.T) {
	for _, s := range []string{"", "a", "hello", strings.Repeat("x", 100)} {
		if got := Truncate(s, 100); got != s {
			t.Fatalf("Truncate(%q, 100) = %q", s, got)
		}
	}
	if got := Truncate("hello", 5); got != "hello" {
		t.Fatalf("a string of exactly n was cut: %q", got)
	}
}

// The bug this exists for: slicing by bytes halves a multi-byte character and
// produces invalid UTF-8.
func TestTruncateNeverEmitsInvalidUTF8(t *testing.T) {
	inputs := []string{
		"aaa→bbb",                     // an arrow
		"✓ done ✗ failed",             // ticks and crosses
		"日本語のテキスト",                    // multi-byte throughout
		"café — naïve",                // accents and an em dash
		strings.Repeat("→", 50),       // every character multi-byte
		"mixed ascii and → and ✓",     //
		strings.Repeat("x", 10) + "→", // boundary lands mid-character
	}
	for _, in := range inputs {
		for n := 0; n <= len([]rune(in))+2; n++ {
			got := Truncate(in, n)
			if !utf8.ValidString(got) {
				t.Fatalf("Truncate(%q, %d) = %q, which is not valid UTF-8", in, n, got)
			}
		}
	}
}

func TestTruncateCountsCharacters(t *testing.T) {
	// Three characters, each three bytes.
	s := "→→→"
	got := Truncate(s, 2)
	if !strings.HasPrefix(got, "→→") {
		t.Fatalf("Truncate(%q, 2) = %q", s, got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("a truncated string should be marked: %q", got)
	}
	// The visible text is never longer than n.
	for _, n := range []int{1, 2, 3, 4} {
		if r := []rune(Truncate("日本語のテキストです", n)); len(r) > n+1 {
			t.Fatalf("Truncate(n=%d) produced %d characters", n, len(r))
		}
	}
}

func TestTruncateEdgeCases(t *testing.T) {
	// A non-positive budget cannot mean "everything"; the ellipsis alone is
	// the only honest answer.
	if got := Truncate("hello", 0); got != "…" {
		t.Fatalf("Truncate(_, 0) = %q", got)
	}
	if got := Truncate("hello", -5); got != "…" {
		t.Fatalf("Truncate(_, -5) = %q", got)
	}
	// Cutting a string that is entirely multi-byte must still land on a
	// boundary.
	if got := Truncate("→→→", 1); !utf8.ValidString(got) {
		t.Fatalf("Truncate gave %q", got)
	}
}

func TestTruncateBytes(t *testing.T) {
	if got := TruncateBytes("hello", 100); got != "hello" {
		t.Fatalf("short text was altered: %q", got)
	}
	if got := TruncateBytes("hello", 0); got != "…" {
		t.Fatalf("TruncateBytes(_, 0) = %q", got)
	}
	if got := TruncateBytes("hello", -1); got != "…" {
		t.Fatalf("TruncateBytes(_, -1) = %q", got)
	}
	// The budget is bytes, but the cut still lands on a character boundary,
	// so a multi-byte character is dropped rather than halved.
	for max := 1; max <= 20; max++ {
		got := TruncateBytes("aaa→bbb", max)
		if !utf8.ValidString(got) {
			t.Fatalf("TruncateBytes(%d) = %q, not valid UTF-8", max, got)
		}
		if len(got) > max+len("…") {
			t.Fatalf("TruncateBytes(%d) returned %d bytes", max, len(got))
		}
	}
	// A budget landing mid-character keeps the whole prefix it can.
	if got := TruncateBytes("aaa→bbb", 4); strings.Contains(got, "→") {
		t.Fatalf("a 4-byte budget should not fit the 3-byte arrow plus 3 a's: %q", got)
	}
}
