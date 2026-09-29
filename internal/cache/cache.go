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

// loadSQL reads the cache out of SQLite, dropping rows that have already
// expired.
//
// The expiry was done in Go afterwards, by pruneMem, which meant every row was
// read first - prompt, answer and a JSON-encoded vector of several hundred
// floats each - and then most of them thrown away. The table has no size bound
// on disk, so a user who ran for a while had a startup cost proportional to
// everything they had ever asked, of which only the last week was usable.
// Filtering in the WHERE clause leaves only live rows crossing the boundary.
func loadSQL(conn *sql.DB) []item {
	cut := time.Now().Add(-ttl).Format(time.RFC3339)
	rows, err := conn.Query(`SELECT prompt,answer,vec,at,provider,model FROM cache_items WHERE at > ?`, cut)
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

// persistSQL rewrites the whole table.
//
// This is a full rewrite - DELETE followed by an INSERT of everything - which
// is right for a one-time import from the legacy JSON file and wrong for every
// subsequent store. A cache row carries a JSON-encoded embedding of several
// hundred floats, and the table holds up to maxItems of them, so rewriting it
// per stored answer meant several megabytes of writes and a WAL that grew by
// the whole table each time, to record one new row. putSQL and pruneSQL do the
// incremental version.
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

// putSQL writes one item. The primary key is (provider, model, prompt), which
// is the same identity the in-memory cache uses, so a repeated store of the
// same prompt updates the row rather than accumulating duplicates.
func (c *Cache) putSQL(it item) {
	vec, _ := json.Marshal(it.Vec)
	_, _ = c.conn.Exec(`INSERT INTO cache_items(provider,model,prompt,answer,vec,at) VALUES(?,?,?,?,?,?)
		ON CONFLICT(provider,model,prompt) DO UPDATE SET answer=excluded.answer,
		vec=excluded.vec, at=excluded.at`,
		it.Provider, it.Model, it.Prompt, it.Answer, string(vec), it.At.Format(time.RFC3339))
}

// pruneSQL deletes the rows that have expired.
//
// Without this the table only ever grows, because loadSQL filters expired rows
// out on read but nothing removed them. The old code relied on the full rewrite
// in persistSQL to do it implicitly; now that stores are incremental, the
// deletion has to be explicit or the file grows forever.
func (c *Cache) pruneSQL() {
	cut := time.Now().Add(-ttl).Format(time.RFC3339)
	_, _ = c.conn.Exec(`DELETE FROM cache_items WHERE at <= ?`, cut)
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

// pruneMem drops expired and surplus items, and reports whether it removed
// anything. The caller uses that to decide whether the table needs deleting
// from, since a prune that dropped nothing means no row can have expired.
func (c *Cache) pruneMem() bool {
	cut := time.Now().Add(-ttl)
	before := len(c.items)
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
	return len(c.items) != before
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
		// A cached entry from a different embedding model is a different
		// dimension, which is not comparable. Skipping it is correct: the
		// alternative is matching against part of a vector and answering
		// from an unrelated prompt.
		s, err := embed.Cosine(vecs[0], it.Vec)
		if err != nil {
			continue
		}
		if s > bestS {
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
	if c.conn != nil {
		// One row, not the whole table. See persistSQL for why.
		c.putSQL(c.items[len(c.items)-1])
		// Expiry is cleaned up here rather than being implied by a full
		// rewrite, which no longer happens. It is not conditional on the
		// in-memory prune having dropped something: the table and the slice
		// can disagree - a row inserted by an older version, or a crash
		// between the two writes - and tying the delete to a slice that is
		// already correct would leave those rows forever. The delete is a
		// range scan on cache_items_at, so it is cheap either way.
		c.pruneSQL()
	} else {
		c.persistFile()
	}
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
