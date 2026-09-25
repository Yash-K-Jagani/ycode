package store

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Yash-K-Jagani/ycode/internal/plugins"
	"github.com/Yash-K-Jagani/ycode/internal/skills"
)

const fixture = `
version: 1
items:
  - name: reviewer
    kind: skill
    source: /x
    subdir: skills/reviewer
    description: Strict code reviewer.
  - name: hello
    kind: plugin
    source: /x
    subdir: plugins/hello
    description: Hello-world plugin.
`

func TestParseSearchGet(t *testing.T) {
	idx, err := Parse([]byte(fixture))
	if err != nil {
		t.Fatal(err)
	}
	if idx.Version != 1 || len(idx.Items) != 2 {
		t.Fatalf("%+v", idx)
	}
	if got := idx.Search("review"); len(got) != 1 || got[0].Name != "reviewer" {
		t.Fatalf("search: %+v", got)
	}
	if got := idx.Search("PLUGIN"); len(got) != 1 {
		t.Fatalf("kind search: %+v", got)
	}
	if len(idx.Search("")) != 2 {
		t.Fatal("empty query should list all")
	}
	if _, ok := idx.Get("REVIEWER"); !ok {
		t.Fatal("case-insensitive get")
	}
	if _, ok := idx.Get("missing"); ok {
		t.Fatal("false positive get")
	}
	if _, err := Parse([]byte("{nope")); err == nil {
		t.Fatal("expected yaml error")
	}
}

func TestInstallLocalSubdir(t *testing.T) {
	// fake "remote": plain local dir tree with skill + plugin subdirs
	remote := t.TempDir()
	skDir := filepath.Join(remote, "skills", "reviewer")
	_ = os.MkdirAll(skDir, 0o755)
	_ = os.WriteFile(filepath.Join(skDir, "SKILL.md"), []byte("# reviewer\nDescription: rev\n\nBody.\n"), 0o644)
	plDir := filepath.Join(remote, "plugins", "hello")
	_ = os.MkdirAll(plDir, 0o755)
	_ = os.WriteFile(filepath.Join(plDir, "plugin.json"), []byte(`{"name":"hello","description":"hi","command":"echo hi"}`), 0o644)

	home := t.TempDir()
	sk := skills.NewManagerAt(filepath.Join(home, "skills"))
	pl := plugins.NewLoaderAt(filepath.Join(home, "plugins"))
	defer pl.Close()

	skEntry := Entry{Name: "reviewer", Kind: "skill", Source: remote, Subdir: "skills/reviewer"}
	if name, err := Install(skEntry, sk, pl); err != nil || name != "reviewer" {
		t.Fatalf("skill: %q %v", name, err)
	}
	if _, _, err := sk.Get("reviewer"); err != nil {
		t.Fatalf("skill not retrievable: %v", err)
	}
	plEntry := Entry{Name: "hello", Kind: "plugin", Source: remote, Subdir: "plugins/hello"}
	if name, err := Install(plEntry, sk, pl); err != nil || name != "hello" {
		t.Fatalf("plugin: %q %v", name, err)
	}
	if len(pl.Names()) != 1 {
		t.Fatalf("plugin not loaded: %v", pl.Names())
	}
	// error paths
	if _, err := Install(Entry{Name: "x", Kind: "weird", Source: remote}, sk, pl); err == nil {
		t.Fatal("expected unknown-kind error")
	}
	if _, err := Install(Entry{Name: "x", Kind: "skill", Source: remote, Subdir: "../evil"}, sk, pl); err == nil {
		t.Fatal("expected subdir traversal error")
	}
	if _, err := Install(Entry{Kind: "skill"}, sk, pl); err == nil {
		t.Fatal("expected bad-entry error")
	}
	if _, err := Install(Entry{Name: "x", Kind: "skill", Source: "https://example.com/a.git"}, sk, pl); err == nil {
		t.Fatal("expected host refusal")
	}
	if !strings.Contains(DefaultIndexURL, "ycode/main/store/index.yaml") {
		t.Fatal("default index URL moved?")
	}
}

func TestUpdateCaches(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(fixture))
	}))
	defer srv.Close()
	idx, err := Update(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Items) != 2 {
		t.Fatalf("%+v", idx)
	}
	loaded, err := Load()
	if err != nil {
		t.Fatalf("cache not readable: %v", err)
	}
	if len(loaded.Items) != 2 {
		t.Fatal("cache mismatch")
	}
}

func TestDirHashAndVerify(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0o644)
	h1, err := dirHash(dir)
	if err != nil || h1 == "" {
		t.Fatal(err)
	}
	h2, err := dirHash(dir)
	if err != nil || h1 != h2 {
		t.Fatal("hash unstable")
	}
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("bye"), 0o644)
	h3, _ := dirHash(dir)
	if h3 == h1 {
		t.Fatal("change undetected")
	}

	home := t.TempDir()
	sk := skills.NewManagerAt(filepath.Join(home, "skills"))
	pl := plugins.NewLoaderAt(filepath.Join(home, "plugins"))
	defer pl.Close()
	remote := t.TempDir()
	pkg := filepath.Join(remote, "pkg")
	_ = os.MkdirAll(pkg, 0o755)
	_ = os.WriteFile(filepath.Join(pkg, "SKILL.md"), []byte("# s\nDescription: d\n\nB.\n"), 0o644)
	e := Entry{Name: "s", Kind: "skill", Source: remote, Subdir: "pkg", SHA256: mustHash(t, pkg)}
	if _, err := Install(e, sk, pl); err != nil {
		t.Fatal(err)
	}
	msg, err := Verify("s", sk, pl)
	if err != nil || !strings.HasPrefix(msg, "ok") {
		t.Fatalf("verify: %q %v", msg, err)
	}
	// tamper → changed (append keeps the # header so the name still resolves)
	installed, _, _ := sk.Get("s")
	_ = os.WriteFile(filepath.Join(installed.Path, "SKILL.md"), []byte("# s\nDescription: d\n\nB.\nTAMPERED.\n"), 0o644)
	if _, err := Verify("s", sk, pl); err == nil {
		t.Fatal("expected CHANGED")
	}
	// wrong checksum on install → error + cleaned up
	bad := Entry{Name: "s2", Kind: "skill", Source: remote, Subdir: "pkg", SHA256: "deadbeef"}
	if _, err := Install(bad, sk, pl); err == nil {
		t.Fatal("expected checksum mismatch")
	}
	if _, _, err := sk.Get("s2"); err == nil {
		t.Fatal("mismatched install should be removed")
	}
	// remove
	if _, err := Remove("s", sk, pl); err != nil {
		t.Fatal(err)
	}
	if _, err := Remove("s", sk, pl); err == nil {
		t.Fatal("expected not-installed")
	}
	if got := VerifyAll(sk, pl); len(got) == 0 {
		t.Fatal("verify-all empty")
	}
}

func mustHash(t *testing.T, dir string) string {
	t.Helper()
	h, err := dirHash(dir)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestDirHashLineEndings(t *testing.T) {
	mk := func(content string) string {
		d := t.TempDir()
		_ = os.WriteFile(filepath.Join(d, "f.md"), []byte(content), 0o644)
		return d
	}
	lf := mustHash(t, mk("a\nb\n"))
	crlf := mustHash(t, mk("a\r\nb\r\n"))
	if lf != crlf {
		t.Fatalf("LF vs CRLF differ:\n%s\n%s", lf, crlf)
	}
	cr := mustHash(t, mk("a\rb\r"))
	if cr != lf {
		t.Fatal("lone CR should canonicalize too")
	}
}
