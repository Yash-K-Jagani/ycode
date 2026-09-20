package db

import (
	"database/sql"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Open creates/opens the SQLite file and runs migrations. Pure Go, no CGO.
func Open(path string) (*sql.DB, error) {
	if dir := filepath.Dir(path); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if _, err := conn.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		_ = conn.Close()
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
	} {
		if _, err := conn.Exec(stmt); err != nil {
			_ = conn.Close()
			return nil, err
		}
	}
	return conn, nil
}
