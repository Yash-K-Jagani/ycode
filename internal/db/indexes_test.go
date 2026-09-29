package db

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The indexes are declared in Open rather than in a migration framework, so
// nothing checks that a database written by an older build actually has them.
// A missing index is a performance regression rather than a failure, which is
// exactly the kind that goes unnoticed.
func TestIndexesAreCreatedOnOpen(t *testing.T) {
	conn, err := Open(filepath.Join(t.TempDir(), "idx.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	rows, err := conn.Query(`SELECT name FROM sqlite_master WHERE type='index'`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var got []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err == nil {
			got = append(got, n)
		}
	}
	sort.Strings(got)
	// Only the ones this package creates by name; SQLite adds its own
	// autoindex entries for the primary keys.
	want := []string{"batch_jobs_created", "cache_items_at", "sessions_updated"}
	for _, w := range want {
		found := false
		for _, g := range got {
			if g == w {
				found = true
			}
		}
		if !found {
			t.Errorf("index %q is missing; have %v", w, got)
		}
	}
}

// Reopening an existing database has to be idempotent, or the second run of
// ycode fails where the first worked. This is the path a real user takes every
// launch.
func TestOpenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "twice.db")
	for i := 0; i < 3; i++ {
		conn, err := Open(path)
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		// Distinct keys: this is about the schema being creatable more than
		// once, and a repeated primary key would fail for an unrelated reason.
		if _, err := conn.Exec(`INSERT INTO kv(k,v) VALUES(?,?)`, fmt.Sprintf("k%d", i), "b"); err != nil {
			_ = conn.Close()
			t.Fatalf("write %d: %v", i, err)
		}
		if err := conn.Close(); err != nil {
			t.Fatalf("close %d: %v", i, err)
		}
	}
	conn, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	var n int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM kv`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("kv has %d rows, want 3; reopening dropped or duplicated data", n)
	}
}

// A database written before the msgs column existed has to open, keep working,
// and gain the column. The ALTER has no IF NOT EXISTS, so its error is the
// existence check and a failure here would mean a user cannot start ycode at
// all after an upgrade.
func TestOpenUpgradesADatabaseWithoutTheMsgsColumn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	conn, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// Rebuild the sessions table without msgs, as an older build would have.
	if _, err := conn.Exec(`DROP INDEX IF EXISTS sessions_updated`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`ALTER TABLE sessions RENAME TO sessions_old`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`CREATE TABLE sessions (
		id TEXT PRIMARY KEY,
		title TEXT NOT NULL DEFAULT '',
		created TEXT NOT NULL DEFAULT '',
		updated TEXT NOT NULL DEFAULT '',
		provider TEXT NOT NULL DEFAULT '',
		model TEXT NOT NULL DEFAULT '',
		messages TEXT NOT NULL DEFAULT '[]'
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO sessions(id,title,created,updated,provider,model,messages)
		VALUES('old','t','','','gemini','m','[{"role":"user","content":"hi"}]')`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`DROP TABLE sessions_old`); err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen, which is what the next launch does.
	conn2, err := Open(path)
	if err != nil {
		t.Fatalf("an existing database must still open: %v", err)
	}
	defer func() { _ = conn2.Close() }()

	var title, msgs string
	var count int
	err = conn2.QueryRow(`SELECT title, msgs, messages FROM sessions WHERE id='old'`).
		Scan(&title, &count, &msgs)
	if err != nil {
		t.Fatalf("row lost across the upgrade: %v", err)
	}
	if !strings.Contains(msgs, "hi") {
		t.Fatalf("messages were lost: %q", msgs)
	}
	// A row that predates the column reads as zero, which the store falls
	// back from using the messages it already loaded.
	if count != 0 {
		t.Fatalf("msgs = %d, want 0 for a row that predates the column", count)
	}

	// And it must be writable now.
	if _, err := conn2.Exec(`UPDATE sessions SET msgs=2 WHERE id='old'`); err != nil {
		t.Fatalf("the upgraded column is not usable: %v", err)
	}
}

func TestOpenCreatesMissingDirectories(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a", "b", "c.db")
	conn, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Exec(`INSERT INTO kv(k,v) VALUES('x','y')`); err != nil {
		t.Fatalf("the database was not actually created: %v", err)
	}
}
