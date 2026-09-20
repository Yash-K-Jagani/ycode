package ctx

import "github.com/Yash-K-Jagani/ycode/pkg/apitypes"

func BudgetFor(model string) int { return 6000 }

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
