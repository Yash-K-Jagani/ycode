package tui

import (
	"strings"
	"testing"
)

func TestFilterSlash(t *testing.T) {
	if got := filterSlash("hello"); len(got) != 0 {
		t.Fatal("non-slash should give nothing")
	}
	all := filterSlash("/")
	if len(all) != len(slashList()) {
		t.Fatalf("bare / should list all: %d vs %d", len(all), len(slashList()))
	}
	got := filterSlash("/mod")
	if len(got) != 2 {
		t.Fatalf("bad /mod filter: %+v", got)
	}
	has := map[string]bool{}
	for _, it := range got {
		has[it.Name] = true
	}
	if !has["/models"] || !has["/model"] {
		t.Fatalf("bad /mod filter: %+v", got)
	}
	got = filterSlash("/models ollama")
	if len(got) != 1 || got[0].Name != "/models" {
		t.Fatalf("args should not break filter: %+v", got)
	}
	if got := filterSlash("/zzz"); len(got) != 0 {
		t.Fatal("no match expected")
	}
	if s := renderPalette(nil, 0, 80, "#fff"); s != "" {
		t.Fatal("empty palette expected")
	}
	if s := renderPalette(filterSlash("/"), 0, 80, "#fff"); !strings.Contains(s, "/models") {
		t.Fatal("palette should contain /models")
	}
}
