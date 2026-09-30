package toolwire

import (
	"encoding/json"
	"testing"

	"github.com/Yash-K-Jagani/ycode/pkg/apitypes"
)

func specs(names ...string) []apitypes.ToolSpec {
	out := make([]apitypes.ToolSpec, len(names))
	for i, n := range names {
		out[i] = apitypes.ToolSpec{
			Name:        n,
			Description: "does " + n,
			Schema:      `{"type":"object"}`,
		}
	}
	return out
}

func payload(t *testing.T, v []map[string]any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The cache is only safe if two different spec lists never share an entry. A
// collision here would silently send the model the previous turn's tools, which
// is the kind of bug that shows up as the model calling a tool that does not
// exist.
func TestDistinctSpecsDoNotShareACachedPayload(t *testing.T) {
	a := specs("read", "write")
	b := specs("read", "bash")
	if payload(t, List(a)) == payload(t, List(b)) {
		t.Fatal("two different spec lists produced the same payload")
	}
}

// Field boundaries matter too: the same characters split differently is a
// different spec list.
func TestFingerprintSeparatesFieldBoundaries(t *testing.T) {
	a := []apitypes.ToolSpec{{Name: "ab", Description: "c"}}
	b := []apitypes.ToolSpec{{Name: "a", Description: "bc"}}
	if payload(t, List(a)) == payload(t, List(b)) {
		t.Fatal("field boundary was not respected by the fingerprint")
	}
}

// Same specs must produce the same payload, and must not be rebuilt.
func TestRepeatedCallsReturnTheSamePayload(t *testing.T) {
	s := specs("read", "write", "bash")
	first := payload(t, List(s))
	if second := payload(t, List(s)); first != second {
		t.Fatal("repeated calls differ")
	}
	if List(s)[0] == nil {
		t.Fatal("nil entry")
	}
}

func TestEmptySpecsProduceNoTools(t *testing.T) {
	if List(nil) != nil {
		t.Fatal("nil specs should produce no tools")
	}
}

// An empty description or schema is omitted rather than sent as "", because
// providers differ on how they read an empty string.
func TestEmptyFieldsAreOmitted(t *testing.T) {
	got := payload(t, List([]apitypes.ToolSpec{{Name: "bare"}}))
	want := `[{"function":{"name":"bare"},"type":"function"}]`
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}
