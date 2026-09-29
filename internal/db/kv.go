package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/Yash-K-Jagani/ycode/internal/config"
)

var (
	sharedMu sync.Mutex
	sharedDB *sql.DB
	sharedOK bool
)

// Shared opens the app database once per process (nil when unavailable).
// Callers fall back to their legacy JSON files when it returns nil.
func Shared() *sql.DB {
	sharedMu.Lock()
	defer sharedMu.Unlock()
	if sharedDB == nil && !sharedOK {
		sharedOK = true
		path := filepath.Join(config.Dir(), "ycode.db")
		conn, err := Open(path)
		if err != nil {
			// Reported once, then never retried. Callers degrade to their JSON
			// files, which looks exactly like working - slower, and with no
			// history across versions - so a permissions problem or a full
			// disk has to say so here or nowhere.
			fmt.Fprintf(os.Stderr, "ycode: cannot open %s (%v); falling back to JSON files for storage\n", path, err)
			return nil
		}
		sharedDB = conn
	}
	return sharedDB
}

// ResetSharedForTest drops the cached handle (tests only).
func ResetSharedForTest() {
	sharedMu.Lock()
	defer sharedMu.Unlock()
	if sharedDB != nil {
		_ = sharedDB.Close()
	}
	sharedDB = nil
	sharedOK = false
}

func KVGet(conn *sql.DB, key string) (string, bool) {
	if conn == nil {
		return "", false
	}
	var v string
	if err := conn.QueryRow(`SELECT v FROM kv WHERE k=?`, key).Scan(&v); err != nil {
		return "", false
	}
	return v, true
}

func KVSet(conn *sql.DB, key, val string) error {
	if conn == nil {
		return sql.ErrConnDone
	}
	_, err := conn.Exec(`INSERT INTO kv(k,v) VALUES(?,?)
		ON CONFLICT(k) DO UPDATE SET v=excluded.v`, key, val)
	return err
}

func KVDelete(conn *sql.DB, key string) error {
	if conn == nil {
		return sql.ErrConnDone
	}
	_, err := conn.Exec(`DELETE FROM kv WHERE k=?`, key)
	return err
}
