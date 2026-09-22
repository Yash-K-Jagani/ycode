package tui

import "testing"

func TestLooksLikeWriteTask(t *testing.T) {
	for _, s := range []string{
		"write hello to notes.txt",
		"create a file with the config",
		"save this in README",
		"put the script in run.sh",
	} {
		if !looksLikeWriteTask(s) {
			t.Fatalf("miss: %q", s)
		}
	}
	for _, s := range []string{
		"what is a file",
		"list models",
		"read main.go",
	} {
		if looksLikeWriteTask(s) {
			t.Fatalf("false positive: %q", s)
		}
	}
	if !hasCodeFence("hi\n```go\nx\n```") || hasCodeFence("plain") {
		t.Fatal("fence detect wrong")
	}
	for _, s := range []string{
		`<write_result>E:\x\index.html</write_result>`,
		`done <tool_result:read>{"a":1}</tool_result:read>`,
	} {
		if !hasResultRoleplay(s) {
			t.Fatalf("miss: %q", s)
		}
	}
	if hasResultRoleplay("just prose") || hasResultRoleplay(`<tool:read>{"a":1}</tool:read>`) {
		t.Fatal("call tags must not count as roleplay")
	}
}

func TestIsBuildIt(t *testing.T) {
	for _, s := range []string{
		"build it", "Build This", "now build", "go ahead", "proceed",
		"do it", "implement it", "approved", "approve", "yes build",
		"start building", "get started",
	} {
		if !isBuildIt(s) {
			t.Fatalf("miss: %q", s)
		}
	}
	for _, s := range []string{
		"what does build do", "rebuild the index", "read go.mod", "",
	} {
		if isBuildIt(s) {
			t.Fatalf("false positive: %q", s)
		}
	}
}
