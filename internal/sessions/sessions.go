package sessions

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
