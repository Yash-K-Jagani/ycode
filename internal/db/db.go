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

// busyTimeout is how long a connection waits for another writer before giving
// up, and it is only reached for writers in *other* processes - see
// maxOpenConns for why ycode never queues against itself.
//
// This is not a tuning knob, it is the difference between persistence working
// and silently losing writes. ycode writes from several goroutines at once -
// the UI thread, the turn goroutine, the cost tracker, the batch queue - and
// SQLite refuses the second concurrent writer immediately by default. A
// measured 131 of 200 concurrent KV writes were dropped with SQLITE_BUSY.
//
// It is also not free to raise. A busy timeout is a *stall*: a measured
// contended write blocked for the full timeout, so a 10s setting turned every
// collision into ten seconds of nothing happening. Cross-process contention is
// rare - a second ycode window, or a test binary sharing a home directory - so
// a short wait that fails visibly beats a long one the user watches. The
// caller's fallback is a JSON file, not a lost session.
//
// The pragmas go in the DSN rather than in conn.Exec because Exec runs on
// whichever one connection the pool hands out. journal_mode is a property of
// the database file so it survives that, but busy_timeout is per-connection,
// and setting it once would leave every other connection with none.
const busyTimeout = 2 * time.Second

// maxOpenConns is deliberately 1.
//
// SQLite allows one writer at a time. An unlimited pool means ycode queues
// against itself, and every one of those collisions costs a full busy_timeout
// of stall. Capping the pool moves the queue into database/sql, where waiting
// for a free connection is instant and does not hold a lock, so a write never
// waits on a timeout at all inside one process.
//
// It is safe because nothing here holds a transaction open across a call back
// into application code: the three write transactions (batch, cache, rag) each
// do their own statements and commit or roll back without calling out. A
// transaction that leaked would take the single connection with it and hang
// the app, so that property is asserted in the tests rather than assumed.
const maxOpenConns = 1

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
	conn.SetMaxOpenConns(maxOpenConns)
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
