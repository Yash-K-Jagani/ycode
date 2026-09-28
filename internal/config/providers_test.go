package config

import (
	"testing"

	"github.com/Yash-K-Jagani/ycode/internal/providers"
)

// The provider table and this package's key accessors are two halves of one
// thing: the registry says which providers need a key, and keyAccessors says
// where the key is stored. If they disagree, a provider silently has no
// working credentials and every request fails with "not set".

func TestEveryKeyBearingProviderHasAnAccessor(t *testing.T) {
	for _, s := range providers.All() {
		t.Run(s.ID, func(t *testing.T) {
			var c Config
			if !s.NeedsKey {
				// A local provider must not be able to hold a key, or the
				// registry would be lying about needing one.
				if got := c.KeyFor(s.ID); got != "" {
					t.Fatalf("%s stored a key with no field to hold it", s.ID)
				}
				if c.SetKeyFor(s.ID, "k") {
					t.Fatalf("%s accepted a key it has nowhere to put", s.ID)
				}
				return
			}
			if _, ok := keyAccessors[s.ID]; !ok {
				t.Fatalf("%s needs a key but keyAccessors has no entry for it", s.ID)
			}
			// Round-trip: a key set for this provider must not appear on
			// another provider's field.
			if !c.SetKeyFor(s.ID, "secret-"+s.ID) {
				t.Fatalf("SetKeyFor(%s) refused", s.ID)
			}
			if got := c.KeyFor(s.ID); got != "secret-"+s.ID {
				t.Fatalf("KeyFor(%s) = %q after setting it", s.ID, got)
			}
			for _, other := range providers.All() {
				if other.ID == s.ID || !other.NeedsKey {
					continue
				}
				if got := c.KeyFor(other.ID); got != "" {
					t.Fatalf("setting %s's key also set %s's key to %q", s.ID, other.ID, got)
				}
			}
		})
	}
}

func TestAccessorTableHasNoStrays(t *testing.T) {
	for id := range keyAccessors {
		s := providers.Get(id)
		if s == nil {
			t.Fatalf("keyAccessors has %q, which is not a registered provider", id)
		}
		if !s.NeedsKey {
			t.Fatalf("keyAccessors stores a key for %q, which needs none", id)
		}
	}
}

func TestKeyEnvMatchesRegistry(t *testing.T) {
	for _, s := range providers.All() {
		if !s.NeedsKey {
			if got := Defaults().KeyEnvFor(s.ID); got != "" {
				t.Fatalf("%s needs no key but KeyEnvFor returned %q", s.ID, got)
			}
			continue
		}
		got := Defaults().KeyEnvFor(s.ID)
		if got == "" {
			t.Fatalf("%s needs a key but has no env var name", s.ID)
		}
		if got != s.KeyEnv {
			t.Fatalf("%s KeyEnv = %q but the registry says %q; the two must agree", s.ID, got, s.KeyEnv)
		}
	}
	if got := Defaults().KeyEnvFor("nope"); got != "" {
		t.Fatalf("an unknown provider should have no env var, got %q", got)
	}
}

// SetupNeeded must agree with the registry about who needs a key.
func TestSetupNeededFollowsRegistry(t *testing.T) {
	for _, s := range providers.All() {
		c := Defaults()
		c.ActiveProvider = s.ID
		c.ActiveModel = providers.DefaultModel(s.ID)
		state := SetupNeeded(c)

		if s.NeedsKey {
			if state.Ready {
				t.Fatalf("%s needs a key but SetupNeeded reported ready", s.ID)
			}
			if !state.NeedsKey {
				t.Fatalf("%s: SetupNeeded did not flag the missing key", s.ID)
			}
			if state.KeyEnv != c.KeyEnvFor(s.ID) {
				t.Fatalf("%s: KeyEnv = %q, want %q", s.ID, state.KeyEnv, c.KeyEnvFor(s.ID))
			}
			// Supplying the key must make it ready, and only that provider's.
			c.SetKeyFor(s.ID, "k")
			if !SetupNeeded(c).Ready {
				t.Fatalf("%s: still unready after the key was set", s.ID)
			}
		} else if !state.Ready {
			t.Fatalf("%s needs no key but SetupNeeded said %q", s.ID, state.Reason)
		}
	}
}
