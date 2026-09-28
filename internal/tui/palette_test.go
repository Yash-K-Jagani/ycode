package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/Yash-K-Jagani/ycode/internal/modes"
)

// Every registered mode must be discoverable in the palette, and no mode may
// appear that the registry does not know about.
func TestPaletteListsEveryMode(t *testing.T) {
	have := map[string]string{}
	for _, it := range slashList() {
		if _, dup := have[it.Name]; dup {
			t.Fatalf("duplicate palette entry %q", it.Name)
		}
		have[it.Name] = it.Hint
		if it.Hint == "" {
			t.Fatalf("palette entry %q has no hint", it.Name)
		}
	}
	for _, md := range modes.All() {
		name := modes.Slash(md)
		hint, ok := have[name]
		if !ok {
			t.Fatalf("mode %s is missing from the palette (expected %q)", md, name)
		}
		if !strings.Contains(hint, "mode") {
			t.Fatalf("palette entry %q has hint %q, which does not read as a mode", name, hint)
		}
		// A mode command must carry the shared mode icon, not the default dot.
		if icon := iconFor(name); icon != modes.Icon(md) {
			t.Fatalf("iconFor(%q) = %q, want %q from the registry", name, icon, modes.Icon(md))
		}
	}
	// The reverse: a palette entry that is not a command or a mode is a bug.
	for name := range have {
		if strings.HasPrefix(name, "/") && !strings.Contains(name, " ") {
			if modes.SlashForCommand(name) == "" && !knownCommand(name) {
				t.Fatalf("palette lists %q but nothing handles it", name)
			}
		}
	}
}

// knownCommand is the slash registry's own list of handlers.
func knownCommand(name string) bool {
	_, ok := slashRegistry()[name]
	return ok
}

// Every registered mode must be selectable by its slash command. The handlers
// are generated from the registry, so this is really a check that generation
// did not skip or overwrite one.
func TestEveryModeIsSelectable(t *testing.T) {
	reg := slashRegistry()
	for _, md := range modes.All() {
		name := modes.Slash(md)
		h, ok := reg[name]
		if !ok {
			t.Fatalf("mode %s has no handler for %q", md, name)
		}
		m := &Model{mode: modes.Chat}
		out, cmd := h(context.Background(), m, "")
		if cmd != nil {
			t.Fatalf("%s started work for a bare switch", name)
		}
		if md == modes.Goal {
			// /goal does more than switch mode, so a bare one explains itself
			// rather than starting a run.
			if !strings.Contains(out, "Usage") {
				t.Fatalf("bare /goal should reply with usage, got %q", out)
			}
			continue
		}
		if out != "" {
			t.Fatalf("%s returned output for a bare switch: %q", name, out)
		}
		if m.mode != md {
			t.Fatalf("%s did not switch mode (still %s)", name, m.mode)
		}
	}
}

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
