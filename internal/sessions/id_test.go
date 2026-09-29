package sessions

import (
	"strings"
	"testing"
	"time"
)

// Session ids used to be time.Now().UnixNano() alone. That is unique only if
// the clock's resolution is finer than the gap between two calls, and a macOS
// CI runner hands out microsecond-resolution wall time - so several sessions
// created in a tight loop shared an id, and because a session is stored as
// <id>.json, the second save overwrote the first. A user's session history
// disappeared with no error anywhere.

// Deterministic: the same instant must still yield different ids, which is
// exactly the case a coarse clock produces.
func TestIDsAreUniqueForTheSameInstant(t *testing.T) {
	fixed := time.Unix(0, 1_700_000_000_000_000_000)
	seen := map[string]bool{}
	for i := 0; i < 500; i++ {
		id := newID(fixed)
		if seen[id] {
			t.Fatalf("id %q was issued twice for the same instant", id)
		}
		seen[id] = true
	}
}

// And the realistic case: sessions made in a tight loop must not collide.
func TestSessionsCreatedInALoopAllSurvive(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	UseJSON()
	defer UseJSON()

	const n = 40
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		s := New("ollama", "m")
		ids = append(ids, s.ID)
		if err := s.Save(); err != nil {
			t.Fatal(err)
		}
	}
	list, err := List()
	if err != nil {
		t.Fatal(err)
	}
	// This is the assertion that failed on CI: sessions were written and one
	// was missing, because two had the same id.
	if len(list) != n {
		t.Fatalf("%d sessions written, %d listed; ids collided", n, len(list))
	}
	// Every id must round-trip to its own session.
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("duplicate id issued: %q", id)
		}
		seen[id] = true
		if _, err := Load(id); err != nil {
			t.Fatalf("Load(%q): %v", id, err)
		}
	}
}

// The id becomes a filename, so it must not be able to escape the sessions
// directory or confuse the JSON backend.
func TestIDsAreSafeAsFilenames(t *testing.T) {
	for i := 0; i < 200; i++ {
		id := newID(time.Now())
		if id == "" {
			t.Fatal("empty id")
		}
		if strings.ContainsAny(id, `/\:*?"<>|`) {
			t.Fatalf("id %q is not filename-safe", id)
		}
		if id == "." || id == ".." || strings.TrimSpace(id) != id {
			t.Fatalf("id %q is not usable as a filename", id)
		}
		// Short enough to stay readable in a list and in a path.
		if len(id) > 64 {
			t.Fatalf("id %q is %d characters", id, len(id))
		}
	}
}

// A fork gets its own id; sharing the parent's would overwrite it.
func TestForkGetsADistinctID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	UseJSON()
	defer UseJSON()

	s := New("ollama", "m")
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	fork, err := Fork(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fork.ID == s.ID {
		t.Fatal("the fork reuses the parent's id, so saving it would overwrite the original")
	}
	if err := fork.Save(); err != nil {
		t.Fatal(err)
	}
	// Both must still exist.
	list, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("after forking there are %d sessions, want 2", len(list))
	}
}
