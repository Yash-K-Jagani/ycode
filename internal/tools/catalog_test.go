package tools

import (
	"strings"
	"testing"
	"time"
)

// The catalog is what makes a tool's network capability and budget
// un-omittable: it is the same row that registers the tool. These tests guard
// the properties that guarantee that, rather than restating the contents.

func TestCatalogNamesMatchTheToolsTheyRegister(t *testing.T) {
	dir := t.TempDir()
	seen := map[string]bool{}
	for _, e := range catalog {
		if e.name == "" {
			t.Fatal("a catalog row has no name")
		}
		if seen[e.name] {
			t.Fatalf("tool %s appears twice in the catalog", e.name)
		}
		seen[e.name] = true
		tool := e.build(dir)
		if tool == nil {
			t.Fatalf("%s built a nil tool", e.name)
		}
		// A typo here would register a tool whose policy lookups all miss, so
		// it would be neither classified nor bounded.
		if tool.Name() != e.name {
			t.Fatalf("catalog says %q but the tool calls itself %q", e.name, tool.Name())
		}
		if e.build(dir) == nil {
			t.Fatalf("%s is not reproducible", e.name)
		}
	}
}

// Every tool the registry hands out must be in the catalog, so no tool can be
// reachable without a declared policy.
func TestDefaultRegistryContainsOnlyCatalogTools(t *testing.T) {
	reg := DefaultRegistry(t.TempDir())
	names := reg.Names()
	if len(names) != len(catalog) {
		t.Fatalf("the registry has %d tools, the catalog has %d", len(names), len(catalog))
	}
	for _, n := range names {
		if entryFor(n) == nil {
			t.Fatalf("%s is registered but has no catalog row", n)
		}
	}
	// And the catalog must not contain a tool the registry fails to build.
	for _, n := range ToolNames() {
		if _, ok := reg.Get(n); !ok {
			t.Fatalf("%s is in the catalog but not in the default registry", n)
		}
	}
}

func TestEveryCatalogToolHasABudget(t *testing.T) {
	for _, e := range catalog {
		if e.timeout <= 0 {
			t.Fatalf("%s has no timeout, so it could run forever", e.name)
		}
		// The loop's own cap must never be the binding constraint; that was
		// the bug the budget table fixed.
		if e.timeout > 15*time.Minute {
			t.Fatalf("%s allows %s, which is long enough to look hung", e.name, e.timeout)
		}
		if got := TimeoutFor(e.name); got != e.timeout {
			t.Fatalf("%s: TimeoutFor = %s, catalog says %s", e.name, got, e.timeout)
		}
	}
}

// A network-capable tool that is not marked is the exact omission that made the
// zero-data-leak promise false, so the set is pinned here.
func TestNetworkClassificationMatchesIntent(t *testing.T) {
	wantNetwork := map[string]bool{
		"api": true, "browser": true, "github": true, "git": true,
		"db": true, "notebook": true, "scaffold": true, "vscode": true,
	}
	for _, e := range catalog {
		if wantNetwork[e.name] && !e.network {
			t.Fatalf("%s reaches the network but is not declared as doing so", e.name)
		}
		if !wantNetwork[e.name] && e.network {
			t.Fatalf("%s is newly declared network-capable; confirm that is intended", e.name)
		}
		if e.network && e.note == "" {
			t.Fatalf("%s is network-capable with no note saying why", e.name)
		}
		if got := ReachesNetwork(e.name); got != e.network {
			t.Fatalf("%s: ReachesNetwork = %v, catalog says %v", e.name, got, e.network)
		}
	}
	// Case must not matter, or a differently-cased call would slip past.
	for _, e := range catalog {
		if e.network && !ReachesNetwork(strings.ToUpper(e.name)) {
			t.Fatalf("%s: uppercase name was not classified", e.name)
		}
	}
	if got := NetworkTools(); len(got) != len(wantNetwork) {
		t.Fatalf("NetworkTools() = %v, want %d entries", got, len(wantNetwork))
	}
}

// The tools that execute arbitrary code are the reason zero-data-leak is a
// guardrail and not a sandbox. If that ever changes, this fails.
func TestResidualRiskToolsAreDocumented(t *testing.T) {
	for _, name := range []string{"bash", "run"} {
		e := entryFor(name)
		if e == nil {
			t.Fatalf("%s is not in the catalog", name)
		}
		if e.network {
			t.Fatalf("%s is now declared network-capable; update the banner and docs/security.md", name)
		}
		if !strings.Contains(e.note, "residual risk") {
			t.Fatalf("%s executes arbitrary code but its note does not say so: %q", name, e.note)
		}
	}
}
