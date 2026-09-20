package skills

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Yash-K-Jagani/ycode/internal/config"
)

type Skill struct {
	Name        string
	Description string
	Path        string
}

type Manager struct {
	dir string
}

func defaultDir() string { return filepath.Join(config.Dir(), "skills") }

func NewManager() *Manager { return NewManagerAt(defaultDir()) }

func NewManagerAt(dir string) *Manager { return &Manager{dir: dir} }

func (m *Manager) List() ([]Skill, error) {
	entries, err := os.ReadDir(m.dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Skill
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		s, err := loadSkill(filepath.Join(m.dir, e.Name()))
		if err != nil {
			continue
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func loadSkill(dir string) (Skill, error) {
	data, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return Skill{}, err
	}
	name := filepath.Base(dir)
	desc := ""
	for _, line := range strings.Split(string(data), "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "# ") && name == filepath.Base(dir) {
			name = strings.TrimSpace(strings.TrimPrefix(l, "# "))
		}
		if strings.HasPrefix(strings.ToLower(l), "description:") {
			desc = strings.TrimSpace(l[len("description:"):])
		}
	}
	return Skill{Name: name, Description: desc, Path: dir}, nil
}

// Install copies a local dir or clones a git URL (owner/name shorthand allowed for GitHub).
func (m *Manager) Install(source string) (Skill, error) {
	if isSkillShorthand(source) {
		source = "https://github.com/" + source + ".git"
	}
	name := ""
	dest := ""
	if isURL(source) {
		base := source[strings.LastIndex(source, "/")+1:]
		name = strings.TrimSuffix(base, ".git")
		dest = filepath.Join(m.dir, name)
		if err := clone(source, dest); err != nil {
			return Skill{}, err
		}
	} else {
		fi, err := os.Stat(source)
		if err != nil || !fi.IsDir() {
			return Skill{}, fmt.Errorf("not a local skill dir: %s", source)
		}
		name = filepath.Base(source)
		dest = filepath.Join(m.dir, name)
		if _, err := os.Stat(dest); err == nil {
			return Skill{}, fmt.Errorf("skill %q already installed", name)
		}
		if err := copyDir(source, dest); err != nil {
			return Skill{}, err
		}
	}
	return loadSkill(dest)
}

func (m *Manager) Get(name string) (Skill, string, error) {
	s, err := loadSkill(filepath.Join(m.dir, name))
	if err != nil {
		// try case-insensitive match
		list, _ := m.List()
		for _, c := range list {
			if strings.EqualFold(c.Name, name) {
				s = c
				err = nil
				break
			}
		}
		if err != nil {
			return Skill{}, "", fmt.Errorf("skill %q not installed", name)
		}
	}
	data, err := os.ReadFile(filepath.Join(s.Path, "SKILL.md"))
	if err != nil {
		return Skill{}, "", err
	}
	return s, string(data), nil
}

func isURL(s string) bool {
	return strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "git@")
}

func isSkillShorthand(s string) bool {
	if strings.Contains(s, "://") || strings.HasPrefix(s, "git@") || !strings.Contains(s, "/") {
		return false
	}
	parts := strings.Split(s, "/")
	return len(parts) == 2 && parts[0] != "" && parts[1] != ""
}

func clone(url, dest string) error {
	if strings.HasPrefix(url, "http") && !strings.Contains(url, "github.com") && !strings.Contains(url, "gitlab.com") {
		return fmt.Errorf("only github.com/gitlab.com URLs allowed")
	}
	_ = os.MkdirAll(filepath.Dir(dest), 0o755)
	cmd := exec.Command("git", "clone", "--depth=1", url, dest)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("clone failed: %v\n%s", err, out)
	}
	return nil
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// Export zips an installed skill for sharing.
func (m *Manager) Export(name, destZip string) error {
	s, _, err := m.Get(name)
	if err != nil {
		return err
	}
	f, err := os.Create(destZip)
	if err != nil {
		return err
	}
	defer f.Close()
	w := zip.NewWriter(f)
	defer w.Close()
	return filepath.WalkDir(s.Path, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(s.Path, path)
		fw, err := w.Create(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		_, err = fw.Write(data)
		return err
	})
}

// Import unzips a shared skill into the library.
func (m *Manager) Import(zipPath string) (Skill, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return Skill{}, err
	}
	defer r.Close()
	base := strings.TrimSuffix(filepath.Base(zipPath), ".zip")
	dest := filepath.Join(m.dir, base)
	if _, err := os.Stat(dest); err == nil {
		return Skill{}, fmt.Errorf("skill %q already installed", base)
	}
	for _, f := range r.File {
		if f.FileInfo().IsDir() || strings.Contains(f.Name, "..") {
			continue
		}
		target := filepath.Join(dest, filepath.FromSlash(f.Name))
		_ = os.MkdirAll(filepath.Dir(target), 0o755)
		rc, err := f.Open()
		if err != nil {
			return Skill{}, err
		}
		data, err := io.ReadAll(io.LimitReader(rc, 10<<20))
		_ = rc.Close()
		if err != nil {
			return Skill{}, err
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return Skill{}, err
		}
	}
	return loadSkill(dest)
}
