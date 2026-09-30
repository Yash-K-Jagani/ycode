package main

import (
	"errors"
	"testing"

	"github.com/Yash-K-Jagani/ycode/internal/config"
	"github.com/Yash-K-Jagani/ycode/internal/providers"
)

// Regression: onboarding used to write the key the user typed into every
// provider's storage at once
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
	if err := applyConfigValue(&c, "groq_api_key", "groq-secret"); err != nil {
		t.Fatalf("groq_api_key was not recognised: %v", err)
	}
	if got := c.KeyFor("groq"); got != "groq-secret" {
		t.Fatalf("groq key = %q", got)
	}
	if got := c.KeyFor("gemini"); got != "" {
		t.Fatalf("setting the groq key also set gemini: %q", got)
	}
	if err := applyConfigValue(&c, "not_a_key", "x"); !errors.Is(err, errUnknownConfigKey) {
		t.Fatalf("an unknown key should be rejected as unknown, got %v", err)
	}
}

// A known key with an unusable value must be reported as a bad value, not as an
// unknown key. The difference matters: one message tells you what to type, the
// other sends you to the list of settings.
func TestApplyConfigValueRejectsBadValues(t *testing.T) {
	for _, tc := range []struct{ key, val string }{
		{"zero_data_leak", "maybe"},
		{"daily_budget_usd", "abc"},
		{"daily_budget_usd", "-1"},
	} {
		var c config.Config
		err := applyConfigValue(&c, tc.key, tc.val)
		if err == nil {
			t.Errorf("%s=%q was accepted", tc.key, tc.val)
			continue
		}
		if errors.Is(err, errUnknownConfigKey) {
			t.Errorf("%s=%q reported as an unknown key rather than a bad value: %v", tc.key, tc.val, err)
		}
		if msg := err.Error(); msg == "" || msg == "unknown config key" {
			t.Errorf("%s=%q got no useful message: %q", tc.key, tc.val, msg)
		}
	}
}

// The rejection has to leave the config untouched. It used to return a bare
// "recognised" bool, the caller saved anyway, and the command printed the bad
// value back as though it had worked.
func TestRejectedValueIsNotStored(t *testing.T) {
	c := config.Defaults()
	if err := applyConfigValue(&c, "daily_budget_usd", "abc"); err == nil {
		t.Fatal("a bad budget was accepted")
	}
	// Only the field the command would have written, since Config holds a map
	// and cannot be compared with ==.
	if c.DailyBudgetUSD != 0 {
		t.Fatalf("a rejected budget was stored as %v", c.DailyBudgetUSD)
	}
	if c.ZeroDataLeak != config.Defaults().ZeroDataLeak {
		t.Fatal("a rejected value disturbed an unrelated field")
	}
	c = config.Defaults()
	c.ZeroDataLeak = true
	if err := applyConfigValue(&c, "zero_data_leak", "maybe"); err == nil {
		t.Fatal("a bad bool was accepted")
	}
	if !c.ZeroDataLeak {
		t.Fatal("a rejected bool overwrote the existing value")
	}
}

func TestValidValuesAreAccepted(t *testing.T) {
	var c config.Config
	if err := applyConfigValue(&c, "daily_budget_usd", "1.5"); err != nil {
		t.Fatal(err)
	}
	if c.DailyBudgetUSD != 1.5 {
		t.Fatalf("budget = %v", c.DailyBudgetUSD)
	}
	// Zero is the documented "no limit", so it must be settable.
	if err := applyConfigValue(&c, "daily_budget_usd", "0"); err != nil {
		t.Fatalf("0 should mean no limit, not be an error: %v", err)
	}
	if err := applyConfigValue(&c, "zero_data_leak", "true"); err != nil {
		t.Fatal(err)
	}
	if !c.ZeroDataLeak {
		t.Fatal("zero_data_leak was not set")
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
