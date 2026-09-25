package config

import "testing"

func TestSetupNeeded(t *testing.T) {
	base := Defaults()

	tests := []struct {
		name           string
		mutate         func(*Config)
		wantReady      bool
		wantNeedsKey   bool
		wantNeedsModel bool
	}{
		{
			name:      "no model selected yet",
			mutate:    func(c *Config) { c.ActiveModel = "" },
			wantReady: false, wantNeedsModel: true,
		},
		{
			name:      "ollama with a model needs nothing else",
			mutate:    func(c *Config) { c.ActiveModel = "qwen2.5-coder:7b" },
			wantReady: true,
		},
		{
			name: "gemini without a key is blocked",
			mutate: func(c *Config) {
				c.ActiveProvider, c.ActiveModel, c.GeminiAPIKey = "gemini", "gemini-2.0-flash", ""
			},
			wantReady: false, wantNeedsKey: true,
		},
		{
			name: "gemini with a key is ready",
			mutate: func(c *Config) {
				c.ActiveProvider, c.ActiveModel, c.GeminiAPIKey = "gemini", "gemini-2.0-flash", "k"
			},
			wantReady: true,
		},
		{
			name: "openrouter without a key is blocked",
			mutate: func(c *Config) {
				c.ActiveProvider, c.ActiveModel, c.OpenRouterKey = "openrouter", "some/model", ""
			},
			wantReady: false, wantNeedsKey: true,
		},
		{
			name: "groq with a key is ready",
			mutate: func(c *Config) {
				c.ActiveProvider, c.ActiveModel, c.GroqKey = "groq", "llama-3.3-70b", "k"
			},
			wantReady: true,
		},
		{
			name: "model check wins over key check",
			mutate: func(c *Config) {
				c.ActiveProvider, c.ActiveModel, c.GeminiAPIKey = "gemini", "", ""
			},
			wantReady: false, wantNeedsModel: true,
		},
		{
			name:      "unknown provider is reported",
			mutate:    func(c *Config) { c.ActiveProvider, c.ActiveModel = "nope", "m" },
			wantReady: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := base
			tt.mutate(&cfg)
			got := SetupNeeded(cfg)
			if got.Ready != tt.wantReady {
				t.Errorf("Ready = %v, want %v (reason: %s)", got.Ready, tt.wantReady, got.Reason)
			}
			if got.NeedsKey != tt.wantNeedsKey {
				t.Errorf("NeedsKey = %v, want %v", got.NeedsKey, tt.wantNeedsKey)
			}
			if got.NeedsModel != tt.wantNeedsModel {
				t.Errorf("NeedsModel = %v, want %v", got.NeedsModel, tt.wantNeedsModel)
			}
			if !got.Ready && got.Reason == "" {
				t.Error("Reason must be set when not ready")
			}
			if got.NeedsKey && got.KeyEnv == "" {
				t.Error("KeyEnv must be set when a key is needed")
			}
		})
	}
}

// A default config has no model, which is exactly the state first-run
// onboarding exists to resolve.
func TestDefaultsNeedSetup(t *testing.T) {
	if SetupNeeded(Defaults()).Ready {
		t.Error("a default config must not report ready; onboarding would never run")
	}
}
