package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListGetInstall(t *testing.T) {
	home := t.TempDir()
	m := NewManagerAt(filepath.Join(home, "skills"))
	if list, err := m.List(); err != nil || len(list) != 0 {
		t.Fatalf("expected empty list: %v %v", list, err)
	}
	// craft a local skill
	src := filepath.Join(home, "src-skill")
	_ = os.MkdirAll(src, 0o755)
	_ = os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("# reviewer\nDescription: reviews code\n\nDo X.\n"), 0o644)
	s, err := m.Install(src)
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "reviewer" || s.Description != "reviews code" {
		t.Fatalf("bad parse: %+v", s)
	}
	list, _ := m.List()
	if len(list) != 1 {
		t.Fatalf("bad list: %v", list)
	}
	got, body, err := m.Get("REVIEWER")
	if err != nil || !strings.Contains(body, "Do X.") || got.Name != "reviewer" {
		t.Fatalf("bad get: %v %q", got, body)
	}
	if _, err := m.Install(src); err == nil {
		t.Fatal("expected duplicate error")
	}
	if _, _, err := m.Get("missing"); err == nil {
		t.Fatal("expected missing error")
	}
	if _, err := m.Install("https://example.com/x.git"); err == nil {
		t.Fatal("expected host refusal")
	}
}

func TestExportImport(t *testing.T) {
	home := t.TempDir()
	m := NewManagerAt(filepath.Join(home, "skills"))
	src := filepath.Join(home, "src")
	_ = os.MkdirAll(src, 0o755)
	_ = os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("# sharer\nDescription: shared skill\n\nHi.\n"), 0o644)
	if _, err := m.Install(src); err != nil {
		t.Fatal(err)
	}
	zp := filepath.Join(home, "sharer.zip")
	if err := m.Export("sharer", zp); err != nil {
		t.Fatal(err)
	}
	m2 := NewManagerAt(filepath.Join(home, "skills2"))
	got, body, err := func() (Skill, string, error) {
		s, err := m2.Import(zp)
		if err != nil {
			return Skill{}, "", err
		}
		return m2.Get(s.Name)
	}()
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "sharer" || !strings.Contains(body, "Hi.") {
		t.Fatalf("bad import: %+v", body)
	}
}
