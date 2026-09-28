package agent

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/Yash-K-Jagani/ycode/internal/agent/fakeprovider"
	"github.com/Yash-K-Jagani/ycode/internal/tools"
	"github.com/Yash-K-Jagani/ycode/pkg/apitypes"
)

func chainMsgs() []apitypes.Message {
	return []apitypes.Message{{Role: apitypes.RoleUser, Content: "hello"}}
}

func run(t *testing.T, cands []Candidate, onRetry func(string)) ChainResult {
	t.Helper()
	return RunChain(context.Background(), cands, chainMsgs(), tools.NewRegistry(),
		nil, nil, io.Discard, nil, 8, "build", onRetry)
}

// The primary serves the turn, so there is no fallback and no retry.
func TestChainUsesThePrimary(t *testing.T) {
	var retries []string
	res := run(t, []Candidate{{Provider: fakeprovider.New("from primary")}}, func(l string) { retries = append(retries, l) })
	if res.Text != "from primary" {
		t.Fatalf("Text = %q", res.Text)
	}
	if res.FellBack() {
		t.Fatal("the primary must not count as a fallback")
	}
	if len(retries) != 0 {
		t.Fatalf("the primary must not trigger a retry: %v", retries)
	}
	if res.Err != nil {
		t.Fatalf("Err = %v", res.Err)
	}
}

// A failing primary falls through to the next candidate, and the caller is
// told which one served the turn.
func TestChainFallsBack(t *testing.T) {
	var retries []string
	res := run(t, []Candidate{
		{Provider: fakeprovider.New("primary").FailAt(1)},
		{Provider: fakeprovider.New("from fallback"), Model: "m", Label: "groq/llama"},
	}, func(l string) { retries = append(retries, l) })
	if res.Text != "from fallback" {
		t.Fatalf("Text = %q, want the fallback's answer", res.Text)
	}
	if !res.FellBack() || res.Used != "groq/llama" {
		t.Fatalf("FellBack=%v Used=%q", res.FellBack(), res.Used)
	}
	if len(retries) != 1 || retries[0] != "groq/llama" {
		t.Fatalf("onRetry = %v, want one call naming the fallback", retries)
	}
	if res.Winner.Label != "groq/llama" {
		t.Fatalf("Winner = %+v; a follow-up retry must use the same provider", res.Winner)
	}
}

// Every candidate failing must report all of them, so the user learns which
// provider refused what — rather than "all providers failed".
func TestChainReportsEveryAttempt(t *testing.T) {
	res := run(t, []Candidate{
		{Provider: fakeprovider.New("x").FailAt(1)},
		{Provider: fakeprovider.New("y").FailAt(1), Model: "b", Label: "groq/b"},
	}, nil)
	if res.Text != "" {
		t.Fatalf("Text = %q, want empty", res.Text)
	}
	if len(res.Attempts) != 2 {
		t.Fatalf("Attempts = %v, want 2", res.Attempts)
	}
	err := res.Failure()
	if err == nil {
		t.Fatal("Failure() should be non-nil")
	}
	msg := err.Error()
	for _, want := range []string{"every provider failed", "primary:", "groq/b:", "fallbacks were tried"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("missing %q in:\n%s", want, msg)
		}
	}
}

func TestChainNoCandidates(t *testing.T) {
	res := run(t, nil, nil)
	if res.Text != "" {
		t.Fatalf("Text = %q", res.Text)
	}
	if !strings.Contains(res.Failure().Error(), "no provider was available") {
		t.Fatalf("Failure = %v", res.Failure())
	}
}

func TestChainStopsAtTheFirstSuccess(t *testing.T) {
	second := fakeprovider.New("second")
	res := run(t, []Candidate{
		{Provider: fakeprovider.New("first")},
		{Provider: second, Model: "m", Label: "groq/m"},
	}, nil)
	if res.Text != "first" {
		t.Fatalf("Text = %q", res.Text)
	}
	if second.Requests() != 0 {
		t.Fatalf("the second candidate was called %d times; a success must stop the chain", second.Requests())
	}
}

// A candidate that errors *after* producing text keeps that text, so partial
// work is not thrown away.
func TestChainKeepsPartialOutput(t *testing.T) {
	res := run(t, []Candidate{{Provider: fakeprovider.New("partial answer")}}, nil)
	if res.Text != "partial answer" {
		t.Fatalf("Text = %q", res.Text)
	}
}

// callCounting proves the chain passes the tool callback and counts calls
// across candidates.
type callCounting struct{ n int }

func (c *callCounting) Name() string     { return "count" }
func (callCounting) Description() string { return "counts" }
func (callCounting) Schema() string      { return `{"type":"object"}` }
func (c *callCounting) Run(context.Context, json.RawMessage) (string, error) {
	c.n++
	return "ok", nil
}

func TestChainAccumulatesToolCalls(t *testing.T) {
	c := &callCounting{}
	reg := tools.NewRegistry()
	reg.Add(c)
	res := RunChain(context.Background(),
		[]Candidate{{Provider: fakeprovider.New(`<tool:count>{}</tool:count>`, "done")}},
		chainMsgs(), reg, []string{"count"}, nil, io.Discard, nil, 8, "build", nil)
	if c.n != 1 {
		t.Fatalf("the tool ran %d times", c.n)
	}
	if res.Calls != 1 || res.OKs != 1 || res.Failed != 0 {
		t.Fatalf("Calls=%d OKs=%d Failed=%d", res.Calls, res.OKs, res.Failed)
	}
}
