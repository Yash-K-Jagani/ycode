package cache

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Yash-K-Jagani/ycode/internal/db"
)

// sqlCache builds a cache backed by a real SQLite file, which is the only way
// to exercise the load and persist paths rather than the in-memory ones.
func sqlCache(t *testing.T) *Cache {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return &Cache{
		file:  filepath.Join(t.TempDir(), "cache.json"),
		conn:  conn,
		embed: fakeEmbed,
	}
}

func countRows(t *testing.T, c *Cache) int {
	t.Helper()
	var n int
	if err := c.conn.QueryRow(`SELECT COUNT(*) FROM cache_items`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A store used to rewrite the entire table - DELETE plus an INSERT of every
// row - to record one new answer. With a few hundred rows each carrying a
// JSON vector that is megabytes of writes per turn, and the WAL growing by the
// whole table each time.
func TestStoreWritesOneRowNotTheTable(t *testing.T) {
	c := sqlCache(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		c.Store(ctx, "prompt number "+string(rune('a'+i)), "answer", "ollama", "m")
	}
	if n := countRows(t, c); n != 5 {
		t.Fatalf("table has %d rows, want 5", n)
	}
	if len(c.items) != 5 {
		t.Fatalf("memory has %d items", len(c.items))
	}
}

// The same prompt must update its row rather than accumulating copies, since
// the primary key is the identity the in-memory cache uses too.
func TestRepeatedStoreUpdatesRatherThanDuplicates(t *testing.T) {
	c := sqlCache(t)
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		c.Store(ctx, "same prompt", "answer", "ollama", "m")
	}
	if n := countRows(t, c); n != 1 {
		t.Fatalf("table has %d rows, want 1; a repeated store must upsert", n)
	}
	// The memory list still grows, which is pre-existing behaviour: pruning is
	// by age and cap, not by identity.
	if len(c.items) != 4 {
		t.Fatalf("memory has %d items, want 4", len(c.items))
	}
}

// A different model is a different cache entry, so the key has to include it.
func TestStoreKeysOnProviderAndModel(t *testing.T) {
	c := sqlCache(t)
	ctx := context.Background()
	c.Store(ctx, "same prompt", "a", "ollama", "m1")
	c.Store(ctx, "same prompt", "b", "ollama", "m2")
	c.Store(ctx, "same prompt", "c", "gemini", "m1")
	if n := countRows(t, c); n != 3 {
		t.Fatalf("table has %d rows, want 3", n)
	}
}

// loadSQL used to read every row and discard the expired ones in Go, so
// startup cost grew with everything ever asked rather than with what is still
// usable. Expired rows must not cross the boundary at all.
func TestExpiredRowsAreNotLoaded(t *testing.T) {
	c := sqlCache(t)
	ctx := context.Background()
	c.Store(ctx, "fresh prompt", "fresh", "ollama", "m")

	// Age one row past the TTL, the way a week-old database would look.
	old := time.Now().Add(-ttl - time.Hour).Format(time.RFC3339)
	if _, err := c.conn.Exec(`INSERT INTO cache_items(provider,model,prompt,answer,vec,at)
		VALUES('ollama','m','stale prompt','stale','[]',?)`, old); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, c); n != 2 {
		t.Fatalf("setup: table has %d rows, want 2", n)
	}

	loaded := loadSQL(c.conn)
	if len(loaded) != 1 {
		t.Fatalf("loadSQL returned %d items, want 1; the expired row was read anyway", len(loaded))
	}
	if loaded[0].Prompt != "fresh prompt" {
		t.Fatalf("loaded the wrong row: %q", loaded[0].Prompt)
	}
}

// Nothing removed expired rows from the table once the full rewrite stopped
// doing it implicitly, so the file would have grown forever. Storing something
// has to clean up.
func TestExpiredRowsAreEventuallyDeleted(t *testing.T) {
	c := sqlCache(t)
	ctx := context.Background()
	old := time.Now().Add(-ttl - time.Hour).Format(time.RFC3339)
	if _, err := c.conn.Exec(`INSERT INTO cache_items(provider,model,prompt,answer,vec,at)
		VALUES('ollama','m','stale prompt','stale','[]',?)`, old); err != nil {
		t.Fatal(err)
	}
	c.Store(ctx, "fresh prompt", "fresh", "ollama", "m")
	if n := countRows(t, c); n != 1 {
		t.Fatalf("table has %d rows, want 1; the expired row is never deleted", n)
	}
	if _, ok := c.Lookup(context.Background(), "stale prompt", "ollama", "m"); ok {
		t.Fatal("an expired entry is still being served")
	}
}

// A round trip through the database has to preserve the answer, or the cache
// silently becomes a way to lose replies.
func TestStoredAnswersSurviveAReload(t *testing.T) {
	c := sqlCache(t)
	ctx := context.Background()
	c.Store(ctx, "remember this", "the answer", "ollama", "m")

	fresh := &Cache{conn: c.conn, embed: fakeEmbed}
	fresh.items = loadSQL(c.conn)
	if len(fresh.items) != 1 {
		t.Fatalf("reloaded %d items", len(fresh.items))
	}
	if fresh.items[0].Answer != "the answer" {
		t.Fatalf("answer = %q", fresh.items[0].Answer)
	}
	if len(fresh.items[0].Vec) == 0 {
		t.Fatal("the vector was not persisted, so semantic lookup cannot work after a restart")
	}
}

// The vectors are what make a row large, and they must be written and read
// faithfully rather than truncated.
func TestVectorsRoundTripExactly(t *testing.T) {
	c := sqlCache(t)
	ctx := context.Background()
	want := []float64{0.1, -0.25, 1e-9, 3.5, 0}
	c.items = nil
	c.mu.Lock()
	c.items = append(c.items, item{Prompt: "p", Answer: "a", Vec: want, At: time.Now(), Provider: "ollama", Model: "m"})
	c.putSQL(c.items[0])
	c.mu.Unlock()

	got := loadSQL(c.conn)
	if len(got) != 1 {
		t.Fatalf("loaded %d", len(got))
	}
	if len(got[0].Vec) != len(want) {
		t.Fatalf("vector length %d, want %d", len(got[0].Vec), len(want))
	}
	for i := range want {
		if got[0].Vec[i] != want[i] {
			t.Fatalf("vec[%d] = %v, want %v", i, got[0].Vec[i], want[i])
		}
	}
	_ = ctx
}

func TestPruneMemReportsWhetherItRemovedAnything(t *testing.T) {
	c := &Cache{}
	now := time.Now()
	c.items = []item{
		{At: now},
		{At: now.Add(-ttl - time.Hour)},
		{At: now},
	}
	if !c.pruneMem() {
		t.Fatal("pruneMem dropped an expired item and said it did not")
	}
	c2 := &Cache{items: []item{{At: now}, {At: now}}}
	if c2.pruneMem() {
		t.Fatal("pruneMem reported a removal when there was nothing to remove")
	}
}

// A row whose prompt is the empty string must still round trip, since the
// column is part of the primary key.
func TestEmptyPromptRoundTrips(t *testing.T) {
	c := sqlCache(t)
	c.mu.Lock()
	c.items = append(c.items, item{Prompt: "", Answer: "a", At: time.Now(), Provider: "ollama", Model: "m"})
	c.putSQL(c.items[0])
	c.mu.Unlock()
	got := loadSQL(c.conn)
	if len(got) != 1 || got[0].Answer != "a" {
		t.Fatalf("got %+v", got)
	}
}

func TestPutSQLToleratesANilConn(t *testing.T) {
	// The JSON fallback path has no connection, and Store must not panic on it.
	c := &Cache{embed: fakeEmbed}
	c.items = nil
	c.Store(context.Background(), "p", "a", "ollama", "m")
	if len(c.items) != 1 {
		t.Fatalf("items = %d", len(c.items))
	}
	if _, ok := c.Lookup(context.Background(), "p", "ollama", "m"); !ok {
		t.Fatal("the in-memory cache stopped working")
	}
}

func BenchmarkCacheStore(b *testing.B) {
	conn, err := db.Open(filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	c := &Cache{conn: conn, embed: fakeEmbed}
	ctx := context.Background()
	// Pre-fill so the benchmark measures storing into a realistic table rather
	// than into an empty one.
	for i := 0; i < 300; i++ {
		c.Store(ctx, "seed prompt "+strings.Repeat("x", i%40)+string(rune('a'+i%26)), "answer", "ollama", "m")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Store(ctx, "new prompt "+string(rune('A'+i%26))+string(rune('a'+i%26)), "answer", "ollama", "m")
	}
}
