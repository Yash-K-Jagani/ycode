package cost

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Yash-K-Jagani/ycode/internal/config"
	"github.com/Yash-K-Jagani/ycode/internal/db"
)

// USD per 1K tokens (prompt, completion). Local = free.
var pricing = map[string][2]float64{
	"gemini":     {0.000075, 0.0003},
	"openrouter": {0.0001, 0.0003},
	"groq":       {0.00005, 0.00008},
	"ollama":     {0, 0},
}

type dayEntry struct {
	PromptTok int     `json:"prompt_tokens"`
	ComplTok  int     `json:"completion_tokens"`
	USD       float64 `json:"usd"`
}

type Tracker struct {
	mu   sync.Mutex
	file string
	conn *sql.DB
	days map[string]*dayEntry
}

func New() *Tracker {
	t := &Tracker{file: filepath.Join(config.Dir(), "cost.json"), days: map[string]*dayEntry{}}
	t.conn = db.Shared()
	if t.conn != nil {
		if raw, ok := db.KVGet(t.conn, "cost/days"); ok {
			_ = json.Unmarshal([]byte(raw), &t.days)
		} else if data, err := os.ReadFile(t.file); err == nil {
			// one-time import from legacy JSON
			_ = json.Unmarshal(data, &t.days)
			t.persist()
		}
		if t.days == nil {
			t.days = map[string]*dayEntry{}
		}
		return t
	}
	data, _ := os.ReadFile(t.file)
	_ = json.Unmarshal(data, &t.days)
	if t.days == nil {
		t.days = map[string]*dayEntry{}
	}
	return t
}

func (t *Tracker) Add(provider string, promptTok, complTok int) float64 {
	p := pricing[provider]
	usd := float64(promptTok)/1000*p[0] + float64(complTok)/1000*p[1]
	t.mu.Lock()
	defer t.mu.Unlock()
	d := time.Now().Format("2006-01-02")
	e := t.days[d]
	if e == nil {
		e = &dayEntry{}
		t.days[d] = e
	}
	e.PromptTok += promptTok
	e.ComplTok += complTok
	e.USD += usd
	t.persist()
	return usd
}

func (t *Tracker) persist() {
	data, _ := json.Marshal(t.days)
	if t.conn != nil {
		if err := db.KVSet(t.conn, "cost/days", string(data)); err == nil {
			return
		}
	}
	_ = os.MkdirAll(config.Dir(), 0o755)
	_ = os.WriteFile(t.file, data, 0o644)
}

func (t *Tracker) Today() (prompt, compl int, usd float64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e := t.days[time.Now().Format("2006-01-02")]
	if e == nil {
		return 0, 0, 0
	}
	return e.PromptTok, e.ComplTok, e.USD
}
