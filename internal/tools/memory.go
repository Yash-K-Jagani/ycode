package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Yash-K-Jagani/ycode/internal/config"
	"github.com/Yash-K-Jagani/ycode/internal/db"
)

type MemoryTool struct {
	Dir string
}

func (MemoryTool) Name() string { return "memory" }
func (MemoryTool) Description() string {
	return "Persistent notes across sessions. Args: action (save|recall|list|forget), key, text (for save). Recall/list work in read-only mode."
}
func (MemoryTool) Schema() string {
	return `{"type":"object","required":["action"],"properties":{"action":{"type":"string"},"key":{"type":"string"},"text":{"type":"string"}}}`
}

func (t *MemoryTool) file() string {
	base := t.Dir
	if base == "" {
		base = config.Dir()
	}
	return filepath.Join(base, "memory.json")
}

func (t *MemoryTool) load() map[string]string {
	// Dir override (tests) always uses the JSON file; otherwise prefer SQLite.
	if t.Dir == "" {
		if conn := db.Shared(); conn != nil {
			if raw, ok := db.KVGet(conn, "memory/map"); ok {
				m := map[string]string{}
				if _ = json.Unmarshal([]byte(raw), &m); m != nil {
					return m
				}
				return map[string]string{}
			}
			// one-time import from legacy file
			m := map[string]string{}
			if data, err := os.ReadFile(t.file()); err == nil {
				_ = json.Unmarshal(data, &m)
				_ = t.persist(m)
			}
			return m
		}
	}
	data, _ := os.ReadFile(t.file())
	m := map[string]string{}
	_ = json.Unmarshal(data, &m)
	return m
}

func (t *MemoryTool) save(m map[string]string) error {
	return t.persist(m)
}

func (t *MemoryTool) persist(m map[string]string) error {
	if t.Dir == "" {
		if conn := db.Shared(); conn != nil {
			data, _ := json.Marshal(m)
			if err := db.KVSet(conn, "memory/map", string(data)); err == nil {
				return nil
			}
		}
	}
	_ = os.MkdirAll(filepath.Dir(t.file()), 0o755)
	data, _ := json.MarshalIndent(m, "", "  ")
	return os.WriteFile(t.file(), data, 0o644)
}

func (t *MemoryTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Action string `json:"action"`
		Key    string `json:"key"`
		Text   string `json:"text"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return "", err
	}
	m := t.load()
	switch a.Action {
	case "list":
		if len(m) == 0 {
			return "(no memories)", nil
		}
		var keys []string
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return "keys: " + strings.Join(keys, ", "), nil
	case "recall":
		v, ok := m[a.Key]
		if !ok {
			return "", fmt.Errorf("no memory %q", a.Key)
		}
		return v, nil
	case "save", "forget":
		if IsReadOnly(ctx) {
			return "", fmt.Errorf("memory %s is blocked in read-only mode", a.Action)
		}
		if a.Key == "" {
			return "", fmt.Errorf("key required")
		}
		if a.Action == "save" {
			m[a.Key] = a.Text
		} else {
			delete(m, a.Key)
		}
		if err := t.save(m); err != nil {
			return "", err
		}
		return a.Action + "d " + a.Key, nil
	default:
		return "", fmt.Errorf("unknown memory action %q", a.Action)
	}
}
