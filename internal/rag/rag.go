package rag

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Yash-K-Jagani/ycode/internal/config"
	"github.com/Yash-K-Jagani/ycode/internal/db"
	"github.com/Yash-K-Jagani/ycode/internal/embed"
)

const (
	chunkLines   = 40
	chunkOverlap = 5
	maxFileBytes = 200 * 1024
	topK         = 4
)

type Chunk struct {
	Path string    `json:"path"`
	Text string    `json:"text"`
	Vec  []float64 `json:"vec"`
}

type Index struct {
	Workdir string    `json:"workdir"`
	BuiltAt time.Time `json:"built_at"`
	Chunks  []Chunk   `json:"chunks"`
}

func indexPath(workdir string) string {
	return filepath.Join(config.Dir(), "rag", workKey(workdir)+".json")
}

func workKey(workdir string) string {
	h := sha1.Sum([]byte(workdir))
	return fmt.Sprintf("%x", h[:8])
}

func ChunkText(path, text string) []Chunk {
	lines := strings.Split(text, "\n")
	var out []Chunk
	for start := 0; start < len(lines); start += chunkLines - chunkOverlap {
		end := start + chunkLines
		if end > len(lines) {
			end = len(lines)
		}
		out = append(out, Chunk{Path: path, Text: strings.Join(lines[start:end], "\n")})
		if end == len(lines) {
			break
		}
	}
	return out
}

var skipDirs = map[string]bool{".git": true, "node_modules": true, "__pycache__": true, ".venv": true, "dist": true, "build": true, ".ycode": true}

var textExts = map[string]bool{
	".go": true, ".js": true, ".mjs": true, ".cjs": true, ".ts": true, ".tsx": true, ".jsx": true,
	".py": true, ".pyi": true, ".rs": true, ".java": true, ".kt": true, ".kts": true, ".scala": true,
	".c": true, ".h": true, ".hpp": true, ".cpp": true, ".cc": true, ".cs": true, ".swift": true,
	".rb": true, ".php": true, ".lua": true, ".zig": true, ".dart": true, ".ex": true, ".exs": true,
	".erl": true, ".hs": true, ".ml": true, ".pl": true, ".r": true, ".jl": true, ".vue": true,
	".svelte": true, ".astro": true, ".md": true, ".mdx": true, ".txt": true, ".yaml": true, ".yml": true,
	".json": true, ".jsonc": true, ".toml": true, ".ini": true, ".cfg": true, ".html": true, ".css": true,
	".scss": true, ".less": true, ".sh": true, ".bash": true, ".zsh": true, ".ps1": true, ".bat": true,
	".cmd": true, ".sql": true, ".proto": true, ".thrift": true, ".graphql": true, ".gql": true,
	".tf": true, ".tfvars": true, ".cmake": true, ".gradle": true, ".dockerfile": true, ".env": true,
	".xml": true, ".svg": true, ".tex": true,
}

// Ingest walks root, chunks text files and embeds them. embedFn allows testing without Ollama.
func Ingest(ctx context.Context, workdir, root string, embedFn func(ctx context.Context, inputs []string) ([][]float64, error)) (Index, error) {
	if root == "" {
		root = workdir
	}
	var chunks []Chunk
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !textExts[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		fi, err := d.Info()
		if err != nil || fi.Size() > maxFileBytes {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(workdir, path)
		chunks = append(chunks, ChunkText(rel, string(data))...)
		return nil
	})
	const batch = 32
	for i := 0; i < len(chunks); i += batch {
		end := i + batch
		if end > len(chunks) {
			end = len(chunks)
		}
		inputs := make([]string, 0, end-i)
		for _, c := range chunks[i:end] {
			inputs = append(inputs, c.Path+"\n"+c.Text)
		}
		vecs, err := embedFn(ctx, inputs)
		if err != nil {
			return Index{}, err
		}
		for j, v := range vecs {
			chunks[i+j].Vec = v
		}
	}
	idx := Index{Workdir: workdir, BuiltAt: time.Now(), Chunks: chunks}
	if err := saveIndex(workdir, idx); err != nil {
		return Index{}, err
	}
	return idx, nil
}

func saveIndex(workdir string, idx Index) error {
	if conn := db.Shared(); conn != nil {
		if err := saveIndexSQL(conn, workdir, idx); err == nil {
			return nil
		}
	}
	p := indexPath(workdir)
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	data, err := json.Marshal(idx)
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func saveIndexSQL(conn *sql.DB, workdir string, idx Index) error {
	key := workKey(workdir)
	tx, err := conn.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM rag_chunks WHERE wkey=?`, key); err != nil {
		return err
	}
	for _, c := range idx.Chunks {
		vec, _ := json.Marshal(c.Vec)
		if _, err := tx.Exec(`INSERT INTO rag_chunks(wkey,path,text,vec) VALUES(?,?,?,?)`,
			key, c.Path, c.Text, string(vec)); err != nil {
			return err
		}
	}
	built, _ := json.Marshal(idx.BuiltAt)
	if _, err := tx.Exec(`INSERT INTO kv(k,v) VALUES(?,?)
		ON CONFLICT(k) DO UPDATE SET v=excluded.v`, "rag/built/"+key, string(built)); err != nil {
		return err
	}
	return tx.Commit()
}

func Load(workdir string) (Index, bool) {
	return loadIndex(workdir)
}

func loadIndex(workdir string) (Index, bool) {
	if conn := db.Shared(); conn != nil {
		if idx, ok := loadIndexSQL(conn, workdir); ok {
			return idx, true
		}
		// one-time import from legacy JSON
		if idx, ok := loadIndexFile(workdir); ok {
			_ = saveIndexSQL(conn, workdir, idx)
			return idx, true
		}
		return Index{}, false
	}
	return loadIndexFile(workdir)
}

func loadIndexSQL(conn *sql.DB, workdir string) (Index, bool) {
	key := workKey(workdir)
	rows, err := conn.Query(`SELECT path,text,vec FROM rag_chunks WHERE wkey=?`, key)
	if err != nil {
		return Index{}, false
	}
	defer func() { _ = rows.Close() }()
	idx := Index{Workdir: workdir}
	for rows.Next() {
		var c Chunk
		var vec string
		if err := rows.Scan(&c.Path, &c.Text, &vec); err != nil {
			continue
		}
		_ = json.Unmarshal([]byte(vec), &c.Vec)
		idx.Chunks = append(idx.Chunks, c)
	}
	if err := rows.Err(); err != nil || len(idx.Chunks) == 0 {
		return Index{}, false
	}
	if raw, ok := db.KVGet(conn, "rag/built/"+key); ok {
		_ = json.Unmarshal([]byte(raw), &idx.BuiltAt)
	}
	return idx, true
}

func loadIndexFile(workdir string) (Index, bool) {
	data, err := os.ReadFile(indexPath(workdir))
	if err != nil {
		return Index{}, false
	}
	var idx Index
	if err := json.Unmarshal(data, &idx); err != nil {
		return Index{}, false
	}
	return idx, len(idx.Chunks) > 0
}

// Query returns top-K chunks by cosine similarity.
func Query(idx Index, qvec []float64, k int) []Chunk {
	if k <= 0 {
		k = topK
	}
	type scored struct {
		c Chunk
		s float64
	}
	var ss []scored
	for _, c := range idx.Chunks {
		if len(c.Vec) == 0 {
			continue
		}
		ss = append(ss, scored{c, embed.Cosine(qvec, c.Vec)})
	}
	sort.Slice(ss, func(i, j int) bool { return ss[i].s > ss[j].s })
	var out []Chunk
	for i := 0; i < len(ss) && i < k; i++ {
		out = append(out, ss[i].c)
	}
	return out
}

func FormatContext(chunks []Chunk) string {
	if len(chunks) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Relevant repo context (local RAG):\n")
	for _, c := range chunks {
		fmt.Fprintf(&b, "\n--- %s ---\n%s\n", c.Path, c.Text)
	}
	return b.String()
}
