package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/Yash-K-Jagani/ycode/internal/config"
	"github.com/Yash-K-Jagani/ycode/internal/keys"
	"github.com/Yash-K-Jagani/ycode/internal/providers"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// configCmd exposes config.yaml for scripts and non-interactive setup, so
// users are not forced through the wizard just to pin a model.
func configCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "config",
		Short: "Read and write ycode configuration",
		Run:   func(cmd *cobra.Command, args []string) { printConfig() },
	}
	c.AddCommand(configGetCmd(), configSetCmd())
	return c
}

func printConfig() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Println("config error:", err)
		return
	}
	for _, kv := range configPairs(cfg) {
		fmt.Printf("%s = %s\n", kv[0], kv[1])
	}
}

func configGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <key>",
		Short: "Print one config value",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			cfg, err := config.Load()
			if err != nil {
				fmt.Println("config error:", err)
				return
			}
			for _, kv := range configPairs(cfg) {
				if kv[0] == args[0] {
					fmt.Println(kv[1])
					return
				}
			}
			fmt.Fprintf(os.Stderr, "unknown key %q (known keys: %s)\n", args[0], strings.Join(configKeys(), ", "))
			os.Exit(1)
		},
	}
}

func configSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Write one config value",
		Args:  cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			key, val := args[0], args[1]
			cfg, err := config.Load()
			if err != nil {
				fmt.Println("config error:", err)
				return
			}
			if !applyConfigValue(&cfg, key, val) {
				fmt.Fprintf(os.Stderr, "unknown key %q (known keys: %s)\n", key, strings.Join(configKeys(), ", "))
				os.Exit(1)
			}
			if err := cfg.Save(); err != nil {
				fmt.Println("could not save config:", err)
				os.Exit(1)
			}
			// Secret values belong in the keyring, never in config.yaml.
			if isSecretConfigKey(key) {
				if err := keys.Set(configKeyEnvFor(key), val); err != nil {
					fmt.Fprintf(os.Stderr, "saved, but could not store in OS keyring: %v\n", err)
					return
				}
				fmt.Printf("%s saved to the OS keyring.\n", key)
				return
			}
			fmt.Printf("%s = %s\n", key, val)
		},
	}
}

// configPairs returns the config as ordered key/value pairs. Secrets are
// reported as "set"/"unset" so `ycode config` can be pasted or logged safely.
func configPairs(c config.Config) [][2]string {
	secret := func(k string) string {
		if k == "" {
			return "unset"
		}
		return "set"
	}
	out := [][2]string{
		{"active_provider", c.ActiveProvider},
		{"active_model", c.ActiveModel},
		{"ollama_host", c.OllamaHost},
		{"theme", c.Theme},
		{"zero_data_leak", strconv.FormatBool(c.ZeroDataLeak)},
	}
	// One row per key-bearing provider, so `ycode config list` and the
	// keyring routing cannot fall behind the registry.
	for _, id := range providers.IDs() {
		if k := c.KeyFor(id); k != "" || providers.Get(id).NeedsKey {
			out = append(out, [2]string{providerConfigKey(id), secret(k)})
		}
	}
	return out
}

func configKeys() []string {
	var out []string
	for _, kv := range configPairs(config.Defaults()) {
		out = append(out, kv[0])
	}
	return out
}

// isSecretConfigKey reports whether a config key holds a credential, which must
// be routed to the OS keyring instead of written into config.yaml. Every
// provider's API key is a secret by the same naming rule.
func isSecretConfigKey(key string) bool {
	_, ok := providerFromConfigKey(key)
	return ok
}

// configKeyEnvFor maps a secret config key to the env var name used for both
// the process environment and the keyring account. API keys are named
// <provider>_api_key, so the rule covers every provider without a case each.
func configKeyEnvFor(key string) string {
	provider, ok := providerFromConfigKey(key)
	if !ok {
		return key
	}
	return config.Defaults().KeyEnvFor(provider)
}

// configKeyForEnv is the inverse of configKeyEnvFor, so onboarding can tell the
// user the exact `ycode config set` invocation to run.
func configKeyForEnv(env string) string {
	d := config.Defaults()
	for _, id := range providers.IDs() {
		if d.KeyEnvFor(id) == env {
			return providerConfigKey(id)
		}
	}
	return ""
}

// providerConfigKey is the `ycode config` name for a provider's API key.
func providerConfigKey(provider string) string { return provider + "_api_key" }

// providerFromConfigKey is the inverse of providerConfigKey.
func providerFromConfigKey(key string) (string, bool) {
	for _, id := range providers.IDs() {
		if providerConfigKey(id) == key {
			return id, true
		}
	}
	return "", false
}

// applyConfigValue writes val into the named config field, reporting whether
// the key was recognised.
func applyConfigValue(c *config.Config, key, val string) bool {
	// Every provider's API key follows the same naming rule, so the key goes
	// to whichever provider owns it rather than to a fixed set of fields.
	if provider, ok := providerFromConfigKey(key); ok {
		c.SetKeyFor(provider, val)
		return true
	}
	switch key {
	case "active_provider":
		c.ActiveProvider = val
	case "active_model":
		c.ActiveModel = val
	case "ollama_host":
		c.OllamaHost = val
	case "theme":
		c.Theme = val
	case "zero_data_leak":
		b, err := strconv.ParseBool(val)
		if err != nil {
			fmt.Fprintf(os.Stderr, "zero_data_leak expects true or false, got %q\n", val)
			return true // recognised, just invalid; do not claim "unknown key"
		}
		c.ZeroDataLeak = b
	default:
		return false
	}
	return true
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
