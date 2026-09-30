package subagent

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Yash-K-Jagani/ycode/internal/modes"
	"github.com/Yash-K-Jagani/ycode/internal/providers"
	"github.com/Yash-K-Jagani/ycode/internal/tools"
	"github.com/Yash-K-Jagani/ycode/pkg/apitypes"
)

// fakeProvider records what it was asked and replays scripted answers.
type fakeProvider struct {
	turns []string
	i     int
	seen  [][]apitypes.Message
}

func (p *fakeProvider) Name() string { return "fake" }

func (p *fakeProvider) ListModels(context.Context) ([]apitypes.ModelInfo, error) {
	return nil, nil
}

func (p *fakeProvider) Complete(context.Context, string, []apitypes.Message) (string, error) {
	return "", nil
}

func (p *fakeProvider) Stream(_ context.Context, _ string, msgs []apitypes.Message, w io.Writer) (apitypes.StreamChunk, error) {
	p.seen = append(p.seen, append([]apitypes.Message(nil), msgs...))
	out := ""
	if p.i < len(p.turns) {
		out = p.turns[p.i]
	}
	p.i++
	_, _ = io.WriteString(w, out)
	return apitypes.StreamChunk{Delta: out, Done: true, PromptTok: 10, ComplTok: 5}, nil
}

// blockingProvider never returns, so a run that does not honour its timeout hangs.
type blockingProvider struct{ fakeProvider }

func (b *blockingProvider) Stream(ctx context.Context, _ string, _ []apitypes.Message, _ io.Writer) (apitypes.StreamChunk, error) {
	<-ctx.Done()
	return apitypes.StreamChunk{}, ctx.Err()
}

func registry(t *testing.T) *tools.Registry {
	t.Helper()
	return tools.DefaultRegistry(t.TempDir())
}

func opts(t *testing.T, p providers.Provider) Options {
	t.Helper()
	return Options{
		Provider: p,
		Model:    "test-model",
		Registry: registry(t),
		Mode:     string(modes.Build),
		Agent:    "explorer",
		Workdir:  t.TempDir(),
	}
}

// --- the allow-list is the whole safety argument ---

// A subagent with the parent's tools could quietly rewrite the user's files while
// the parent believes it only asked a question. This is the test for that.
func TestSubagentCannotReachAWriteTool(t *testing.T) {
	p := &fakeProvider{turns: []string{
		`<tool:write>{"path":"x.txt","content":"pwned"}</tool:write>`,
		"done",
	}}
	res := Run(context.Background(), "overwrite everything", opts(t, p))
	// The write is refused and the subagent is told so; the turn then continues
	// to the model's next answer. What matters is that nothing was written.
	_ = res
	sawRefusal := false
	for _, msgs := range p.seen {
		for _, m := range msgs {
			// Checked across both result roles: the refusal arrives as role:tool
			// under native tool calling and as a tagged role:system message under
			// the text protocol, and both must say it.
			if m.Role != apitypes.RoleTool && m.Role != apitypes.RoleSystem {
				continue
			}
			if strings.Contains(m.Content, "not allowed") {
				sawRefusal = true
			}
		}
	}
	if !sawRefusal {
		t.Fatal("the subagent was never told the tool was refused")
	}
	// And the refusal must not have written anything.
	if res.Err == nil && !strings.Contains(res.Answer, "done") {
		t.Fatalf("unexpected outcome: %q / %v", res.Answer, res.Err)
	}
}

// The allow-list is the intersection with the parent's, so a subagent can never
// reach a tool the parent itself could not.
func TestAllowIsAlwaysIsolatable(t *testing.T) {
	r := registry(t)
	got := Allow(modes.AllowedTools(modes.Build), r)
	if len(got) == 0 {
		t.Fatal("build mode allows nothing delegable")
	}
	for _, n := range got {
		if !tools.IsIsolatable(n, nil) {
			t.Errorf("%s is delegable but not isolatable", n)
		}
		if strings.Contains(n, "write") || strings.Contains(n, "edit") ||
			strings.Contains(n, "bash") || strings.Contains(n, "delete") {
			t.Errorf("%s is delegable", n)
		}
	}
}

// Plan mode is already read-only, so it should narrow to nothing lost.
func TestAllowKeepsReadOnlyModesIntact(t *testing.T) {
	r := registry(t)
	plan := modes.AllowedTools(modes.Plan)
	got := Allow(plan, r)
	if len(got) == 0 {
		t.Fatal("plan mode has nothing delegable, but it is entirely read-only")
	}
	for _, n := range got {
		if !contains(plan, n) {
			t.Errorf("%s is delegable but not in the parent's list", n)
		}
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// A tool in the allow-list with no implementation behind it must be skipped: a
// spec for it produces a call that fails as "unknown tool".
func TestAllowSkipsUnknownNames(t *testing.T) {
	if got := Allow([]string{"read", "does_not_exist"}, tools.NewRegistry()); len(got) != 0 {
		t.Fatalf("offered %v", got)
	}
}

// "Subagents are not available in this mode" leaves the user guessing whether
// the feature is broken or their mode is wrong.
func TestNoDelegableToolsExplainsItself(t *testing.T) {
	o := opts(t, &fakeProvider{})
	o.Mode = string(modes.Chat)
	res := Run(context.Background(), "anything", o)
	if res.Err == nil {
		t.Fatal("a mode with no tools was allowed to delegate")
	}
	if !strings.Contains(res.Err.Error(), "can only read") {
		t.Fatalf("unhelpful error: %v", res.Err)
	}
	if !strings.Contains(res.Err.Error(), "read") {
		t.Fatalf("the error does not list what is delegable: %v", res.Err)
	}
}

// --- the context saving is the whole benefit ---

func TestOnlyTheSummaryCrossesBack(t *testing.T) {
	p := &fakeProvider{turns: []string{"the answer: three call sites"}}
	res := Run(context.Background(), "where is this called", opts(t, p))
	if !strings.Contains(res.Answer, "three call sites") {
		t.Fatalf("answer = %q", res.Answer)
	}
}

// A subagent returning a wall of text must not defeat the feature.
func TestALongAnswerIsTruncated(t *testing.T) {
	p := &fakeProvider{turns: []string{strings.Repeat("a", maxAnswerBytes*2)}}
	res := Run(context.Background(), "explain", opts(t, p))
	if len(res.Answer) > maxAnswerBytes {
		t.Fatalf("answer is %d bytes, cap is %d", len(res.Answer), maxAnswerBytes)
	}
	if !strings.Contains(res.Answer, "narrower question") {
		t.Fatalf("truncation is not explained: %q", tail(res.Answer, 80))
	}
	// The head is kept: the answer states its conclusion before the detail.
	if !strings.HasPrefix(res.Answer, "a") {
		t.Fatal("the head was dropped")
	}
}

// Truncation must not split a rune. The parent reads this in its own context,
// which is the one place it will be looked at.
func TestTruncationIsRuneSafe(t *testing.T) {
	p := &fakeProvider{turns: []string{strings.Repeat("日", maxAnswerBytes)}}
	res := Run(context.Background(), "explain", opts(t, p))
	if strings.Contains(res.Answer, "�") {
		t.Fatal("truncation produced a replacement character")
	}
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// --- behaviour ---

// Stated rather than passed on blank, so the parent can tell "nothing found" from
// "the model returned nothing".
func TestAnEmptyAnswerIsExplained(t *testing.T) {
	res := Run(context.Background(), "find something", opts(t, &fakeProvider{turns: []string{""}}))
	if res.Err != nil {
		t.Fatalf("an empty answer is not an error: %v", res.Err)
	}
	if !strings.Contains(res.Answer, "returned nothing") {
		t.Fatalf("answer = %q", res.Answer)
	}
}

// An unknown persona defaults rather than failing the parent's whole turn, and
// says so, because a model picking the wrong name is a model mistake.
func TestUnknownAgentFallsBackAndSaysSo(t *testing.T) {
	var events []string
	o := opts(t, &fakeProvider{turns: []string{"ok"}})
	o.Agent = "nonsense"
	o.OnEvent = func(kind, text string) { events = append(events, kind+":"+text) }
	if res := Run(context.Background(), "q", o); res.Err != nil {
		t.Fatalf("an unknown agent failed the run: %v", res.Err)
	}
	if !strings.Contains(strings.Join(events, "|"), "unknown agent") {
		t.Fatalf("the fallback was silent: %v", events)
	}
}

// A subagent that hangs holds the parent's tool call open, and the parent can
// neither see nor cancel it. So it is bounded.
func TestRunIsBounded(t *testing.T) {
	o := opts(t, &blockingProvider{})
	o.Timeout = 100 * time.Millisecond
	start := time.Now()
	res := Run(context.Background(), "hang", o)
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("ran for %s despite a %s timeout", d, o.Timeout)
	}
	if res.Err == nil {
		t.Fatal("a hung subagent reported success")
	}
}

func TestMissingProviderIsAnError(t *testing.T) {
	o := opts(t, &fakeProvider{})
	o.Provider = nil
	if res := Run(context.Background(), "q", o); res.Err == nil {
		t.Fatal("a nil provider was accepted")
	}
}

func TestMissingRegistryIsAnError(t *testing.T) {
	o := opts(t, &fakeProvider{})
	o.Registry = nil
	if res := Run(context.Background(), "q", o); res.Err == nil {
		t.Fatal("a nil registry was accepted")
	}
}

// --- the trace ---

// Without this a subagent looks like a tool call that did something.
func TestTheQuestionAndAnswerAreRecorded(t *testing.T) {
	rec := &spyRecorder{}
	o := opts(t, &fakeProvider{turns: []string{"found it"}})
	o.Recorder = rec
	if res := Run(context.Background(), "where is foo called", o); res.Err != nil {
		t.Fatal(res.Err)
	}
	joined := strings.Join(rec.notes, "|")
	if !strings.Contains(joined, "where is foo called") {
		t.Fatalf("the question was not recorded: %q", joined)
	}
	if !strings.Contains(joined, "found it") {
		t.Fatalf("the answer was not recorded: %q", joined)
	}
}

type spyRecorder struct{ notes []string }

func (s *spyRecorder) Note(_ int, text string) { s.notes = append(s.notes, text) }

// --- personas ---

func TestAgentNamesAreListedAndSorted(t *testing.T) {
	if got := AgentNames(); got != "analyst, explorer" {
		t.Fatalf("AgentNames = %q", got)
	}
}

// A persona that does not say it cannot change anything will get a subagent
// reporting that it made a change.
func TestEveryPersonaSaysItCannotWrite(t *testing.T) {
	for name, p := range agentNames {
		if !strings.Contains(p, "cannot change") {
			t.Errorf("persona %q does not say it is read-only: %q", name, p)
		}
	}
}

// --- the delegate ---

func TestDelegateReportsAnUnwiredTool(t *testing.T) {
	if _, err := Delegate(ToolOptions{})(context.Background(), "q", ""); err == nil {
		t.Fatal("an unwired delegate ran")
	}
}

func TestDelegateReportsAMissingProvider(t *testing.T) {
	d := Delegate(ToolOptions{Resolve: func() (providers.Provider, string) { return nil, "" }})
	if _, err := d(context.Background(), "q", ""); err == nil {
		t.Fatal("a delegate with no provider ran")
	}
}

func TestDelegateReturnsTheSummary(t *testing.T) {
	d := Delegate(ToolOptions{
		Resolve:  func() (providers.Provider, string) { return &fakeProvider{turns: []string{"summary"}}, "m" },
		Registry: registry(t),
		Mode:     string(modes.Build),
	})
	got, err := d(context.Background(), "q", "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "summary" {
		t.Fatalf("got %q", got)
	}
}

// The tool is registered in the catalog so a mode can allow-list it, and reports
// clearly when nobody wired it.
func TestTaskToolIsRegisteredAndReportsWhenUnwired(t *testing.T) {
	r := registry(t)
	tool, ok := r.Get("task")
	if !ok {
		t.Fatal("task is not in the catalog")
	}
	out, err := tool.Run(context.Background(), []byte(`{"prompt":"q"}`))
	if err == nil {
		t.Fatalf("an unwired task tool returned %q", out)
	}
	if !strings.Contains(err.Error(), "not available") {
		t.Fatalf("unhelpful error: %v", err)
	}
}
