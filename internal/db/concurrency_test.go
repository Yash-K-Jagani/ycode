package db

import (
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

// This is the test for the bug the probe found: ycode writes from several
// goroutines at once - the UI thread, the turn goroutine, the cost tracker, the
// batch queue - and sql.Open hands back a pool, so those writes land on
// different connections. SQLite refuses the second concurrent writer
// immediately unless every connection has a busy timeout. Measured before the
// fix: 131 of 200 concurrent writes dropped with SQLITE_BUSY.
func TestConcurrentWritesAreNotLost(t *testing.T) {
	conn, err := Open(filepath.Join(t.TempDir(), "conc.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	const writers, each = 8, 25
	var wg sync.WaitGroup
	errs := make(chan error, writers*each)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := "writer-" + strconv.Itoa(i)
			for j := 0; j < each; j++ {
				if err := KVSet(conn, key, "v"); err != nil {
					errs <- err
				}
			}
		}(i)
	}
	wg.Wait()
	close(errs)

	var failed int
	for err := range errs {
		if failed == 0 {
			t.Errorf("first lost write: %v", err)
		}
		failed++
	}
	if failed > 0 {
		t.Fatalf("%d of %d concurrent writes were refused", failed, writers*each)
	}
	// Every write must also be readable, not merely accepted.
	for i := 0; i < writers; i++ {
		if _, ok := KVGet(conn, "writer-"+strconv.Itoa(i)); !ok {
			t.Fatalf("writer %d's write did not land", i)
		}
	}
}

// The busy timeout has to be on every pooled connection, not just the one a
// pragma Exec happened to land on. Reading it back through a fresh connection
// is how we know it is not per-Exec luck.
func TestBusyTimeoutIsSetOnEveryConnection(t *testing.T) {
	conn, err := Open(filepath.Join(t.TempDir(), "pragma.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	want := int(busyTimeout / time.Millisecond)
	// More than one iteration, so a pool that recycles a single connection
	// cannot pass by accident.
	for i := 0; i < 5; i++ {
		var got int
		if err := conn.QueryRow(`PRAGMA busy_timeout`).Scan(&got); err != nil {
			t.Fatalf("reading busy_timeout: %v", err)
		}
		if got != want {
			t.Fatalf("busy_timeout = %dms, want %dms", got, want)
		}
	}
	var mode string
	if err := conn.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatalf("reading journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}
}

// A busy timeout of zero would pass a test that only checked the pragma
// existed.
func TestBusyTimeoutIsNotZero(t *testing.T) {
	if busyTimeout <= 0 {
		t.Fatalf("busyTimeout = %s, which refuses concurrent writers immediately", busyTimeout)
	}
}
