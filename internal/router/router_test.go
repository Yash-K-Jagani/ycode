package router

import (
	"testing"

	"github.com/Yash-K-Jagani/ycode/internal/config"
)

func TestZeroDataLeak(t *testing.T) {
	cfg := config.Defaults()
	cfg.ZeroDataLeak = true
	cfg.GeminiAPIKey = "x"
	cfg.ActiveProvider = "ollama"
	r := New(cfg)
	if _, err := r.Provider("gemini"); err == nil {
		t.Fatal("expected cloud block in ZDL")
	}
	if _, err := r.Provider("ollama"); err != nil {
		t.Fatalf("ollama must work in ZDL: %v", err)
	}
	if fbs := r.Fallbacks(); len(fbs) != 0 {
		t.Fatalf("no fallbacks in ZDL: %v", fbs)
	}
	cfg2 := config.Defaults()
	cfg2.GeminiAPIKey = "x"
	if fbs := New(cfg2).Fallbacks(); len(fbs) == 0 {
		t.Fatal("expected fallbacks when not ZDL")
	}
}
