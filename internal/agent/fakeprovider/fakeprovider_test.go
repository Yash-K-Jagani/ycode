package fakeprovider_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Yash-K-Jagani/ycode/internal/agent/fakeprovider"
	"github.com/Yash-K-Jagani/ycode/pkg/apitypes"
)

func TestRepliesInOrderThenRepeats(t *testing.T) {
	p := fakeprovider.New("one", "two")
	msgs := []apitypes.Message{{Role: apitypes.RoleSystem, Content: "s"}}
	for i, want := range []string{"one", "two", "two"} {
		var sb strings.Builder
		chunk, err := p.Stream(context.Background(), "m", msgs, &sb)
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		if chunk.Delta != want {
			t.Fatalf("request %d: delta = %q, want %q", i, chunk.Delta, want)
		}
		if sb.String() != want {
			t.Fatalf("request %d: nothing streamed to the writer (got %q)", i, sb.String())
		}
	}
	if p.Requests() != 3 {
		t.Fatalf("Requests() = %d, want 3", p.Requests())
	}
}

func TestRecordsRequestsForAssertion(t *testing.T) {
	p := fakeprovider.New("ok")
	req := []apitypes.Message{
		{Role: apitypes.RoleSystem, Content: "SYSTEM PROMPT"},
		{Role: apitypes.RoleUser, Content: "do the thing"},
	}
	if _, err := p.Complete(context.Background(), "m", req); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.LastSystem(), "SYSTEM PROMPT") {
		t.Fatalf("LastSystem = %q", p.LastSystem())
	}
	if p.LastRole() != apitypes.RoleUser {
		t.Fatalf("LastRole = %q, want user", p.LastRole())
	}
	// Mutating the caller's slice must not corrupt the record.
	req[0].Content = "MUTATED"
	if strings.Contains(p.LastSystem(), "MUTATED") {
		t.Fatal("the provider aliased the caller's message slice")
	}
}

func TestFailAt(t *testing.T) {
	p := fakeprovider.New("a", "b").FailAt(1)
	if _, err := p.Complete(context.Background(), "m", nil); err == nil {
		t.Fatal("the first request should fail")
	}
	got, err := p.Complete(context.Background(), "m", nil)
	if err != nil {
		t.Fatalf("the second request should succeed: %v", err)
	}
	if got != "b" {
		t.Fatalf("second reply = %q, want b", got)
	}
}

func TestHasToolCalls(t *testing.T) {
	if !fakeprovider.HasToolCalls(`<tool:read>{"path":"a.go"}</tool:read>`) {
		t.Fatal("should detect a tagged call")
	}
	if fakeprovider.HasToolCalls("I will read the file next turn.") {
		t.Fatal("prose is not a tool call")
	}
}
