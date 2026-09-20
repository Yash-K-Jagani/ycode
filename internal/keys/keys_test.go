package keys

import (
	"testing"
)

func TestRoundTrip(t *testing.T) {
	const acct = "ycode-test-acct"
	defer func() { _ = Delete(acct) }()
	if err := Set(acct, "s3cr3t"); err != nil {
		t.Skipf("no keyring backend: %v", err)
	}
	got, ok := Get(acct)
	if !ok || got != "s3cr3t" {
		t.Fatalf("got %q %v", got, ok)
	}
	if err := Delete(acct); err != nil {
		t.Fatal(err)
	}
	if _, ok := Get(acct); ok {
		t.Fatal("should be gone")
	}
	if err := Set("", "x"); err == nil {
		t.Fatal("expected account error")
	}
}
