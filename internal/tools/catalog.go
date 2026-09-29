package tools

import "time"

// The tool catalog.
//
// Registration, network capability and call budget used to be three separate
// hand-maintained lists: the r.Add sequence in DefaultRegistry, the
// networkTools map, and the toolTimeouts map. A tool added to the first and
// forgotten in the second was registered but unclassified, which is how the
// zero-data-leak promise became false — api, db, notebook and scaffold all
// reached the network while the banner claimed local only.
//
// They are one table now. A built-in tool cannot be registered without
// declaring whether it reaches the network and how long it may run, so the
// omission that caused the bug is no longer expressible.
//
// What this is not: a sandbox. bash and run execute arbitrary commands, so a
// `bash curl ...` still leaves the machine. Blocking every tool that can open
// a socket would mean blocking all real work, so the direct channels are
// closed, the residual risk is declared here in the same row, and
// docs/security.md and the UI banner say so.
type entry struct {
	name  string
	build func(workdir string) Tool
	// network is true when the tool can send data off the machine, which
	// zero-data-leak mode refuses.
	network bool
	// timeout is how long one call may run. A tool whose own internal
	// deadline is shorter still wins, because it derives from the same context.
	timeout time.Duration
	// concurrent is true when the tool only reads: it mutates no local state,
	// so several such calls may be in flight at once without them interfering.
	// The agent loop uses this to fan out the read-only calls in one assistant
	// message instead of serialising them, which is the common shape of a
	// "read these three files" turn.
	//
	// The bar is deliberately strict. A tool that reads *most* of the time but
	// can write on some code path - git commit, github issue_create, api POST,
	// db write, todo add, memory save, models import, notebook execute, and
	// every code-execution tool - is NOT concurrent, because the classification
	// has to hold for every call the model might make, including the one it
	// invents mid-turn.
	concurrent bool
	// isolatable is true when the tool is safe to hand to a subagent. A
	// subagent runs with a narrow allow-list and returns only a summary, so a
	// tool that writes the user's files or talks to a network is not one to
	// delegate without the user watching.
	isolatable bool
	// note records a judgement call worth explaining at the point of decision.
	note string
}

const (
	quick  = 30 * time.Second
	normal = 60 * time.Second
	slow   = 3 * time.Minute
	// external is the budget for MCP and plugin tools, which are arbitrary
	// shell commands whose runtime nobody here controls.
	external = 5 * time.Minute
)

var catalog = []entry{
	// Pure readers: no local mutation on any code path, so they are safe to
	// run concurrently and safe to delegate to a subagent.
	{name: "read", build: func(string) Tool { return &ReadTool{} }, timeout: quick, concurrent: true, isolatable: true},
	{name: "changes", build: func(w string) Tool { return &ChangesTool{Workdir: w} }, timeout: quick, concurrent: true, isolatable: true},
	{name: "grep", build: func(string) Tool { return &GrepTool{} }, timeout: quick, concurrent: true, isolatable: true},
	{name: "glob", build: func(string) Tool { return &GlobTool{} }, timeout: quick, concurrent: true, isolatable: true},
	{name: "tree", build: func(string) Tool { return &TreeTool{} }, timeout: quick, concurrent: true, isolatable: true},
	{name: "summary", build: func(string) Tool { return &SummaryTool{} }, timeout: quick, concurrent: true, isolatable: true},
	{name: "security", build: func(w string) Tool { return &SecurityTool{Workdir: w} }, timeout: normal, concurrent: true, isolatable: true},
	// A fetch, so it reaches the network, but it writes nothing local and the
	// url allowlist is the same one every call goes through.
	{name: "browser", build: func(string) Tool { return &BrowserTool{} }, network: true, timeout: quick,
		concurrent: true, isolatable: true, note: "fetches arbitrary URLs"},

	// Read-mostly tools. Each has at least one action that writes (a file, a
	// queue, a subprocess), so they stay sequential. A tool that is parallel
	// safe on some arguments and not others cannot be classified, because the
	// model chooses the arguments.
	{name: "todo", build: func(w string) Tool { return &TodoTool{Workdir: w} }, timeout: quick, isolatable: true,
		note: "list is read-only but add/done write todos.json"},
	{name: "memory", build: func(string) Tool { return &MemoryTool{} }, timeout: quick, isolatable: true,
		note: "recall is read-only but save rewrites memory.json"},
	{name: "models", build: func(w string) Tool { return &ModelsTool{Workdir: w} }, timeout: normal, isolatable: true,
		note: "info is read-only but import writes to Ollama"},

	// Writing. Confined to the workdir and blocked in read-only modes by the
	// tools themselves; see containPath.
	{name: "write", build: func(w string) Tool { return &WriteTool{Workdir: w} }, timeout: quick},
	{name: "create", build: func(w string) Tool { return &CreateTool{Workdir: w} }, timeout: quick},
	{name: "add", build: func(w string) Tool { return &AddTool{Workdir: w} }, timeout: quick},
	{name: "edit", build: func(w string) Tool { return &EditTool{Workdir: w} }, timeout: quick},
	{name: "remove", build: func(w string) Tool { return &RemoveTool{Workdir: w} }, timeout: quick},
	{name: "delete", build: func(w string) Tool { return &DeleteTool{Workdir: w} }, timeout: quick},
	{name: "patch", build: func(w string) Tool { return &PatchTool{Workdir: w} }, timeout: normal},

	// Reaches the network, and several can write to the world.
	{name: "api", build: func(string) Tool { return &APITool{} }, network: true, timeout: quick,
		note: "arbitrary HTTP methods, headers and bodies"},
	{name: "github", build: func(w string) Tool { return &GitHubTool{Workdir: w} }, network: true, timeout: external,
		note: "GitHub API; clone is slow on a cold cache"},
	{name: "git", build: func(w string) Tool { return &GitTool{Workdir: w} }, network: true, timeout: normal,
		note: "fetch and push; commit/checkout mutate the working tree"},
	{name: "db", build: func(string) Tool { return &DBTool{} }, network: true, timeout: quick,
		note: "postgres/mysql/mongo DSNs can name remote hosts"},
	{name: "vscode", build: func(w string) Tool { return &VSCodeTool{Workdir: w} }, network: true, timeout: quick,
		note: "shells out to the editor, which may fetch extensions"},

	// Executes code. Declared local because the direct channel is not the
	// network, but a curl in the code is: this is the residual risk that keeps
	// zero-data-leak a guardrail rather than a sandbox.
	{name: "bash", build: func(w string) Tool { return NewBashTool(w) }, timeout: normal,
		note: "executes arbitrary commands: documented residual risk"},
	{name: "run", build: func(w string) Tool { return &RunTool{Workdir: w} }, timeout: slow,
		note: "executes arbitrary code: documented residual risk"},
	{name: "testgen", build: func(w string) Tool { return &TestGenTool{Workdir: w} }, timeout: external,
		note: "runs the project's own suite, which may be slow to build"},
	{name: "notebook", build: func(w string) Tool { return &NotebookTool{Workdir: w} }, network: true, timeout: external,
		note: "executes arbitrary code and may fetch data"},
	{name: "scaffold", build: func(w string) Tool { return &ScaffoldTool{Workdir: w} }, network: true, timeout: 10 * time.Minute,
		note: "runs npm/pip against remote registries"},
}

var catalogByName = func() map[string]*entry {
	m := make(map[string]*entry, len(catalog))
	for i := range catalog {
		m[catalog[i].name] = &catalog[i]
	}
	return m
}()

// entryFor returns the catalog row for a built-in tool, or nil.
func entryFor(name string) *entry { return catalogByName[name] }

// ToolNames lists the built-in tools in catalog order.
func ToolNames() []string {
	out := make([]string, len(catalog))
	for i, e := range catalog {
		out[i] = e.name
	}
	return out
}
