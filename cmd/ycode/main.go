package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/Yash-K-Jagani/ycode/internal/audit"
	"github.com/Yash-K-Jagani/ycode/internal/automation"
	"github.com/Yash-K-Jagani/ycode/internal/batch"
	"github.com/Yash-K-Jagani/ycode/internal/cache"
	"github.com/Yash-K-Jagani/ycode/internal/config"
	"github.com/Yash-K-Jagani/ycode/internal/cost"
	"github.com/Yash-K-Jagani/ycode/internal/headless"
	"github.com/Yash-K-Jagani/ycode/internal/keys"
	"github.com/Yash-K-Jagani/ycode/internal/modes"
	"github.com/Yash-K-Jagani/ycode/internal/plugins"
	"github.com/Yash-K-Jagani/ycode/internal/providers/ollama"
	"github.com/Yash-K-Jagani/ycode/internal/router"
	"github.com/Yash-K-Jagani/ycode/internal/serve"
	"github.com/Yash-K-Jagani/ycode/internal/sessions"
	"github.com/Yash-K-Jagani/ycode/internal/skills"
	"github.com/Yash-K-Jagani/ycode/internal/store"
	"github.com/Yash-K-Jagani/ycode/internal/tools"
	"github.com/Yash-K-Jagani/ycode/internal/tui"
	"github.com/Yash-K-Jagani/ycode/internal/webhooks"
)

func main() {
	root := &cobra.Command{Use: "ycode", Short: "AI coding harness (M6: hardened + ecosystem)"}
	root.Version = Version
	root.SetVersionTemplate("ycode {{.Version}}\n")
	root.AddCommand(statusCmd(), runCmd(), serveCmd(), batchCmd(), ciCmd(), daemonCmd(), auditCmd(), versionCmd(), storeCmd(), doctorCmd(), setupCmd())
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
	if _, err := prog.Run(); err != nil {
		fmt.Println("tui error:", err)
		os.Exit(1)
	}
}

// isFirstRun reports whether ycode has never written a config file, which is
// the cue to run the full onboarding wizard instead of a quick repair.
func isFirstRun() bool {
	_, err := os.Stat(filepath.Join(config.Dir(), "config.yaml"))
	return os.IsNotExist(err)
}

// onboard ensures cfg has a usable provider/model. It returns false if the
// user (or a non-interactive environment) declined to continue, in which case
// the caller should exit without launching the TUI.
func onboard(cfg *config.Config) bool {
	state := config.SetupNeeded(*cfg)
	if state.Ready {
		return true
	}

	if !term.IsTerminal(int(os.Stdin.Fd())) {
		// Non-interactive (CI, pipes, editor task runners). Print actionable
		// instructions and exit rather than blocking on a prompt forever.
		fmt.Println("ycode is not configured yet. Non-interactive session detected.")
		fmt.Println()
		fmt.Println("Fix it with one of:")
		if state.NeedsModel {
			fmt.Printf("  ollama pull qwen2.5-coder:7b        # then: ycode config set model <name>\n")
		}
		if state.NeedsKey {
			fmt.Printf("  set %s=<your key>                 # or: ycode config set %s <key>\n", state.KeyEnv, state.KeyEnv)
		}
		fmt.Println("  ycode setup                        # interactive wizard")
		fmt.Println()
		fmt.Println("See https://github.com/Yash-K-Jagani/ycode#setup")
		return false
	}

	if isFirstRun() {
		return runSetupWizard(cfg)
	}
	return runSetupRepair(cfg, state)
}

// runSetupWizard is the first-run experience: pick a provider, supply a key if
// the provider is cloud-hosted, then choose a model.
func runSetupWizard(cfg *config.Config) bool {
	fmt.Println()
	fmt.Println("  ycode " + Version + " — welcome!")
	fmt.Println()
	fmt.Println("  Let's get you set up. This takes about a minute.")
	fmt.Println()

	providers := []struct {
		id, label, keyEnv string
		needsKey          bool
	}{
		{"ollama", "Ollama (local, free, private — recommended)", "", false},
		{"gemini", "Google Gemini (fast, generous free tier)", cfg.GeminiKeyEnv, true},
		{"openrouter", "OpenRouter (many models, pay per token)", cfg.OpenRouterKeyEnv, true},
		{"groq", "Groq (very fast, free tier)", cfg.GroqKeyEnv, true},
	}
	for i, p := range providers {
		fmt.Printf("    %d) %s\n", i+1, p.label)
	}
	fmt.Println()
	idx, ok := promptInt("provider", 1, 1, len(providers))
	if !ok {
		return false
	}
	p := providers[idx-1]
	cfg.ActiveProvider = p.id

	if p.needsKey {
		fmt.Printf("\n  %s API key (input hidden; leave empty to set it later with YCODE=%s)\n", p.id, p.keyEnv)
		key, ok := promptSecret(p.id + " key")
		if !ok {
			return false
		}
		if key == "" {
			fmt.Printf("\n  No key entered. Set %s in your environment, or run 'ycode setup' again.\n", p.keyEnv)
			return false
		}
		if err := os.Setenv(p.keyEnv, key); err != nil {
			fmt.Println("could not set env var:", err)
			return false
		}
		// Persist to the OS keyring so later runs find it without the env var.
		if err := keys.Set(p.keyEnv, key); err != nil {
			fmt.Println("note: could not store key in OS keyring; set", p.keyEnv, "in your environment instead")
		}
		switch p.id {
		case "gemini":
			cfg.GeminiAPIKey = key
		case "openrouter":
			cfg.OpenRouterKey = key
		case "groq":
			cfg.GroqKey = key
		}
	}

	fmt.Println()
	model, ok := chooseModel(p.id, cfg)
	if !ok {
		return false
	}
	cfg.ActiveModel = model

	if err := cfg.Save(); err != nil {
		fmt.Println("could not save config:", err)
		return false
	}
	fmt.Printf("\n  Saved. You're set up — launching ycode.\n\n")
	return true
}

// runSetupRepair fixes a config that exists but is incomplete.
func runSetupRepair(cfg *config.Config, state config.SetupState) bool {
	fmt.Println()
	fmt.Println("  ycode needs a little more setup before it can chat.")
	fmt.Printf("  Problem: %s\n", state.Reason)
	fmt.Println()
	if state.NeedsModel {
		model, ok := chooseModel(cfg.ActiveProvider, cfg)
		if !ok {
			return false
		}
		cfg.ActiveModel = model
	}
	if state.NeedsKey && state.KeyEnv != "" {
		fmt.Printf("\n  %s is required for the %s provider.\n", state.KeyEnv, cfg.ActiveProvider)
		fmt.Println("  You can set it later in your environment, or enter it now.")
		key, ok := promptSecret(state.KeyEnv)
		if !ok {
			return false
		}
		if key == "" {
			fmt.Printf("\n  Set %s and re-run ycode.\n", state.KeyEnv)
			return false
		}
		if err := os.Setenv(state.KeyEnv, key); err != nil {
			fmt.Println("could not set env var:", err)
			return false
		}
		_ = keys.Set(state.KeyEnv, key)
		cfg.GeminiAPIKey, cfg.OpenRouterKey, cfg.GroqKey = key, key, key
	}
	// Still unready after the targeted repairs (e.g. an unrecognised
	// provider): fall back to the full wizard so the user can pick again.
	if s := config.SetupNeeded(*cfg); !s.Ready {
		fmt.Printf("\n  Still not ready: %s\n", s.Reason)
		fmt.Println("  Starting the full setup wizard instead.")
		return runSetupWizard(cfg)
	}
	if err := cfg.Save(); err != nil {
		fmt.Println("could not save config:", err)
		return false
	}
	fmt.Printf("\n  Saved. Launching ycode.\n\n")
	return true
}

// chooseModel lists candidate models for the provider and prompts for one. For
// Ollama it offers to pull a recommended model when the daemon is reachable but
// empty.
func chooseModel(provider string, cfg *config.Config) (string, bool) {
	const timeout = 5 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var models []string
	if provider == "ollama" {
		list, err := ollama.New(cfg.OllamaHost).ListModels(ctx)
		if err != nil {
			fmt.Printf("  Could not reach Ollama at %s.\n", cfg.OllamaHost)
			fmt.Println("  Start it with: ollama serve")
			fmt.Println("  Then pull a model: ollama pull qwen2.5-coder:7b")
			return "", false
		}
		for _, m := range list {
			models = append(models, m.ID)
		}
	}
	if len(models) == 0 {
		if provider != "ollama" {
			fmt.Println("  Enter the model id to use (or leave empty to let the provider decide).")
			m, ok := promptLine("model")
			if !ok || m == "" {
				return "", false
			}
			return m, true
		}
		// Ollama reachable but no models installed.
		recommended := "qwen2.5-coder:7b"
		fmt.Println("  Ollama is running but has no models installed.")
		fmt.Printf("  Recommended for coding: %s (needs ~5 GB)\n", recommended)
		other, ok := promptLine("model (blank for the recommendation)")
		if !ok {
			return "", false
		}
		if other == "" {
			other = recommended
		}
		fmt.Printf("  Pulling %s — this can take a while on first run...\n", other)
		if err := pullOllamaModel(cfg.OllamaHost, other); err != nil {
			fmt.Println("  Could not pull automatically:", err)
			fmt.Printf("  Run this yourself: ollama pull %s\n", other)
		} else {
			fmt.Println("  Pulled.")
		}
		return other, true
	}

	fmt.Println("  Available models:")
	for i, m := range models {
		fmt.Printf("    %d) %s\n", i+1, m)
	}
	fmt.Println()
	idx, ok := promptInt("model", 1, 1, len(models))
	if !ok {
		return "", false
	}
	return models[idx-1], true
}

// pullOllamaModel shells out to the ollama CLI to fetch a model.
func pullOllamaModel(host, model string) error {
	cmd := exec.Command("ollama", "pull", model)
	cmd.Env = append(os.Environ(), "OLLAMA_HOST="+host)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// promptInt asks for a number within [min,max], re-prompting on bad input.
func promptInt(label string, def, min, max int) (int, bool) {
	for {
		s, ok := promptLine(fmt.Sprintf("%s [%d-%d, default %d]", label, min, max, def))
		if !ok {
			return 0, false
		}
		if s == "" {
			return def, true
		}
		n, err := strconv.Atoi(s)
		if err != nil || n < min || n > max {
			fmt.Printf("  Please enter a number between %d and %d.\n", min, max)
			continue
		}
		return n, true
	}
}

// promptLine reads one line. Returns ok=false on EOF/interrupt.
func promptLine(label string) (string, bool) {
	fmt.Printf("  %s: ", label)
	r := bufio.NewReader(os.Stdin)
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		fmt.Println()
		return "", false
	}
	return strings.TrimSpace(line), true
}

// promptSecret reads a line without echoing it, so API keys do not land in
// scrollback or terminal logs.
func promptSecret(label string) (string, bool) {
	fmt.Printf("  %s: ", label)
	fd := int(os.Stdin.Fd())
	// Switch the terminal to raw mode so the key is never echoed to the
	// screen or captured in scrollback, then restore it afterwards.
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		// Fall back to a normal read if the terminal cannot be switched.
		return promptLine(label)
	}
	defer func() { _ = term.Restore(fd, oldState) }()
	r := bufio.NewReader(os.Stdin)
	line, err := r.ReadString('\n')
	fmt.Println()
	if err != nil && line == "" {
		return "", false
	}
	return strings.TrimSpace(line), true
}

// setupCmd re-runs the onboarding wizard at any time.
func setupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Interactive provider/key/model setup",
		Run: func(cmd *cobra.Command, args []string) {
			cfg, err := config.Load()
			if err != nil {
				fmt.Println("config error:", err)
				return
			}
			if !term.IsTerminal(int(os.Stdin.Fd())) {
				fmt.Println("setup needs an interactive terminal; set YCODE_* env vars and run 'ycode status' instead.")
				return
			}
			if !runSetupWizard(&cfg) {
				fmt.Println("setup cancelled.")
				return
			}
			fmt.Println("Run 'ycode' to start.")
		},
	}
}

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show provider health and cost",
		Run:   func(cmd *cobra.Command, args []string) { runStatus() },
	}
}

func runStatus() {
	cfg, _ := config.Load()
	r := router.New(cfg)
	_, model, err := r.Active()
	if err != nil {
		fmt.Println("error:", err)
		os.Exit(1)
	}
	fmt.Printf("provider=%s model=%s\n", cfg.ActiveProvider, model)
	if ms, err := ollama.New(cfg.OllamaHost).ListModels(context.Background()); err == nil {
		fmt.Printf("ollama models (%d):", len(ms))
		for _, mi := range ms {
			fmt.Printf(" %s", mi.ID)
		}
		fmt.Println()
	} else {
		fmt.Println("ollama:", err)
	}
	p, c, usd := cost.New().Today()
	fmt.Printf("today: %d prompt + %d completion tokens · $%.4f\n", p, c, usd)
}

func runCmd() *cobra.Command {
	var mode, agent string
	c := &cobra.Command{
		Use:   "run <prompt>",
		Short: "Headless single turn (tools in build/plan)",
		Args:  cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			cfg, _ := config.Load()
			m := modes.Build
			if mode != "" {
				var err error
				m, err = modes.Parse(mode)
				if err != nil {
					fmt.Println(err)
					os.Exit(1)
				}
			}
			workdir, _ := os.Getwd()
			answer, err := headless.Run(context.Background(), cfg, strings.Join(args, " "), headless.Options{Mode: m, Agent: agent, Workdir: workdir, Stderr: os.Stderr})
			if err != nil {
				fmt.Println("error:", err)
				os.Exit(1)
			}
			fmt.Println(answer)
		},
	}
	c.Flags().StringVar(&mode, "mode", "build", "chat|plan|build|thinking")
	c.Flags().StringVar(&agent, "agent", "builder", "agent name")
	return c
}

func serveCmd() *cobra.Command {
	var addr string
	c := &cobra.Command{
		Use:   "serve",
		Short: "Local HTTP API (see api/openapi.yaml)",
		Run: func(cmd *cobra.Command, args []string) {
			if err := serve.Run(addr); err != nil {
				fmt.Println(err)
				os.Exit(1)
			}
		},
	}
	c.Flags().StringVar(&addr, "addr", "127.0.0.1:8471", "listen address")
	return c
}

func batchCmd() *cobra.Command {
	addCmd := &cobra.Command{
		Use: "add <prompt>", Short: "Queue a job",
		Args: cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			mode, _ := cmd.Flags().GetString("mode")
			workdir, _ := os.Getwd()
			j := batch.Load().Add(strings.Join(args, " "), mode, "", workdir)
			fmt.Println("queued", j.ID)
		},
	}
	addCmd.Flags().String("mode", "build", "mode for the job")
	clearCmd := &cobra.Command{
		Use: "clear", Short: "Clear jobs (finished only by default)",
		Run: func(cmd *cobra.Command, args []string) {
			all, _ := cmd.Flags().GetBool("all")
			n := batch.Load().Clear(!all)
			fmt.Printf("cleared %d\n", n)
		},
	}
	clearCmd.Flags().Bool("all", false, "clear all including queued")
	c := &cobra.Command{Use: "batch", Short: "Offline batch queue"}
	c.AddCommand(
		addCmd,
		&cobra.Command{
			Use: "list", Short: "List jobs",
			Run: func(cmd *cobra.Command, args []string) {
				for _, j := range batch.Load().List() {
					fmt.Printf("%s [%s] %s\n", j.ID, j.Status, truncate(j.Prompt, 80))
					if j.Status != "queued" && j.Result != "" {
						fmt.Printf("  -> %s\n", truncate(j.Result, 200))
					}
				}
			},
		},
		&cobra.Command{
			Use: "run", Short: "Run queued jobs when a provider is reachable",
			Run: func(cmd *cobra.Command, args []string) {
				q := batch.Load()
				q.RunPending(context.Background())
				for _, j := range q.List() {
					fmt.Printf("%s [%s]\n", j.ID, j.Status)
				}
			},
		},
		clearCmd,
	)
	return c
}

func ciCmd() *cobra.Command {
	var post bool
	c := &cobra.Command{
		Use:   "ci",
		Short: "CI: review current diff + run tests, print markdown summary",
		Run: func(cmd *cobra.Command, args []string) {
			summary, failed := ciSummary()
			fmt.Println(summary)
			if post {
				if err := postPRComment(summary); err != nil {
					fmt.Println("comment:", err)
				}
			}
			if failed {
				os.Exit(1)
			}
		},
	}
	c.Flags().BoolVar(&post, "post", false, "post summary as PR comment (needs GH_TOKEN + PR context)")
	return c
}

func ciSummary() (string, bool) {
	var b strings.Builder
	ctx := context.Background()
	workdir, _ := os.Getwd()
	cfg, _ := config.Load()
	raw, _ := json.Marshal(map[string]string{"action": "diff"})
	diff, derr := (&tools.GitTool{Workdir: workdir}).Run(ctx, raw)
	raw2, _ := json.Marshal(map[string]string{"path": workdir})
	testOut, terr := (&tools.TestGenTool{Workdir: workdir}).Run(ctx, raw2)
	b.WriteString("<!-- ycode-ci -->\n## ycode CI summary\n\n")
	if derr != nil || strings.TrimSpace(diff) == "" || diff == "(clean)" {
		b.WriteString("Diff: (clean or unavailable)\n")
	} else {
		if len(diff) > 20000 {
			diff = diff[:20000] + "\n…(truncated)"
		}
		answer, err := headless.Run(ctx, cfg, "Review this diff briefly (summary + top risks):\n```diff\n"+diff+"\n```", headless.Options{Mode: modes.Chat, Workdir: workdir, Stderr: os.Stderr})
		if err != nil {
			b.WriteString("Review: unavailable (" + err.Error() + ")\n")
		} else {
			b.WriteString(answer + "\n")
		}
	}
	b.WriteString("\n")
	failed := false
	if terr != nil {
		failed = true
		b.WriteString("Tests: FAILED\n```\n" + truncate(testOut, 4000) + "\n```\n")
	} else {
		b.WriteString("Tests: PASSED\n```\n" + truncate(testOut, 4000) + "\n```\n")
	}
	return b.String(), failed
}

// postPRComment upserts the summary as a PR comment (create or update by marker).
func postPRComment(body string) error {
	token := os.Getenv("GH_TOKEN")
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
	if token == "" {
		return fmt.Errorf("GH_TOKEN not set")
	}
	owner, repo, err := ciRepo()
	if err != nil {
		return err
	}
	pr, err := ciPRNumber()
	if err != nil {
		return err
	}
	api := func(method, path string, payload any) (int, []byte, error) {
		var rdr io.Reader
		if payload != nil {
			b, _ := json.Marshal(payload)
			rdr = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, "https://api.github.com"+path, rdr)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("Authorization", "Bearer "+token)
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0, nil, err
		}
		defer func() { _ = resp.Body.Close() }()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
		return resp.StatusCode, data, nil
	}
	code, data, err := api("GET", fmt.Sprintf("/repos/%s/%s/issues/%s/comments?per_page=100", owner, repo, pr), nil)
	if err != nil {
		return err
	}
	if code >= 400 {
		return fmt.Errorf("list comments %d: %s", code, truncate(string(data), 300))
	}
	var comments []struct {
		ID   int64  `json:"id"`
		Body string `json:"body"`
	}
	_ = json.Unmarshal(data, &comments)
	for _, c := range comments {
		if strings.Contains(c.Body, "<!-- ycode-ci -->") {
			code, data, err := api("PATCH", fmt.Sprintf("/repos/%s/%s/issues/comments/%d", owner, repo, c.ID), map[string]string{"body": body})
			if err != nil || code >= 400 {
				return fmt.Errorf("update comment: %v %s", err, truncate(string(data), 200))
			}
			fmt.Println("updated PR comment")
			return nil
		}
	}
	code, data, err = api("POST", fmt.Sprintf("/repos/%s/%s/issues/%s/comments", owner, repo, pr), map[string]string{"body": body})
	if err != nil || code >= 400 {
		return fmt.Errorf("create comment: %v %s", err, truncate(string(data), 200))
	}
	fmt.Println("posted PR comment")
	return nil
}

func ciRepo() (string, string, error) {
	if slug := os.Getenv("GITHUB_REPOSITORY"); slug != "" {
		parts := strings.SplitN(slug, "/", 2)
		if len(parts) == 2 {
			return parts[0], parts[1], nil
		}
	}
	out, err := exec.Command("git", "remote", "get-url", "origin").Output()
	if err != nil {
		return "", "", fmt.Errorf("no GITHUB_REPOSITORY and no git origin")
	}
	u := strings.TrimSpace(string(out))
	u = strings.TrimSuffix(u, ".git")
	if i := strings.LastIndex(u, ":"); strings.Contains(u, "@") && i >= 0 {
		u = u[i+1:]
	} else if strings.HasPrefix(u, "https://") {
		u = strings.TrimPrefix(u, "https://github.com/")
	}
	parts := strings.SplitN(u, "/", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("cannot parse owner/repo from %q", u)
	}
	return parts[0], parts[1], nil
}

func ciPRNumber() (string, error) {
	if n := os.Getenv("GH_PR"); n != "" {
		return n, nil
	}
	if ref := os.Getenv("GITHUB_REF"); ref != "" {
		parts := strings.Split(ref, "/")
		if len(parts) == 4 && parts[1] == "pull" {
			return parts[2], nil
		}
	}
	return "", fmt.Errorf("set GH_PR or run from a pull_request event")
}

func daemonCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "daemon",
		Short: "Run scheduled automations (see automations.yaml)",
		Run: func(cmd *cobra.Command, args []string) {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			fmt.Println("ycode daemon running (Ctrl+C to stop)")
			automation.Daemon(ctx)
		},
	}
}

func storeCmd() *cobra.Command {
	c := &cobra.Command{Use: "store", Short: "Curated skill/plugin store"}
	c.AddCommand(
		&cobra.Command{
			Use: "update [url]", Short: "Refresh the cached index",
			Run: func(cmd *cobra.Command, args []string) {
				url := ""
				if len(args) > 0 {
					url = args[0]
				}
				idx, err := store.Update(url)
				if err != nil {
					fmt.Println("store update:", err)
					os.Exit(1)
				}
				fmt.Printf("store: %d entries\n", len(idx.Items))
			},
		},
		&cobra.Command{
			Use: "list", Short: "List entries",
			Run: func(cmd *cobra.Command, args []string) {
				idx, err := store.Load()
				if err != nil {
					fmt.Println("store:", err)
					os.Exit(1)
				}
				for _, e := range idx.Items {
					fmt.Printf("- %s [%s] %s\n  %s\n", e.Name, e.Kind, e.Source, e.Description)
				}
			},
		},
		&cobra.Command{
			Use: "search <q>", Short: "Search entries",
			Args: cobra.MinimumNArgs(1),
			Run: func(cmd *cobra.Command, args []string) {
				idx, err := store.Load()
				if err != nil {
					fmt.Println("store:", err)
					os.Exit(1)
				}
				for _, e := range idx.Search(strings.Join(args, " ")) {
					fmt.Printf("- %s [%s] %s\n  %s\n", e.Name, e.Kind, e.Source, e.Description)
				}
			},
		},
		&cobra.Command{
			Use: "install <name>", Short: "Install an entry",
			Args: cobra.ExactArgs(1),
			Run: func(cmd *cobra.Command, args []string) {
				idx, err := store.Load()
				if err != nil {
					fmt.Println("store:", err)
					os.Exit(1)
				}
				e, ok := idx.Get(args[0])
				if !ok {
					fmt.Println("no store entry", strconv.Quote(args[0]))
					os.Exit(1)
				}
				name, err := store.Install(e, skills.NewManager(), plugins.NewLoader())
				if err != nil {
					fmt.Println("install:", err)
					os.Exit(1)
				}
				fmt.Println("installed", e.Kind+":", name)
			},
		},
		&cobra.Command{
			Use: "remove <name>", Short: "Uninstall a skill or plugin",
			Args: cobra.ExactArgs(1),
			Run: func(cmd *cobra.Command, args []string) {
				what, err := store.Remove(args[0], skills.NewManager(), plugins.NewLoader())
				if err != nil {
					fmt.Println("remove:", err)
					os.Exit(1)
				}
				fmt.Println("removed", what)
			},
		},
		&cobra.Command{
			Use: "verify [name]", Short: "Verify installed packages against records",
			Run: func(cmd *cobra.Command, args []string) {
				sk, pl := skills.NewManager(), plugins.NewLoader()
				if len(args) > 0 {
					msg, err := store.Verify(args[0], sk, pl)
					if err != nil {
						fmt.Println(args[0]+":", err)
						os.Exit(1)
					}
					fmt.Println(args[0] + ": " + msg)
					return
				}
				for _, line := range store.VerifyAll(sk, pl) {
					fmt.Println(line)
				}
			},
		},
	)
	return c
}
func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func doctorCmd() *cobra.Command {
	var fix bool
	c := &cobra.Command{
		Use:   "doctor",
		Short: "Health check (Ollama, models, config)",
		Run: func(cmd *cobra.Command, args []string) {
			cfg, _ := config.Load()
			fmt.Printf("provider=%s model=%s zero_data_leak=%v\n", cfg.ActiveProvider, cfg.ActiveModel, cfg.ZeroDataLeak)
			if ms, err := ollama.New(cfg.OllamaHost).ListModels(context.Background()); err != nil {
				fmt.Println("ollama: UNREACHABLE (" + err.Error() + ")")
			} else {
				fmt.Printf("ollama: ok (%d models)\n", len(ms))
			}
			if _, err := os.Stat(config.Dir()); err != nil {
				fmt.Println("config dir: MISSING (" + config.Dir() + ")")
			} else {
				fmt.Println("config dir: " + config.Dir())
			}
			if !fix {
				return
			}
			cache.New(nil).Clear()
			fmt.Println("fix: semantic cache cleared")
			fmt.Printf("fix: batch cleared %d finished jobs\n", batch.Load().Clear(true))
			if err := cfg.Save(); err != nil {
				fmt.Println("fix: config save FAILED:", err)
			} else {
				fmt.Println("fix: config re-saved ok")
			}
		},
	}
	c.Flags().BoolVar(&fix, "fix", false, "auto-fix: clear cache + finished jobs, re-save config")
	return c
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

// buildInfo returns a single-line build fingerprint for version output.
func buildInfo() string {
	return fmt.Sprintf("ycode %s (commit %s, built %s, %s/%s)", Version, Commit, Date, runtime.GOOS, runtime.GOARCH)
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println(buildInfo())
		},
	}
}

func auditCmd() *cobra.Command {
	var date string
	c := &cobra.Command{
		Use:   "audit",
		Short: "Decrypted local audit log (requires keyring.key)",
		Run: func(cmd *cobra.Command, args []string) {
			st := audit.NewStore()
			if date == "list" {
				for _, d := range st.Dates() {
					fmt.Println(d)
				}
				return
			}
			recs, err := st.Read(date)
			if err != nil {
				fmt.Println("audit:", err)
				os.Exit(1)
			}
			for _, r := range recs {
				b, _ := json.Marshal(r)
				fmt.Println(string(b))
			}
		},
	}
	c.Flags().StringVar(&date, "date", "", "YYYY-MM-DD (default today, 'list' for dates)")
	return c
}
