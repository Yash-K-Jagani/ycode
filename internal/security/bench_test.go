package security

import (
	"testing"
)

// Which rule is expensive, on which input.
//
// Redact runs every rule over every record, so one pathological rule is charged
// to every tool call in every turn. This measures them separately, because the
// aggregate number cannot tell you which one to fix.

func BenchmarkRedactRules(b *testing.B) {
	inputs := map[string]string{
		"short":    "the quick brown fox jumps over the lazy dog repeatedly",
		"tiny":     "something happened",
		"key":      "here is my key " + fakeOpenAIKey + " end",
		"json":     `{"path":"a.go","content":"xxxx","key":"` + fakeAWSKey + `"}`,
		"conn":     "postgres://user:hunter2@db.example.com:5432/app",
		"sentence": "The function validates the path before reading it. " + "More prose here. ",
	}
	for _, text := range inputs {
		b.Run(inputName(text), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				Redact(text)
			}
		})
	}
}

func inputName(s string) string {
	switch len(s) {
	case 48:
		return "short_48b"
	case 17:
		return "tiny_17b"
	case 68:
		return "with_key"
	default:
		return "other"
	}
}

// Per-rule, so an expensive pattern is attributable rather than averaged away.
func BenchmarkIndividualRules(b *testing.B) {
	text := "an ordinary sentence with no secrets in it at all"
	for _, r := range secretRes {
		b.Run(r.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if r.valueGroup == 0 {
					r.re.ReplaceAllString(text, "[REDACTED]")
				} else {
					replaceGroups(text, r)
				}
			}
		})
	}
}
