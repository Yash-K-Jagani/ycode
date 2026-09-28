package headless

import (
	"testing"

	"github.com/Yash-K-Jagani/ycode/internal/config"
	"github.com/Yash-K-Jagani/ycode/internal/modes"
)

// Goal mode is dispatched to the goal loop before any single-turn work, so an
// unusable provider config fails the same way regardless of mode.
func TestGoalModeDispatch(t *testing.T) {
	cfg := config.Defaults()
	cfg.ActiveProvider = "ollama"
	cfg.OllamaHost = "http://127.0.0.1:1" // nothing listens here
	_, err := Run(t.Context(), cfg, "add a healthcheck", Options{Mode: modes.Goal, Workdir: t.TempDir()})
	if err == nil {
		t.Fatal("expected a provider error with no reachable model")
	}
	// The default headless budget must be shorter than the interactive one:
	// nobody watches a CI run burn a dozen iterations.
	o := Options{Mode: modes.Goal}
	o.withDefaults()
	if o.GoalIters != 0 {
		t.Fatalf("withDefaults should not invent a goal budget: %d", o.GoalIters)
	}
	if goalMaxIters >= modes.DefaultGoalIters {
		t.Fatalf("headless budget %d should be under the interactive %d", goalMaxIters, modes.DefaultGoalIters)
	}
}
