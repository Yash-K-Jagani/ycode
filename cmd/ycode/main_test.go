package main

import (
	"testing"

	"github.com/Yash-K-Jagani/ycode/internal/config"
	"github.com/Yash-K-Jagani/ycode/internal/providers"
)

// Regression: onboarding used to write the key the user typed into every
// provider's field at once
// (`cfg.GeminiAPIKey, cfg.OpenRouterKey, cfg.GroqKey = key, key, key`).
// A Gemini key entered once was then sent to OpenRouter and Groq whenever a
// turn fell back to them. A credential must only ever reach the host the user
// chose for it.
func TestOnboardingStoresKeyOnlyForTheChosenProvider(t *testing.T) {
	c := config.Defaults()
	c.ActiveProvider = "gemini"

	c.SetKeyFor(c.ActiveProvider, "gemini-secret")

	if got := c.KeyFor("gemini"); got != "gemini-secret" {
		t.Fatalf("the key was not stored for the active provider: %q", got)
	}
	for _, id := range providers.IDs() {
		if id == "gemini" {
			continue
		}
		if got := c.KeyFor(id); got != "" {
			t.Fatalf("the %s key leaked to %s", "gemini", id)
		}
	}
}

func TestProviderConfigKeysRoundTrip(t *testing.T) {
	for _, id := range providers.IDs() {
		s := providers.Get(id)
		if !s.NeedsKey {
			continue
		}
		key := providerConfigKey(id)
		back, ok := providerFromConfigKey(key)
		if !ok || back != id {
			t.Fatalf("%s did not round-trip (got %q, ok=%v)", key, back, ok)
		}
		// The name onboarding prints must be the name `config set` accepts.
		if got := configKeyForEnv(config.Defaults().KeyEnvFor(id)); got != key {
			t.Fatalf("%s: configKeyForEnv gave %q, want %q", id, got, key)
		}
		if got := configKeyEnvFor(key); got != config.Defaults().KeyEnvFor(id) {
			t.Fatalf("%s: configKeyEnvFor gave %q", id, got)
		}
		// Every API key is a secret and must be routed to the keyring.
		if !isSecretConfigKey(key) {
			t.Fatalf("%s is not treated as a secret", key)
		}
	}
	if _, ok := providerFromConfigKey("theme"); ok {
		t.Fatal("a non-provider key parsed as a provider key")
	}
	if isSecretConfigKey("theme") {
		t.Fatal("theme must not be a secret")
	}
}

func TestApplyConfigValueRoutesProviderKeys(t *testing.T) {
	var c config.Config
	if !applyConfigValue(&c, "groq_api_key", "groq-secret") {
		t.Fatal("groq_api_key was not recognised")
	}
	if got := c.KeyFor("groq"); got != "groq-secret" {
		t.Fatalf("groq key = %q", got)
	}
	if got := c.KeyFor("gemini"); got != "" {
		t.Fatalf("setting the groq key also set gemini: %q", got)
	}
	if applyConfigValue(&c, "not_a_key", "x") {
		t.Fatal("an unknown key should be rejected")
	}
}

// `ycode config list` must show every key-bearing provider, so a user can see
// which credentials are set without reading the file.
func TestConfigListCoversEveryProviderKey(t *testing.T) {
	c := config.Defaults()
	c.SetKeyFor("gemini", "k")
	have := map[string]bool{}
	for _, kv := range configPairs(c) {
		have[kv[0]] = true
	}
	for _, s := range providers.All() {
		if !s.NeedsKey {
			continue
		}
		if !have[providerConfigKey(s.ID)] {
			t.Fatalf("config list omits %s", s.ID)
		}
	}
}
