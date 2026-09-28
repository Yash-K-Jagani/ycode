package agents

import (
	"strings"
	"testing"
)

// The agent prompt is prepended to every turn, so a malformed one is not a
// cosmetic problem: it changes how the model behaves on every request.

func TestEveryAgentIsWellFormed(t *testing.T) {
	all := All()
	if len(all) == 0 {
		t.Fatal("no agents are registered")
	}
	seen := map[string]bool{}
	for _, a := range all {
		if a.Name == "" {
			t.Fatal("an agent has no name")
		}
		if seen[a.Name] {
			t.Fatalf("agent %q is registered twice", a.Name)
		}
		seen[a.Name] = true
		// /agent lists these, and BuildSystem puts the prompt in front of the
		// system prompt; an empty one would render as a blank row.
		if a.Description == "" {
			t.Fatalf("%s has no description", a.Name)
		}
		if a.Prompt == "" {
			t.Fatalf("%s has no prompt", a.Name)
		}
		if a.Name != strings.ToLower(a.Name) {
			t.Fatalf("agent name %q should be lowercase so lookup is predictable", a.Name)
		}
	}
}

func TestGet(t *testing.T) {
	for _, a := range All() {
		got, ok := Get(a.Name)
		if !ok {
			t.Fatalf("Get(%q) failed", a.Name)
		}
		if got != a {
			t.Fatalf("Get(%q) returned %+v, want %+v", a.Name, got, a)
		}
	}
	// /agent accepts whatever the user types, so lookup cannot be exact-match.
	for _, in := range []string{"BUILDER", "Builder", "  planner  "} {
		if _, ok := Get(in); !ok {
			t.Fatalf("Get(%q) should be tolerant of case and padding", in)
		}
	}
	if got, ok := Get("nope"); ok || got != (Agent{}) {
		t.Fatalf("Get(\"nope\") = %+v, %v", got, ok)
	}
}

// A prompt longer than a small model's whole context window would crowd out the
// task, so the length is worth a ceiling rather than a comment.
func TestPromptsStayShort(t *testing.T) {
	for _, a := range All() {
		if len(a.Prompt) > 600 {
			t.Fatalf("%s has a %d-character prompt; it will crowd out the task", a.Name, len(a.Prompt))
		}
	}
}
