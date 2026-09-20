package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type todoItem struct {
	ID      int    `json:"id"`
	Text    string `json:"text"`
	Done    bool   `json:"done"`
	Created string `json:"created"`
}

type TodoTool struct{ Workdir string }

func (TodoTool) Name() string { return "todo" }
func (TodoTool) Description() string {
	return "Agent task list for multi-step work. Args: action (list|add|done|clear), text (for add), id (for done)."
}
func (TodoTool) Schema() string {
	return `{"type":"object","required":["action"],"properties":{"action":{"type":"string"},"text":{"type":"string"},"id":{"type":"integer"}}}`
}

func (t *TodoTool) file() string { return filepath.Join(t.Workdir, ".ycode", "todos.json") }

func (t *TodoTool) load() []todoItem {
	data, _ := os.ReadFile(t.file())
	var items []todoItem
	_ = json.Unmarshal(data, &items)
	return items
}

func (t *TodoTool) save(items []todoItem) error {
	_ = os.MkdirAll(filepath.Join(t.Workdir, ".ycode"), 0o755)
	data, _ := json.MarshalIndent(items, "", "  ")
	return os.WriteFile(t.file(), data, 0o644)
}

func (t *TodoTool) Run(_ context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Action string `json:"action"`
		Text   string `json:"text"`
		ID     int    `json:"id"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return "", err
	}
	items := t.load()
	switch a.Action {
	case "list":
		if len(items) == 0 {
			return "(empty todo list)", nil
		}
		sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
		var b strings.Builder
		for _, it := range items {
			mark := "[ ]"
			if it.Done {
				mark = "[x]"
			}
			fmt.Fprintf(&b, "%s %d. %s\n", mark, it.ID, it.Text)
		}
		return b.String(), nil
	case "add":
		if strings.TrimSpace(a.Text) == "" {
			return "", fmt.Errorf("add needs text")
		}
		id := 1
		for _, it := range items {
			if it.ID >= id {
				id = it.ID + 1
			}
		}
		items = append(items, todoItem{ID: id, Text: a.Text, Created: time.Now().Format(time.RFC3339)})
		if err := t.save(items); err != nil {
			return "", err
		}
		return fmt.Sprintf("added #%d", id), nil
	case "done":
		found := false
		for i, it := range items {
			if it.ID == a.ID {
				items[i].Done = true
				found = true
			}
		}
		if !found {
			return "", fmt.Errorf("no todo #%d", a.ID)
		}
		if err := t.save(items); err != nil {
			return "", err
		}
		return fmt.Sprintf("done #%d", a.ID), nil
	case "clear":
		_ = os.Remove(t.file())
		return "cleared", nil
	default:
		return "", fmt.Errorf("unknown todo action %q", a.Action)
	}
}
