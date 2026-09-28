// Package fakeprovider is a scripted providers.Provider for tests.
//
// It exists because almost every interesting behaviour in the harness — the
// agent tool loop, goal-mode verdicts, provider fallbacks, context compaction,
// the self-correction retry — is a conversation, and driving those
// conversations against a real model makes tests slow, flaky and unable to
// assert on a specific failure. A scripted provider makes the whole turn
// pipeline deterministic, and records what the model was actually shown.
//
// It is test infrastructure: the package compiles into the binary's dependency
// graph, so keep it dependency-free and side-effect-free.
package fakeprovider

import (
	"context"
	"io"
	"strings"
	"sync"

	"github.com/Yash-K-Jagani/ycode/pkg/apitypes"
)

// Provider replays a fixed script of replies. The last reply repeats once the
// script is exhausted, so a test only has to script the interesting turns.
type Provider struct {
	mu      sync.Mutex
	replies []string
	seen    [][]apitypes.Message
	failAt  int // if > 0, Stream returns an error on the Nth request (1-based)
}

// New returns a provider that answers with replies in order.
func New(replies ...string) *Provider {
	if len(replies) == 0 {
		replies = []string{""}
	}
	return &Provider{replies: replies}
}

// FailAt makes the nth request (1-based) return an error instead of a reply,
// for testing fallback and error paths.
func (p *Provider) FailAt(n int) *Provider {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failAt = n
	return p
}

func (p *Provider) Name() string { return "fake" }

func (p *Provider) ListModels(context.Context) ([]apitypes.ModelInfo, error) {
	return []apitypes.ModelInfo{{ID: "fake-model", Provider: "fake", Status: "installed"}}, nil
}

func (p *Provider) Stream(_ context.Context, _ string, msgs []apitypes.Message, w io.Writer) (apitypes.StreamChunk, error) {
	p.mu.Lock()
	snapshot := make([]apitypes.Message, len(msgs))
	copy(snapshot, msgs)
	p.seen = append(p.seen, snapshot)
	n := len(p.seen)
	var reply string
	if n-1 < len(p.replies) {
		reply = p.replies[n-1]
	} else {
		reply = p.replies[len(p.replies)-1]
	}
	fail := p.failAt > 0 && p.failAt == n
	p.mu.Unlock()

	if fail {
		return apitypes.StreamChunk{}, errScripted
	}
	if w != nil && reply != "" {
		_, _ = io.WriteString(w, reply)
	}
	return apitypes.StreamChunk{Delta: reply, Done: true}, nil
}

func (p *Provider) Complete(_ context.Context, _ string, msgs []apitypes.Message) (string, error) {
	chunk, err := p.Stream(context.Background(), "fake-model", msgs, nil)
	return chunk.Delta, err
}

// Requests is how many times the provider was called.
func (p *Provider) Requests() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.seen)
}

// Seen returns every request's message list, in order.
func (p *Provider) Seen() [][]apitypes.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([][]apitypes.Message, len(p.seen))
	copy(out, p.seen)
	return out
}

// LastSystem returns the system prompt of the most recent request.
func (p *Provider) LastSystem() string {
	msgs := p.Seen()
	if len(msgs) == 0 {
		return ""
	}
	return msgs[len(msgs)-1][0].Content
}

// LastRole is the role of the final message in the most recent request. A
// continuation nudge must arrive as a system message, not a user one.
func (p *Provider) LastRole() apitypes.Role {
	msgs := p.Seen()
	if len(msgs) == 0 || len(msgs[len(msgs)-1]) == 0 {
		return ""
	}
	return msgs[len(msgs)-1][len(msgs[len(msgs)-1])-1].Role
}

// SystemCount is how many requests carried a system prompt (i.e. how many
// turns were built).
func (p *Provider) SystemCount() int { return p.Requests() }

// Scripted is the error a FailAt provider returns.
type scriptedError struct{}

func (scriptedError) Error() string { return "fakeprovider: scripted failure" }

var errScripted = scriptedError{}

// ErrScripted is exported for tests that assert on the failure text.
var ErrScripted = errScripted

// HasToolCalls is a test convenience: true when s looks like a real tagged
// tool call rather than prose.
func HasToolCalls(s string) bool { return strings.Contains(s, "<tool:") }
