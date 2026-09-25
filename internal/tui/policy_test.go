package tui

import (
	"strings"
	"testing"
)

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

func TestDelegatesToUser(t *testing.T) {
	for _, s := range []string{
		"you can run the tests yourself",
		"You should check the files yourself",
		"do it yourself please",
		"try running npm test",
		"as an AI, I can't access files",
	} {
		if !delegatesToUser(s) {
			t.Fatalf("miss: %q", s)
		}
	}
	for _, s := range []string{
		"here is the summary",
		"the file contains X",
		"done",
	} {
		if delegatesToUser(s) {
			t.Fatalf("false positive: %q", s)
		}
	}
}

func TestShellToCalls(t *testing.T) {
	rm := shellToCalls("rm index.html", "delete index.html")
	if len(rm) != 1 || rm[0].Name != "delete" || !strings.Contains(string(rm[0].Args), "index.html") {
		t.Fatalf("%+v", rm)
	}
	touch := shellToCalls("```touch style.css```", "create style.css")
	if len(touch) != 1 || touch[0].Name != "write" {
		t.Fatalf("%+v", touch)
	}
	for _, bad := range []string{
		"rm -rf /", "rm a b", "echo hi", "see rm index.html here",
		"rm index.html\nsecond line", "",
	} {
		if shellToCalls(bad, "delete index.html") != nil {
			t.Fatalf("must reject %q", bad)
		}
	}
	if shellToCalls("rm index.html", "what is this") != nil {
		t.Fatal("needs task intent")
	}
}

func TestDeleteAndClaimDetect(t *testing.T) {
	for _, s := range []string{"delete notes.txt", "remove the file old.log", "erase temp dir"} {
		if !looksLikeDeleteTask(s) {
			t.Fatalf("miss: %q", s)
		}
	}
	if looksLikeDeleteTask("list files") {
		t.Fatal("false positive")
	}
	for _, s := range []string{"done, deleted it", "file created successfully"} {
		if !claimsCompletion(s) {
			t.Fatalf("miss: %q", s)
		}
	}
	if claimsCompletion("nothing to report") {
		t.Fatal("false positive")
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
