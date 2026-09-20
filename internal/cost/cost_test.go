package cost

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

func TestSQLiteCarryOver(t *testing.T) {
	isolate(t)
	tr := New()
	tr.Add("gemini", 1000, 500)
	p, c, usd := tr.Today()
	if p != 1000 || c != 500 || usd == 0 {
		t.Fatalf("got %d %d %f", p, c, usd)
	}
	tr2 := New()
	p2, c2, usd2 := tr2.Today()
	if p2 != p || c2 != c || usd2 != usd {
		t.Fatalf("not persisted: %d %d %f", p2, c2, usd2)
	}
}
