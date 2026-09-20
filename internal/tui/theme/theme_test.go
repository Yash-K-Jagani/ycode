package theme

import (
	"strings"
	"testing"
)

func TestSplash(t *testing.T) {
	s := Splash("#fff")
	if !strings.Contains(s, "ycode") {
		t.Fatalf("splash should contain plain ycode wordmark:\n%s", s)
	}
}
