package db

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Yash-K-Jagani/ycode/internal/config"
)

// Everything above this package - sessions, cost, batch, RAG, the semantic
// cache - stores through it. A bug here loses user data, so the properties
// worth pinning are durability across a reopen, correct behaviour when the
// database is unavailable, and the migrations actually creating every table
// the rest of the tree queries.

func tempHome(t *testing.T) string {
	t.Helper()
	home, err := filepath.Abs(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// The directory is the test's own; a best-effort removal is enough.
	_ = os.RemoveAll(home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows
	t.Setenv("HOME", home)        // and on unix
	return home
}

// A value that was written must still be there after the process restarts.
// This is the property the whole layer exists for.
func TestDataSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "y.db")
	conn, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := KVSet(conn, "sessions/last", "abc123"); err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}

	again, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = again.Close() }()
	v, ok := KVGet(again, "sessions/last")
	if !ok || v != "abc123" {
		t.Fatalf("after reopen: %q %v", v, ok)
	}
}

// Every table the rest of the codebase queries must exist after migration.
// A missing one is a nil conn at the call site, which reads as "no data".
func TestMigrationsCreateEveryTable(t *testing.T) {
	conn, err := Open(filepath.Join(t.TempDir(), "y.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	for _, tbl := range []string{"sessions", "kv", "batch_jobs", "rag_chunks", "cache_items"} {
		var name string
		err := conn.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, tbl).Scan(&name)
		if err != nil {
			t.Errorf("table %s: %v", tbl, err)
		}
	}
	// Opening an existing database must be a no-op, not an error - the TUI
	// opens it on every start.
	again, err := Open(filepath.Join(t.TempDir(), "y.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := again.Exec(`SELECT 1`); err != nil {
		t.Fatal(err)
	}
	_ = again.Close()
}

// Callers pass Shared()'s result straight through, so a nil database has to be
// a miss rather than a panic.
func TestKVOnANilDatabase(t *testing.T) {
	if v, ok := KVGet(nil, "k"); ok || v != "" {
		t.Fatalf("KVGet(nil) = %q, %v", v, ok)
	}
	if err := KVSet(nil, "k", "v"); err == nil {
		t.Fatal("KVSet(nil) should report an error, not silently succeed")
	}
	if err := KVDelete(nil, "k"); err == nil {
		t.Fatal("KVDelete(nil) should report an error, not silently succeed")
	}
}

// Values are arbitrary JSON blobs from callers; quotes, newlines and unicode
// must round-trip rather than being truncated or mangled.
func TestKVValuesRoundTrip(t *testing.T) {
	conn, err := Open(filepath.Join(t.TempDir(), "y.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	cases := map[string]string{
		"json":       `{"a":1,"b":[2,3],"c":{"d":"e"}}`,
		"quotes":     `he said "hi" and 'bye'`,
		"newlines":   "line one\nline two\r\nline three",
		"unicode":    "café — naïve ✓ 日本語",
		"empty":      "",
		"long":       strings.Repeat("x", 100000),
		"sql-ish":    `'; DROP TABLE kv; --`,
		"nul-lookal": "a\x00b",
	}
	for k, v := range cases {
		if err := KVSet(conn, k, v); err != nil {
			t.Fatalf("KVSet(%q): %v", k, err)
		}
		got, ok := KVGet(conn, k)
		if !ok {
			t.Errorf("KVGet(%q) missed", k)
			continue
		}
		if got != v {
			t.Errorf("KVGet(%q) = %q, want %q", k, got, v)
		}
	}
	// Deleting one key must not disturb the others.
	if err := KVDelete(conn, "json"); err != nil {
		t.Fatal(err)
	}
	if _, ok := KVGet(conn, "json"); ok {
		t.Error("the deleted key is still there")
	}
	if v, ok := KVGet(conn, "unicode"); !ok || v != cases["unicode"] {
		t.Error("deleting one key disturbed another")
	}
	// Deleting a key that is not there is not an error - the callers treat it
	// as "already gone".
	if err := KVDelete(conn, "never-existed"); err != nil {
		t.Errorf("deleting a missing key: %v", err)
	}
}

// Shared is a process-wide singleton, and every package calls it, so it has to
// be safe to hammer and must hand back the same handle.
func TestSharedIsOnceAndConcurrencySafe(t *testing.T) {
	tempHome(t)
	ResetSharedForTest()
	t.Cleanup(ResetSharedForTest)

	var wg sync.WaitGroup
	got := make([]*sql.DB, 16)
	for i := range got {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got[i] = Shared()
		}(i)
	}
	wg.Wait()

	first := got[0]
	if first == nil {
		t.Fatal("Shared returned nil")
	}
	for i, c := range got {
		if c != first {
			t.Fatalf("caller %d got a different handle; Shared is not a singleton", i)
		}
	}
	// And it is usable.
	if err := KVSet(first, "shared/probe", "v"); err != nil {
		t.Fatal(err)
	}
	if v, ok := KVGet(first, "shared/probe"); !ok || v != "v" {
		t.Fatalf("%q %v", v, ok)
	}
	// It lives where the rest of the app expects it.
	if !strings.HasSuffix(config.Dir(), ".ycode") {
		t.Fatalf("config.Dir() = %q", config.Dir())
	}
}

// ResetSharedForTest must actually release the handle, or a test that changes
// the home directory would keep reading the previous one.
func TestResetSharedForTest(t *testing.T) {
	first := tempHome(t)
	ResetSharedForTest()
	t.Cleanup(ResetSharedForTest)
	conn := Shared()
	if conn == nil {
		t.Fatal("Shared returned nil")
	}
	if err := KVSet(conn, "marker", "first"); err != nil {
		t.Fatal(err)
	}

	second := tempHome(t)
	if second == first {
		t.Fatal("the two homes are the same, so this proves nothing")
	}
	ResetSharedForTest()
	fresh := Shared()
	if fresh == nil {
		t.Fatal("Shared returned nil after reset")
	}
	// The value must not be visible in a different home.
	if _, ok := KVGet(fresh, "marker"); ok {
		t.Fatal("the previous handle's data leaked into a new home")
	}
}
