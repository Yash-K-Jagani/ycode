package security

import (
	"strings"
	"testing"
)

func TestRedact(t *testing.T) {
	in := `token = "ghp_1234567890123456789012345678901234" and AKIAIOSFODNN7EXAMPLE ok`
	got := Redact(in)
	if strings.Contains(got, "ghp_1234") || strings.Contains(got, "AKIAIOSF") {
		t.Fatalf("leak: %q", got)
	}
	if !strings.Contains(got, "[REDACTED:github-token]") || !strings.Contains(got, "[REDACTED:aws-key]") {
		t.Fatalf("markers missing: %q", got)
	}
	if Redact("clean text") != "clean text" {
		t.Fatal("clean text altered")
	}
}
