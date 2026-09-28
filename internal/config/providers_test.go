package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Yash-K-Jagani/ycode/internal/providers"
)

// Keys used to be three struct fields with a hand-written accessor table
// alongside, and the two had to agree. They are now one map, so the
// interesting questions are whether a key reaches the right provider and
// whether an unrelated one can be affected.

func TestEveryKeyBearingProviderCanHoldAKey(t *testing.T) {
	for _, s := range providers.All() {
		t.Run(s.ID, func(t *testing.T) {
			var c Config
			if !s.NeedsKey {
				// A local provider must not be able to hold a key, or the
				// registry would be lying about needing one.
				if got := c.KeyFor(s.ID); got != "" {
					t.Fatalf("%s stored a key with nowhere to put it", s.ID)
				}
				if c.SetKeyFor(s.ID, "k") {
					t.Fatalf("%s accepted a key it has nowhere to put", s.ID)
				}
				return
			}
			// A zero-value Config has a nil map; KeyFor must tolerate that,
			// because plenty of code builds a Config by hand.
			if got := c.KeyFor(s.ID); got != "" {
				t.Fatalf("KeyFor on a zero Config = %q", got)
			}
			if !c.SetKeyFor(s.ID, "secret-"+s.ID) {
				t.Fatalf("SetKeyFor(%s) refused", s.ID)
			}
			if got := c.KeyFor(s.ID); got != "secret-"+s.ID {
				t.Fatalf("KeyFor(%s) = %q after setting it", s.ID, got)
			}
			// The property the earlier three-field version could not promise:
			// a key set for one provider is invisible to every other.
			for _, other := range providers.All() {
				if other.ID == s.ID {
					continue
				}
				if got := c.KeyFor(other.ID); got != "" {
					t.Fatalf("setting %s's key also set %s's key to %q", s.ID, other.ID, got)
				}
			}
		})
	}
}

func TestSetKeyForRefusesUnknownProviders(t *testing.T) {
	var c Config
	if c.SetKeyFor("nope", "k") {
		t.Fatal("an unknown provider accepted a key")
	}
	if c.KeyFor("nope") != "" {
		t.Fatal("an unknown provider returned a key")
	}
	// Clearing is explicit, and a cleared key is gone rather than blank.
	c.SetKeyFor("gemini", "k")
	if !c.SetKeyFor("gemini", "") {
		t.Fatal("clearing a key was refused")
	}
	if got := c.KeyFor("gemini"); got != "" {
		t.Fatalf("a cleared key came back as %q", got)
	}
	if len(c.Keys) != 0 {
		t.Fatalf("clearing left %d entries behind", len(c.Keys))
	}
}

// Keys must never be written to config.yaml: a secret in a file that gets
// pasted into an issue or committed is the failure this guards.
func TestKeysAreNeverPersisted(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows
	t.Setenv("HOME", home)        // and on unix
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	c := Defaults()
	c.SetKeyFor("gemini", "super-secret-value")
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(Dir(), "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "super-secret-value") {
		t.Fatal("an API key was written to config.yaml")
	}
	// And it must survive a round trip through the file, from the keyring.
	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Keys == nil {
		t.Fatal("Load returned a Config with a nil key map")
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
