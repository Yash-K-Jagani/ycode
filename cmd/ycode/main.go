package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/Yash-K-Jagani/ycode/internal/audit"
	"github.com/Yash-K-Jagani/ycode/internal/automation"
	"github.com/Yash-K-Jagani/ycode/internal/batch"
	"github.com/Yash-K-Jagani/ycode/internal/config"
	"github.com/Yash-K-Jagani/ycode/internal/cost"
	"github.com/Yash-K-Jagani/ycode/internal/headless"
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
	root.AddCommand(statusCmd(), runCmd(), serveCmd(), batchCmd(), ciCmd(), daemonCmd(), auditCmd(), versionCmd(), storeCmd())
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
	)
	return c
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

var Version = "0.7.0"

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("ycode", Version)
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
