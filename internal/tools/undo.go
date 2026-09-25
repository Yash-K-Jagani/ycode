package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const maxBackups = 20

type backupRec struct {
	TS     int64  `json:"ts"`
	Orig   string `json:"orig"`
	Backup string `json:"backup"`
}

func backupDir(workdir string) string {
	return filepath.Join(workdir, ".ycode", "backups")
}

func backupIndex(workdir string) []backupRec {
	data, _ := os.ReadFile(filepath.Join(backupDir(workdir), "backups.json"))
	var recs []backupRec
	_ = json.Unmarshal(data, &recs)
	return recs
}

func saveBackupIndex(workdir string, recs []backupRec) {
	_ = os.MkdirAll(backupDir(workdir), 0o755)
	data, _ := json.MarshalIndent(recs, "", "  ")
	_ = os.WriteFile(filepath.Join(backupDir(workdir), "backups.json"), data, 0o644)
}

// backupFile snapshots existing content before overwrite. No-op when the
// target doesn't exist or workdir is empty. Prunes to maxBackups.
func backupFile(workdir, path string, content []byte) {
	if workdir == "" || len(content) == 0 {
		return
	}
	name := fmt.Sprintf("%d-%s", time.Now().UnixNano(), filepath.Base(path))
	dst := filepath.Join(backupDir(workdir), name)
	_ = os.MkdirAll(backupDir(workdir), 0o755)
	if err := os.WriteFile(dst, content, 0o644); err != nil {
		return
	}
	recs := append(backupIndex(workdir), backupRec{TS: time.Now().UnixNano(), Orig: path, Backup: dst})
	sort.Slice(recs, func(i, j int) bool { return recs[i].TS < recs[j].TS })
	for len(recs) > maxBackups {
		_ = os.Remove(recs[0].Backup)
		recs = recs[1:]
	}
	saveBackupIndex(workdir, recs)
}

// UndoLast restores the most recent backup.
func UndoLast(workdir string) (string, error) {
	recs := backupIndex(workdir)
	if len(recs) == 0 {
		return "", fmt.Errorf("nothing to undo")
	}
	last := recs[len(recs)-1]
	data, err := os.ReadFile(last.Backup)
	if err != nil {
		return "", fmt.Errorf("backup unreadable: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(last.Orig), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(last.Orig, data, 0o644); err != nil {
		return "", err
	}
	_ = os.Remove(last.Backup)
	saveBackupIndex(workdir, recs[:len(recs)-1])
	return last.Orig, nil
}
