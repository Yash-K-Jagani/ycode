package ctx

import (
	"os"
	"strconv"
	"strings"

	"github.com/Yash-K-Jagani/ycode/pkg/apitypes"
)

// Context budgeting.
//
// BudgetFor used to return a hardcoded 6000 and ignore its argument, so every
// model got the same allowance: a 4k model overflowed it, and a 128k cloud model
// had its history thrown away for no reason. The harness already knows how to
// recognise a small model (modes.isSmallModel), so the same idea applies here —
// read the size off the model name, and stay conservative when it is unknown.

// contextWindows are the context sizes of the model families ycode talks to.
// Order matters: the first match wins, so put specific families first.
var contextWindows = []struct {
	match  string
	window int
}{
	{"gemini-1.5-pro", 2_000_000},
	{"gemini-2.5-pro", 1_000_000},
	{"gemini", 1_000_000},
	{"gpt-4.1", 1_000_000},
	{"gpt-4o", 128_000},
	{"gpt-4", 128_000},
	{"o1", 200_000},
	{"o3", 200_000},
	{"claude", 200_000},
	{"qwen2.5-coder", 32_768},
	{"qwen", 32_768},
	{"deepseek-coder", 65_536},
	{"deepseek", 65_536},
	{"llama3.1", 128_000},
	{"llama3.2", 131_072},
	{"llama3", 8_192},
	{"llama2", 4_096},
	{"mistral", 32_768},
	{"mixtral", 32_768},
	{"codellama", 16_384},
	{"starcoder2", 16_384},
	{"granite-code", 32_768},
	{"phi3", 16_384},
	{"gemma", 8_192},
	{"command-r", 128_000},
	{"kimi", 128_000},
}

// defaultWindow is used for a model we do not recognise. Deliberately small:
// guessing high overflows the provider (a 400 with no useful message), while
// guessing low only costs a little history.
const defaultWindow = 8_192

// reserves are held back from the window for the system prompt, the tool
// schemas and the model's own reply, none of which live in the history budget.
const (
	replyReserve  = 2_500
	minimumBudget = 2_000
	maximumBudget = 100_000
)

// ContextWindow reports a model's context size, defaulting to a conservative
// value for unknown models.
func ContextWindow(model string) int {
	m := strings.ToLower(model)
	if m == "" {
		return defaultWindow
	}
	// An explicit window in the name wins: ollama tags look like
	// "qwen2.5-coder:7b-instruct-q4_0" and cloud ids can carry "-32k".
	if w, ok := windowFromName(m); ok {
		return w
	}
	for _, e := range contextWindows {
		if strings.Contains(m, e.match) {
			return e.window
		}
	}
	return defaultWindow
}

// windowFromName reads an explicit size: "…:32k", "…-128k", "ctx=8192".
func windowFromName(m string) (int, bool) {
	if i := strings.IndexByte(m, '='); i >= 0 {
		if n, err := strconv.Atoi(m[i+1:]); err == nil && n >= 1024 {
			return n, true
		}
	}
	for _, suffix := range []string{"k", "m"} {
		i := strings.Index(m, suffix)
		if i <= 0 {
			continue
		}
		// Must be preceded by a digit.
		if !isDigit(m[i-1]) {
			continue
		}
		j := i
		for j > 0 && isDigit(m[j-1]) {
			j--
		}
		n, err := strconv.Atoi(m[j:i])
		if err != nil || n <= 0 {
			continue
		}
		if suffix == "m" {
			return n * 1_000_000, true
		}
		return n * 1024, true
	}
	return 0, false
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// localBudget is the allowance for a locally served model. Ollama's actual
// context is whatever the user set (num_ctx), which is often 4k–8k even for a
// model whose card says 32k, and exceeding it truncates or errors rather than
// growing. So local models keep the conservative allowance: scaling it up would
// be optimistic in a way we cannot check.
const localBudget = 6_000

// isLocalModel reports an Ollama-style "name:tag", which is how locally served
// models are identified. Cloud model ids contain no colon.
func isLocalModel(model string) bool {
	// A path or a URL is not a model tag, and Ollama tags are often dotted
	// ("deepseek-coder:6.7b-instruct-q4_0"), so the test cannot be "no dot".
	if strings.Contains(model, "://") || strings.ContainsAny(model, "/\\") {
		return false
	}
	i := strings.LastIndexByte(model, ':')
	return i > 0 && i < len(model)-1
}

// BudgetFor is the number of tokens of conversation history worth keeping for a
// model. It is deliberately about half the window: history, system prompt and
// tool results all have to coexist, and the harness streams replies.
func BudgetFor(model string) int {
	if v := os.Getenv("YCODE_CONTEXT_BUDGET"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	if isLocalModel(model) {
		return localBudget
	}
	b := ContextWindow(model)/2 - replyReserve
	if b < minimumBudget {
		return minimumBudget
	}
	if b > maximumBudget {
		return maximumBudget
	}
	return b
}

// compactedMarker replaces the history that did not fit. It is deliberately
// terse: it is inserted on every trim, so a long explanation would be paid for
// on every turn for no benefit.
const compactedMarker = "(earlier history compacted to fit context)"

// perMessageOverhead is the fixed cost attributed to every message, covering
// the role and separator tokens a real tokenizer adds beyond the raw text.
const perMessageOverhead = 4

// costOf is the estimated token cost of one message.
func costOf(m apitypes.Message) int { return len(m.Content)/4 + perMessageOverhead }

func Estimate(msgs []apitypes.Message) int {
	n := 0
	for _, m := range msgs {
		n += costOf(m)
	}
	return n
}

// Trim keeps the leading system message plus the newest messages fitting budget.
// Returns the trimmed slice and the number of dropped messages.
//
// This walks the history once, backwards, accumulating until the next message
// would not fit. It used to drop one message at a time and re-estimate the
// whole remaining slice after every drop, which is quadratic: a 500-message
// session that had to shed 200 of them re-walked roughly 60,000 messages per
// turn, on the critical path of every turn that had a long history. The
// quantities involved are tiny - history is capped in the thousands of tokens -
// but the shape of the cost is what made it worth replacing.
//
// The one-pass version has to preserve the old behaviour exactly, including two
// details that are easy to get wrong:
//
//   - At least one non-system message is always kept, even if it alone exceeds
//     the budget. Returning an empty history would leave the model with no
//     context at all, which is worse than a context that is over budget.
//   - The marker message is only inserted when a system message was actually
//     dropped around. Prefixing a system message onto a conversation that did
//     not have one changes the shape of the request the provider sees.
func Trim(msgs []apitypes.Message, budget int) ([]apitypes.Message, int) {
	if budget <= 0 || Estimate(msgs) <= budget {
		return msgs, 0
	}
	hasSys := len(msgs) > 0 && msgs[0].Role == apitypes.RoleSystem
	rest := msgs
	sysCost := 0
	if hasSys {
		sysCost = costOf(msgs[0])
		rest = msgs[1:]
	}

	// The budget the newest messages have to share, once the system message is
	// paid for.
	limit := budget - sysCost

	// Walk back from the newest message, the one we are least willing to lose.
	// keepFrom ends up as the index of the oldest message still included, so
	// rest[keepFrom:] is what survives.
	keepFrom := len(rest)
	used := 0
	for i := len(rest) - 1; i >= 0; i-- {
		c := costOf(rest[i])
		// The newest message is kept unconditionally; every earlier one has to
		// fit. Without that exemption a single oversized message would empty
		// the history.
		if i < len(rest)-1 && used+c > limit {
			break
		}
		used += c
		keepFrom = i
	}

	kept := rest[keepFrom:]
	dropped := keepFrom

	// The marker goes in front of what survived, but only when there was a
	// system message for it to sit alongside.
	out := make([]apitypes.Message, 0, 1+1+len(kept))
	if hasSys {
		out = append(out, msgs[0])
		out = append(out, apitypes.Message{Role: apitypes.RoleSystem, Content: compactedMarker})
	}
	out = append(out, kept...)
	return out, dropped
}
