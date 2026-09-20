package store

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Yash-K-Jagani/ycode/internal/config"
	"github.com/Yash-K-Jagani/ycode/internal/skills"
	"gopkg.in/yaml.v3"
)

// DefaultIndexURL is the curated index; `store update` caches it locally.
const DefaultIndexURL = "https://raw.githubusercontent.com/Yash-K-Jagani/ycode/main/store/index.yaml"

type Entry struct {
	Name        string `yaml:"name"`
	Kind        string `yaml:"kind"` // skill|plugin
	Source      string `yaml:"source"`
	Subdir      string `yaml:"subdir,omitempty"`
	Ref         string `yaml:"ref,omitempty"`
	Description string `yaml:"description,omitempty"`
}

type Index struct {
	Version int     `yaml:"version"`
	Items   []Entry `yaml:"items"`
}

func cachePath() string { return filepath.Join(config.Dir(), "store.yaml") }

func Parse(data []byte) (Index, error) {
	var idx Index
	if err := yaml.Unmarshal(data, &idx); err != nil {
		return Index{}, err
	}
	return idx, nil
}

// Update fetches the index URL into the local cache.
func Update(url string) (Index, error) {
	if url == "" {
		url = DefaultIndexURL
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return Index{}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Index{}, fmt.Errorf("fetch index: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return Index{}, fmt.Errorf("index http %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Index{}, err
	}
	idx, err := Parse(data)
	if err != nil {
		return Index{}, err
	}
	_ = os.MkdirAll(config.Dir(), 0o755)
	if err := os.WriteFile(cachePath(), data, 0o644); err != nil {
		return Index{}, err
	}
	return idx, nil
}

// Load reads the cached index (`store update` first).
func Load() (Index, error) {
	data, err := os.ReadFile(cachePath())
	if err != nil {
		return Index{}, fmt.Errorf("no cached index — run `store update` first")
	}
	return Parse(data)
}

func (idx Index) Get(name string) (Entry, bool) {
	for _, e := range idx.Items {
		if strings.EqualFold(e.Name, name) {
			return e, true
		}
	}
	return Entry{}, false
}

func (idx Index) Search(q string) []Entry {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return append([]Entry(nil), idx.Items...)
	}
	var out []Entry
	for _, e := range idx.Items {
		if strings.Contains(strings.ToLower(e.Name), q) ||
			strings.Contains(strings.ToLower(e.Description), q) ||
			strings.EqualFold(e.Kind, q) {
			out = append(out, e)
		}
	}
	return out
}

type SkillInstaller interface {
	Install(source string) (skills.Skill, error)
}

type PluginInstaller interface {
	Install(source string) (string, error)
}

// Install fetches the entry (clone URL or local dir, honoring subdir/ref)
// and installs it through the skill/plugin managers.
func Install(e Entry, sk SkillInstaller, pl PluginInstaller) (string, error) {
	if e.Name == "" || e.Source == "" {
		return "", fmt.Errorf("bad store entry (name/source required)")
	}
	src := e.Source
	if isRemote(src) {
		tmp, err := clone(src, e.Ref)
		if err != nil {
			return "", err
		}
		defer func() { _ = os.RemoveAll(tmp) }()
		src = tmp
	}
	if e.Subdir != "" {
		if strings.Contains(e.Subdir, "..") {
			return "", fmt.Errorf("bad subdir %q", e.Subdir)
		}
		src = filepath.Join(src, filepath.FromSlash(e.Subdir))
	}
	switch strings.ToLower(e.Kind) {
	case "skill":
		if sk == nil {
			return "", fmt.Errorf("no skill manager")
		}
		s, err := sk.Install(src)
		if err != nil {
			return "", err
		}
		return s.Name, nil
	case "plugin":
		if pl == nil {
			return "", fmt.Errorf("no plugin loader")
		}
		return pl.Install(src)
	default:
		return "", fmt.Errorf("unknown kind %q (skill|plugin)", e.Kind)
	}
}

func isRemote(s string) bool {
	return strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "git@")
}

func clone(url, ref string) (string, error) {
	if strings.HasPrefix(url, "http") && !strings.Contains(url, "github.com") && !strings.Contains(url, "gitlab.com") {
		return "", fmt.Errorf("only github.com/gitlab.com sources allowed: %q", url)
	}
	dir, err := os.MkdirTemp("", "ycode-store-*")
	if err != nil {
		return "", err
	}
	args := []string{"clone", "--depth=1"}
	if ref != "" {
		args = append(args, "--branch", ref)
	}
	args = append(args, url, dir)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("clone failed: %v\n%s", err, out)
	}
	return dir, nil
}
