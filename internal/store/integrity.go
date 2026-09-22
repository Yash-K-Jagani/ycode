package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Yash-K-Jagani/ycode/internal/plugins"
	"github.com/Yash-K-Jagani/ycode/internal/skills"
)

// record is written as .store.json inside every store-installed package.
type record struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Source      string `json:"source"`
	Subdir      string `json:"subdir,omitempty"`
	Ref         string `json:"ref,omitempty"`
	SHA256      string `json:"sha256"`
	InstalledAt string `json:"installed_at"`
}

const recordFile = ".store.json"

// dirHash hashes a directory tree deterministically (sorted rel paths + contents).
func dirHash(dir string) (string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		if rel == recordFile {
			return nil
		}
		files = append(files, rel)
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(files)
	h := sha256.New()
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			return "", err
		}
		h.Write([]byte(rel + "\x00"))
		h.Write(data)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func writeRecord(dir string, e Entry, sum string) error {
	rec := record{Name: e.Name, Kind: e.Kind, Source: e.Source, Subdir: e.Subdir, Ref: e.Ref, SHA256: sum, InstalledAt: time.Now().UTC().Format(time.RFC3339)}
	data, _ := json.MarshalIndent(rec, "", "  ")
	return os.WriteFile(filepath.Join(dir, recordFile), data, 0o644)
}

func readRecord(dir string) (record, bool) {
	var rec record
	data, err := os.ReadFile(filepath.Join(dir, recordFile))
	if err != nil {
		return rec, false
	}
	if err := json.Unmarshal(data, &rec); err != nil {
		return rec, false
	}
	return rec, true
}

// Remove uninstalls a skill or plugin by name.
func Remove(name string, sk *skills.Manager, pl *plugins.Loader) (string, error) {
	if s, _, err := sk.Get(name); err == nil {
		if err := os.RemoveAll(s.Path); err != nil {
			return "", err
		}
		return "skill:" + s.Name, nil
	}
	if err := pl.Uninstall(name); err == nil {
		return "plugin:" + name, nil
	}
	return "", fmt.Errorf("not installed: %q", name)
}

// Verify checks one installed package against its record (or index checksum).
func Verify(name string, sk *skills.Manager, pl *plugins.Loader) (string, error) {
	if s, _, err := sk.Get(name); err == nil {
		return verifyDir(s.Path)
	}
	for n, dir := range pl.Dirs() {
		if strings.EqualFold(n, name) {
			return verifyDir(dir)
		}
	}
	return "", fmt.Errorf("not installed: %q", name)
}

// VerifyAll checks every record-carrying package under both managers.
func VerifyAll(sk *skills.Manager, pl *plugins.Loader) []string {
	var out []string
	if list, err := sk.List(); err == nil {
		for _, s := range list {
			msg, err := verifyDir(s.Path)
			if err != nil {
				out = append(out, s.Name+": "+err.Error())
			} else {
				out = append(out, s.Name+": "+msg)
			}
		}
	}
	for n, dir := range pl.Dirs() {
		msg, err := verifyDir(dir)
		if err != nil {
			out = append(out, n+": "+err.Error())
		} else {
			out = append(out, n+": "+msg)
		}
	}
	if len(out) == 0 {
		out = append(out, "nothing installed")
	}
	sort.Strings(out)
	return out
}

func verifyDir(dir string) (string, error) {
	rec, ok := readRecord(dir)
	if !ok {
		return "no record (not store-installed?)", nil
	}
	sum, err := dirHash(dir)
	if err != nil {
		return "", err
	}
	if rec.SHA256 != "" && sum != rec.SHA256 {
		return "", fmt.Errorf("CHANGED since install (record %s, now %s)", short(rec.SHA256), short(sum))
	}
	return "ok (" + short(sum) + ")", nil
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
