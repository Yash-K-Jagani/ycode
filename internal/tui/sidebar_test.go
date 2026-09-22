package tui

import (
	"strings"
	"testing"
)

func TestCtxBar(t *testing.T) {
	if got := ctxBar(0, 0); got != "—" {
		t.Fatalf("zero budget: %q", got)
	}
	got := ctxBar(3700, 6000)
	if !strings.Contains(got, "61%") || !strings.Contains(got, "3.7k/6.0k") {
		t.Fatalf("bad bar: %q", got)
	}
	if !strings.HasPrefix(got, "▓▓▓▓▓▓░░░░") {
		t.Fatalf("bad cells: %q", got)
	}
	if got := ctxBar(99999, 6000); !strings.Contains(got, "100%") {
		t.Fatalf("no clamp: %q", got)
	}
	if shortTokens(999) != "999" || shortTokens(1500) != "1.5k" {
		t.Fatal("shortTokens")
	}
}
