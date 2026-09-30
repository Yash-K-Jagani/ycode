package headless

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Yash-K-Jagani/ycode/internal/config"
	"github.com/Yash-K-Jagani/ycode/internal/cost"
	"github.com/Yash-K-Jagani/ycode/internal/db"
	"github.com/Yash-K-Jagani/ycode/internal/providers"
	"github.com/Yash-K-Jagani/ycode/internal/providers/openaicompat"
	"github.com/Yash-K-Jagani/ycode/internal/router"
	"github.com/Yash-K-Jagani/ycode/pkg/apitypes"
)

// Tests here use cost.NewEphemeral rather than cost.New.
//
// cost.New seeds itself from the persisted record, which is correct behaviour
// and untestable behaviour: a budget test that depends on the shared day total
// also depends on whatever the developer spent that morning. The production
// path still reads the persisted total, and Options.Tracker exists so it can.

// redirectConfig points config.Dir() at a temp dir so these tests cannot
// rewrite a real ~/.ycode.
//
// db.Shared is primed first, deliberately: it resolves its path on first use and
// caches the open handle for the life of the process, so redirecting the env
// first would create a database inside the temp dir that nothing can close, and
// TempDir's cleanup would fail on a locked file.
func redirectConfig(t *testing.T) {
	t.Helper()
	db.Shared()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir) // os.UserHomeDir on Windows
}

// record reports a metered turn at an exact USD figure by deriving the token
// count from the real price table. Pricing is per thousand tokens, so a
// hardcoded count records a fraction of a cent and every assertion below would
// pass for the wrong reason.
func record(t *testing.T, tr *cost.Tracker, provider string, usd float64) {
	t.Helper()
	per1k, _ := providers.Pricing(provider)
	if per1k <= 0 {
		t.Fatalf("provider %s has no prompt price, so spend cannot be recorded", provider)
	}
	if got := tr.Add(provider, int(usd/per1k*1000), 0); got <= 0 {
		t.Fatalf("asked to record $%v, got $%v", usd, got)
	}
}

// --- the budget stop ---

// Before usage was recorded, headless spent money that no limit could see, so
// this check could never fire on a session that only ran `ycode -p`.
func TestOverBudgetStopsARunThatAlreadySpent(t *testing.T) {
	redirectConfig(t)
	cfg := config.Defaults()
	cfg.DailyBudgetUSD = 1.00
	tr := cost.NewEphemeral()
	record(t, tr, "gemini", 1.20)

	var log bytes.Buffer
	err := overBudget(cfg, tr, &log)
	if err == nil {
		t.Fatal("a run started while over budget was allowed")
	}
	if !strings.Contains(err.Error(), "daily budget reached") {
		t.Fatalf("unhelpful error: %v", err)
	}
	// The log carries it too, since a CI job's diagnosis comes from the log.
	if !strings.Contains(log.String(), "budget") {
		t.Fatalf("nothing written to the log: %q", log.String())
	}
}

func TestOverBudgetAllowsARunUnderTheLimit(t *testing.T) {
	redirectConfig(t)
	cfg := config.Defaults()
	cfg.DailyBudgetUSD = 1.00
	tr := cost.NewEphemeral()
	record(t, tr, "gemini", 0.50)
	var log bytes.Buffer
	if err := overBudget(cfg, tr, &log); err != nil {
		t.Fatalf("an under-budget run was stopped: %v", err)
	}
	if log.String() != "" {
		t.Fatalf("an allowed run wrote to the log: %q", log.String())
	}
}

// The default has to be untouched: no limit means no limit.
func TestOverBudgetIsInertByDefault(t *testing.T) {
	redirectConfig(t)
	tr := cost.NewEphemeral()
	record(t, tr, "gemini", 500)
	var log bytes.Buffer
	if err := overBudget(config.Defaults(), tr, &log); err != nil {
		t.Fatalf("the default config stopped a run: %v", err)
	}
}

// --- usage recording ---

// A server that reports usage the way a real one does: content deltas first,
// then a final chunk carrying the counts.
func usageServer(t *testing.T, promptTok, complTok int) *httptest.Server {
	t.Helper()
	return serveSSE(t, func(w http.ResponseWriter) {
		for _, d := range []string{"hel", "lo"} {
			writeChunk(w, map[string]any{
				"choices": []any{map[string]any{"delta": map[string]string{"content": d}}},
			})
		}
		writeChunk(w, map[string]any{
			"choices": []any{map[string]any{"delta": map[string]string{}}},
			"usage":   map[string]int{"prompt_tokens": promptTok, "completion_tokens": complTok},
		})
	})
}

// A server that reports no usage at all, which is what several OpenAI-compatible
// proxies and older Ollama endpoints do.
func noUsageServer(t *testing.T) *httptest.Server {
	t.Helper()
	return serveSSE(t, func(w http.ResponseWriter) {
		writeChunk(w, map[string]any{
			"choices": []any{map[string]any{"delta": map[string]string{"content": "hi"}}},
		})
	})
}

func serveSSE(t *testing.T, body func(http.ResponseWriter)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		body(w)
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func writeChunk(w http.ResponseWriter, v map[string]any) {
	b, _ := json.Marshal(v)
	_, _ = w.Write([]byte("data: " + string(b) + "\n\n"))
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// testRouter points one provider id at a local test server.
//
// It goes through the router's own factory seam rather than reaching into the
// provider registry, so the test exercises the same construction path the
// product uses.
func testRouter(t *testing.T, id, baseURL string) *router.Router {
	t.Helper()
	cfg := config.Defaults()
	// router.New defaults to the local provider, and the factory below only
	// knows how to build the one under test.
	cfg.ActiveProvider = id
	cfg.ActiveModel = "test-model"
	r := router.New(cfg)
	r.SetFactoryForTest(func(gotID, key, host string) (providers.Provider, error) {
		if gotID != id {
			return nil, errors.New("unexpected provider " + gotID)
		}
		return openaicompat.New(id, baseURL, "sk-test"), nil
	})
	return r
}

var onePrompt = []apitypes.Message{{Role: apitypes.RoleUser, Content: "hi"}}

// The end-to-end claim of this change: a provider's reported counts reach the
// tracker, so headless spend is recorded and a budget can act on it.
//
// Without this the whole chain - router ledger, recordUsage, tracker - could be
// individually correct and still never move a number.
func TestReportedUsageIsRecorded(t *testing.T) {
	redirectConfig(t)
	srv := usageServer(t, 1200, 340)
	tr := cost.NewEphemeral()

	r := testRouter(t, "gemini", srv.URL)
	r.Begin()
	if _, _, err := r.StreamWithFallback(context.Background(), onePrompt, io.Discard); err != nil {
		t.Fatal(err)
	}

	var log bytes.Buffer
	recordUsage(config.Defaults(), tr, r, &log)

	_, _, spent := tr.Today()
	if spent <= 0 {
		t.Fatalf("a metered turn charged nothing ($%v)", spent)
	}
	if !strings.Contains(log.String(), "1200 prompt + 340 completion") {
		t.Fatalf("the counts were not reported: %q", log.String())
	}
	if strings.Contains(log.String(), "unmeasured") {
		t.Fatalf("a reported turn was called unmeasured: %q", log.String())
	}
}

// An unreported turn must not be recorded as a free one, and must say so.
// Silence here is what made the old estimate read as a measurement.
func TestUnreportedTurnIsNotRecordedAsZeroCost(t *testing.T) {
	redirectConfig(t)
	srv := noUsageServer(t)
	tr := cost.NewEphemeral()

	r := testRouter(t, "gemini", srv.URL)
	r.Begin()
	if _, _, err := r.StreamWithFallback(context.Background(), onePrompt, io.Discard); err != nil {
		t.Fatal(err)
	}

	var log bytes.Buffer
	recordUsage(config.Defaults(), tr, r, &log)

	if !strings.Contains(log.String(), "no token counts") {
		t.Fatalf("an unreported turn was not called out: %q", log.String())
	}
	if !strings.Contains(log.String(), "unmeasured") {
		t.Fatalf("the log does not say the number is not a measurement: %q", log.String())
	}
	if _, _, spent := tr.Today(); spent != 0 {
		t.Fatalf("an unmeasured turn was charged $%v", spent)
	}
}

// A turn that fell back was priced by the fallback. Charging it to the
// configured provider reports $0 for a Gemini turn when Ollama is configured,
// which is precisely the case the budget has to catch.
func TestReportedUsageIsChargedToTheServingProvider(t *testing.T) {
	redirectConfig(t)
	srv := usageServer(t, 1000, 200)
	tr := cost.NewEphemeral()

	r := testRouter(t, "gemini", srv.URL)
	r.Begin()
	if _, _, err := r.StreamWithFallback(context.Background(), onePrompt, io.Discard); err != nil {
		t.Fatal(err)
	}

	var log bytes.Buffer
	// The configured provider is deliberately left as the local default.
	recordUsage(config.Defaults(), tr, r, &log)

	if !strings.Contains(log.String(), "gemini") {
		t.Fatalf("usage not attributed to the serving provider: %q", log.String())
	}
	if _, _, spent := tr.Today(); spent <= 0 {
		t.Fatalf("a served Gemini turn charged $%v; a local price made it free", spent)
	}
}

// Usage is taken, not read: recording twice must not double-charge.
func TestRecordingTwiceDoesNotDoubleCharge(t *testing.T) {
	redirectConfig(t)
	srv := usageServer(t, 1000, 200)
	tr := cost.NewEphemeral()
	r := testRouter(t, "gemini", srv.URL)
	r.Begin()
	if _, _, err := r.StreamWithFallback(context.Background(), onePrompt, io.Discard); err != nil {
		t.Fatal(err)
	}

	var log bytes.Buffer
	recordUsage(config.Defaults(), tr, r, &log)
	_, _, once := tr.Today()
	recordUsage(config.Defaults(), tr, r, &log)
	_, _, twice := tr.Today()
	if once != twice {
		t.Fatalf("recording the same turn twice charged $%v then $%v", once, twice)
	}
}

// --- exit codes ---

// exhausted means raise MaxIter; spend-limited means raise daily_budget_usd.
// Sending someone to the wrong setting is worse than either error.
func TestSpendLimitedHasItsOwnExitCode(t *testing.T) {
	if OutcomeSpendLimited.ExitCode() == OutcomeExhausted.ExitCode() {
		t.Fatal("spend-limited and iteration-exhausted share an exit code")
	}
	if OutcomeSpendLimited.ExitCode() == 0 {
		t.Fatal("a budget stop exits 0, so CI reads it as success")
	}
	if OutcomeExhausted.ExitCode() != 3 {
		t.Fatalf("OutcomeExhausted exit code changed to %d", OutcomeExhausted.ExitCode())
	}
	if OutcomeMet.ExitCode() != 0 || OutcomeNone.ExitCode() != 0 {
		t.Fatal("a successful run no longer exits 0")
	}
}

// Every failure needs its own code, or CI cannot tell them apart. The two
// success outcomes share 0 on purpose.
func TestFailureOutcomesHaveDistinctExitCodes(t *testing.T) {
	failures := []Outcome{OutcomeBlocked, OutcomeExhausted, OutcomeStalled,
		OutcomeCancelled, OutcomeUnverified, OutcomeSpendLimited}
	seen := map[int]Outcome{}
	for _, o := range failures {
		if o.ExitCode() == 0 {
			t.Errorf("%q exits 0, so CI reads a failure as success", o)
		}
		if prev, dup := seen[o.ExitCode()]; dup {
			t.Errorf("%q and %q share exit code %d", prev, o, o.ExitCode())
		}
		seen[o.ExitCode()] = o
	}
}

func TestSpendLimitedIsItsOwnOutcome(t *testing.T) {
	if string(OutcomeSpendLimited) == string(OutcomeExhausted) {
		t.Fatal("the two budgets share an outcome string")
	}
	if !strings.Contains(string(OutcomeSpendLimited), "spend") {
		t.Fatalf("outcome name does not say which budget: %q", OutcomeSpendLimited)
	}
}
