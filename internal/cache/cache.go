package cache

import (
	"context"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Yash-K-Jagani/ycode/internal/config"
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
	items  []item
	hits   int
	misses int
	embed  func(ctx context.Context, inputs []string) ([][]float64, error)
}

func filePath() string { return filepath.Join(config.Dir(), "cache.json") }

func New(embedFn func(ctx context.Context, inputs []string) ([][]float64, error)) *Cache {
	return NewAt(filePath(), embedFn)
}

func NewAt(path string, embedFn func(ctx context.Context, inputs []string) ([][]float64, error)) *Cache {
	c := &Cache{file: path, embed: embedFn}
	data, _ := os.ReadFile(c.file)
	_ = json.Unmarshal(data, &c.items)
	if c.items == nil {
		c.items = nil
	}
	c.prune()
	return c
}

func (c *Cache) prune() {
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
	c.prune()
	data, _ := json.Marshal(c.items)
	_ = os.MkdirAll(config.Dir(), 0o755)
	_ = os.WriteFile(c.file, data, 0o644)
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
	_ = os.Remove(c.file)
}
