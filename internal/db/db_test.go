package db

import (
	"path/filepath"
	"testing"
)

func TestOpenMigrate(t *testing.T) {
	conn, err := Open(filepath.Join(t.TempDir(), "y.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, tbl := range []string{"sessions", "kv"} {
		var name string
		if err := conn.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, tbl).Scan(&name); err != nil {
			t.Fatalf("table %s: %v", tbl, err)
		}
	}
}
