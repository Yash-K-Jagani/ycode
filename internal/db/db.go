package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	_ "modernc.org/sqlite"
)

// busyTimeout is how long a connection waits for another writer to finish
// before giving up.
//
// This is not a tuning knob, it is the difference between persistence working
// and silently losing writes. ycode writes from several goroutines at once -
// the UI thread, the turn goroutine, the cost tracker, the batch queue - and
// sql.Open hands back a pool, so those writes land on different connections.
// SQLite refuses the second concurrent writer immediately by default: a
// measured 131 of 200 concurrent KV writes were dropped with SQLITE_BUSY.
//
// The pragmas go in the DSN rather than in conn.Exec because Exec runs on
// whichever one connection the pool hands out. journal_mode is a property of
// the database file so it survives that, but busy_timeout is per-connection,
// and setting it once would leave every other connection with none.
const busyTimeout = 10 * time.Second

// Open creates/opens the SQLite file and runs migrations. Pure Go, no CGO.
func Open(path string) (*sql.DB, error) {
	if dir := filepath.Dir(path); dir != "" {
		// Reported rather than ignored: a permissions problem here surfaces
		// later as a confusing "unable to open database file".
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create %s: %w", dir, err)
		}
	}
	dsn := path + "?_pragma=busy_timeout(" + strconv.Itoa(int(busyTimeout/time.Millisecond)) +
		")&_pragma=journal_mode(WAL)"
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL DEFAULT '',
			created TEXT NOT NULL DEFAULT '',
			updated TEXT NOT NULL DEFAULT '',
			provider TEXT NOT NULL DEFAULT '',
			model TEXT NOT NULL DEFAULT '',
			messages TEXT NOT NULL DEFAULT '[]'
		)`,
		`CREATE TABLE IF NOT EXISTS kv (k TEXT PRIMARY KEY, v TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE IF NOT EXISTS batch_jobs (
			id TEXT PRIMARY KEY,
			created TEXT NOT NULL DEFAULT '',
			data TEXT NOT NULL DEFAULT '{}'
		)`,
		`CREATE TABLE IF NOT EXISTS rag_chunks (
			wkey TEXT NOT NULL,
			path TEXT NOT NULL,
			text TEXT NOT NULL,
			vec TEXT NOT NULL DEFAULT '[]',
			PRIMARY KEY (wkey, path, text)
		)`,
		`CREATE TABLE IF NOT EXISTS cache_items (
			provider TEXT NOT NULL,
			model TEXT NOT NULL,
			prompt TEXT NOT NULL,
			answer TEXT NOT NULL DEFAULT '',
			vec TEXT NOT NULL DEFAULT '[]',
			at TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (provider, model, prompt)
		)`,
	} {
		if _, err := conn.Exec(stmt); err != nil {
			_ = conn.Close()
			return nil, err
		}
	}
	return conn, nil
}
