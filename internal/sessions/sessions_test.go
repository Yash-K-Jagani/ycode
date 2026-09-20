package sessions

import (
	"path/filepath"
	"testing"

	"github.com/Yash-K-Jagani/ycode/internal/db"
	"github.com/Yash-K-Jagani/ycode/pkg/apitypes"
)

func TestSQLRoundTrip(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "y.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	UseSQLite(conn)
	defer UseJSON()

	s := New("ollama", "m")
	s.Messages = []apitypes.Message{{Role: apitypes.RoleUser, Content: "hi"}}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	if Backend() != "sqlite" {
		t.Fatal("backend not sqlite")
	}
	got, err := Load(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "m" || len(got.Messages) != 1 || got.Messages[0].Content != "hi" {
		t.Fatalf("%+v", got)
	}
	list, err := List()
	if err != nil || len(list) != 1 {
		t.Fatalf("%v %+v", err, list)
	}
	if _, err := Load("missing"); err == nil {
		t.Fatal("expected not-found")
	}
	// upsert path
	s.Model = "m2"
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	got, _ = Load(s.ID)
	if got.Model != "m2" {
		t.Fatal("upsert failed")
	}
}

func TestJSONFallbackStillWorks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	UseJSON()
	defer UseJSON()
	if Backend() != "json" {
		t.Fatal("want json")
	}
	s := New("ollama", "m")
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	got, err := Load(s.ID)
	if err != nil || len(got.Messages) != 0 {
		t.Fatalf("%v %+v", err, got)
	}
	list, err := List()
	if err != nil || len(list) != 1 {
		t.Fatalf("%v %+v", err, list)
	}
}

func TestImportJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	UseJSON()
	defer UseJSON()
	legacy := New("ollama", "legacy-model")
	if err := legacy.Save(); err != nil {
		t.Fatal(err)
	}
	conn, err := db.Open(filepath.Join(home, "ycode.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	sqlStore := &SQLStore{db: conn}
	n, err := sqlStore.ImportJSON()
	if err != nil || n != 1 {
		t.Fatalf("import: %d %v", n, err)
	}
	n, err = sqlStore.ImportJSON()
	if err != nil || n != 0 {
		t.Fatalf("re-import not idempotent: %d %v", n, err)
	}
	UseSQLite(conn)
	got, err := Load(legacy.ID)
	if err != nil || got.Model != "legacy-model" {
		t.Fatalf("%v %+v", err, got)
	}
}
