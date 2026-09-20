package audit

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Yash-K-Jagani/ycode/internal/config"
	"github.com/Yash-K-Jagani/ycode/internal/security"
	"golang.org/x/crypto/nacl/secretbox"
)

type Store struct {
	mu  sync.Mutex
	dir string
	key [32]byte
}

func defaultBase() string { return config.Dir() }

func loadKey(base string) ([32]byte, error) {
	var key [32]byte
	p := filepath.Join(base, "keyring.key")
	data, err := os.ReadFile(p)
	if err == nil && len(data) == 32 {
		copy(key[:], data)
		return key, nil
	}
	if _, err := rand.Read(key[:]); err != nil {
		return key, err
	}
	_ = os.MkdirAll(base, 0o755)
	if err := os.WriteFile(p, key[:], 0o600); err != nil {
		return key, err
	}
	return key, nil
}

func NewStore() *Store { return NewStoreAt(defaultBase()) }

func NewStoreAt(base string) *Store {
	key, err := loadKey(base)
	if err != nil {
		key = [32]byte{}
	}
	return &Store{dir: filepath.Join(base, "audit"), key: key}
}

// Append redacts secrets, encrypts, and appends one record.
func (s *Store) Append(event string, fields map[string]any) error {
	rec := map[string]any{"ts": time.Now().UTC().Format(time.RFC3339), "event": event}
	for k, v := range fields {
		rec[k] = v
	}
	raw, _ := json.Marshal(rec)
	raw = []byte(security.Redact(string(raw)))
	var nonce [24]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	sealed := secretbox.Seal(nonce[:], raw, &nonce, &s.key)
	line := base64.StdEncoding.EncodeToString(sealed) + "\n"
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = os.MkdirAll(s.dir, 0o755)
	f, err := os.OpenFile(filepath.Join(s.dir, time.Now().Format("2006-01-02")+".log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = f.WriteString(line)
	return err
}

// Read decrypts all records for a date (YYYY-MM-DD, "" = today).
func (s *Store) Read(date string) ([]map[string]any, error) {
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	data, err := os.ReadFile(filepath.Join(s.dir, date+".log"))
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	for _, line := range splitLines(string(data)) {
		line = trimSpace(line)
		if line == "" {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(line)
		if err != nil || len(raw) < 24 {
			continue
		}
		var nonce [24]byte
		copy(nonce[:], raw[:24])
		opened, ok := secretbox.Open(nil, raw[24:], &nonce, &s.key)
		if !ok {
			return out, fmt.Errorf("decrypt failed (wrong key?)")
		}
		var rec map[string]any
		if err := json.Unmarshal(opened, &rec); err == nil {
			out = append(out, rec)
		}
	}
	return out, nil
}

func (s *Store) Dates() []string {
	entries, _ := os.ReadDir(s.dir)
	var out []string
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".log" {
			out = append(out, e.Name()[:len(e.Name())-4])
		}
	}
	return out
}

var defaultStore = NewStore()

// Log is a best-effort package helper (never returns error).
func Log(event string, fields map[string]any) {
	_ = defaultStore.Append(event, fields)
}

func splitLines(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == '\n' {
			out = append(out, cur)
			cur = ""
		} else {
			cur += string(r)
		}
	}
	return append(out, cur)
}

func trimSpace(s string) string {
	i, j := 0, len(s)
	for i < j && (s[i] == ' ' || s[i] == '\t' || s[i] == '\r') {
		i++
	}
	for j > i && (s[j-1] == ' ' || s[j-1] == '\t' || s[j-1] == '\r') {
		j--
	}
	return s[i:j]
}
