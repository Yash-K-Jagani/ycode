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

func Estimate(msgs []apitypes.Message) int {
	n := 0
	for _, m := range msgs {
		n += len(m.Content)/4 + 4
	}
	return n
}

// Trim keeps the leading system message plus the newest messages fitting budget.
// Returns trimmed slice and number of dropped messages.
func Trim(msgs []apitypes.Message, budget int) ([]apitypes.Message, int) {
	if budget <= 0 || Estimate(msgs) <= budget {
		return msgs, 0
	}
	var sys []apitypes.Message
	rest := msgs
	if len(msgs) > 0 && msgs[0].Role == apitypes.RoleSystem {
		sys = msgs[:1]
		rest = msgs[1:]
	}
	kept := rest
	dropped := 0
	for len(kept) > 1 && Estimate(append(sys, kept...)) > budget {
		kept = kept[1:]
		dropped++
	}
	out := append(append([]apitypes.Message(nil), sys...), apitypes.Message{Role: apitypes.RoleSystem, Content: "(earlier history compacted to fit context)"})
	out = append(out, kept...)
	if len(sys) == 0 {
		out = out[1:]
	}
	return out, dropped
}
