package ctx

import (
	"strings"
	"testing"

	"github.com/Yash-K-Jagani/ycode/pkg/apitypes"
)

// BudgetFor used to return a hardcoded 6000 regardless of the model, which
// overflowed small models and needlessly discarded history on large ones.
func TestBudgetForVariesByModel(t *testing.T) {
	cases := []struct {
		model string
		min   int
		max   int
	}{
		// Locally served models keep a conservative allowance: Ollama's real
		// context is the user's num_ctx, not the model card.
		{"qwen2.5-coder:3b", 2_000, 6_000},
		{"qwen2.5-coder:7b-instruct-q4_0", 2_000, 6_000},
		{"deepseek-coder:6.7b-instruct-q4_0", 2_000, 6_000},
		{"gpt-4o", 50_000, 80_000},
		{"gemini-2.5-pro", 100_000, 100_000}, // clamped at the ceiling
		{"totally-unknown-model", 2_000, 8_000},
		{"", 2_000, 8_000},
	}
	for _, c := range cases {
		got := BudgetFor(c.model)
		if got < c.min || got > c.max {
			t.Fatalf("BudgetFor(%q) = %d, want %d..%d", c.model, got, c.min, c.max)
		}
	}
	// A big cloud model must be allowed far more history than a small one.
	small := BudgetFor("llama2")
	big := BudgetFor("gpt-4o")
	if big <= small*4 {
		t.Fatalf("BudgetFor barely scales: small=%d big=%d", small, big)
	}
}

func TestIsLocalModel(t *testing.T) {
	for _, m := range []string{"qwen2.5-coder:3b", "llama3.2:1b", "gemma:7b", "llama3.2:1b-instruct", "deepseek-coder:6.7b-instruct-q4_0"} {
		if !isLocalModel(m) {
			t.Fatalf("%q is an ollama tag", m)
		}
	}
	for _, m := range []string{"gpt-4o", "gemini-2.5-pro", "no-colon", "trailing:", ":leading"} {
		if isLocalModel(m) {
			t.Fatalf("%q should not be treated as a local tag", m)
		}
	}
	// A URL that slipped in as a model name must not be parsed as a tag.
	if isLocalModel("http://localhost:11434/x") {
		t.Fatal("a URL is not a local model tag")
	}
}

func TestContextWindow(t *testing.T) {
	if got := ContextWindow("gpt-4o"); got != 128_000 {
		t.Fatalf("gpt-4o window = %d", got)
	}
	if got := ContextWindow("qwen2.5-coder:3b"); got != 32_768 {
		t.Fatalf("qwen2.5-coder window = %d", got)
	}
	// An explicit size in the tag wins over the family table.
	if got := ContextWindow("some-model:4k"); got != 4096 {
		t.Fatalf("explicit 4k = %d", got)
	}
	if got := ContextWindow("model-ctx=16384"); got != 16_384 {
		t.Fatalf("ctx= override = %d", got)
	}
	if got := ContextWindow(""); got != defaultWindow {
		t.Fatalf("empty model = %d, want the conservative default", got)
	}
	// A name that merely contains letters after a digit must not be read as a
	// window: "qwen2.5-coder" is a family, not "2.5k".
	if got := ContextWindow("qwen2.5-coder:3b"); got < 8192 {
		t.Fatalf("qwen2.5 was misread as a tiny window: %d", got)
	}
}

func TestBudgetForHonoursOverride(t *testing.T) {
	t.Setenv("YCODE_CONTEXT_BUDGET", "12345")
	if got := BudgetFor("gpt-4o"); got != 12345 {
		t.Fatalf("override ignored: %d", got)
	}
	t.Setenv("YCODE_CONTEXT_BUDGET", "nonsense")
	if got := BudgetFor("llama2:7b"); got == 12345 {
		t.Fatal("a bad override should be ignored")
	}
}

func TestEstimateAndTrim(t *testing.T) {
	m := []apitypes.Message{
		{Role: apitypes.RoleSystem, Content: "SYSTEM"},
		{Role: apitypes.RoleUser, Content: strings.Repeat("a", 400)},
		{Role: apitypes.RoleAssistant, Content: strings.Repeat("b", 400)},
		{Role: apitypes.RoleUser, Content: strings.Repeat("c", 400)},
	}
	if got := Estimate(m); got < 300 {
		t.Fatalf("Estimate = %d, too small", got)
	}
	// Under budget: untouched.
	out, dropped := Trim(m, 100_000)
	if dropped != 0 || len(out) != len(m) {
		t.Fatalf("Trim dropped %d of %d under budget", dropped, len(m))
	}
	// Over budget: keeps the system message and the newest, drops the middle.
	out, dropped = Trim(m, 250)
	if dropped == 0 {
		t.Fatal("Trim should have dropped something")
	}
	if out[0].Content != "SYSTEM" {
		t.Fatal("Trim must keep the system prompt")
	}
	if out[len(out)-1].Content != m[len(m)-1].Content {
		t.Fatal("Trim must keep the newest message")
	}
	if Estimate(out) > 250+Estimate(out[:1])+200 {
		t.Fatalf("Trim left %d tokens for a 250 budget", Estimate(out))
	}
	// A marker tells the model history was compacted.
	found := false
	for _, msg := range out {
		if strings.Contains(msg.Content, "compacted") {
			found = true
		}
	}
	if !found {
		t.Fatal("Trim should note that it compacted history")
	}
	// Non-positive budget means "do not trim".
	if _, dropped := Trim(m, 0); dropped != 0 {
		t.Fatal("a zero budget should not trim")
	}
	// A single message is never dropped, or there would be no context left.
	one := []apitypes.Message{{Role: apitypes.RoleSystem, Content: "only"}}
	if _, dropped := Trim(one, 1); dropped != 0 {
		t.Fatal("Trim must not drop the only message")
	}
}
