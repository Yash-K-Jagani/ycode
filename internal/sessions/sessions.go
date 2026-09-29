package sessions

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/Yash-K-Jagani/ycode/internal/textutil"
	"time"

	"github.com/Yash-K-Jagani/ycode/internal/config"
	"github.com/Yash-K-Jagani/ycode/pkg/apitypes"
)

type Session struct {
	ID        string             `json:"id"`
	Title     string             `json:"title"`
	CreatedAt time.Time          `json:"created_at"`
	UpdatedAt time.Time          `json:"updated_at"`
	Provider  string             `json:"provider"`
	Model     string             `json:"model"`
	Messages  []apitypes.Message `json:"messages"`
	// MessageCount is how many messages the session has. It is stored beside
	// the messages rather than derived from them, because the only callers that
	// want it - the search filter, the picker - are exactly the ones that
	// should not have to load every transcript in the database to count them.
	//
	// It is not part of the session file: there it is trivially len(Messages),
	// and duplicating it would be a second thing to keep correct. Populated by
	// every read path.
	MessageCount int `json:"-"`
}

func dir() string { return filepath.Join(config.Dir(), "sessions") }

// newID returns a session id that stays unique when the clock does not.
//
// Ids used to be time.Now().UnixNano() and nothing else. That is unique only
// if the system clock's resolution is finer than the gap between two calls,
// which is not something a program can rely on: a macOS runner hands out
// microsecond-resolution wall time, and several sessions created in a tight
// loop get the same nanosecond. The ids then collide, and because a session is
// stored as <id>.json, the second save silently overwrites the first - a user's
// session history disappearing with no error anywhere.
//
// The random suffix also makes ids unique across processes, which matters when
// two ycode windows start at once. The character set is hex plus a dash, since
// the id becomes a filename.
func newID(now time.Time) string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand does not fail in practice. If it somehow did, a
		// process-local counter is still better than a bare timestamp.
		return fmt.Sprintf("%d-%d", now.UnixNano(), atomic.AddUint64(&idFallback, 1))
	}
	return fmt.Sprintf("%d-%s", now.UnixNano(), hex.EncodeToString(b[:]))
}

var idFallback uint64

func New(provider, model string) *Session {
	now := time.Now()
	return &Session{
		ID:        newID(now),
		Title:     "session " + now.Format("2006-01-02 15:04"),
		CreatedAt: now,
		UpdatedAt: now,
		Provider:  provider,
		Model:     model,
	}
}

func (s *Session) Save() error { return backend.Save(s) }

func List() ([]Session, error) { return backend.List() }

// ListMeta returns sessions without their messages, for callers that only need
// identity - filtering, pruning, counting. See Store.ListMeta for why the
// distinction is worth making.
func ListMeta() ([]Session, error) { return backend.ListMeta() }

func Load(id string) (*Session, error) { return backend.Load(id) }

func Delete(id string) error { return backend.Delete(id) }

// PruneIDs returns IDs beyond the newest keep sessions (list must be newest-first).
func PruneIDs(list []Session, keep int) []string {
	if keep < 0 {
		keep = 0
	}
	var out []string
	for i, s := range list {
		if i >= keep {
			out = append(out, s.ID)
		}
	}
	return out
}

// MaybeAutoTitle replaces timestamp titles with a short derived title once
// the first exchange exists. Returns true when it changed anything.
func MaybeAutoTitle(s *Session) bool {
	if !strings.HasPrefix(s.Title, "session ") {
		return false
	}
	for _, m := range s.Messages {
		if m.Role != apitypes.RoleUser {
			continue
		}
		if t := DeriveTitle(m.Content); t != "" {
			s.Title = t
			return true
		}
		return false
	}
	return false
}

// DeriveTitle condenses a user message into a ≤40-char title.
func DeriveTitle(text string) string {
	t := strings.TrimSpace(text)
	t = strings.TrimPrefix(t, "/")
	if i := strings.IndexByte(t, '\n'); i >= 0 {
		t = t[:i]
	}
	t = strings.Join(strings.Fields(t), " ")
	if t == "" {
		return ""
	}
	if len([]rune(t)) > 40 {
		t = textutil.Truncate(t, 40)
	}
	return t
}

// Fork duplicates a session under a new ID (history shared, future diverges).
func Fork(id string) (*Session, error) {
	src, err := backend.Load(id)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	fork := &Session{
		ID:        newID(now),
		Title:     src.Title + " (fork)",
		CreatedAt: now,
		UpdatedAt: now,
		Provider:  src.Provider,
		Model:     src.Model,
		Messages:  append([]apitypes.Message(nil), src.Messages...),
	}
	if err := backend.Save(fork); err != nil {
		return nil, err
	}
	return fork, nil
}

// Export renders the transcript as markdown (tool calls collapsed).
func Export(s *Session) string {
	var b strings.Builder
	fmt.Fprintf(&b, "---\ntitle: %s\nprovider: %s\nmodel: %s\nexported: %s\n---\n\n",
		s.Title, s.Provider, s.Model, time.Now().Format(time.RFC3339))
	for _, m := range s.Messages {
		switch m.Role {
		case apitypes.RoleUser:
			b.WriteString("## you\n\n" + m.Content + "\n\n")
		case apitypes.RoleAssistant:
			b.WriteString("## assistant\n\n" + stripToolJSON(m.Content) + "\n\n")
		}
	}
	return b.String()
}

// stripToolJSON collapses <tool:...>...</tool:...> blocks to one line each.
func stripToolJSON(s string) string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "<tool:") || strings.HasPrefix(t, "<tool_result:") {
			name := t
			if i := strings.IndexAny(name, "> "); i >= 0 {
				name = name[:i] + ">"
			}
			out = append(out, "`"+name+"`")
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func saveJSON(s *Session) error {
	if err := os.MkdirAll(dir(), 0o755); err != nil {
		return err
	}
	s.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir(), s.ID+".tmp")
	dst := filepath.Join(dir(), s.ID+".json")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

func EstimateTokens(msgs []apitypes.Message) int {
	n := 0
	for _, m := range msgs {
		n += len(m.Content) / 4
	}
	return n
}
