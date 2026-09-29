package sessions

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Yash-K-Jagani/ycode/internal/db"
	"github.com/Yash-K-Jagani/ycode/pkg/apitypes"
)

func openTestDB(t *testing.T) *SQLStore {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return &SQLStore{db: conn}
}

func bigSession(id string, msgs int) *Session {
	s := New("gemini", "m")
	s.ID = id
	s.Title = "session " + id
	s.Messages = make([]apitypes.Message, msgs)
	for i := range s.Messages {
		s.Messages[i] = apitypes.Message{
			Role:    apitypes.RoleUser,
			Content: strings.Repeat("x", 2000),
		}
	}
	return s
}

// ListMeta exists because List has to read every transcript, and the callers
// that only want identity were paying for it. The count is stored precisely so
// that dropping the messages does not also drop the "N msgs" the search filter
// prints.
func TestListMetaOmitsMessagesButKeepsTheCount(t *testing.T) {
	s := openTestDB(t)
	if err := s.Save(bigSession("a", 7)); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(bigSession("b", 3)); err != nil {
		t.Fatal(err)
	}

	full, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(full) != 2 {
		t.Fatalf("List returned %d", len(full))
	}
	for _, sess := range full {
		if len(sess.Messages) == 0 {
			t.Fatalf("List must still carry messages for %s", sess.ID)
		}
	}

	meta, err := s.ListMeta()
	if err != nil {
		t.Fatal(err)
	}
	if len(meta) != 2 {
		t.Fatalf("ListMeta returned %d", len(meta))
	}
	for _, sess := range meta {
		if len(sess.Messages) != 0 {
			t.Fatalf("ListMeta leaked %d messages for %s", len(sess.Messages), sess.ID)
		}
	}
	// The count is the whole reason ListMeta is usable here.
	byID := map[string]int{}
	for _, sess := range meta {
		byID[sess.ID] = sess.MessageCount
	}
	if byID["a"] != 7 || byID["b"] != 3 {
		t.Fatalf("counts = %v, want a=7 b=3", byID)
	}
}

// The identity fields have to match, or a caller that switched to ListMeta
// would start showing a different list.
func TestListMetaMatchesList(t *testing.T) {
	s := openTestDB(t)
	for i, n := range []int{1, 5, 2, 9} {
		if err := s.Save(bigSession(fmt.Sprintf("s%d", i), n)); err != nil {
			t.Fatal(err)
		}
	}
	full, _ := s.List()
	meta, _ := s.ListMeta()
	if len(full) != len(meta) {
		t.Fatalf("lengths differ: %d vs %d", len(full), len(meta))
	}
	for i := range full {
		if full[i].ID != meta[i].ID || full[i].Title != meta[i].Title ||
			full[i].Provider != meta[i].Provider || full[i].Model != meta[i].Model {
			t.Fatalf("row %d differs:\n full=%+v\n meta=%+v", i, full[i], meta[i])
		}
		if !full[i].UpdatedAt.Equal(meta[i].UpdatedAt) {
			t.Fatalf("row %d updated differs", i)
		}
		if full[i].MessageCount != meta[i].MessageCount {
			t.Fatalf("row %d count: %d vs %d", i, full[i].MessageCount, meta[i].MessageCount)
		}
	}
}

// The picker still uses List, because selecting a session hands the transcript
// to renderAll. If that changed, this fails.
func TestListStillReturnsMessages(t *testing.T) {
	s := openTestDB(t)
	if err := s.Save(bigSession("only", 4)); err != nil {
		t.Fatal(err)
	}
	list, _ := s.List()
	if len(list) != 1 || len(list[0].Messages) != 4 {
		t.Fatalf("List must keep returning messages: %d", len(list[0].Messages))
	}
	loaded, err := s.Load("only")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Messages) != 4 {
		t.Fatalf("Load returned %d messages", len(loaded.Messages))
	}
	if loaded.MessageCount != 4 {
		t.Fatalf("Load count = %d", loaded.MessageCount)
	}
}

// A row written before the count column existed must still report the right
// number, because the messages are right there and the column just reads zero.
func TestMessageCountFallsBackForOldRows(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "old.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	// Write a row the pre-count way, with a real transcript.
	msgs := make([]apitypes.Message, 6)
	for i := range msgs {
		msgs[i] = apitypes.Message{Role: apitypes.RoleAssistant, Content: "hi"}
	}
	blob := fmt.Sprintf(`[{"role":"user","content":"x"}%s]`, strings.Repeat(`,{"role":"assistant","content":"y"}`, 5))
	_, err = conn.Exec(`INSERT INTO sessions(id,title,created,updated,provider,model,msgs,messages)
		VALUES('old','old','','','gemini','m',0,?)`, blob)
	if err != nil {
		t.Fatal(err)
	}
	s := &SQLStore{db: conn}
	loaded, err := s.Load("old")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Messages) != 6 {
		t.Fatalf("messages = %d, want 6", len(loaded.Messages))
	}
	if loaded.MessageCount != 6 {
		t.Fatalf("MessageCount = %d; a zero count must fall back to len(messages)", loaded.MessageCount)
	}
}

// The index is the other half: List sorts by updated, and without an index on
// it the query is a full scan plus a sort. This checks the plan rather than the
// timing, so it is not machine dependent.
func TestListUsesTheUpdatedIndex(t *testing.T) {
	s := openTestDB(t)
	for i := 0; i < 20; i++ {
		if err := s.Save(bigSession(fmt.Sprintf("s%d", i), 1)); err != nil {
			t.Fatal(err)
		}
		// Distinct timestamps, since the sort key is the string form.
		time.Sleep(2 * time.Millisecond)
	}
	rows, err := s.db.Query(`EXPLAIN QUERY PLAN SELECT id FROM sessions ORDER BY updated DESC`)
	if err != nil {
		t.Skipf("no explain support: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var plan string
	for rows.Next() {
		var a, b, c int
		var detail string
		if err := rows.Scan(&a, &b, &c, &detail); err == nil {
			plan += detail + " "
		}
	}
	if !strings.Contains(strings.ToUpper(plan), "USING INDEX") {
		t.Fatalf("ORDER BY updated is not using the index; plan: %s", plan)
	}
}

// The cost that motivated ListMeta: the messages are most of the row, so
// loading them to filter on a title reads the entire history of the database.
func BenchmarkListMetaVsList(b *testing.B) {
	conn, err := db.Open(filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	s := &SQLStore{db: conn}
	// 200 sessions with 40 messages of 2KB each: a realistic long-lived user.
	for i := 0; i < 200; i++ {
		sess := bigSession(fmt.Sprintf("s%03d", i), 40)
		if err := s.Save(sess); err != nil {
			b.Fatal(err)
		}
	}
	b.Run("ListMeta", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := s.ListMeta(); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("List", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := s.List(); err != nil {
				b.Fatal(err)
			}
		}
	})
}
