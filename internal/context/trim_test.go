package ctx

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/Yash-K-Jagani/ycode/pkg/apitypes"
)

// trimReference is a deliberately naive, obviously-correct Trim used only as
// the oracle for the one-pass version. It shares no structure with it: it keeps
// the newest messages and walks backwards with a plain O(n^2) cost, which is
// fine for a test.
//
// The previous implementation is NOT used as the oracle, and the reason is
// worth recording. It evaluated its own budget as Estimate(append(sys, kept...))
// with sys aliasing the caller's slice (sys = msgs[:1]). That append wrote the
// surviving messages back over the slots of the dropped ones, so the loop was
// reading an array it was in the middle of rewriting. Given
//
//	system(7) system(90) tool(12) assistant(34), budget 142
//
// it returned the two-message tail [assistant(34), assistant(34)] - a message
// that never existed - while reporting the same drop count as the correct
// answer. It also left the caller's history as
// SYS|DDD|DDD|DDD|DDD in a smaller case.
//
// A rewrite cannot be validated against a broken reference, so this is a fresh
// implementation of the same specification.
func trimReference(msgs []apitypes.Message, budget int) ([]apitypes.Message, int) {
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
	// Start from the newest message and walk backwards, prepending one at a
	// time. Each candidate is built into a fresh slice and measured in full,
	// which is quadratic and wasteful but has no way to read an array it is
	// writing. The len(kept) > 0 guard is the "always keep at least one" rule.
	var kept []apitypes.Message
	for i := len(rest) - 1; i >= 0; i-- {
		cand := make([]apitypes.Message, 0, len(kept)+1)
		cand = append(cand, rest[i])
		cand = append(cand, kept...)
		if len(kept) > 0 && sysCost+Estimate(cand) > budget {
			break
		}
		kept = cand
	}
	var out []apitypes.Message
	if hasSys {
		out = append(out, msgs[0])
		out = append(out, apitypes.Message{Role: apitypes.RoleSystem, Content: compactedMarker})
	}
	out = append(out, kept...)
	return out, len(rest) - len(kept)
}

func equalMessages(a, b []apitypes.Message) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Role != b[i].Role || a[i].Content != b[i].Content {
			return false
		}
	}
	return true
}

func summarise(msgs []apitypes.Message) string {
	var b strings.Builder
	for i, m := range msgs {
		if i > 0 {
			b.WriteString(" | ")
		}
		body := m.Content
		if len(body) > 12 {
			body = body[:12] + "…"
		}
		fmt.Fprintf(&b, "%s:%s(c%d)", m.Role, body, costOf(m))
	}
	return b.String()
}

// The equivalence check: the one-pass Trim against the naive reference, over a
// spread of shapes and randomised budgets.
func TestTrimMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(20260929))
	roles := []apitypes.Role{apitypes.RoleUser, apitypes.RoleAssistant, apitypes.RoleSystem, apitypes.RoleTool}

	for _, n := range []int{0, 1, 2, 3, 5, 20, 100, 400} {
		for _, withSys := range []bool{false, true} {
			for trial := 0; trial < 40; trial++ {
				msgs := make([]apitypes.Message, 0, n)
				if withSys && n > 0 {
					msgs = append(msgs, apitypes.Message{Role: apitypes.RoleSystem, Content: "you are ycode"})
				}
				for i := 0; i < n; i++ {
					// A spread of sizes, including empty and oversized, so the
					// "always keep at least one" rule is exercised.
					var body string
					switch rng.Intn(10) {
					case 0:
						body = ""
					case 1:
						body = strings.Repeat("x", 40_000) // far over any budget
					default:
						body = strings.Repeat("w", rng.Intn(400))
					}
					msgs = append(msgs, apitypes.Message{Role: roles[rng.Intn(len(roles))], Content: body})
				}
				total := Estimate(msgs)
				// Budgets around the interesting boundary: exactly fitting,
				// just under, just over, and absurdly small.
				for _, budget := range []int{
					total + 1, total, total - 1, total / 2, total / 10, 1, 0, -5, 2000,
				} {
					// The reference does not mutate its input, but each side gets its
					// own copy anyway so a future regression in either is
					// reported as itself rather than as a cross-contamination.
					gotMsgs, gotDropped := Trim(cloneMessages(msgs), budget)
					wantMsgs, wantDropped := trimReference(cloneMessages(msgs), budget)
					label := fmt.Sprintf("n=%d sys=%v trial=%d budget=%d total=%d",
						n, withSys, trial, budget, total)
					if gotDropped != wantDropped {
						t.Fatalf("%s: dropped %d, want %d\n in=%s\n got=%s\nwant=%s",
							label, gotDropped, wantDropped, summarise(msgs), summarise(gotMsgs), summarise(wantMsgs))
					}
					if !equalMessages(gotMsgs, wantMsgs) {
						t.Fatalf("%s: content differs\n in=%s\n got=%s\nwant=%s",
							label, summarise(msgs), summarise(gotMsgs), summarise(wantMsgs))
					}
				}
			}
		}
	}
}

// A single message far larger than the budget must still be kept. Returning
// nothing would leave the model with no context at all, which is a worse
// failure than exceeding the budget.
func TestTrimKeepsAtLeastOneMessage(t *testing.T) {
	msgs := []apitypes.Message{
		{Role: apitypes.RoleSystem, Content: "sys"},
		{Role: apitypes.RoleUser, Content: strings.Repeat("a", 100_000)},
		{Role: apitypes.RoleAssistant, Content: "reply"},
	}
	out, dropped := Trim(msgs, 10)
	if len(out) < 2 {
		t.Fatalf("kept %d messages, want at least the system message and the newest", len(out))
	}
	if out[len(out)-1].Content != "reply" {
		t.Fatalf("the newest message must survive: %s", summarise(out))
	}
	if dropped != 1 {
		t.Fatalf("dropped = %d, want 1", dropped)
	}
}

// The marker is only meaningful when it stands in for dropped history. With no
// system message there is nothing to compact around, and prepending a
// system-role message would change the shape of the request.
func TestTrimOmitsMarkerWithoutSystemMessage(t *testing.T) {
	msgs := []apitypes.Message{
		{Role: apitypes.RoleUser, Content: strings.Repeat("a", 2000)},
		{Role: apitypes.RoleAssistant, Content: strings.Repeat("b", 2000)},
		{Role: apitypes.RoleUser, Content: "newest"},
	}
	out, dropped := Trim(msgs, 20)
	if dropped == 0 {
		// Fatal, not Skip: a fixture that stopped requiring trimming means this
		// test has quietly stopped testing what it names. As a skip it would go
		// green, and a regression that broke trimming would silence it instead
		// of reporting the broken fixture.
		t.Fatal("this fixture is supposed to require trimming, and nothing was dropped")
	}
	for i, m := range out {
		if m.Content == compactedMarker {
			t.Fatalf("marker inserted at %d with no system message: %s", i, summarise(out))
		}
	}
}

func TestTrimWithSystemMessageInsertsMarker(t *testing.T) {
	msgs := []apitypes.Message{
		{Role: apitypes.RoleSystem, Content: "you are ycode"},
		{Role: apitypes.RoleUser, Content: strings.Repeat("a", 2000)},
		{Role: apitypes.RoleAssistant, Content: strings.Repeat("b", 2000)},
		{Role: apitypes.RoleUser, Content: "newest"},
	}
	out, dropped := Trim(msgs, 20)
	if dropped == 0 {
		// See above: a broken fixture is a failure, not a reason to skip.
		t.Fatal("this fixture is supposed to require trimming, and nothing was dropped")
	}
	if out[0].Role != apitypes.RoleSystem || out[0].Content != "you are ycode" {
		t.Fatalf("the system message must stay first: %s", summarise(out))
	}
	if out[1].Content != compactedMarker {
		t.Fatalf("marker should follow the system message: %s", summarise(out))
	}
}

func TestTrimUnderBudgetIsIdentity(t *testing.T) {
	msgs := []apitypes.Message{
		{Role: apitypes.RoleSystem, Content: "sys"},
		{Role: apitypes.RoleUser, Content: "hi"},
	}
	out, dropped := Trim(msgs, 100_000)
	if dropped != 0 {
		t.Fatalf("dropped = %d, want 0", dropped)
	}
	if len(out) != 2 {
		t.Fatalf("got %d messages", len(out))
	}
}

func cloneMessages(msgs []apitypes.Message) []apitypes.Message {
	return append([]apitypes.Message(nil), msgs...)
}

// Trim used to destroy the caller's history. Its loop evaluated
// Estimate(append(sys, kept...)), and because sys aliases the caller's slice
// (sys = msgs[:1]), that append wrote the surviving messages back over the
// slots of the ones already dropped. A five-message history went in as
// SYS|AAA|BBB|CCC|DDD and came back as SYS|DDD|DDD|DDD|DDD.
//
// Nothing caught it because every caller trims and then keeps using the same
// slice - the session's message list, in all three of them. The turn appeared
// to work, and the next trim operated on a history in which most of the
// conversation had been replaced by copies of its most recent message.
func TestTrimDoesNotMutateInput(t *testing.T) {
	big := func(s string) string { return s + strings.Repeat("w", 200) }
	msgs := []apitypes.Message{
		{Role: apitypes.RoleSystem, Content: big("SYS")},
		{Role: apitypes.RoleUser, Content: big("AAA")},
		{Role: apitypes.RoleUser, Content: big("BBB")},
		{Role: apitypes.RoleUser, Content: big("CCC")},
		{Role: apitypes.RoleAssistant, Content: big("DDD")},
	}
	before := make([]string, len(msgs))
	for i, m := range msgs {
		before[i] = m.Content
	}

	out, dropped := Trim(msgs, 200)
	if dropped == 0 {
		t.Fatal("this fixture is supposed to require trimming")
	}
	if len(out) == 0 {
		t.Fatal("nothing survived")
	}
	for i, m := range msgs {
		if m.Content != before[i] {
			t.Fatalf("Trim overwrote message %d of the caller's history:\n had: %s\nnow: %s",
				i, before[i][:min(len(before[i]), 12)], m.Content[:min(len(m.Content), 12)])
		}
	}
}

// The same guarantee without a system message, where the old code's aliasing
// was different: sys was nil, so the overlap happened one slot later.
func TestTrimWithoutSystemMessageDoesNotMutateInput(t *testing.T) {
	big := func(s string) string { return s + strings.Repeat("w", 200) }
	msgs := []apitypes.Message{
		{Role: apitypes.RoleUser, Content: big("AAA")},
		{Role: apitypes.RoleUser, Content: big("BBB")},
		{Role: apitypes.RoleUser, Content: big("CCC")},
		{Role: apitypes.RoleAssistant, Content: big("DDD")},
	}
	before := make([]string, len(msgs))
	for i, m := range msgs {
		before[i] = m.Content
	}
	if _, dropped := Trim(msgs, 200); dropped == 0 {
		t.Fatal("this fixture is supposed to require trimming")
	}
	for i, m := range msgs {
		if m.Content != before[i] {
			t.Fatalf("Trim overwrote message %d of the caller's history", i)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// --- benchmarks ---

func benchHistory(n int) []apitypes.Message {
	msgs := make([]apitypes.Message, n)
	msgs[0] = apitypes.Message{Role: apitypes.RoleSystem, Content: "you are ycode"}
	body := strings.Repeat("lorem ipsum dolor sit amet ", 20) // ~540 bytes
	for i := 1; i < n; i++ {
		role := apitypes.RoleUser
		if i%2 == 0 {
			role = apitypes.RoleAssistant
		}
		msgs[i] = apitypes.Message{Role: role, Content: body}
	}
	return msgs
}

func BenchmarkTrim(b *testing.B) {
	for _, n := range []int{50, 200, 1000} {
		msgs := benchHistory(n)
		// A budget that forces shedding most of the history: the worst case,
		// and the one the quadratic version was pathological on.
		budget := Estimate(msgs) / 4
		b.Run(fmt.Sprintf("n=%d/shed-most", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, _ = Trim(msgs, budget)
			}
		})
		// A budget that sheds only a few: the common case late in a session.
		b.Run(fmt.Sprintf("n=%d/shed-few", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, _ = Trim(msgs, Estimate(msgs)-2000)
			}
		})
	}
}

// The naive reference, kept runnable so the improvement is measurable rather
// than asserted. It shares the shape of the old implementation (rebuild and
// re-measure per step) without the aliasing, so the delta it shows is the cost
// of the rewrite rather than the cost of a bug. Run with -bench=BenchmarkTrimReference.
func BenchmarkTrimReference(b *testing.B) {
	for _, n := range []int{50, 200, 1000} {
		msgs := benchHistory(n)
		budget := Estimate(msgs) / 4
		b.Run(fmt.Sprintf("n=%d/shed-most", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, _ = trimReference(cloneMessages(msgs), budget)
			}
		})
	}
}

func BenchmarkEstimate(b *testing.B) {
	msgs := benchHistory(500)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = Estimate(msgs)
	}
}
