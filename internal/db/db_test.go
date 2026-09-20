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
	defer func() { _ = conn.Close() }()
	for _, tbl := range []string{"sessions", "kv", "batch_jobs"} {
		var name string
		if err := conn.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, tbl).Scan(&name); err != nil {
			t.Fatalf("table %s: %v", tbl, err)
		}
	}
}

func TestKV(t *testing.T) {
	conn, err := Open(filepath.Join(t.TempDir(), "y.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, ok := KVGet(conn, "missing"); ok {
		t.Fatal("false positive")
	}
	if err := KVSet(conn, "k", "v1"); err != nil {
		t.Fatal(err)
	}
	if v, ok := KVGet(conn, "k"); !ok || v != "v1" {
		t.Fatalf("%q %v", v, ok)
	}
	if err := KVSet(conn, "k", "v2"); err != nil {
		t.Fatal(err)
	}
	if v, _ := KVGet(conn, "k"); v != "v2" {
		t.Fatal("upsert failed")
	}
	if err := KVDelete(conn, "k"); err != nil {
		t.Fatal(err)
	}
	if _, ok := KVGet(conn, "k"); ok {
		t.Fatal("delete failed")
	}
	if _, ok := KVGet(nil, "k"); ok {
		t.Fatal("nil db should miss")
	}
}
