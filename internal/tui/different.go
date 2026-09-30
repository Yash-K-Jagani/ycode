package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Yash-K-Jagani/ycode/internal/config"
	yctx "github.com/Yash-K-Jagani/ycode/internal/context"
	"github.com/Yash-K-Jagani/ycode/internal/cost"
	"github.com/Yash-K-Jagani/ycode/internal/providers"
	"github.com/Yash-K-Jagani/ycode/internal/router"
	"github.com/Yash-K-Jagani/ycode/pkg/apitypes"
)

// /different: ask several models the same question and put the answers side by
// side.
//
// The reason this exists is that model choice is guesswork. Someone deciding
// between a 3B local model and a paid frontier model for a particular kind of
// task has no way to find out short of writing the same prompt five times by hand
// and trying to remember which answer came from where. Here they write it once.
//
// It is also the honest way to compare, because every answer is produced with the
// same system prompt and the same tools: a difference in the output is a
// difference in the model rather than in the setup.

// comparison is one model's answer to the question.
type comparison struct {
	Label   string
	Answer  string
	Err     error
	PromptT int
	ComplT  int
	USD     float64
	// Measured distinguishes a provider's reported counts from an estimate.
	// Comparing a measured answer against an estimated one and calling the
	// difference a model difference is the exact mistake this feature invites.
	Measured bool
	Took     time.Duration
}

// ask runs one model and records the result.
//
// Streaming to io.Discard rather than the transcript: five answers scrolling past
// before any of them is readable would be worse than useless, and the comparison
// is the output.
func ask(ctx context.Context, p providers.Provider, model, label string, msgs []apitypes.Message) comparison {
	c := comparison{Label: label}
	start := time.Now()
	var buf strings.Builder
	chunk, err := p.Stream(ctx, model, msgs, &buf)
	c.Took = time.Since(start)
	if err != nil {
		c.Err = err
		// Whatever arrived before the failure is kept: a provider that died
		// halfway has still said something, and discarding it makes the
		// comparison look empty rather than interrupted.
		c.Answer = buf.String()
		return c
	}
	c.Answer = buf.String()
	if c.Answer == "" {
		c.Answer = chunk.Delta
	}
	if chunk.UsageReported() {
		c.PromptT, c.ComplT, c.Measured = chunk.PromptTok, chunk.ComplTok, true
	} else {
		// Estimated from the request and the answer, and labelled as such in the
		// rendering. Guessing silently is how a cost comparison becomes fiction.
		c.PromptT = yctx.Estimate(msgs)
		c.ComplT = len(c.Answer) / 4
	}
	return c
}

// candidates decides which models to compare.
//
// With no argument: the active provider's recommended model plus every other
// provider the user has actually configured, using their recommended model. That
// is the comparison someone usually wants - "is the free local one as good as the
// paid one for this?" - without making them name five models.
//
// With arguments: read as provider/model or bare provider, so
// `/different gemini/gemini-2.5-flash groq` works and the bare form means that
// provider's recommended model.
func candidates(cfg config.Config, args []string) ([]target, []string) {
	var out []target
	var skipped []string

	add := func(id, model string) {
		if model == "" {
			return
		}
		for _, t := range out {
			if t.Provider == id && t.Model == model {
				return
			}
		}
		out = append(out, target{Provider: id, Model: model})
	}

	if len(args) == 0 {
		if spec := providers.Get(cfg.ActiveProvider); spec != nil {
			add(cfg.ActiveProvider, pick(cfg, cfg.ActiveProvider, *spec))
		}
		for _, spec := range providers.All() {
			if spec.ID == cfg.ActiveProvider {
				continue
			}
			// Only providers that would actually work. Offering a comparison that
			// ends in "no API key" for three of five rows wastes the whole turn.
			if spec.NeedsKey && cfg.KeyFor(spec.ID) == "" {
				skipped = append(skipped, spec.ID+" (no API key)")
				continue
			}
			add(spec.ID, pick(cfg, spec.ID, spec))
		}
		return out, skipped
	}

	for _, a := range args {
		id, model, hasModel := strings.Cut(a, "/")
		spec := providers.Get(id)
		if spec == nil {
			skipped = append(skipped, a+" (unknown provider)")
			continue
		}
		if spec.NeedsKey && cfg.KeyFor(spec.ID) == "" {
			skipped = append(skipped, id+" (no API key)")
			continue
		}
		if !hasModel || model == "" {
			model = pick(cfg, id, *spec)
		}
		add(id, model)
	}
	return out, skipped
}

type target struct{ Provider, Model string }

func (t target) label() string { return t.Provider + "/" + t.Model }

// pick prefers the model this provider was last configured with, then its
// recommended one.
//
// The last configured model is the better guess: someone who set gemini to a
// specific model did so for a reason, and being asked about a different one
// answers a question they did not ask.
func pick(cfg config.Config, id string, spec providers.Spec) string {
	if cfg.ActiveProvider == id && cfg.ActiveModel != "" {
		return cfg.ActiveModel
	}
	return spec.Recommended
}

// compare runs the same question against every target concurrently.
//
// Concurrent because the alternative is waiting five times over for answers that
// do not depend on each other. Every result is collected even when one fails, so
// a single rate-limited provider does not lose the other four answers - which is
// the most likely reason to run this at all.
func compare(ctx context.Context, cfg config.Config, r providerResolver, prompt string, args []string) ([]comparison, []string, error) {
	targets, skipped := candidates(cfg, args)
	if len(targets) == 0 {
		return nil, skipped, fmt.Errorf(
			"nothing to compare against. Give one or more: /different gemini groq/llama-3.3-70b\n" +
				"  with no argument, the active provider plus every other one with a key")
	}

	// The system prompt is the same for every row, so a difference in the
	// answers is a difference in the model rather than in the setup.
	sys := "You are a coding assistant. Answer the question directly and concisely. " +
		"This answer will be compared against another model's, so do not explain your " +
		"reasoning or hedge."
	msgs := []apitypes.Message{
		{Role: apitypes.RoleSystem, Content: sys},
		{Role: apitypes.RoleUser, Content: prompt},
	}

	// One tracker for the whole comparison, not one per row: cost.New reads the
	// persisted day total, so five of them would each decide against the same
	// starting figure and the row costs would not add up.
	tracker := cost.New()

	results := make([]comparison, len(targets))
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		go func(i int, t target) {
			defer wg.Done()
			p, model, err := r.Resolve(t.Provider)
			if err != nil {
				results[i] = comparison{Label: t.label(), Err: err}
				return
			}
			if model == "" {
				model = t.Model
			}
			results[i] = ask(ctx, p, model, t.label(), msgs)
		}(i, t)
	}
	wg.Wait()

	// Costed against the provider that served, not the active one, so a row
	// answered by groq is not priced as ollama and shown as free. This does
	// charge the daily tracker: a comparison is five real model calls and the
	// budget should see them.
	for i := range results {
		if results[i].Measured && results[i].Err == nil {
			results[i].USD = tracker.Add(targets[i].Provider, results[i].PromptT, results[i].ComplT)
		}
	}
	sortFailedLast(results)
	return results, skipped, nil
}

// compareTimeout bounds the whole comparison.
//
// Five providers run at once, and a wedged one would otherwise hold the terminal
// indefinitely with nothing on screen. Per-answer deadlines are the provider's
// business; this is the ceiling on the entire operation.
const compareTimeout = 3 * time.Minute

// runComparison is the slash-command entry point.
//
// It renders the result as one message rather than streaming, deliberately: five
// answers arriving interleaved is unreadable, and the comparison is the output.
// The wait is the price, and the spinner is what covers it.
func runComparison(ctx context.Context, m *Model, prompt string, args []string, full bool) (string, tea.Cmd) {
	cctx, cancel := context.WithTimeout(ctx, compareTimeout)
	defer cancel()
	results, skipped, err := compare(cctx, m.cfg, routerResolver{m.router}, prompt, args)
	if err != nil {
		return "/different: " + err.Error(), nil
	}
	return render(results, skipped, full), nil
}

// providerResolver is what compare needs from the model, declared here so this
// file can be tested without a router.
type providerResolver interface {
	// Resolve returns a provider and, when the resolver knows a better model for
	// it than the caller asked for, that model. An empty model means "use the
	// one the caller chose".
	Resolve(id string) (providers.Provider, string, error)
}

// routerResolver adapts the router.
//
// The model is deliberately left empty: /different picks each target's model, and
// the router only tracks a model for the active provider. Returning the active
// one for every row would quietly compare five different questions.
type routerResolver struct{ r *router.Router }

func (x routerResolver) Resolve(id string) (providers.Provider, string, error) {
	p, err := x.r.Provider(id)
	return p, "", err
}

// render lays the answers out side by side.
//
// Narrow by default. Two answers in a 100-column terminal is legible; five is a
// wall, and a wall is where a comparison stops being read.
func render(results []comparison, skipped []string, full bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "compared %d model(s) on the same prompt\n", len(results))
	if len(skipped) > 0 {
		fmt.Fprintf(&b, "skipped: %s\n", strings.Join(skipped, ", "))
	}
	if !full {
		fmt.Fprintf(&b, "showing first lines only — /different full to read them all\n")
	}
	b.WriteString("\n")

	for i, c := range results {
		if i > 0 {
			b.WriteString(strings.Repeat("─", 60) + "\n")
		}
		if c.Err != nil {
			// An error row is shown, not dropped. Two answers that agree while a
			// third failed is a different conclusion from three agreeing answers.
			fmt.Fprintf(&b, "✗ %s — %v\n", c.Label, c.Err)
			if strings.TrimSpace(c.Answer) == "" {
				continue
			}
		} else {
			fmt.Fprintf(&b, "%s", c.Label)
			if c.Measured {
				fmt.Fprintf(&b, " — %d+%d tok, $%.4f, %.1fs",
					c.PromptT, c.ComplT, c.USD, c.Took.Seconds())
			} else {
				// Said on the row, because an unmarked estimate in a cost
				// comparison is a number nobody should trust.
				fmt.Fprintf(&b, " — ~%d+%d tok (estimated, provider reported none), %.1fs",
					c.PromptT, c.ComplT, c.Took.Seconds())
			}
			b.WriteString("\n")
		}
		answer := strings.TrimRight(c.Answer, "\n")
		if answer == "" {
			b.WriteString("  (no answer)\n")
			continue
		}
		for _, line := range splitLines(answer, full) {
			fmt.Fprintf(&b, "  %s\n", line)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// splitLines caps a preview at six lines, and says so rather than stopping
// mid-sentence.
func splitLines(s string, full bool) []string {
	lines := strings.Split(s, "\n")
	if full || len(lines) <= 6 {
		return lines
	}
	out := append([]string(nil), lines[:6]...)
	return append(out, fmt.Sprintf("… %d more line(s)", len(lines)-6))
}

// sortFailedLast puts the rows that worked first.
//
// A comparison where the errored row is buried at the bottom is read as though
// it succeeded.
func sortFailedLast(cs []comparison) {
	sort.SliceStable(cs, func(i, j int) bool {
		return cs[i].Err == nil && cs[j].Err != nil
	})
}
