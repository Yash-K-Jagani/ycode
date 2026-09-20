package rag

import (
	"context"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Yash-K-Jagani/ycode/internal/config"
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
	h := sha1.Sum([]byte(workdir))
	return filepath.Join(config.Dir(), "rag", fmt.Sprintf("%x.json", h[:8]))
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
	".go": true, ".js": true, ".ts": true, ".tsx": true, ".jsx": true, ".py": true,
	".rs": true, ".java": true, ".c": true, ".h": true, ".cpp": true, ".cs": true,
	".md": true, ".txt": true, ".yaml": true, ".yml": true, ".json": true, ".toml": true,
	".html": true, ".css": true, ".sh": true, ".ps1": true, ".sql": true, ".rb": true, ".php": true,
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

func Load(workdir string) (Index, bool) {
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
