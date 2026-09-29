package db

import (
	"database/sql"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// A busy timeout is a stall, and this is the measurement that keeps it honest.
//
// ycode can have more than one thing writing at once - the UI thread, the
// daemon, a second window, a test binary sharing a home directory. The
// original fix for dropped writes (131 of 200 concurrent writes refused with
// SQLITE_BUSY) set a 10s timeout, which meant every collision became ten
// seconds of nothing happening. That is fine for a batch job and terrible for
// a TUI, and a package full of database-touching tests can accumulate enough of
// them to hit a 600s suite timeout without anything looking wrong.
func TestContendedWriteIsRefusedQuickly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "contend.db")
	a, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	// A second, independent handle to the same file: another process.
	b, err := sql.Open("sqlite", path+"?_pragma=busy_timeout("+
		strconv.Itoa(int(busyTimeout/time.Millisecond))+")&_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = b.Close() }()

	tx, err := a.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`INSERT INTO kv(k,v) VALUES('hold','1')`); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	_, werr := b.Exec(`INSERT INTO kv(k,v) VALUES('other','1')`)
	took := time.Since(start)

	if werr == nil {
		t.Skip("WAL let the second writer through, so there is no stall to measure")
	}
	t.Logf("contended write refused after %s", took.Round(time.Millisecond))

	// The bound is absolute, not relative: a caller must never be stuck for
	// long enough to look hung. Generous enough to allow for a slow machine.
	if max := 5 * time.Second; took > max {
		t.Errorf("a contended write blocked for %s, over the %s ceiling", took, max)
	}
	// And it must be bounded by the configured timeout, or something is
	// waiting somewhere else entirely.
	if took > busyTimeout+2*time.Second {
		t.Errorf("a contended write blocked for %s, well past the %s timeout", took, busyTimeout)
	}
}

// Inside one process the pool queue replaces the busy wait entirely, which is
// the other half of the fix: ycode should never contend with itself.
func TestPoolIsCappedSoYcodeDoesNotQueueAgainstItself(t *testing.T) {
	conn, err := Open(filepath.Join(t.TempDir(), "pool.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	if got := conn.Stats().MaxOpenConnections; got != maxOpenConns {
		t.Fatalf("MaxOpenConnections = %d, want %d", got, maxOpenConns)
	}
	// The cap has to be low enough that concurrent writers queue in the pool
	// rather than colliding on the database lock.
	if maxOpenConns > 1 {
		t.Fatalf("maxOpenConns = %d; SQLite allows one writer, so more than one "+
			"means ycode queues against itself and pays busyTimeout for each", maxOpenConns)
	}
}

// A leaked transaction would take the single connection with it and hang the
// app, so the property that makes maxOpenConns safe is asserted rather than
// assumed. Every write transaction in the tree commits or rolls back without
// calling back into application code; this is the test that would notice if
// one of them started to.
func TestTransactionsDoNotLeakTheConnection(t *testing.T) {
	conn, err := Open(filepath.Join(t.TempDir(), "leak.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	// A transaction that is rolled back - the error path - must release the
	// connection, or every later write would queue behind it forever.
	for i := 0; i < 3; i++ {
		tx, err := conn.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`INSERT INTO kv(k,v) VALUES('k','v')`); err != nil {
			t.Fatal(err)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
	}
	// The connection must be usable, and available, after the rollbacks.
	done := make(chan error, 1)
	go func() { done <- KVSet(conn, "after", "v") }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("a write after a rolled-back transaction: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a rolled-back transaction kept the only connection")
	}
}
