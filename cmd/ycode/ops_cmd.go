package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/Yash-K-Jagani/ycode/internal/audit"
	"github.com/Yash-K-Jagani/ycode/internal/automation"
	"github.com/Yash-K-Jagani/ycode/internal/batch"
	"github.com/Yash-K-Jagani/ycode/internal/cache"
	"github.com/Yash-K-Jagani/ycode/internal/config"
	"github.com/Yash-K-Jagani/ycode/internal/cost"
	"github.com/Yash-K-Jagani/ycode/internal/headless"
	"github.com/Yash-K-Jagani/ycode/internal/modes"
	"github.com/Yash-K-Jagani/ycode/internal/plugins"
	"github.com/Yash-K-Jagani/ycode/internal/providers/ollama"
	"github.com/Yash-K-Jagani/ycode/internal/router"
	"github.com/Yash-K-Jagani/ycode/internal/serve"
	"github.com/Yash-K-Jagani/ycode/internal/skills"
	"github.com/Yash-K-Jagani/ycode/internal/store"
	"github.com/spf13/cobra"
)

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
	var goalIters int
	c := &cobra.Command{
		Use:   "run <prompt>",
		Short: "Headless single turn (tools in build/goal/plan; goal loops until done)",
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
			answer, outcome, err := headless.RunWithStatus(context.Background(), cfg, strings.Join(args, " "), headless.Options{Mode: m, Agent: agent, Workdir: workdir, Stderr: os.Stderr, GoalIters: goalIters})
			if err != nil {
				fmt.Println(answer)
				fmt.Println("error:", err)
				os.Exit(1)
			}
			fmt.Println(answer)
			// In goal mode the exit code is the result: 0 only when the work is
			// actually done. A run that stalled, blocked or ran out of budget
			// used to exit 0, so CI could not tell success from a model that
			// merely said it was finished.
			if code := outcome.ExitCode(); code != 0 {
				fmt.Fprintf(os.Stderr, "\ngoal not met: %s (exit %d)\n", outcome, code)
				os.Exit(code)
			}
		},
	}
	// Derived from the mode registry, so the help cannot list a mode that
	// does not exist or omit one that does.
	c.Flags().StringVar(&mode, "mode", "build", strings.Join(modes.Names(), "|"))
	c.Flags().StringVar(&agent, "agent", "builder", "agent name")
	c.Flags().IntVar(&goalIters, "goal-iters", 0, "goal mode: max autonomous iterations (0 = default)")
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
	addCmd.Flags().String("mode", "build", "mode for the job ("+strings.Join(modes.Names(), "|")+")")
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
