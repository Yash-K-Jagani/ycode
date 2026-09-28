package agent

import (
	"context"
	"io"
	"strings"

	"github.com/Yash-K-Jagani/ycode/internal/hooks"
	"github.com/Yash-K-Jagani/ycode/internal/providers"
	"github.com/Yash-K-Jagani/ycode/internal/tools"
	"github.com/Yash-K-Jagani/ycode/pkg/apitypes"
)

// Candidate is one provider/model to try.
type Candidate struct {
	Provider providers.Provider
	Model    string
	// Label is shown to the user when a retry happens. Empty means the primary.
	Label string
}

// ChainResult is the outcome of running a turn against a chain of candidates.
type ChainResult struct {
	Text   string
	Rounds int
	Calls  int
	OKs    int
	Failed int
	// Used is the label of the candidate that produced Text; empty for the
	// primary.
	Used string
	// Winner is the candidate that served the turn, so a caller doing a
	// follow-up (self-correction, verification) retries on the same provider
	// rather than whatever happens to be first.
	Winner Candidate
	// Attempts records every failure, in order, as "provider/model: reason".
	Attempts []string
	// Err is the error from the candidate that produced Text, if it errored
	// after producing output.
	Err error
}

// FellBack reports whether a non-primary candidate served the turn.
func (c ChainResult) FellBack() bool { return c.Used != "" }

// Failure renders every attempt, for an error message that says what was tried.
func (c ChainResult) Failure() error {
	if len(c.Attempts) == 0 {
		if c.Err != nil {
			return c.Err
		}
		return errNoProvider
	}
	msg := "every provider failed:\n" + strings.Join(c.Attempts, "\n")
	if len(c.Attempts) > 1 {
		msg += "\n(fallbacks were tried; see /status for latency and failures)"
	}
	return &chainError{msg: msg}
}

type chainError struct{ msg string }

func (e *chainError) Error() string { return e.msg }

var errNoProvider = &chainError{msg: "no provider was available for this turn — check /status and /connect"}

// RunChain runs one turn against a chain of candidates: the primary, then each
// configured fallback, until one produces output.
//
// This existed as three divergent implementations. The TUI walked the full
// chain; the headless turn tried only fallbacks[0]; and the headless goal run
// had no fallback at all, so a CI goal run died with the primary provider's
// error even when a working fallback was configured. One loop, one set of
// attempt accounting.
//
// onRetry, if non-nil, is called before each non-primary attempt so a streaming
// UI can reset its buffer and say what it is doing.
func RunChain(
	ctx context.Context,
	candidates []Candidate,
	msgs []apitypes.Message,
	reg *tools.Registry,
	allowed []string,
	hk *hooks.Hooks,
	w io.Writer,
	onTool func(name, args, result string, err error),
	rounds int,
	mode string,
	onRetry func(label string),
) ChainResult {
	var out ChainResult
	if len(candidates) == 0 {
		out.Err = errNoProvider
		return out
	}
	for i, cand := range candidates {
		if i > 0 && onRetry != nil {
			onRetry(cand.Label)
		}
		res, err := RunWithRounds(ctx, cand.Provider, cand.Model, msgs, reg, allowed, hk, w, onTool, rounds, mode)
		out.Calls += res.Calls
		out.Rounds += res.Rounds
		out.OKs += res.OKs
		out.Failed += res.Failed
		label := cand.Label
		if label == "" {
			label = "primary"
		}
		if err != nil && res.Text == "" {
			out.Attempts = append(out.Attempts, label+": "+err.Error())
			if i < len(candidates)-1 {
				continue
			}
			out.Err = out.Failure()
			return out
		}
		out.Text = res.Text
		out.Err = err
		out.Winner = cand
		if i > 0 {
			out.Used = cand.Label
		}
		return out
	}
	out.Err = out.Failure()
	return out
}
