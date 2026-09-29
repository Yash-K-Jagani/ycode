package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Yash-K-Jagani/ycode/internal/config"
	"github.com/Yash-K-Jagani/ycode/internal/keys"
	"github.com/Yash-K-Jagani/ycode/internal/providers"
	"github.com/Yash-K-Jagani/ycode/internal/providers/ollama"
	"github.com/Yash-K-Jagani/ycode/internal/upgrade"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

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
			// Derived from the provider registry, so the command printed here
			// is the same one `ycode setup` would act on.
			rec := providers.DefaultModel(cfg.ActiveProvider)
			fmt.Printf("  ollama pull %s\n", rec)
			fmt.Printf("  ycode config set active_model %s\n", rec)
		}
		if state.NeedsKey {
			if k := configKeyForEnv(state.KeyEnv); k != "" {
				fmt.Printf("  ycode config set %s <your key>   # stored in your OS keyring\n", k)
			} else {
				fmt.Printf("  set %s=<your key>\n", state.KeyEnv)
			}
		}
		fmt.Println("  ycode setup                        # interactive wizard")
		fmt.Println()
		fmt.Println("See https://github.com/Yash-K-Jagani/ycode#2b-setup")
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

	// The picker is the registry, so a provider added there is offered here.
	choices := providers.All()
	for i, p := range choices {
		fmt.Printf("    %d) %s\n", i+1, p.Label)
	}
	fmt.Println()
	idx, ok := promptInt("provider", 1, 1, len(choices))
	if !ok {
		return false
	}
	p := choices[idx-1]
	cfg.ActiveProvider = p.ID

	if p.NeedsKey {
		keyEnv := cfg.KeyEnvFor(p.ID)
		fmt.Printf("\n  %s API key (input hidden; leave empty to set it later with %s)\n", p.ID, keyEnv)
		key, ok := promptSecret(p.ID + " key")
		if !ok {
			return false
		}
		if key == "" {
			fmt.Printf("\n  No key entered. Set %s in your environment, or run 'ycode setup' again.\n", keyEnv)
			return false
		}
		if err := os.Setenv(keyEnv, key); err != nil {
			fmt.Println("could not set env var:", err)
			return false
		}
		// Persist to the OS keyring so later runs find it without the env var.
		if err := keys.Set(keyEnv, key); err != nil {
			fmt.Println("note: could not store key in OS keyring; set", keyEnv, "in your environment instead")
		}
		cfg.SetKeyFor(p.ID, key)
	}

	fmt.Println()
	model, ok := chooseModel(p.ID, cfg)
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
		// Only the active provider's key. Writing it into every provider's
		// field would later send this secret to hosts the user never chose.
		cfg.SetKeyFor(cfg.ActiveProvider, key)
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
// a local provider it offers to pull a recommended model when the daemon is
// reachable but empty.
func chooseModel(provider string, cfg *config.Config) (string, bool) {
	const timeout = 5 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Locality is a registry property, not a name comparison, so a new local
	// provider is handled without touching this function.
	local := providers.Local(provider)
	recommended := providers.DefaultModel(provider)

	var models []string
	if local {
		list, err := ollama.New(cfg.OllamaHost).ListModels(ctx)
		if err != nil {
			fmt.Printf("  Could not reach Ollama at %s.\n", cfg.OllamaHost)
			fmt.Println("  Start it with: ollama serve")
			fmt.Printf("  Then pull a model: ollama pull %s\n", recommended)
			return "", false
		}
		for _, m := range list {
			models = append(models, m.ID)
		}
	}
	if len(models) == 0 {
		if !local {
			fmt.Println("  Enter the model id to use (or leave empty to let the provider decide).")
			m, ok := promptLine("model")
			if !ok || m == "" {
				return "", false
			}
			return m, true
		}
		// Ollama reachable but no models installed.
		fmt.Println("  Ollama is running but has no models installed.")
		fmt.Printf("  Recommended for coding: %s", recommended)
		if hint := providers.Get(provider).SizeHint; hint != "" {
			fmt.Printf(" (%s)", hint)
		}
		fmt.Println()
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

// stdinReader is buffered once per stdin file and reused.
//
// Each prompt used to build its own bufio.Reader, which looked harmless and
// was not: a reader buffers everything the pipe has already delivered, and
// discarding it throws away whatever followed the first line. So a user who
// mistyped their answer - the exact moment promptInt's retry loop exists to
// handle - had the rest of their input eaten and was dropped out of the
// wizard instead of being asked again.
//
// Keyed on the *os.File so a test that swaps os.Stdin gets a fresh reader.
var (
	stdinMu  sync.Mutex
	stdinSrc *os.File
	stdinBuf *bufio.Reader
)

func lineReader() *bufio.Reader {
	stdinMu.Lock()
	defer stdinMu.Unlock()
	if stdinBuf == nil || stdinSrc != os.Stdin {
		stdinSrc = os.Stdin
		stdinBuf = bufio.NewReader(os.Stdin)
	}
	return stdinBuf
}

// promptLine reads one line. Returns ok=false on EOF/interrupt.
func promptLine(label string) (string, bool) {
	fmt.Printf("  %s: ", label)
	line, err := lineReader().ReadString('\n')
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
	line, err := lineReader().ReadString('\n')
	fmt.Println()
	if err != nil && line == "" {
		return "", false
	}
	return strings.TrimSpace(line), true
}

// upgradeCmd reports the latest published release and, with --apply, replaces
// the running binary after verifying the published SHA256.
func upgradeCmd() *cobra.Command {
	var apply bool
	var repo string
	c := &cobra.Command{
		Use:   "upgrade",
		Short: "Check for a newer ycode release",
		Run:   func(cmd *cobra.Command, args []string) { runUpgrade(apply, repo) },
	}
	c.Flags().BoolVar(&apply, "apply", false, "download, verify, and install the newer release")
	c.Flags().StringVar(&repo, "repo", upgrade.DefaultRepo, "GitHub repo to check")
	return c
}

func runUpgrade(apply bool, repo string) {
	self, err := os.Executable()
	if err != nil {
		fmt.Println("cannot locate the running binary:", err)
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	fmt.Println("checking", repo, "for a newer release...")
	rel, err := upgrade.Latest(ctx, repo)
	if err != nil {
		fmt.Println("upgrade check failed:", err)
		os.Exit(1)
	}

	current := Version
	if !upgrade.CleanVersion(current) {
		// A local `go build` or `go install` copy. Say so rather than
		// comparing a dev sentinel against real releases.
		fmt.Printf("running ycode %s (local build, not a tagged release)\n", current)
		fmt.Println("latest published release:", rel.TagName)
		fmt.Println("re-run 'ycode upgrade --apply' to switch to the release build.")
	} else if !upgrade.IsNewer(rel.TagName, current) {
		fmt.Printf("ycode %s is up to date (%s)\n", current, rel.TagName)
		return
	} else {
		fmt.Printf("update available: %s -> %s\n", current, rel.TagName)
	}
	fmt.Println("release notes:", rel.HTMLURL)

	if !apply {
		fmt.Println("\nrun 'ycode upgrade --apply' to install it")
		return
	}

	// The running executable is locked for overwrite on Windows, so install
	// beside it and rename. Refuse if we lack permission rather than half-write.
	res, err := upgrade.Apply(ctx, rel, current, self)
	if err != nil {
		fmt.Println("upgrade failed:", err)
		os.Exit(1)
	}
	fmt.Printf("\ninstalled %s (sha256 verified)\n", res.To)
	fmt.Println("previous binary kept as", res.Backup)
	fmt.Println("restart ycode to use it: ycode version")
}
