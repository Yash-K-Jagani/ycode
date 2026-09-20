package lang

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mkproj(t *testing.T, files ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, f := range files {
		p := filepath.Join(dir, f)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, []byte("x"), 0o644)
	}
	return dir
}

func TestDetect(t *testing.T) {
	cases := []struct {
		files []string
		lang  string
		test0 string
	}{
		{[]string{"go.mod"}, "Go", "go"},
		{[]string{"Cargo.toml"}, "Rust", "cargo"},
		{[]string{"package.json"}, "JavaScript", "npm"},
		{[]string{"package.json", "bun.lockb"}, "Bun", "bun"},
		{[]string{"deno.json"}, "Deno", "deno"},
		{[]string{"pom.xml"}, "Maven", "mvn"},
		{[]string{"build.gradle"}, "Gradle", "gradle"},
		{[]string{"app.csproj"}, "C#", "dotnet"},
		{[]string{"phpunit.xml"}, "PHP", "phpunit"},
		{[]string{"composer.json"}, "PHP", "phpunit"},
		{[]string{"Gemfile"}, "Ruby", "bundle"},
		{[]string{"pytest.ini"}, "Python", "python"},
	}
	for _, c := range cases {
		p, ok := Detect(mkproj(t, c.files...))
		if !ok {
			t.Fatalf("%v: not detected", c.files)
		}
		if !strings.Contains(p.Language, c.lang) || !strings.HasPrefix(p.TestCmd[0], c.test0) {
			t.Fatalf("%v: got %+v", c.files, p)
		}
	}
	// nested file resolves upward
	dir := mkproj(t, "go.mod")
	sub := filepath.Join(dir, "a", "b")
	_ = os.MkdirAll(sub, 0o755)
	if p, ok := Detect(sub); !ok || p.Language != "Go" || p.Root != dir {
		t.Fatalf("upward: %+v %v", p, ok)
	}
	if _, ok := Detect(mkproj(t, "notes.txt")); ok {
		t.Fatal("false positive")
	}
	if ShortName("Java (Maven)") != "Java" {
		t.Fatal("short name")
	}
}
