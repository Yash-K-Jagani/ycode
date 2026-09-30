package router

import (
	"testing"

	"github.com/Yash-K-Jagani/ycode/pkg/apitypes"
)

// --- folding chunks in ---

func TestUsageSumsReportedChunks(t *testing.T) {
	var u Usage
	u.Add("gemini", "m", Usage{PromptTok: 100, ComplTok: 20, Reported: true})
	u.Add("gemini", "m", Usage{PromptTok: 300, ComplTok: 40, Reported: true})
	if !u.Reported {
		t.Fatal("a turn with reported chunks is not marked reported")
	}
	if u.PromptTok != 400 || u.ComplTok != 60 {
		t.Fatalf("tokens = %d + %d, want 400 + 60", u.PromptTok, u.ComplTok)
	}
	if u.Total() != 460 {
		t.Fatalf("Total = %d", u.Total())
	}
	if u.Calls != 2 {
		t.Fatalf("Calls = %d, want 2: a tool loop makes several round trips", u.Calls)
	}
}

// A provider that reports nothing must not look like a free turn. Zero presented
// as a measurement is the specific lie this type exists to prevent.
func TestUnreportedUsageIsNotZeroCost(t *testing.T) {
	var u Usage
	u.Add("ollama", "m", Usage{})
	if u.Reported {
		t.Fatal("an empty chunk was treated as reported usage")
	}
	if u.PromptTok != 0 || u.ComplTok != 0 {
		t.Fatalf("tokens = %d/%d", u.PromptTok, u.ComplTok)
	}
	// A call still happened, which is what a caller uses to decide whether it
	// needs an estimate at all.
	if u.Calls != 1 {
		t.Fatalf("Calls = %d", u.Calls)
	}
}

// Mixing a guess with a measurement produces a number that means nothing, so a
// reported chunk discards anything counted before it rather than adding to it.
func TestFirstReportedChunkResetsRatherThanAdds(t *testing.T) {
	var u Usage
	u.Add("ollama", "m", Usage{})
	u.Add("ollama", "m", Usage{})
	u.Add("gemini", "m2", Usage{PromptTok: 50, ComplTok: 5, Reported: true})
	if !u.Reported {
		t.Fatal("not marked reported after a reported chunk")
	}
	if u.PromptTok != 50 || u.ComplTok != 5 {
		t.Fatalf("pre-report counts leaked into a measured total: %d/%d", u.PromptTok, u.ComplTok)
	}
	// The provider follows the measurement, not the earlier guess.
	if u.Provider != "gemini" || u.Model != "m2" {
		t.Fatalf("attributed to %s/%s, want gemini/m2", u.Provider, u.Model)
	}
}

// --- chunkUsage ---

// This is the boundary where the distinction is actually made, so it is worth
// testing against the real chunk type rather than a hand-built Usage.
func TestChunkUsageReadsTheProvidersCounts(t *testing.T) {
	if got := chunkUsage(apitypes.StreamChunk{PromptTok: 7, ComplTok: 3}); !got.Reported ||
		got.PromptTok != 7 || got.ComplTok != 3 {
		t.Fatalf("reported chunk not read: %+v", got)
	}
	if got := chunkUsage(apitypes.StreamChunk{}); got.Reported {
		t.Fatalf("an empty chunk was reported as usage: %+v", got)
	}
	// A completion-only report is still a report. Some providers omit the
	// prompt count on cached requests.
	if got := chunkUsage(apitypes.StreamChunk{ComplTok: 12}); !got.Reported {
		t.Fatal("a completion-only chunk was treated as unreported")
	}
}

// --- the router ledger ---

func TestTakeUsageResets(t *testing.T) {
	r := &Router{}
	r.record("gemini", "m", Usage{PromptTok: 10, ComplTok: 2, Reported: true})
	if got := r.TakeUsage(); got.PromptTok != 10 {
		t.Fatalf("TakeUsage = %+v", got)
	}
	// The Router outlives any single turn. Without the reset, turn two's cost
	// would include turn one's and roughly double it.
	if got := r.TakeUsage(); got.PromptTok != 0 || got.Reported {
		t.Fatalf("second TakeUsage returned %+v; the ledger was not reset", got)
	}
}

func TestBeginClearsAPendingTurn(t *testing.T) {
	r := &Router{}
	r.record("gemini", "m", Usage{PromptTok: 99, ComplTok: 1, Reported: true})
	r.Begin()
	if got := r.TakeUsage(); got.PromptTok != 0 {
		t.Fatalf("Begin did not clear the ledger: %+v", got)
	}
}

func TestTakeUsageOnAFreshRouterIsEmpty(t *testing.T) {
	r := &Router{}
	got := r.TakeUsage()
	if got.Reported || got.Total() != 0 || got.Provider != "" {
		t.Fatalf("a fresh router reported %+v", got)
	}
}

// Two turns in a row on one router must not contaminate each other.
func TestConsecutiveTurnsDoNotContaminate(t *testing.T) {
	r := &Router{}
	r.Begin()
	r.record("gemini", "m", Usage{PromptTok: 100, ComplTok: 20, Reported: true})
	first := r.TakeUsage()

	r.Begin()
	r.record("gemini", "m", Usage{PromptTok: 30, ComplTok: 5, Reported: true})
	second := r.TakeUsage()

	if first.Total() != 120 || second.Total() != 35 {
		t.Fatalf("first %d, second %d: the second turn absorbed the first", first.Total(), second.Total())
	}
}
