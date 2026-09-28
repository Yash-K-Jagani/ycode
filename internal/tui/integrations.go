package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	yctx "github.com/Yash-K-Jagani/ycode/internal/context"
	"github.com/Yash-K-Jagani/ycode/internal/providers/registry"
	"github.com/Yash-K-Jagani/ycode/internal/rag"
	"github.com/Yash-K-Jagani/ycode/internal/store"
	"github.com/Yash-K-Jagani/ycode/internal/tools"
	"github.com/Yash-K-Jagani/ycode/pkg/apitypes"
	tea "github.com/charmbracelet/bubbletea"
)

// reviewCmd streams a single-shot review of diff through the model.
func (m *Model) reviewCmd(diff string) tea.Cmd {
	m.busy = true
	m.stream.Reset()
	m.cancelled = false
	turnCtx, turnCancel := context.WithCancel(context.Background())
	m.turnCancel = turnCancel
	prog := m.prog
	prompt := "You are a strict code reviewer (" + m.agent.Name + " perspective: " + m.agent.Prompt + "). " +
		"Review the unified diff below. Output: summary, then per-file findings with file:line, then concrete fixes. Be concise.\n\n```diff\n" + diff + "\n```"
	hist := []apitypes.Message{{Role: apitypes.RoleUser, Content: prompt}}
	return func() tea.Msg {
		w := progWriter{send: func(s string) {
			if prog != nil {
				prog.Send(deltaMsg(s))
			}
		}}
		full, err := m.router.Stream(turnCtx, hist, w)
		if err != nil {
			return errMsg{err}
		}
		pt := yctx.Estimate(hist)
		ct := len(full) / 4
		usd := m.tracker.Add(m.cfg.ActiveProvider, pt, ct)
		return doneMsg{text: "## Code review\n\n" + full, ptok: pt, ctok: ct, usd: usd, ctx: pt, ctxB: yctx.BudgetFor("")}
	}
}

// reviewPostCmd streams a review constrained to JSON findings for posting.
func (m *Model) reviewPostCmd(diff, repo string, pr int) tea.Cmd {
	m.busy = true
	m.stream.Reset()
	m.cancelled = false
	turnCtx, turnCancel := context.WithCancel(context.Background())
	m.turnCancel = turnCancel
	prog := m.prog
	prompt := "You are a strict code reviewer. Review the unified diff below. " +
		"Output ONLY raw JSON, no fences, no prose: " +
		`{"summary": "2-3 sentence verdict", "comments": [{"path": "file", "line": N, "body": "finding + fix"}]}. ` +
		"line = NEW-file line number of the finding. Empty comments array when clean.\n\n```diff\n" + diff + "\n```"
	hist := []apitypes.Message{{Role: apitypes.RoleUser, Content: prompt}}
	return func() tea.Msg {
		w := progWriter{send: func(s string) {
			if prog != nil {
				prog.Send(deltaMsg(s))
			}
		}}
		full, err := m.router.Stream(turnCtx, hist, w)
		if err != nil {
			return errMsg{err}
		}
		return reviewPostDone{text: full, repo: repo, pr: pr}
	}
}

func selectModelSilent(m *Model, model string) string { return applyModel(m, "ollama", model) }

// postReview parses model JSON findings and posts them as a PR review.
func (m *Model) postReview(text, repo string, pr int) string {
	clean := strings.TrimSpace(text)
	clean = strings.TrimPrefix(clean, "```json")
	clean = strings.TrimPrefix(clean, "```")
	clean = strings.TrimSuffix(clean, "```")
	var findings struct {
		Summary  string `json:"summary"`
		Comments []struct {
			Path string `json:"path"`
			Line int    `json:"line"`
			Body string `json:"body"`
		} `json:"comments"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(clean)), &findings); err != nil {
		return "could not parse review JSON: " + err.Error()
	}
	comments := make([]map[string]any, 0, len(findings.Comments))
	for _, c := range findings.Comments {
		comments = append(comments, map[string]any{"path": c.Path, "line": c.Line, "body": c.Body})
	}
	raw, _ := json.Marshal(map[string]any{
		"action": "review_post", "repo": repo, "number": pr,
		"review": map[string]any{"summary": findings.Summary, "comments": comments},
	})
	out, err := (&tools.GitHubTool{Workdir: m.workdir}).Run(context.Background(), raw)
	if err != nil {
		return "post failed: " + err.Error()
	}
	return out
}

func storeList(items []store.Entry) string {
	if len(items) == 0 {
		return "Store is empty."
	}
	var b strings.Builder
	for _, e := range items {
		fmt.Fprintf(&b, "- %s [%s] %s\n  %s\n", e.Name, e.Kind, displaySource(e), e.Description)
	}
	b.WriteString("/store install <name> · /store search <q> · /store update")
	return b.String()
}

func displaySource(e store.Entry) string {
	s := e.Source
	if e.Subdir != "" {
		s += "#" + e.Subdir
	}
	if e.Ref != "" {
		s += "@" + e.Ref
	}
	return s
}

// storeUpdateCmd refreshes the cached index in the background.
func (m *Model) storeUpdateCmd(url string) tea.Cmd {
	return func() tea.Msg {
		idx, err := store.Update(url)
		if err != nil {
			return sysMsg("store update failed: " + err.Error())
		}
		return sysMsg(fmt.Sprintf("store: %d entries", len(idx.Items)))
	}
}

// storeInstallCmd installs a store entry in the background.
func (m *Model) storeInstallCmd(e store.Entry) tea.Cmd {
	return func() tea.Msg {
		name, err := store.Install(e, m.skillMgr, m.pluginLoader)
		return storeInstalledMsg{kind: e.Kind, name: name, err: err}
	}
}

// modelsPullCmd pulls an Ollama model in the background.
func modelsPullCmd(model string) tea.Cmd {
	return func() tea.Msg {
		out, err := registry.Pull(context.Background(), model)
		if err != nil {
			return sysMsg("pull failed: " + err.Error() + "\n" + out)
		}
		return sysMsg("Installed " + model + " — switch with /models " + model)
	}
}

func modelsImportCmd(workdir, path, name string) tea.Cmd {
	return func() tea.Msg {
		if !filepath.IsAbs(path) {
			path = filepath.Join(workdir, path)
		}
		out, err := registry.ImportGGUF(context.Background(), path, name)
		if err != nil {
			return sysMsg("import failed: " + err.Error() + "\n" + out)
		}
		return sysMsg(out + "\nSwitch with /models " + name)
	}
}

func restOrEmpty(f []string, i int) string {
	if i < len(f) {
		return strings.Join(f[i:], " ")
	}
	return ""
}

// ragIngestCmd builds the repo vector index in the background.
func (m *Model) ragIngestCmd(root string) tea.Cmd {
	if root == "" {
		root = m.workdir
	}
	return func() tea.Msg {
		idx, err := rag.Ingest(context.Background(), m.workdir, root, m.embedder.Embed)
		if err != nil {
			return sysMsg("rag ingest failed: " + err.Error())
		}
		m.ragIdx = &idx
		m.ragOff = false
		return sysMsg(fmt.Sprintf("rag: indexed %d chunks from %s", len(idx.Chunks), root))
	}
}

// filterNetworkTools drops every tool that can reach the network, in
// Zero-Data-Leak mode. Derived from tools.ReachesNetwork so this list and the
// loop's central check cannot drift apart — it previously hardcoded exactly two
// names while nine more could reach the network.
func filterNetworkTools(names []string) []string {
	var out []string
	for _, n := range names {
		if tools.ReachesNetwork(n) {
			continue
		}
		out = append(out, n)
	}
	return out
}
