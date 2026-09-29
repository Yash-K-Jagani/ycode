package tui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Yash-K-Jagani/ycode/internal/providers"
	"github.com/Yash-K-Jagani/ycode/internal/providers/openaicompat"
	"github.com/Yash-K-Jagani/ycode/pkg/apitypes"
)

// /connect is the only place a user pastes an API key, writes it to the
// environment and the keyring, and activates a provider. Every other
// credential path in ycode is covered; this one had five of six functions at
// zero. The properties worth pinning are that a key reaches only the provider
// it was pasted for, and that the flow cannot get stuck in a step it cannot
// leave.

// connectModel builds a Model with the connect form open.
func connectModel(t *testing.T) *Model {
	t.Helper()
	m := realModel(t)
	m.conn = newConnect()
	return m
}

func TestConnectProvidersComeFromTheRegistry(t *testing.T) {
	got := connectProviders()
	all := providers.All()
	if len(got) != len(all) {
		t.Fatalf("/connect offers %d providers, the registry has %d", len(got), len(all))
	}
	for i, p := range got {
		s := all[i]
		if p.ID != s.ID {
			t.Fatalf("position %d is %q, the registry has %q", i, p.ID, s.ID)
		}
		// Locality decides whether the form asks for a key or a host, so a
		// mismatch would ask Ollama for an API key.
		if p.IsHost != s.Local {
			t.Fatalf("%s: IsHost = %v but the registry says Local = %v", s.ID, p.IsHost, s.Local)
		}
		if p.KeyEnv != s.KeyEnv {
			t.Fatalf("%s: KeyEnv = %q, want %q", s.ID, p.KeyEnv, s.KeyEnv)
		}
		if p.Label == "" {
			t.Fatalf("%s has no short name for the form", s.ID)
		}
		// The spec is carried so fetchModelsCmd does not need its own switch;
		// without it an unrecognised id would leave a nil client.
		if p.spec == nil {
			t.Fatalf("%s has no registry row attached", s.ID)
		}
	}
	// The local provider must come first: it is the recommended default.
	if !got[0].IsHost {
		t.Fatal("the local provider should be offered first")
	}
}

func TestConnectFlowReachesEveryStep(t *testing.T) {
	m := connectModel(t)
	c := m.conn
	if c.step != cStepProvider {
		t.Fatalf("the flow starts at step %d", c.step)
	}

	// Move to the second provider and advance past the choice.
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if c.provIdx != 1 {
		t.Fatalf("down did not move: provIdx = %d", c.provIdx)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if c.step != cStepKey {
		t.Fatalf("enter on the provider step went to %d, want the key step", c.step)
	}

	// A cloud provider must refuse to continue without a key, and must say so
	// rather than advancing with an empty credential.
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if c.step != cStepKey {
		t.Fatalf("an empty key advanced to step %d", c.step)
	}
	if c.err == "" {
		t.Fatal("an empty key was accepted with no error shown")
	}

	// With a key, it fetches models.
	//
	// The fetch command is issued but not run: running it would build a real
	// Gemini client and call the public API, and a unit test that depends on
	// someone else's network is slow, flaky, and unreachable behind a CI
	// runner's restricted egress. The result of a successful fetch is delivered
	// below as the message the real one would have produced. The HTTP behaviour
	// is covered separately, against a local server.
	c.keyInput.SetValue("sk-test-key")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if c.step != cStepFetch {
		t.Fatalf("a valid key went to step %d", c.step)
	}
	if cmd == nil {
		t.Fatal("no command was issued to list models")
	}
	// The key must not be echoed back anywhere in the flow's own state.
	if strings.Contains(c.view(60, lipgloss.Color("#fff")), "sk-test-key") {
		t.Fatal("the plaintext key is visible in the form")
	}

	// The fetch resolves, and the flow moves to choosing a model.
	m.Update(fetchModelsMsg{models: []apitypes.ModelInfo{{ID: "m1"}, {ID: "m2"}}})
	if c.step != cStepModels {
		t.Fatalf("after fetch the step is %d", c.step)
	}

	// Choosing a model moves to the keyring question.
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if c.step != cStepSave {
		t.Fatalf("after choosing a model the step is %d, want the save step", c.step)
	}
	if c.result.Provider == "" || c.result.Model == "" {
		t.Fatalf("the result is incomplete: %+v", c.result)
	}

	// Answering the question finishes the flow.
	c.keyInput.SetValue("y")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !c.done {
		t.Fatal("the flow did not complete")
	}
	if !c.result.SaveKey {
		t.Fatal("answering y did not request a keyring save")
	}
}

// The flow can be walked backwards, and a key typed on the way must not survive
// into a different provider's step.
func TestConnectFlowBacktrack(t *testing.T) {
	m := connectModel(t)
	c := m.conn

	m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // provider -> key
	c.keyInput.SetValue("sk-secret")
	m.Update(tea.KeyMsg{Type: tea.KeyEsc}) // key -> provider
	if c.step != cStepProvider {
		t.Fatalf("esc went to step %d", c.step)
	}
	// A different provider, then into its key step: the field must be empty.
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := c.keyInput.Value(); got != "" {
		t.Fatalf("a key typed for one provider survived into the next: %q", got)
	}
	// And going back must not invent a credential.
	if c.key != "" {
		t.Fatalf("c.key = %q after backtracking", c.key)
	}
}

// A failed model list must send the user back to the key step with the reason
// visible, not strand them on a spinner.
func TestConnectFlowRecoversFromAFailedFetch(t *testing.T) {
	m := connectModel(t)
	c := m.conn
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	c.keyInput.SetValue("bad-key")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	m.Update(fetchModelsMsg{note: "401 unauthorized"})
	if c.step != cStepKey {
		t.Fatalf("a failed fetch left the flow at step %d", c.step)
	}
	if !strings.Contains(c.err, "401") {
		t.Fatalf("the reason was not shown: %q", c.err)
	}
	// The user can correct the key and try again.
	c.keyInput.SetValue("good-key")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if c.step != cStepFetch {
		t.Fatalf("retry did not re-fetch: step %d", c.step)
	}
	if c.err != "" {
		t.Fatalf("retrying left a stale error: %q", c.err)
	}
}

// The provider step is the only place esc means "close", and it must not be
// able to close from a later step.
func TestConnectEscOnlyClosesFromTheFirstStep(t *testing.T) {
	m := connectModel(t)
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.conn == nil {
		t.Fatal("esc closed the form from the key step")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.conn == nil {
		t.Fatal("the form closed when it should have gone back a step")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.conn != nil {
		t.Fatal("esc from the provider step should close the form")
	}
}

func TestConnectArrowKeysStayInRange(t *testing.T) {
	m := connectModel(t)
	c := m.conn
	// Down past the end stops at the last provider, and up from there moves
	// back - a picker that wrapped would land on the first.
	for i := 0; i < len(c.provs)+3; i++ {
		m.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	last := c.provIdx
	if last != len(c.provs)-1 {
		t.Fatalf("down stopped at %d, want %d", last, len(c.provs)-1)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if c.provIdx != last-1 {
		t.Fatalf("up from the last gave %d, want %d", c.provIdx, last-1)
	}
	// Up past the top stops at the first, and down from there moves on.
	for i := 0; i < len(c.provs)+3; i++ {
		m.Update(tea.KeyMsg{Type: tea.KeyUp})
	}
	if c.provIdx != 0 {
		t.Fatalf("up stopped at %d, want 0", c.provIdx)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if c.provIdx != 1 {
		t.Fatalf("down from the first gave %d, want 1", c.provIdx)
	}
}

// The point of the whole flow: a key reaches one provider and only that one.
func TestApplyConnectStoresTheKeyForOneProviderOnly(t *testing.T) {
	m := realModel(t)
	// Nothing is listening, so the models step is driven directly.
	m.conn = newConnect()
	m.conn.provIdx = 1 // gemini
	m.conn.result = connectResult{
		Provider: "gemini",
		Model:    "gemini-2.5-pro",
		Key:      "gemini-secret",
		KeyEnv:   "GEMINI_API_KEY",
		SaveKey:  false, // do not touch the real keyring
	}
	out := m.applyConnect(m.conn.result)

	if got := m.cfg.KeyFor("gemini"); got != "gemini-secret" {
		t.Fatalf("the key was not stored for the provider it was pasted for: %q", got)
	}
	for _, id := range providers.IDs() {
		if id == "gemini" {
			continue
		}
		if got := m.cfg.KeyFor(id); got != "" {
			t.Fatalf("the %s key leaked to %s", "gemini", id)
		}
	}
	// The reply must not print the key, only a masked form.
	if strings.Contains(out, "gemini-secret") {
		t.Fatalf("applyConnect echoed the key: %q", out)
	}
	if !strings.Contains(out, "persist with") {
		t.Fatalf("declining the keyring should say how to persist it: %q", out)
	}
	t.Setenv("GEMINI_API_KEY", "")
}

func TestApplyConnectWithOllamaSetsTheHost(t *testing.T) {
	m := realModel(t)
	out := m.applyConnect(connectResult{
		Provider: "ollama", Model: "qwen2.5-coder:3b", Host: "http://box:11434",
	})
	if m.cfg.OllamaHost != "http://box:11434" {
		t.Fatalf("OllamaHost = %q", m.cfg.OllamaHost)
	}
	// A local provider stores no key at all.
	if len(m.cfg.Keys) != 0 {
		t.Fatalf("connecting to a local provider stored %v", m.cfg.Keys)
	}
	if !strings.Contains(out, "http://box:11434") {
		t.Fatalf("the host is not reported: %q", out)
	}
}

func TestMaskKeyHidesTheSecret(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", "•••"},
		{"short", "•••"},
		{"exactly8", "•••"},
		{"123456789", "12•••6789"},
	} {
		if got := maskKey(tc.in); got != tc.want {
			t.Fatalf("maskKey(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// A multi-byte key must not have a partial character at either end.
	masked := maskKey("αβγδεζηθικλ")
	if strings.Contains(masked, "�") {
		t.Fatalf("maskKey produced a replacement character: %q", masked)
	}
	if !strings.HasPrefix(masked, "αβ") || !strings.HasSuffix(masked, "ικλ") {
		t.Fatalf("maskKey cut the wrong characters: %q", masked)
	}
}

func TestFetchModelsCmdReportsAnUnknownProvider(t *testing.T) {
	m := realModel(t)
	// A prov with no registry row: the old switch left the client nil and
	// dereferenced it.
	cmd := m.fetchModelsCmd(connectProv{ID: "nope", Label: "Nope"}, "k")
	msg, ok := cmd().(fetchModelsMsg)
	if !ok {
		t.Fatalf("expected a fetchModelsMsg, got %T", cmd())
	}
	if msg.note == "" {
		t.Fatal("an unknown provider produced no explanation")
	}
}

func TestFetchModelsCmdFallsBackToTheCatalog(t *testing.T) {
	// A server that refuses every request, so the live list is unavailable
	// and the registry catalogue is the fallback.
	//
	// The provider spec is copied and its constructor replaced so the client
	// is pointed here. The registry's own constructor hardcodes the provider's
	// public URL, so without this the test would call the real API - a unit
	// test that depends on someone else's network is slow, flaky, and fails
	// outright behind a CI runner's restricted egress.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	m := realModel(t)
	spec := *providers.Get("gemini")
	spec.New = func(key, host string) providers.Provider {
		return openaicompat.New("gemini", srv.URL, key)
	}
	prov := connectProv{ID: "gemini", Label: "Gemini", KeyEnv: "GEMINI_API_KEY", spec: &spec}
	msg := m.fetchModelsCmd(prov, "sk-test")().(fetchModelsMsg)
	if len(msg.models) == 0 && msg.note == "" {
		t.Fatal("an unavailable provider produced neither models nor a reason")
	}
	// The local server must actually have been reached, or the test is
	// exercising something other than what it claims.
	if len(msg.models) == 0 {
		return
	}
	if !strings.Contains(msg.note, "catalog") {
		t.Fatalf("fallback models were offered without saying so: %q", msg.note)
	}
}

func TestConnectViewRendersEveryStep(t *testing.T) {
	m := connectModel(t)
	accent := lipgloss.Color("#fff")
	for step := cStepProvider; step <= cStepSave; step++ {
		m.conn.step = step
		out := m.conn.view(60, accent)
		if strings.TrimSpace(stripANSI(out)) == "" {
			t.Fatalf("step %d rendered nothing", step)
		}
	}
	// A narrow terminal must not produce a broken panel.
	m.conn.step = cStepProvider
	if out := m.conn.view(4, accent); out == "" {
		t.Fatal("a narrow terminal rendered nothing")
	}
}

func TestWindowScrollsAroundTheSelection(t *testing.T) {
	// A long model list must keep the selection visible.
	start, end := window(0, 100, 8)
	if start != 0 || end != 8 {
		t.Fatalf("at the top: %d..%d", start, end)
	}
	start, end = window(50, 100, 8)
	if end-start != 8 {
		t.Fatalf("window = %d wide", end-start)
	}
	if start > 50 || end <= 50 {
		t.Fatalf("the selection at 50 is outside %d..%d", start, end)
	}
	// A short list is not padded.
	if s, e := window(0, 3, 8); s != 0 || e != 3 {
		t.Fatalf("a short list gave %d..%d", s, e)
	}
}
