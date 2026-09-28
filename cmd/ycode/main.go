package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/Yash-K-Jagani/ycode/internal/config"
	"github.com/Yash-K-Jagani/ycode/internal/router"
	"github.com/Yash-K-Jagani/ycode/internal/sessions"
	"github.com/Yash-K-Jagani/ycode/internal/tui"
	"github.com/Yash-K-Jagani/ycode/internal/webhooks"
)

func main() {
	root := &cobra.Command{Use: "ycode", Short: "AI coding harness (M6: hardened + ecosystem)"}
	root.Version = Version
	root.SetVersionTemplate("ycode {{.Version}}\n")
	root.AddCommand(statusCmd(), runCmd(), serveCmd(), batchCmd(), ciCmd(), daemonCmd(), auditCmd(), versionCmd(), storeCmd(), doctorCmd(), setupCmd(), configCmd(), upgradeCmd())
	if len(os.Args) > 1 {
		_ = root.Execute()
		return
	}
	launchTUI()
}

func launchTUI() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Println("config error:", err)
		os.Exit(1)
	}
	// First run: guide the user through provider/key/model selection rather
	// than dropping them into a TUI whose every message will fail.
	if !onboard(&cfg) {
		return
	}
	workdir, _ := os.Getwd()
	r := router.New(cfg)
	_ = sessions.InitDefault()
	sess := sessions.New(cfg.ActiveProvider, cfg.ActiveModel)
	_ = sess.Save()
	webhooks.Fire("session_start", map[string]any{"workdir": workdir})
	m := tui.New(cfg, r, sess, workdir)
	prog := tea.NewProgram(&m, tea.WithAltScreen())
	m.SetProgram(prog)
	// One cleanup point for every exit path — Ctrl+D, the /exit panic, a
	// recovered handler panic, a signal. Without it, MCP child processes
	// survive the parent on Windows and pile up across sessions.
	defer m.Shutdown()
	if _, err := prog.Run(); err != nil {
		fmt.Println("tui error:", err)
		os.Exit(1)
	}
}

var (
	// Version is the release version. Overridden at build time via ldflags
	// (see .goreleaser.yaml and scripts/build.sh). The default is the
	// "unreleased source tree" sentinel so it is obvious when a binary was
	// not built by the release pipeline.
	Version = "0.0.0-dev"
	// Commit is the git commit sha, injected at build time.
	Commit = "unknown"
	// Date is the build date, injected at build time.
	Date = "unknown"
)
