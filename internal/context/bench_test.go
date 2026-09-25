package ctx

import (
	"os"
	"path/filepath"
	"testing"
)

func makeTreeFixtureAt(dir string, n int) {
	for i := 0; i < n; i++ {
		var p string
		switch i % 3 {
		case 0:
			p = filepath.Join(dir, "flat", "file"+itoa(i)+".go")
		case 1:
			p = filepath.Join(dir, "deep", "a", "b", "file"+itoa(i)+".go")
		case 2:
			p = filepath.Join(dir, "mixed_"+itoa(i%100), "file"+itoa(i)+".txt")
		}
		_ = os.MkdirAll(filepath.Dir(p), 0755)
		_ = os.WriteFile(p, []byte("package x\n"), 0644)
	}
}

func makeTreeFixture(t *testing.T, n int) string {
	t.Helper()
	dir := t.TempDir()
	makeTreeFixtureAt(dir, n)
	return dir
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	b := make([]byte, 0, 6)
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func BenchmarkTree10k(b *testing.B) {
	dir, _ := os.MkdirTemp("", "bench10k")
	defer func() { _ = os.RemoveAll(dir) }()
	makeTreeFixtureAt(dir, 10000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Tree(dir, 150, 4000)
	}
}

func TestTreeFixtureIntegrity(t *testing.T) {
	for _, n := range []int{100, 1000} {
		dir := makeTreeFixture(t, n)
		out := Tree(dir, 150, 4000)
		if out == "" {
			t.Fatalf("empty tree for n=%d", n)
		}
	}
}

func TestThreeFixtures(t *testing.T) {
	base := t.TempDir()
	for k := 1; k <= 3; k++ {
		dir := filepath.Join(base, "fixture"+itoa(k))
		_ = os.MkdirAll(dir, 0755)
		makeTreeFixtureAt(dir, 10000)
		// sanity: Tree caps at 150 entries
		out := Tree(dir, 150, 4000)
		if out == "" {
			t.Fatalf("empty fixture %d", k)
		}
	}
}
