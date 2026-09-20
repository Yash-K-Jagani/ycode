package sessions

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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

func (s *Session) Save() error {
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

func List() ([]Session, error) {
	entries, err := os.ReadDir(dir())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Session
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir(), e.Name()))
		if err != nil {
			continue
		}
		var s Session
		if err := json.Unmarshal(data, &s); err != nil {
			continue
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, nil
}

func Load(id string) (*Session, error) {
	data, err := os.ReadFile(filepath.Join(dir(), id+".json"))
	if err != nil {
		return nil, err
	}
	var s Session
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func EstimateTokens(msgs []apitypes.Message) int {
	n := 0
	for _, m := range msgs {
		n += len(m.Content) / 4
	}
	return n
}
