package config

import (
	"os"
	"path/filepath"

	"github.com/Yash-K-Jagani/ycode/internal/keys"
	"github.com/spf13/viper"
)

type Config struct {
	OllamaHost       string `mapstructure:"ollama_host"`
	Theme            string `mapstructure:"theme"`
	ActiveProvider   string `mapstructure:"active_provider"`
	ActiveModel      string `mapstructure:"active_model"`
	ZeroDataLeak     bool   `mapstructure:"zero_data_leak"`
	GeminiAPIKey     string `mapstructure:"-"`
	OpenRouterKey    string `mapstructure:"-"`
	GroqKey          string `mapstructure:"-"`
	GeminiKeyEnv     string `mapstructure:"gemini_key_env"`
	OpenRouterKeyEnv string `mapstructure:"openrouter_key_env"`
	GroqKeyEnv       string `mapstructure:"groq_key_env"`
}

func Defaults() Config {
	return Config{
		OllamaHost:       "http://localhost:11434",
		Theme:            "dark",
		ActiveProvider:   "ollama",
		ActiveModel:      "",
		GeminiKeyEnv:     "GEMINI_API_KEY",
		OpenRouterKeyEnv: "OPENROUTER_API_KEY",
		GroqKeyEnv:       "GROQ_API_KEY",
	}
}

func Dir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".ycode")
}

func Load() (Config, error) {
	cfg := Defaults()
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(Dir())
	v.AddConfigPath(".ycode")
	v.SetEnvPrefix("YCODE")
	v.AutomaticEnv()
	_ = v.ReadInConfig()
	_ = v.Unmarshal(&cfg)
	cfg.GeminiAPIKey = firstNonEmpty(os.Getenv(cfg.GeminiKeyEnv), keyringGet(cfg.GeminiKeyEnv))
	cfg.OpenRouterKey = firstNonEmpty(os.Getenv(cfg.OpenRouterKeyEnv), keyringGet(cfg.OpenRouterKeyEnv))
	cfg.GroqKey = firstNonEmpty(os.Getenv(cfg.GroqKeyEnv), keyringGet(cfg.GroqKeyEnv))
	if h := os.Getenv("OLLAMA_HOST"); h != "" {
		cfg.OllamaHost = h
	}
	return cfg, nil
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

func keyringGet(account string) string {
	if account == "" {
		return ""
	}
	if secret, ok := keys.Get(account); ok {
		return secret
	}
	return ""
}

func (c Config) Save() error {
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return err
	}
	v := viper.New()
	v.Set("ollama_host", c.OllamaHost)
	v.Set("theme", c.Theme)
	v.Set("active_provider", c.ActiveProvider)
	v.Set("active_model", c.ActiveModel)
	v.Set("zero_data_leak", c.ZeroDataLeak)
	v.Set("gemini_key_env", c.GeminiKeyEnv)
	v.Set("openrouter_key_env", c.OpenRouterKeyEnv)
	v.Set("groq_key_env", c.GroqKeyEnv)
	return v.WriteConfigAs(filepath.Join(Dir(), "config.yaml"))
}
