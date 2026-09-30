package headless

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/Yash-K-Jagani/ycode/internal/config"
	"github.com/Yash-K-Jagani/ycode/internal/modes"
	"github.com/Yash-K-Jagani/ycode/internal/providers"
	"github.com/Yash-K-Jagani/ycode/internal/router"
	"github.com/Yash-K-Jagani/ycode/internal/tools"
	"github.com/Yash-K-Jagani/ycode/pkg/apitypes"
)

// stubProvider satisfies the provider interface for wiring tests.
type stubProvider struct{}

func (stubProvider) Name() string { return "stub" }
func (stubProvider) ListModels(context.Context) ([]apitypes.ModelInfo, error) {
	return nil, nil
}
func (stubProvider) Complete(context.Context, string, []apitypes.Message) (string, error) {
	return "", nil
}
func (stubProvider) Stream(context.Context, string, []apitypes.Message, io.Writer) (apitypes.StreamChunk, error) {
	return apitypes.StreamChunk{Delta: "x", Done: true}, nil
}

// stubRouter builds a router whose providers are stubs, so the wiring can be
// checked without a daemon or an API key.
func stubRouter(t *testing.T, cfg config.Config) *router.Router {
	t.Helper()
	r := router.New(cfg)
	r.SetFactoryForTest(func(string, string, string) (providers.Provider, error) {
		return stubProvider{}, nil
	})
	return r
}

// The gap this closes: the TUI could delegate but an unattended run could not, so
// the feature existed only for the entry point a person was sitting in front of.
func TestHeadlessCanDelegate(t *testing.T) {
	cfg := config.Defaults()
	reg := tools.DefaultRegistry(t.TempDir())
	addTaskTool(reg, stubRouter(t, cfg), cfg, Options{Workdir: t.TempDir(), Mode: modes.Build})

	tool, ok := reg.Get("task")
	if !ok {
		t.Fatal("no task tool in a headless registry")
	}
	// Unwired would say "not available"; wired reaches the delegate, which then
	// reports the model answering. Either way it must not be the unwired message.
	_, err := tool.Run(context.Background(), []byte(`{"prompt":"where is foo called"}`))
	if err != nil && strings.Contains(err.Error(), "not available") {
		t.Fatalf("the delegate was not wired: %v", err)
	}
}

// The goal loop builds its own registry, so it needs wiring too. An unattended
// goal run is the longest thing ycode does and must not be the one place that
// cannot delegate.
func TestTheGoalLoopRegistryAlsoGetsIt(t *testing.T) {
	cfg := config.Defaults()
	for _, reg := range []*tools.Registry{
		tools.DefaultRegistry(t.TempDir()),
		tools.DefaultRegistry(t.TempDir()),
	} {
		addTaskTool(reg, stubRouter(t, cfg), cfg, Options{Workdir: t.TempDir(), Mode: modes.Goal})
		if _, ok := reg.Get("task"); !ok {
			t.Fatal("a registry was left without the delegate")
		}
	}
}

// A local-only user would otherwise get a confusing refusal at call time, since
// the only delegable tool that reaches the network is browser.
func TestZeroDataLeakOmitsTheDelegate(t *testing.T) {
	cfg := config.Defaults()
	cfg.ZeroDataLeak = true
	reg := tools.DefaultRegistry(t.TempDir())
	addTaskTool(reg, stubRouter(t, cfg), cfg, Options{Workdir: t.TempDir(), Mode: modes.Build})
	if _, ok := reg.Get("task"); ok {
		t.Fatal("the delegate was offered under zero-data-leak")
	}
}

// Nil arguments must not panic: this runs before anything else in a turn.
func TestNilInputsAreSafe(t *testing.T) {
	cfg := config.Defaults()
	addTaskTool(nil, nil, cfg, Options{})
	addTaskTool(tools.DefaultRegistry(t.TempDir()), nil, cfg, Options{})
	addTaskTool(tools.DefaultRegistry(t.TempDir()), stubRouter(t, cfg), cfg, Options{})
}
