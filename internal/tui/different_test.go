package tui

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/Yash-K-Jagani/ycode/internal/config"
	"github.com/Yash-K-Jagani/ycode/internal/providers"
	"github.com/Yash-K-Jagani/ycode/pkg/apitypes"
)

// stubProvider answers with a fixed string, or fails.
type stubProvider struct {
	answer  string
	err     error
	gotMsgs []apitypes.Message
}

func (s *stubProvider) Name() string { return "stub" }
func (s *stubProvider) ListModels(context.Context) ([]apitypes.ModelInfo, error) {
	return nil, nil
}
func (s *stubProvider) Complete(context.Context, string, []apitypes.Message) (string, error) {
	return "", nil
}
func (s *stubProvider) Stream(_ context.Context, _ string, msgs []apitypes.Message, w io.Writer) (apitypes.StreamChunk, error) {
	s.gotMsgs = msgs
	if s.err != nil {
		return apitypes.StreamChunk{}, s.err
	}
	_, _ = io.WriteString(w, s.answer)
	// Reported usage, so the cost column is a measurement rather than a guess.
	return apitypes.StreamChunk{Delta: s.answer, Done: true, PromptTok: 100, ComplTok: 20}, nil
}

type stubResolver struct {
	byID map[string]providers.Provider
	// failFor makes Resolve fail, as a provider with a dead key would.
	failFor map[string]bool
}

func (s stubResolver) Resolve(id string) (providers.Provider, string, error) {
	if s.failFor[id] {
		return nil, "", errors.New("no API key")
	}
	p, ok := s.byID[id]
	if !ok {
		return nil, "", errors.New("unknown provider " + id)
	}
	return p, "", nil
}

func resolverFor(answers map[string]string) stubResolver {
	byID := map[string]providers.Provider{}
	for id, a := range answers {
		byID[id] = &stubProvider{answer: a}
	}
	return stubResolver{byID: byID}
}

// --- choosing the models ---

// Offering a comparison that ends in "no API key" for three of five rows wastes
// the whole turn.
func TestUnconfiguredProvidersAreSkipped(t *testing.T) {
	cfg := config.Defaults()
	cfg.ActiveProvider = "ollama"
	cfg.ActiveModel = "qwen2.5-coder:3b"
	// No keys set, so every cloud provider is unusable.
	targets, skipped := candidates(cfg, nil)
	for _, tgt := range targets {
		spec := providers.Get(tgt.Provider)
		if spec != nil && spec.NeedsKey && cfg.KeyFor(spec.ID) == "" {
			t.Fatalf("%s was offered with no key", tgt.Provider)
		}
	}
	if len(skipped) == 0 {
		t.Fatal("nothing was reported as skipped, so the user is not told why")
	}
	for _, s := range skipped {
		if !strings.Contains(s, "no API key") {
			t.Errorf("skip reason is unhelpful: %q", s)
		}
	}
}

// The model the provider was last configured with is the better guess: someone
// who set gemini to a specific model did so for a reason.
func TestPickPrefersTheConfiguredModel(t *testing.T) {
	cfg := config.Defaults()
	cfg.ActiveProvider = "gemini"
	cfg.ActiveModel = "gemini-2.5-flash-lite"
	spec := providers.Get("gemini")
	if got := pick(cfg, "gemini", *spec); got != "gemini-2.5-flash-lite" {
		t.Fatalf("pick = %q, want the configured model", got)
	}
	// A different provider has no configured model, so its recommendation stands.
	got := pick(cfg, "groq", *providers.Get("groq"))
	if got != providers.Get("groq").Recommended {
		t.Fatalf("pick = %q, want the recommendation", got)
	}
}

func TestExplicitProviderAndModel(t *testing.T) {
	cfg := config.Defaults()
	cfg.ActiveProvider = "ollama"
	cfg.SetKeyFor("gemini", "k")
	cfg.SetKeyFor("groq", "k")
	got, skipped := candidates(cfg, []string{"gemini/gemini-2.5-pro", "groq"})
	if len(got) != 2 {
		t.Fatalf("got %+v (%v)", got, skipped)
	}
	if got[0].Model != "gemini-2.5-pro" {
		t.Fatalf("explicit model ignored: %+v", got[0])
	}
	// A bare provider means its recommendation, which is what someone naming one
	// provider expects.
	if got[1].Model != providers.Get("groq").Recommended {
		t.Fatalf("bare provider did not get its recommendation: %+v", got[1])
	}
}

func TestUnknownProviderIsSkippedWithAReason(t *testing.T) {
	cfg := config.Defaults()
	got, skipped := candidates(cfg, []string{"not-a-provider"})
	if len(got) != 0 {
		t.Fatalf("an unknown provider was accepted: %+v", got)
	}
	if len(skipped) != 1 || !strings.Contains(skipped[0], "unknown provider") {
		t.Fatalf("skip reason: %v", skipped)
	}
}

func TestDuplicateTargetsAreCollapsed(t *testing.T) {
	cfg := config.Defaults()
	cfg.SetKeyFor("gemini", "k")
	got, _ := candidates(cfg, []string{"gemini", "gemini"})
	if len(got) != 1 {
		t.Fatalf("the same model was queued twice: %+v", got)
	}
}

// --- running ---

func TestCompareReturnsOneRowPerModel(t *testing.T) {
	cfg := config.Defaults()
	// Keys set, or both providers are skipped as unusable and the comparison
	// correctly refuses to run.
	cfg.SetKeyFor("gemini", "k")
	cfg.SetKeyFor("groq", "k")
	r := resolverFor(map[string]string{"gemini": "answer from gemini", "groq": "answer from groq"})
	got, _, err := compare(context.Background(), cfg, r, "what does X do", []string{"gemini", "groq"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d rows", len(got))
	}
	seen := map[string]bool{}
	for _, c := range got {
		seen[c.Answer] = true
		if c.Err != nil {
			t.Fatalf("row %s failed: %v", c.Label, c.Err)
		}
	}
	if !seen["answer from gemini"] || !seen["answer from groq"] {
		t.Fatalf("answers = %v", seen)
	}
}

// A single rate-limited provider must not lose the other four answers, which is
// the most likely reason to run this at all.
func TestOneFailureDoesNotLoseTheOthers(t *testing.T) {
	cfg := config.Defaults()
	cfg.SetKeyFor("gemini", "k")
	cfg.SetKeyFor("ollama", "k")
	r := resolverFor(map[string]string{"gemini": "the good answer", "groq": "unused"})
	r.byID["ollama"] = &stubProvider{err: errors.New("rate limited")}
	got, _, err := compare(context.Background(), cfg, r, "q", []string{"gemini", "ollama"})
	if err != nil {
		t.Fatal(err)
	}
	var ok, failed int
	for _, c := range got {
		if c.Err != nil {
			failed++
			if !strings.Contains(c.Err.Error(), "rate limited") {
				t.Errorf("the reason was lost: %v", c.Err)
			}
			continue
		}
		ok++
		if c.Answer != "the good answer" {
			t.Errorf("wrong answer: %q", c.Answer)
		}
	}
	if ok != 1 || failed != 1 {
		t.Fatalf("ok=%d failed=%d", ok, failed)
	}
}

// A provider that dies halfway has still said something.
func TestPartialOutputSurvivesAFailure(t *testing.T) {
	p := &partialProvider{}
	c := ask(context.Background(), p, "m", "label", []apitypes.Message{{Role: apitypes.RoleUser, Content: "q"}})
	if c.Err == nil {
		t.Fatal("expected a failure")
	}
	if !strings.Contains(c.Answer, "partial") {
		t.Fatalf("partial output was discarded: %q", c.Answer)
	}
}

type partialProvider struct{ stubProvider }

func (p *partialProvider) Stream(_ context.Context, _ string, _ []apitypes.Message, w io.Writer) (apitypes.StreamChunk, error) {
	_, _ = io.WriteString(w, "partial answer")
	return apitypes.StreamChunk{}, errors.New("connection reset")
}

// Every row gets the identical prompt. A difference in the answers is a
// difference in the model, not in the setup.
func TestEveryModelGetsTheSamePrompt(t *testing.T) {
	cfg := config.Defaults()
	cfg.SetKeyFor("gemini", "k")
	cfg.SetKeyFor("groq", "k")
	ga, gr := &stubProvider{answer: "a"}, &stubProvider{answer: "b"}
	r := stubResolver{byID: map[string]providers.Provider{"gemini": ga, "groq": gr}}
	if _, _, err := compare(context.Background(), cfg, r, "the question", []string{"gemini", "groq"}); err != nil {
		t.Fatal(err)
	}
	if len(ga.gotMsgs) != len(gr.gotMsgs) {
		t.Fatalf("different message counts: %d vs %d", len(ga.gotMsgs), len(gr.gotMsgs))
	}
	for i := range ga.gotMsgs {
		if ga.gotMsgs[i].Content != gr.gotMsgs[i].Content {
			t.Fatalf("row %d differs:\n  %q\n  %q", i, ga.gotMsgs[i].Content, gr.gotMsgs[i].Content)
		}
		if ga.gotMsgs[i].Role != gr.gotMsgs[i].Role {
			t.Fatalf("row %d role differs", i)
		}
	}
}

// Nothing to compare against must say how to give something.
func TestNoTargetsExplainsItself(t *testing.T) {
	cfg := config.Defaults()
	_, _, err := compare(context.Background(), cfg, resolverFor(nil), "q", []string{"nope"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "/different") {
		t.Fatalf("the error does not show usage: %v", err)
	}
}

// --- rendering ---

// Comparing a measured answer against an estimated one and calling the difference
// a model difference is the exact mistake this feature invites.
func TestEstimatedUsageIsLabelledOnItsRow(t *testing.T) {
	rows := []comparison{{Label: "gemini/x", Answer: "hi", PromptT: 10, ComplT: 2, Measured: true, USD: 0.01}}
	out := render(rows, nil, false)
	if !strings.Contains(out, "$0.0100") {
		t.Fatalf("measured cost missing: %q", out)
	}
	rows = []comparison{{Label: "ollama/y", Answer: "hi", PromptT: 10, ComplT: 2}}
	out = render(rows, nil, false)
	if !strings.Contains(out, "estimated") {
		t.Fatalf("an estimate is not marked: %q", out)
	}
}

// An error row is shown, not dropped. Two answers that agree while a third failed
// is a different conclusion from three agreeing answers.
func TestAFailedRowIsShown(t *testing.T) {
	rows := []comparison{
		{Label: "ok/model", Answer: "fine"},
		{Label: "bad/model", Err: errors.New("rate limited")},
	}
	out := render(rows, nil, false)
	if !strings.Contains(out, "bad/model") || !strings.Contains(out, "rate limited") {
		t.Fatalf("the failed row is missing: %q", out)
	}
}

// A failed row buried at the bottom is read as though it succeeded.
func TestFailedRowsSortLast(t *testing.T) {
	rows := []comparison{
		{Label: "bad/one", Err: errors.New("x")},
		{Label: "good/one", Answer: "a"},
	}
	sortFailedLast(rows)
	if rows[0].Label != "good/one" {
		t.Fatalf("order = %s, %s", rows[0].Label, rows[1].Label)
	}
}

// A wall of text is where a comparison stops being read.
func TestPreviewIsCappedAndSaysSo(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 40; i++ {
		b.WriteString("line of answer\n")
	}
	out := render([]comparison{{Label: "a/b", Answer: b.String()}}, nil, false)
	if !strings.Contains(out, "more line(s)") {
		t.Fatalf("the preview is not marked as truncated: %q", out)
	}
	if n := strings.Count(out, "line of answer"); n > 8 {
		t.Fatalf("preview printed %d lines", n)
	}
	full := render([]comparison{{Label: "a/b", Answer: b.String()}}, nil, true)
	if strings.Contains(full, "more line(s)") {
		t.Fatal("full view was still truncated")
	}
	if n := strings.Count(full, "line of answer"); n != 40 {
		t.Fatalf("full view printed %d lines", n)
	}
}

func TestSkippedModelsAreListed(t *testing.T) {
	out := render([]comparison{{Label: "a/b", Answer: "x"}}, []string{"groq (no API key)"}, false)
	if !strings.Contains(out, "skipped") || !strings.Contains(out, "groq") {
		t.Fatalf("skipped not reported: %q", out)
	}
}

func TestAnEmptyAnswerIsShownAsSuch(t *testing.T) {
	out := render([]comparison{{Label: "a/b"}}, nil, false)
	if !strings.Contains(out, "no answer") {
		t.Fatalf("an empty answer is not marked: %q", out)
	}
}

func TestRenderSaysHowManyWereCompared(t *testing.T) {
	out := render([]comparison{{Label: "a/b", Answer: "x"}, {Label: "c/d", Answer: "y"}}, nil, false)
	if !strings.Contains(out, "compared 2") {
		t.Fatalf("count missing: %q", out)
	}
}
