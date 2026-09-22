package sessions

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
}

func dir() string { return filepath.Join(config.Dir(), "sessions") }

func New(provider, model string) *Session {
	now := time.Now()
	return &Session{
		ID:        fmt.Sprintf("%d", now.UnixNano()),
		Title:     "session " + now.Format("2006-01-02 15:04"),
		CreatedAt: now,
		UpdatedAt: now,
		Provider:  provider,
		Model:     model,
	}
}

func (s *Session) Save() error { return backend.Save(s) }

func List() ([]Session, error) { return backend.List() }

func Load(id string) (*Session, error) { return backend.Load(id) }

// Fork duplicates a session under a new ID (history shared, future diverges).
func Fork(id string) (*Session, error) {
	src, err := backend.Load(id)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	fork := &Session{
		ID:        fmt.Sprintf("%d", now.UnixNano()),
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
