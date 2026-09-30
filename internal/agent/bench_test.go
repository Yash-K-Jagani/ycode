package agent

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/Yash-K-Jagani/ycode/internal/tools"
	"github.com/Yash-K-Jagani/ycode/pkg/apitypes"
)

// Benchmarks for the code that runs on every single turn.
//
// The tool loop is where a turn's cost is spent after the model has answered, and
// it is all regexes and string handling. That is exactly the shape of code where a
// careless change turns a 200 microsecond turn into a 200 millisecond one, and
// nothing in the functional tests would notice - they assert on output, not time.

// benchTool answers instantly and counts its calls.
type benchTool struct {
	n    int
	name string
}

func (b *benchTool) Name() string { return b.name }
func (b *benchTool) Description() string {
	return "a tool for benchmarking, with a description long enough to be realistic"
}
func (b *benchTool) Schema() string {
	return `{"type":"object","required":["x"],"properties":{"x":{"type":"string"}}}`
}
func (b *benchTool) Run(context.Context, json.RawMessage) (string, error) {
	b.n++
	return "ECHO:" + b.name, nil
}

// benchProvider replays scripted turns: N tool calls, then an answer.
type benchProvider struct {
	callText string
	calls    int
	i        int
}

func (p *benchProvider) Name() string { return "bench" }
func (p *benchProvider) ListModels(context.Context) ([]apitypes.ModelInfo, error) {
	return nil, nil
}
func (p *benchProvider) Complete(context.Context, string, []apitypes.Message) (string, error) {
	return "", nil
}
func (p *benchProvider) Stream(_ context.Context, _ string, _ []apitypes.Message, w io.Writer) (apitypes.StreamChunk, error) {
	out := "done"
	if p.i < p.calls {
		out = p.callText
	}
	p.i++
	_, _ = io.WriteString(w, out)
	return apitypes.StreamChunk{Delta: out, Done: true, PromptTok: 100, ComplTok: 20}, nil
}

func benchRegistry() *tools.Registry {
	r := tools.NewRegistry()
	r.Add(&benchTool{name: "echo"})
	return r
}

func benchCall(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(`<tool:echo>{"x":"some argument value that a model would plausibly emit"}</tool:echo>`)
	}
	return b.String()
}

// --- the parser ---

// ParseCalls runs two regex passes over every assistant message, and the tolerant
// pass only runs when the strict one finds nothing. That ordering is a deliberate
// optimisation, and this is what keeps it worth having.
func BenchmarkParseCalls(b *testing.B) {
	for _, bc := range []struct {
		name string
		text string
	}{
		{"no_calls_prose", "Here is what I found. The function is in internal/tui/turn.go and it does three things."},
		{"no_calls_long_prose", strings.Repeat("Some explanation about the code. ", 40)},
		{"one_call_strict", benchCall(1)},
		{"four_calls_strict", benchCall(4)},
		// The expensive path: the strict pattern misses and the tolerant scan runs
		// over the whole message.
		{"tolerant_fence", "Let me check.\n```tool:echo\n{\"x\":\"1\"}\n```\n"},
		{"tolerant_unclosed", `<tool:echo>{"x":"1"}`},
		{"tolerant_mentions_only", "You could call <tool:echo> here if you wanted."},
	} {
		b.Run(bc.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				ParseCalls(bc.text)
			}
		})
	}
}

// A pathological input is the one that decides whether a turn ever appears to
// hang, so it is measured rather than assumed.
func BenchmarkParseCallsPathological(b *testing.B) {
	for _, bc := range []struct {
		name string
		text string
	}{
		// Many openers with no object after them: every one starts an extractObject
		// scan that finds nothing.
		{"many_openers_no_object", strings.Repeat("<tool:echo> ", 50)},
		// A very long single argument, which extractObject brace-matches through.
		{"huge_object", `<tool:echo>{"x":"` + strings.Repeat("a", 20000) + `"}</tool:echo>`},
		// Deeply nested, which defeats a naive brace counter.
		{"nested_object", `<tool:echo>{"x":` + strings.Repeat(`{"y":`, 30) + "1" + strings.Repeat("}", 30) + `}</tool:echo>`},
	} {
		b.Run(bc.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				ParseCalls(bc.text)
			}
		})
	}
}

// Scaling, because a single number hides the shape and the shape is what decides
// whether a confused model can make a turn hang.
//
// ParseCalls is called on every assistant message. Two inputs grow in practice: a
// large tool argument, which is normal in build mode (writing a whole file), and
// the number of stray openers, which is what a model stuck in a loop emits. The
// second is the one to worry about, because the tolerant scan calls extractObject
// once per opener and each call scans forward through the rest of the message.
func BenchmarkParseCallsScaling(b *testing.B) {
	b.Run("argument_size", func(b *testing.B) {
		for _, size := range []int{1 << 10, 1 << 13, 1 << 16} {
			b.Run(itoa(size), func(b *testing.B) {
				text := `<tool:echo>{"x":"` + strings.Repeat("a", size) + `"}</tool:echo>`
				b.SetBytes(int64(len(text)))
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					ParseCalls(text)
				}
			})
		}
	})
	b.Run("stray_openers", func(b *testing.B) {
		for _, n := range []int{10, 50, 200} {
			b.Run(itoa(n), func(b *testing.B) {
				// Openers each followed by an object that never closes, which is
				// the shape that makes each extractObject scan run to the end of
				// the message rather than stopping at the first match.
				text := strings.Repeat(`<tool:echo>{"x":"unterminated `, n)
				b.SetBytes(int64(len(text)))
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					ParseCalls(text)
				}
			})
		}
	})
}

// --- the guards ---

// isRepeat and normText run on every round, before anything else. A turn that is
// 8 rounds long runs them 8 times.
func BenchmarkRepeatGuard(b *testing.B) {
	long := strings.Repeat("The answer is that the function validates the path before reading it. ", 20)
	for _, bc := range []struct {
		name string
		a, b string
	}{
		{"short", "short answer", "another answer"},
		{"long_same", long, long},
		{"long_different", long, strings.Repeat("A completely different explanation follows here. ", 20)},
	} {
		b.Run(bc.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if isRepeat(normText(bc.a), normText(bc.b)) {
					continue
				}
			}
		})
	}
}

// --- the whole turn ---

// The end-to-end number: one turn of the real loop, minus the model call. This is
// what "how much does the harness cost on top of inference" means.
func BenchmarkTurnNoTools(b *testing.B) {
	p := &benchProvider{calls: 0, callText: "here is your answer"}
	msgs := []apitypes.Message{
		{Role: apitypes.RoleSystem, Content: "you are a coding assistant"},
		{Role: apitypes.RoleUser, Content: "what does this module do"},
	}
	reg := benchRegistry()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		p.i = 0
		if _, err := RunWithRounds(context.Background(), p, "m", msgs, reg,
			[]string{"echo"}, nil, io.Discard, nil, 2, "build"); err != nil {
			b.Fatal(err)
		}
	}
}

// A turn that actually runs tools, which is the common case in build mode.
func BenchmarkTurnWithFourTools(b *testing.B) {
	p := &benchProvider{calls: 1, callText: benchCall(4)}
	msgs := []apitypes.Message{
		{Role: apitypes.RoleSystem, Content: "you are a coding assistant"},
		{Role: apitypes.RoleUser, Content: "find the callers"},
	}
	reg := benchRegistry()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		p.i = 0
		if _, err := RunWithRounds(context.Background(), p, "m", msgs, reg,
			[]string{"echo"}, nil, io.Discard, nil, 3, "build"); err != nil {
			b.Fatal(err)
		}
	}
}

// A deep conversation, where the prompt grows. The loop appends two messages per
// tool call, so an 8-round turn with parallel calls ends up with a prompt many
// times the original - and the cost of that is the harness's, not the model's.
func BenchmarkTurnDeepConversation(b *testing.B) {
	for _, rounds := range []int{2, 8} {
		b.Run("rounds_"+itoa(rounds), func(b *testing.B) {
			p := &benchProvider{calls: rounds - 1, callText: benchCall(2)}
			msgs := []apitypes.Message{
				{Role: apitypes.RoleSystem, Content: "you are a coding assistant"},
				{Role: apitypes.RoleUser, Content: "do the thing"},
			}
			reg := benchRegistry()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				p.i = 0
				if _, err := RunWithRounds(context.Background(), p, "m", msgs, reg,
					[]string{"echo"}, nil, io.Discard, nil, rounds, "build"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}

// The observer callbacks are called per tool call, and a UI forwards them into a
// message loop. Worth knowing what the forwarding costs before adding more of them.
func BenchmarkObserverCallbacks(b *testing.B) {
	obs := &Observer{
		OnTool:  func(name, args, result string, err error) {},
		OnChunk: func(name, chunk string) {},
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		obs.tool("echo", `{"x":"1"}`, "out", nil)
		if obs.streams() {
			obs.OnChunk("echo", "out")
		}
	}
}
