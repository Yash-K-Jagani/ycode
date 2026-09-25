package distribution

import "testing"

// The values below mirror the winget stanza in .goreleaser.yaml. If you change
// one, change both, and let Problems() tell you if winget-pkgs will accept it.
func currentWinget() Winget {
	return Winget{
		Publisher:           "Yash-K-Jagani",
		PublisherURL:        "https://github.com/Yash-K-Jagani",
		PublisherSupportURL: "https://github.com/Yash-K-Jagani/ycode/issues",
		ShortDescription:    "Terminal-first AI coding harness with a local-first agent loop",
		Description:         "Single-binary terminal coding assistant with a real agent tool loop.",
		PackageName:         "ycode",
		Homepage:            "https://github.com/Yash-K-Jagani/ycode",
		License:             "MIT",
		LicenseURL:          "https://github.com/Yash-K-Jagani/ycode/blob/main/LICENSE",
		Tags:                []string{"cli", "ai", "terminal"},
	}
}

func TestWingetMetadataIsAcceptable(t *testing.T) {
	w := currentWinget()
	if problems := w.Problems("ycode"); len(problems) != 0 {
		t.Errorf("winget manifest would be rejected by winget-pkgs:")
		for _, p := range problems {
			t.Errorf("  - %s", p)
		}
	}
}

func TestWingetIdentifierMatchesReadme(t *testing.T) {
	// The README tells users to run `winget install Yash-K-Jagani.ycode`.
	if got, want := currentWinget().Identifier("ycode"), "Yash-K-Jagani.ycode"; got != want {
		t.Errorf("Identifier = %q, want %q", got, want)
	}
}

func TestWingetIdentifierStripsSpaces(t *testing.T) {
	w := Winget{Publisher: "Foo Inc"}
	if got, want := w.Identifier("bar"), "FooInc.bar"; got != want {
		t.Errorf("Identifier = %q, want %q", got, want)
	}
}

func TestWingetProblems(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Winget)
		wantSub string
	}{
		{"missing publisher", func(w *Winget) { w.Publisher = "" }, "publisher is required"},
		{"missing license", func(w *Winget) { w.License = "" }, "license is required"},
		{"missing short description", func(w *Winget) { w.ShortDescription = "" }, "short_description is required"},
		{"missing description", func(w *Winget) { w.Description = "" }, "description is required"},
		{"missing publisher url", func(w *Winget) { w.PublisherURL = "" }, "publisher_url is required"},
		{"missing support url", func(w *Winget) { w.PublisherSupportURL = "" }, "publisher_support_url is required"},
		{"short description too long", func(w *Winget) { w.ShortDescription = repeat("x", 81) }, "limit 80"},
		{"description too long", func(w *Winget) { w.Description = repeat("x", 102) }, "limit 101"},
		{"package name too long", func(w *Winget) { w.PackageName = repeat("x", 65) }, "limit 64"},
		{"too many tags", func(w *Winget) { w.Tags = []string{"a", "b", "c", "d"} }, "at most 3"},
		{"tag too long", func(w *Winget) { w.Tags = []string{repeat("t", 65)} }, "limit 64"},
		{"identifier too long", func(w *Winget) { w.Publisher = repeat("P", 45) }, "package identifier"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := currentWinget()
			tt.mutate(&w)
			problems := w.Problems("ycode")
			if len(problems) == 0 {
				t.Fatalf("expected a problem mentioning %q, got none", tt.wantSub)
			}
			found := false
			for _, p := range problems {
				if contains(p, tt.wantSub) {
					found = true
				}
			}
			if !found {
				t.Errorf("problems %v do not mention %q", problems, tt.wantSub)
			}
		})
	}
}

// Limits are inclusive: a string exactly at the limit must pass.
func TestWingetLimitsAreInclusive(t *testing.T) {
	w := currentWinget()
	w.ShortDescription = repeat("x", WingetMaxShortDesc)
	w.Description = repeat("y", WingetMaxDescription)
	if problems := w.Problems("ycode"); len(problems) != 0 {
		t.Errorf("values exactly at the limit should be accepted, got %v", problems)
	}
}

func repeat(s string, n int) string {
	out := make([]byte, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, s[0])
	}
	return string(out)
}

func contains(haystack, needle string) bool {
	return len(needle) == 0 || indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
