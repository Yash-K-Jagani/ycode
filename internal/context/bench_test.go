package ctx

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func makeTreeFixtureAt(dir string, n int) {
	// The directories are created once, up front.
	//
	// This used to call MkdirAll for every file, 10,000 times for the same
	// ~100 directories, and each call re-stats the whole path chain. Fixture
	// creation was the dominant cost of this package rather than Tree itself:
	// measured at 972ms for 1,000 files and 24.4s for 10,000 - superlinear in the
	// number of files, for a set of directories that never grows.
	for _, d := range treeFixtureDirs(dir) {
		if err := os.MkdirAll(d, 0o755); err != nil {
			panic("tree fixture: " + err.Error())
		}
	}
	for i := range n {
		var p string
		switch i % 3 {
		case 0:
			p = filepath.Join(dir, "flat", "file"+strconv.Itoa(i)+".go")
		case 1:
			p = filepath.Join(dir, "deep", "a", "b", "file"+strconv.Itoa(i)+".go")
		case 2:
			p = filepath.Join(dir, "mixed_"+strconv.Itoa(i%100), "file"+strconv.Itoa(i)+".txt")
		}
		if err := os.WriteFile(p, []byte("package x\n"), 0o644); err != nil {
			panic("tree fixture: " + err.Error())
		}
	}
}

// treeFixtureDirs is every directory makeTreeFixtureAt can write into: the two
// fixed ones, plus one per mixed_N bucket.
func treeFixtureDirs(dir string) []string {
	dirs := make([]string, 0, 102)
	dirs = append(dirs, filepath.Join(dir, "flat"), filepath.Join(dir, "deep", "a", "b"))
	for i := range 100 {
		dirs = append(dirs, filepath.Join(dir, "mixed_"+strconv.Itoa(i)))
	}
	return dirs
}

func makeTreeFixture(t *testing.T, n int) string {
	t.Helper()
	dir := t.TempDir()
	makeTreeFixtureAt(dir, n)
	return dir
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

// 2000 rather than 10000, which is what this used to build.
//
// Tree caps at 150 entries and 4000 characters, so a 10,000-file tree exercises
// nothing a 2,000-file one does not - both produce about 3 KB of output, well
// under the cap - and cost four times as much to create. The 10k tree is kept in
// BenchmarkTree10k, where b.ResetTimer() excludes construction from the
// measurement, so the large scale is still covered by the number that needs it.
const threeFixtureFiles = 2000

// Called three times on one fixture, because the point is that a repeated call
// gives the same answer.
//
// This used to build three 10,000-file fixtures and check each produced a
// non-empty string - three times the cost, and the check could not distinguish
// the first call from the third. Given identical inputs and a function that
// caches nothing, the only way repeated calls can disagree is if the directory
// walk is order-dependent or state leaks between calls, so comparing the
// results against each other tests that directly. Building one fixture instead of
// three also removes the only reason the three could have differed at all.
func TestThreeFixtures(t *testing.T) {
	dir := makeTreeFixture(t, threeFixtureFiles)
	first := Tree(dir, 150, 4000)
	if first == "" {
		t.Fatal("empty tree")
	}
	for k := range 2 {
		got := Tree(dir, 150, 4000)
		if got != first {
			t.Fatalf("call %d differs from the first:\nfirst: %s\ngot:   %s", k+2, first, got)
		}
	}
}
