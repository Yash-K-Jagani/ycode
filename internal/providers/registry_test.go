package providers

import (
	"testing"
)

// The registry is the single source of truth for what a provider is. These
// tests guard the properties the rest of the tree relies on rather than
// re-listing the contents, which would just be a second copy to maintain.

func TestSpecsAreWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range All() {
		if s.ID == "" {
			t.Fatal("a provider has no ID")
		}
		if seen[s.ID] {
			t.Fatalf("provider %s is registered twice", s.ID)
		}
		seen[s.ID] = true
		if s.Label == "" {
			t.Fatalf("%s has no label for the picker", s.ID)
		}
		if s.New == nil {
			t.Fatalf("%s has no constructor", s.ID)
		}
		if len(s.Models) == 0 {
			t.Fatalf("%s has no models", s.ID)
		}
		found := false
		for _, m := range s.Models {
			if m == s.Recommended {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s recommends %q, which is not among its models %v", s.ID, s.Recommended, s.Models)
		}
		// A key-bearing provider must say where the key comes from.
		if s.NeedsKey && s.KeyEnv == "" {
			t.Fatalf("%s needs a key but names no environment variable", s.ID)
		}
		if !s.NeedsKey && s.KeyEnv != "" {
			t.Fatalf("%s needs no key yet names %q", s.ID, s.KeyEnv)
		}
		// Only a local provider may be free, or spend would go untracked.
		if s.PromptUSD == 0 && s.ComplUSD == 0 && !s.Local {
			t.Fatalf("%s is cloud but priced at zero", s.ID)
		}
		if s.Local && (s.PromptUSD != 0 || s.ComplUSD != 0) {
			t.Fatalf("%s is local but priced", s.ID)
		}
	}
}

func TestGetResolvesNames(t *testing.T) {
	if len(IDs()) != len(All()) {
		t.Fatal("IDs and All disagree in length")
	}
	for i, id := range IDs() {
		if All()[i].ID != id {
			t.Fatalf("IDs()[%d] = %q, All()[%d] = %q", i, id, i, All()[i].ID)
		}
		if !Known(id) {
			t.Fatalf("%q is listed but not Known", id)
		}
		if s := Get(id); s == nil || s.ID != id {
			t.Fatalf("Get(%q) did not return itself", id)
		}
	}
	// An empty name means the default, which is local: nothing should be sent
	// off the machine because a field was left blank.
	if s := Get(""); s == nil || !s.Local {
		t.Fatalf("the default provider should be local, got %v", s)
	}
	if Get("nope") != nil {
		t.Fatal("an unknown provider should not resolve")
	}
	if Known("nope") {
		t.Fatal("an unknown provider must not be Known")
	}
}

// Zero-data-leak asks "is this local?", so an unrecognised name must not be
// reported as cloud: blocking on a typo would be worse than allowing it,
// which is the caller's decision to make.
func TestLocalDefaultsToTrueForUnknownNames(t *testing.T) {
	if !Local("nope") {
		t.Fatal("an unknown name should not be classified as cloud")
	}
	if !Local("") {
		t.Fatal("the default provider should be local")
	}
	found := false
	for _, s := range All() {
		if s.Local {
			found = true
		}
		if s.Local != Local(s.ID) {
			t.Fatalf("%s: Local() = %v, spec says %v", s.ID, Local(s.ID), s.Local)
		}
	}
	if !found {
		t.Fatal("no local provider is registered, so zero-data-leak mode is unusable")
	}
}

func TestPricingAndFree(t *testing.T) {
	for _, s := range All() {
		p, c := Pricing(s.ID)
		if p != s.PromptUSD || c != s.ComplUSD {
			t.Fatalf("%s: Pricing = (%v, %v), spec has (%v, %v)", s.ID, p, c, s.PromptUSD, s.ComplUSD)
		}
		if Free(s.ID) != s.Local {
			t.Fatalf("%s: Free = %v, but Local = %v", s.ID, Free(s.ID), s.Local)
		}
		if DefaultModel(s.ID) != s.Recommended {
			t.Fatalf("%s: DefaultModel = %q", s.ID, DefaultModel(s.ID))
		}
	}
	// An unknown provider is reported as metered, not free: under-reporting
	// spend is the worse error.
	if Free("nope") {
		t.Fatal("an unknown provider must not be reported as free")
	}
	if p, c := Pricing("nope"); p != 0 || c != 0 {
		t.Fatalf("an unknown provider has prices (%v, %v)", p, c)
	}
	if DefaultModel("nope") != "" {
		t.Fatal("an unknown provider has no recommended model")
	}
}

func TestConstructorsHonourTheirCredentials(t *testing.T) {
	for _, s := range All() {
		p := s.New("test-key", "http://localhost:11434")
		if p == nil {
			t.Fatalf("%s built a nil client", s.ID)
		}
		// Name must match the registry, or stats and cost tracking attribute
		// calls to the wrong provider.
		if p.Name() != s.ID {
			t.Fatalf("%s built a client that calls itself %q", s.ID, p.Name())
		}
	}
}
