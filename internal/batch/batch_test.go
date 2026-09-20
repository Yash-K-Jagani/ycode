package batch

import (
	"testing"

	"github.com/Yash-K-Jagani/ycode/internal/db"
)

func isolate(t *testing.T) {
	t.Helper()
	db.ResetSharedForTest()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Cleanup(db.ResetSharedForTest)
}

func TestQueueSQLitePersistence(t *testing.T) {
	isolate(t)
	q := Load()
	j := q.Add("do thing", "chat", "", t.TempDir())
	if j.Status != "queued" {
		t.Fatal("bad status")
	}
	q2 := Load()
	list := q2.List()
	if len(list) != 1 || list[0].Prompt != "do thing" {
		t.Fatalf("%+v", list)
	}
	if n := q2.Clear(false); n != 1 {
		t.Fatalf("clear: %d", n)
	}
	if len(Load().List()) != 0 {
		t.Fatal("not cleared")
	}
}
