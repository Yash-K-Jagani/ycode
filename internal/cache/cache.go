package cache

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Yash-K-Jagani/ycode/internal/config"
	"github.com/Yash-K-Jagani/ycode/internal/db"
	"github.com/Yash-K-Jagani/ycode/internal/embed"
)

const (
	threshold = 0.985
	ttl       = 7 * 24 * time.Hour
	maxItems  = 500
)

type item struct {
	Prompt   string    `json:"prompt"`
	Answer   string    `json:"answer"`
	Vec      []float64 `json:"vec"`
	At       time.Time `json:"at"`
	Provider string    `json:"provider"`
	Model    string    `json:"model"`
}

type Cache struct {
	mu     sync.Mutex
	file   string
	conn   *sql.DB
	items  []item
	hits   int
	misses int
	embed  func(ctx context.Context, inputs []string) ([][]float64, error)
}

func filePath() string { return filepath.Join(config.Dir(), "cache.json") }

func New(embedFn func(ctx context.Context, inputs []string) ([][]float64, error)) *Cache {
	c := &Cache{file: filePath(), embed: embedFn, conn: db.Shared()}
	if c.conn != nil {
		c.items = loadSQL(c.conn)
		if len(c.items) == 0 {
			// one-time import from legacy JSON
			if data, err := os.ReadFile(c.file); err == nil {
				_ = json.Unmarshal(data, &c.items)
				if len(c.items) > 0 {
					c.persistSQL()
				}
			}
		}
		if c.items == nil {
			c.items = nil
		}
		c.pruneMem()
		return c
	}
	return NewAt(c.file, embedFn)
}

func loadSQL(conn *sql.DB) []item {
	rows, err := conn.Query(`SELECT prompt,answer,vec,at,provider,model FROM cache_items`)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()
	var out []item
	for rows.Next() {
		var it item
		var vec, at string
		if err := rows.Scan(&it.Prompt, &it.Answer, &vec, &at, &it.Provider, &it.Model); err != nil {
			continue
		}
		_ = json.Unmarshal([]byte(vec), &it.Vec)
		it.At, _ = time.Parse(time.RFC3339, at)
		out = append(out, it)
	}
	return out
}

func (c *Cache) persistSQL() {
	tx, err := c.conn.Begin()
	if err != nil {
		return
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM cache_items`); err != nil {
		return
	}
	for _, it := range c.items {
		vec, _ := json.Marshal(it.Vec)
		if _, err := tx.Exec(`INSERT INTO cache_items(provider,model,prompt,answer,vec,at) VALUES(?,?,?,?,?,?)`,
			it.Provider, it.Model, it.Prompt, it.Answer, string(vec), it.At.Format(time.RFC3339)); err != nil {
			return
		}
	}
	_ = tx.Commit()
}

func (c *Cache) persistFile() {
	data, _ := json.Marshal(c.items)
	_ = os.MkdirAll(config.Dir(), 0o755)
	_ = os.WriteFile(c.file, data, 0o644)
}

func (c *Cache) persist() {
	if c.conn != nil {
		c.persistSQL()
		return
	}
	c.persistFile()
}

func NewAt(path string, embedFn func(ctx context.Context, inputs []string) ([][]float64, error)) *Cache {
	c := &Cache{file: path, embed: embedFn}
	data, _ := os.ReadFile(c.file)
	_ = json.Unmarshal(data, &c.items)
	if c.items == nil {
		c.items = nil
	}
	c.pruneMem()
	return c
}

func (c *Cache) pruneMem() {
	cut := time.Now().Add(-ttl)
	kept := c.items[:0]
	for _, it := range c.items {
		if it.At.After(cut) {
			kept = append(kept, it)
		}
	}
	c.items = kept
	if len(c.items) > maxItems {
		c.items = c.items[len(c.items)-maxItems:]
	}
}

func key(prompt, provider, model string) string {
	h := sha1.Sum([]byte(provider + "\x00" + model + "\x00" + prompt))
	return fmt.Sprintf("%x", h[:])
}

// Lookup returns a cached answer on exact match or high cosine similarity.
func (c *Cache) Lookup(ctx context.Context, prompt, provider, model string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := key(prompt, provider, model)
	for _, it := range c.items {
		if key(it.Prompt, it.Provider, it.Model) == k {
			c.hits++
			return it.Answer, true
		}
	}
	vecs, err := c.embed(ctx, []string{prompt})
	if err != nil || len(vecs) == 0 {
		c.misses++
		return "", false
	}
	best, bestS := -1, 0.0
	for i, it := range c.items {
		if it.Provider != provider || len(it.Vec) == 0 {
			continue
		}
		if s := embed.Cosine(vecs[0], it.Vec); s > bestS {
			best, bestS = i, s
		}
	}
	if best >= 0 && bestS >= threshold {
		c.hits++
		return c.items[best].Answer, true
	}
	c.misses++
	return "", false
}

func (c *Cache) Store(ctx context.Context, prompt, answer, provider, model string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	vecs, err := c.embed(ctx, []string{prompt})
	var vec []float64
	if err == nil && len(vecs) > 0 {
		vec = vecs[0]
	}
	c.items = append(c.items, item{Prompt: prompt, Answer: answer, Vec: vec, At: time.Now(), Provider: provider, Model: model})
	c.pruneMem()
	c.persist()
}

func (c *Cache) Stats() (hits, misses, size int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hits, c.misses, len(c.items)
}

func (c *Cache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = nil
	if c.conn != nil {
		_, _ = c.conn.Exec(`DELETE FROM cache_items`)
		return
	}
	_ = os.Remove(c.file)
}
