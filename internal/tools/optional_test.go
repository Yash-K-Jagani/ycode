package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- test doubles ---

// shellCmdCtx builds a command for the host platform. The context is wired in,
// as the real tools do, so that cancelling it actually kills the process -
// without that, a timeout test measures the sleep and not the timeout.
func shellCmdCtx(ctx context.Context, script string) *exec.Cmd {
	if isWindows() {
		return exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	}
	return exec.CommandContext(ctx, "sh", "-c", script)
}

func shellCmd(script string) *exec.Cmd {
	return shellCmdCtx(context.Background(), script)
}

// plainTool is a tool with no optional capabilities: the 30-tool default.
type plainTool struct {
	name string
	run  func(ctx context.Context, args json.RawMessage) (string, error)
}

func (p plainTool) Name() string        { return p.name }
func (p plainTool) Description() string { return "plain" }
func (p plainTool) Schema() string      { return `{"type":"object"}` }
func (p plainTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	return p.run(ctx, args)
}

// streamTool additionally implements Streamer.
type streamTool struct{ plainTool }

func (s streamTool) Stream(ctx context.Context, args json.RawMessage, emit func(string)) (string, error) {
	for _, chunk := range []string{"one ", "two ", "three"} {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		// A nil emit is legal and means "no progress reporting".
		if emit != nil {
			emit(chunk)
		}
		time.Sleep(time.Millisecond)
	}
	return s.run(ctx, args)
}

// isoTool additionally implements Isolatable.
type isoTool struct{ plainTool }

func (i isoTool) Isolatable() bool { return true }

func echoTool(name string) plainTool {
	return plainTool{name: name, run: func(_ context.Context, _ json.RawMessage) (string, error) {
		return "ran " + name, nil
	}}
}

// --- Streamer discovery ---

func TestIsStreamer(t *testing.T) {
	if IsStreamer(plainTool{name: "x"}) {
		t.Error("a plain tool must not report as a streamer")
	}
	if !IsStreamer(streamTool{plainTool: echoTool("x")}) {
		t.Error("a tool with Stream must report as a streamer")
	}
	if IsStreamer(nil) {
		t.Error("nil is not a streamer")
	}
}

// A nil sink is the common case (headless, the API) and must not allocate a
// streaming path at all.
func TestRunWithSinkNilUsesRun(t *testing.T) {
	called := false
	tl := plainTool{name: "x", run: func(_ context.Context, _ json.RawMessage) (string, error) {
		called = true
		return "done", nil
	}}
	out, err := RunWithSink(context.Background(), tl, json.RawMessage(`{}`), nil)
	if err != nil || out != "done" || !called {
		t.Fatalf("got %q %v called=%v", out, err, called)
	}
}

// A non-streamer still has to reach the sink, or a caller that renders whatever
// arrives would show nothing at all for that tool.
func TestRunWithSinkOnPlainToolStillEmits(t *testing.T) {
	var got []string
	tl := echoTool("plain")
	out, err := RunWithSink(context.Background(), tl, json.RawMessage(`{}`), func(s string) { got = append(got, s) })
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if out != "ran plain" {
		t.Fatalf("out = %q", out)
	}
	if len(got) != 1 || got[0] != "ran plain" {
		t.Fatalf("sink got %v", got)
	}
}

func TestRunWithSinkStreams(t *testing.T) {
	var got []string
	st := streamTool{plainTool: echoTool("streamy")}
	out, err := RunWithSink(context.Background(), st, json.RawMessage(`{}`), func(s string) { got = append(got, s) })
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if out != "ran streamy" {
		t.Fatalf("final output = %q", out)
	}
	if len(got) != 3 || got[0] != "one " || got[2] != "three" {
		t.Fatalf("incremental output = %v", got)
	}
}

func TestRunAndStreamAgree(t *testing.T) {
	// The contract is that a Streamer's final result is what Run would have
	// returned, so a caller that ignores the sink is unaffected.
	st := streamTool{plainTool: echoTool("agree")}
	args := json.RawMessage(`{}`)
	fromRun, errRun := st.Run(context.Background(), args)
	fromStream, errStream := st.Stream(context.Background(), args, nil)
	if fromRun != fromStream || (errRun == nil) != (errStream == nil) {
		t.Fatalf("Run=%q/%v Stream=%q/%v", fromRun, errRun, fromStream, errStream)
	}
}

func TestCanStream(t *testing.T) {
	reg := NewRegistry()
	reg.Add(echoTool("plain"))
	reg.Add(streamTool{plainTool: echoTool("streamy")})
	if CanStream(reg, "plain") {
		t.Error("plain should not stream")
	}
	if !CanStream(reg, "streamy") {
		t.Error("streamy should stream")
	}
	if CanStream(reg, "nope") {
		t.Error("unknown tool should not stream")
	}
}

// --- catalog classification ---

// The classification is only useful if the read-only tools are in it. This is
// the "read these files" turn, and if any of these were excluded the agent loop
// would serialise the common case and the feature would never fire.
func TestReadersAreConcurrent(t *testing.T) {
	for _, name := range []string{"read", "grep", "glob", "tree", "summary", "changes", "security", "browser"} {
		if !IsConcurrent(name) {
			t.Errorf("%q should be concurrent", name)
		}
	}
}

// The stricter half: every tool that can write anything, on any code path, must
// stay serial. A tool that is safe on most arguments is not safe to classify,
// because the model picks the arguments.
func TestWritersAreNotConcurrent(t *testing.T) {
	for _, name := range []string{
		"write", "create", "add", "edit", "remove", "delete", "patch",
		"git", "github", "api", "db", "todo", "memory", "models", "notebook",
		"bash", "run", "testgen", "scaffold", "vscode",
	} {
		if IsConcurrent(name) {
			t.Errorf("%q mutates state and must not be concurrent", name)
		}
	}
}

func TestUnknownToolIsNotConcurrent(t *testing.T) {
	// MCP and plugin tools are arbitrary programs this package has never seen.
	// Serialising them is the safe default.
	for _, name := range []string{"mcp__srv__tool", "plugin__thing", "made_up"} {
		if IsConcurrent(name) {
			t.Errorf("%q should not be treated as concurrent", name)
		}
	}
}

func TestIsIsolatableBuiltins(t *testing.T) {
	for _, name := range []string{"read", "grep", "glob", "tree", "summary", "changes", "security", "todo", "memory", "models", "browser"} {
		if !IsIsolatable(name, nil) {
			t.Errorf("%q should be delegable to a subagent", name)
		}
	}
	// A subagent must not be able to rewrite the user's files or push to a
	// remote without anyone watching.
	for _, name := range []string{"write", "edit", "delete", "remove", "create", "add", "patch",
		"bash", "run", "git", "github", "api", "db", "scaffold", "vscode", "notebook", "testgen"} {
		if IsIsolatable(name, nil) {
			t.Errorf("%q must not be delegable to a subagent", name)
		}
	}
}

// An external tool can opt in by answering for itself.
func TestIsIsolatableExternalOptIn(t *testing.T) {
	if IsIsolatable("plugin__x", plainTool{name: "plugin__x"}) {
		t.Error("a plain external tool should not be delegable")
	}
	if !IsIsolatable("plugin__x", isoTool{plainTool: plainTool{name: "plugin__x"}}) {
		t.Error("an external tool that says Isolatable should be delegable")
	}
}

func TestPartition(t *testing.T) {
	concurrent, sequential := Partition(nil, []string{"read", "write", "grep", "edit", "tree"})
	wantC := []string{"read", "grep", "tree"}
	wantS := []string{"write", "edit"}
	if !equal(concurrent, wantC) {
		t.Errorf("concurrent = %v, want %v", concurrent, wantC)
	}
	if !equal(sequential, wantS) {
		t.Errorf("sequential = %v, want %v", sequential, wantS)
	}
}

func TestPartitionEmpty(t *testing.T) {
	c, s := Partition(nil, nil)
	if len(c) != 0 || len(s) != 0 {
		t.Fatalf("got %v %v", c, s)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// --- streaming subprocess ---

// A real command, because the point of the change is that output appears while
// the process is still running.
//
// This used to assert the command took at least 400ms, which measured the OS's
// sleep rather than anything about ycode - CI failed it at 356ms, on a runner
// whose `sleep 0.4` simply returned early. The assertion was also redundant:
// execStream cannot have delivered "b" without having run the command at all.
//
// What it should ask is whether output arrives *while* the process is alive, and
// that can be arranged rather than timed. The command writes "a" and then waits
// for a file to appear before writing "b"; the sink creates that file the moment
// it sees "a". If output were buffered until exit, the command would wait for a
// file nothing creates, and the test times out with a message that says so - no
// dependency on how long any sleep actually slept.
func TestExecStreamEmitsBeforeFinish(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "release")
	var cmd *exec.Cmd
	if isWindows() {
		cmd = shellCmd(fmt.Sprintf(
			`[Console]::Out.Write('a'); while (-not (Test-Path '%s')) { Start-Sleep -Milliseconds 10 }; [Console]::Out.Write('b')`,
			marker))
	} else {
		cmd = shellCmd(fmt.Sprintf(
			`printf 'a'; while [ ! -f '%s' ]; do sleep 0.01; done; printf 'b'`, marker))
	}

	var mu sync.Mutex
	var chunks []string
	released := false
	release := func() {
		mu.Lock()
		defer mu.Unlock()
		if released {
			return
		}
		released = true
		_ = os.WriteFile(marker, nil, 0o644)
	}
	// If the assertion below ever fails, the command is left spinning on the
	// marker; release it so the test does not leak a busy process.
	t.Cleanup(release)

	done := make(chan streamResult, 1)
	go func() {
		done <- execStream(context.Background(), cmd, func(s string) {
			mu.Lock()
			chunks = append(chunks, s)
			mu.Unlock()
			if strings.Contains(s, "a") {
				release()
			}
		}, maxOutBytes)
	}()

	select {
	case res := <-done:
		if res.err != nil {
			t.Fatalf("err: %v", res.err)
		}
		mu.Lock()
		n := len(chunks)
		got := append([]string(nil), chunks...)
		mu.Unlock()
		if n < 2 {
			t.Fatalf("expected incremental chunks, got %d (%q)", n, got)
		}
		if string(res.out) != "ab" {
			t.Fatalf("combined output = %q, want %q", res.out, "ab")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("command never finished: it was waiting for output that only arrived at exit, " +
			"so the sink was never called while the process was running")
	}
}

func TestExecStreamCapturesStderr(t *testing.T) {
	cmd := shellCmd(shellScript("out", "err"))
	res := execStream(context.Background(), cmd, nil, maxOutBytes)
	// Without this the assertions below are satisfiable by a script that failed:
	// the error text contains the words being looked for. That is how this test
	// spent a long time passing on Windows while executing nothing.
	if res.err != nil {
		t.Fatalf("the script did not run: %v", res.err)
	}
	joined := string(res.out)
	if !strings.Contains(joined, "out") || !strings.Contains(joined, "err") {
		t.Fatalf("stdout and stderr both belong in the combined output, got %q", joined)
	}
}

func TestExecStreamCapturesExitError(t *testing.T) {
	cmd := shellCmd(`printf "before"; exit 3`)
	res := execStream(context.Background(), cmd, nil, maxOutBytes)
	if res.err == nil {
		t.Fatal("expected a non-zero exit to be reported")
	}
	if !strings.Contains(string(res.out), "before") {
		t.Fatalf("output before the failure was lost: %q", res.out)
	}
}

func TestExecStreamTruncatesRetainedOutput(t *testing.T) {
	// A runaway command must not be able to put megabytes into the model's
	// context, so retention is capped even though live output is not.
	cmd := shellCmd(`head -c 200000 /dev/zero | tr "\0" "x"`)
	res := execStream(context.Background(), cmd, nil, 1024)
	if len(res.out) > 1024 {
		t.Fatalf("retained %d bytes, want <= 1024", len(res.out))
	}
	if !strings.Contains(string(res.out), truncMarker) {
		t.Fatalf("truncation was silent: %q", res.out[:40])
	}
	if !strings.HasPrefix(string(res.out), truncMarker) {
		t.Fatalf("marker should lead, since the front is what was dropped: %q", res.out[:40])
	}
}

// The tail is what a reader needs. A command that prints a megabyte of build
// noise and then fails puts the failure at the end, so dropping the tail throws
// away the only line that says what happened.
//
// This is the case that used to break: appendChunk stopped accepting output
// once the buffer was full, so the sentinel below was silently dropped and the
// result was indistinguishable from a command that had produced nothing useful.
func TestExecStreamKeepsTailNotHead(t *testing.T) {
	const sentinel = "BUILD FAILED: undefined: symbol foo"
	cmd := shellCmd(`head -c 200000 /dev/zero | tr "\0" "n"; printf '%s' '` + sentinel + `'`)
	res := execStream(context.Background(), cmd, nil, 1024)
	if got := string(res.out); !strings.Contains(got, sentinel) {
		t.Fatalf("final output lost; got tail %q", tail(res.out, 60))
	}
	if len(res.out) > 1024 {
		t.Fatalf("retained %d bytes, want <= 1024", len(res.out))
	}
}

func tail(b []byte, n int) string {
	if len(b) > n {
		return string(b[len(b)-n:])
	}
	return string(b)
}

func TestExecStreamDetectsTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	cmd := shellCmdCtx(ctx, `sleep 5`)
	start := time.Now()
	res := execStream(ctx, cmd, nil, maxOutBytes)
	elapsed := time.Since(start)
	if !res.timedOut {
		t.Fatal("expected the deadline to be reported as a timeout")
	}
	// Generous, but well under the 5s the command asked for: the point is that
	// the caller is released promptly rather than waiting out the sleep.
	if elapsed > 4*time.Second {
		t.Fatalf("took %v; the killed process still held the turn", elapsed)
	}
}

// The two pipes are drained on separate goroutines. If they were drained
// sequentially, a command writing a lot to stderr while stdout stays open would
// block forever - which is the classic way a streaming executor deadlocks.
func TestExecStreamDoesNotDeadlockOnLargeStderr(t *testing.T) {
	// Far more than a pipe buffer (64KB on Linux), so a sequential drain would
	// block on the stderr write until the reader got there.
	//
	// This used to be a POSIX-only script with 1>&2, which PowerShell cannot
	// parse - so on Windows it ran nothing, and the test still passed because
	// the ParserError quotes "printf done". It passed locally for years while
	// CI failed on it every run. The flood now goes through floodStderr, and the
	// sentinel through writeOut, so the scenario actually runs on both.
	var script strings.Builder
	floodStderr(&script, 300000)
	writeOut(&script, "done", false)
	cmd := shellCmd(script.String())
	done := make(chan streamResult, 1)
	go func() { done <- execStream(context.Background(), cmd, nil, maxOutBytes) }()
	select {
	case res := <-done:
		if res.err != nil {
			t.Fatalf("the script did not run: %v", res.err)
		}
		if !strings.Contains(string(res.out), "done") {
			t.Fatalf("stdout after the stderr flood was lost: %q", res.out[len(res.out)-20:])
		}
	case <-time.After(20 * time.Second):
		t.Fatal("execStream deadlocked draining stderr")
	}
}

func TestExecStreamSinkIsSerialised(t *testing.T) {
	// stdout and stderr both call emit. If those calls were not serialised, a
	// caller appending to a slice without a lock would race.
	//
	// Two streams on purpose: with one, the emit mutex is never contended and
	// removing it would change nothing.
	cmd := shellCmd(shellScript("aaaaaaaaaa", "bbbbbbbbbb"))
	var mu sync.Mutex
	n := 0
	res := execStream(context.Background(), cmd, func(string) {
		mu.Lock()
		n++
		mu.Unlock()
	}, maxOutBytes)
	// Both streams must actually have run, or this proves nothing about
	// contention.
	if res.err != nil {
		t.Fatalf("the script did not run: %v", res.err)
	}
	if !strings.Contains(string(res.out), "aaaaaaaaaa") || !strings.Contains(string(res.out), "bbbbbbbbbb") {
		t.Fatalf("both streams should have been drained: %q", res.out)
	}
	if n == 0 {
		t.Fatal("sink was never called")
	}
}

// shellScript builds a script that writes fixed strings to the two streams,
// using whatever syntax the platform actually has.
//
// This exists because PowerShell cannot parse `1>&2` - it raises a ParserError
// and runs nothing - while `sh -c` needs it. Three tests below were written with
// `1>&2` and therefore did nothing on Windows, while still passing: PowerShell's
// ParserError quotes the line it failed to parse, so an assertion for "out" and
// "err" was satisfied by the error message describing the script that contains
// those words.
//
// s must not contain a single quote; these are literal test strings.
func shellScript(stdout, stderr string) string {
	var b strings.Builder
	writeOut(&b, stdout, false)
	writeOut(&b, stderr, true)
	return b.String()
}

func writeOut(b *strings.Builder, s string, toStderr bool) {
	if s == "" {
		return
	}
	if isWindows() {
		// [Console]::Out.Write goes to stdout without the newline Write-Output
		// would add, and [Console]::Error.Write goes to stderr.
		if toStderr {
			fmt.Fprintf(b, `[Console]::Error.Write('%s'); `, s)
		} else {
			fmt.Fprintf(b, `[Console]::Out.Write('%s'); `, s)
		}
		return
	}
	if toStderr {
		fmt.Fprintf(b, `printf '%%s' %s 1>&2; `, s)
	} else {
		fmt.Fprintf(b, `printf '%%s' %s; `, s)
	}
}

// floodStderr appends a script writing n bytes to stderr.
//
// Same reasoning as shellScript: on Windows this cannot use head and tr, and on
// Unix it cannot use a string multiplier.
func floodStderr(b *strings.Builder, n int) {
	if isWindows() {
		fmt.Fprintf(b, `[Console]::Error.Write('x' * %d); `, n)
		return
	}
	fmt.Fprintf(b, `head -c %d /dev/zero | tr "\0" "x" 1>&2; `, n)
}

// --- bash tool integration ---

func TestBashIsAStreamer(t *testing.T) {
	if !IsStreamer(NewBashTool("")) {
		t.Fatal("bash should stream its output")
	}
}

func TestBashStreamEmitsAndMatchesRun(t *testing.T) {
	cmdText := `printf "hello "; sleep 0.3; printf "world"`
	tl := NewBashTool("")
	args := json.RawMessage(`{"command":` + quote(cmdText) + `}`)

	var mu sync.Mutex
	var got []string
	streamed, err := tl.Stream(context.Background(), args, func(s string) {
		mu.Lock()
		got = append(got, s)
		mu.Unlock()
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	plain, err := tl.Run(context.Background(), args)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if streamed != plain {
		t.Fatalf("Stream=%q Run=%q; they must agree", streamed, plain)
	}
	mu.Lock()
	n := len(got)
	mu.Unlock()
	if n < 2 {
		t.Fatalf("expected incremental output, got %d chunk(s)", n)
	}
}

func TestBashStreamPreservesDenylist(t *testing.T) {
	tl := NewBashTool("")
	_, err := tl.Stream(context.Background(), json.RawMessage(`{"command":"rm -rf /"}`), nil)
	if err == nil || !strings.Contains(err.Error(), "denylist") {
		t.Fatalf("denylist must still apply on the streaming path, got %v", err)
	}
}

func TestBashStreamRequiresCommand(t *testing.T) {
	tl := NewBashTool("")
	_, err := tl.Stream(context.Background(), json.RawMessage(`{"command":""}`), func(string) { t.Fatal("sink called") })
	if err == nil {
		t.Fatal("expected an error for an empty command")
	}
}

// The exec path uses two goroutines and several locks. `go test -race` is the
// real check, but it needs a 64-bit C toolchain and cannot run on a 32-bit
// MinGW (see the race target's note in the Makefile); CI runs it in a separate
// job. This test is the local substitute: it stresses the paths where a race
// would show up as lost, duplicated or torn output rather than as a data race
// report, which is the failure that would actually reach a user's transcript.
func TestExecStreamStressIntegrity(t *testing.T) {
	if testing.Short() {
		t.Skip("stress test")
	}
	// Each iteration writes a unique, self-delimiting token sequence to stdout
	// and a different one to stderr, then asserts that every token appears
	// exactly once. A lost write, a double-buffered chunk or a torn buffer
	// shows up as a count mismatch.
	//
	// The two streams are written with platform-appropriate syntax: PowerShell
	// has no printf(1) (its `printf` is an alias for Write-Output) and a
	// different stderr redirect, so a POSIX script silently fails there.
	const iterations = 6
	const lines = 30
	for i := 0; i < iterations; i++ {
		var script strings.Builder
		for j := 1; j <= lines; j++ {
			writeToken(&script, "o", j, false)
		}
		for j := 1; j <= lines; j++ {
			writeToken(&script, "e", j, true)
		}
		cmd := shellCmd(script.String())
		res := execStream(context.Background(), cmd, nil, maxOutBytes)
		if res.err != nil {
			t.Fatalf("iteration %d: %v (script was %d bytes)", i, res.err, script.Len())
		}
		out := string(res.out)
		for j := 1; j <= lines; j++ {
			ot, et := fmt.Sprintf("o%d ", j), fmt.Sprintf("e%d ", j)
			if n := strings.Count(out, ot); n != 1 {
				t.Fatalf("iteration %d: stdout token %q appeared %d times", i, ot, n)
			}
			if n := strings.Count(out, et); n != 1 {
				t.Fatalf("iteration %d: stderr token %q appeared %d times", i, et, n)
			}
		}
	}
}

// writeToken appends one `printf` that emits "<prefix><n> " to the given stream.
func writeToken(b *strings.Builder, prefix string, n int, toStderr bool) {
	tok := fmt.Sprintf("%s%d ", prefix, n)
	if isWindows() {
		// [Console]::Error.Write goes to stderr; Write-Output goes to stdout
		// but is a pipeline cmdlet, so it adds a newline we do not want.
		if toStderr {
			fmt.Fprintf(b, `[Console]::Error.Write('%s'); `, tok)
		} else {
			fmt.Fprintf(b, `[Console]::Out.Write('%s'); `, tok)
		}
		return
	}
	if toStderr {
		fmt.Fprintf(b, `printf '%s' 1>&2; `, tok)
	} else {
		fmt.Fprintf(b, `printf '%s'; `, tok)
	}
}

// Live emission under concurrency must not lose or duplicate anything, and the
// concatenation of what the sink saw must cover the whole command's output -
// this is the guarantee the TUI's live tool card depends on.
func TestExecStreamStressEmission(t *testing.T) {
	if testing.Short() {
		t.Skip("stress test")
	}
	const lines = 200
	var script strings.Builder
	for j := 1; j <= lines; j++ {
		fmt.Fprintf(&script, `printf "L%d."; `, j)
	}
	var mu sync.Mutex
	var seen strings.Builder
	cmd := shellCmd(script.String())
	res := execStream(context.Background(), cmd, func(s string) {
		mu.Lock()
		seen.WriteString(s)
		mu.Unlock()
	}, maxOutBytes)
	mu.Lock()
	emitted := seen.String()
	mu.Unlock()
	if res.err != nil {
		t.Fatalf("err: %v", res.err)
	}
	// The sink sees at least everything retained; a chunk boundary can split a
	// token, so compare on the concatenated form rather than token by token.
	if len(emitted) < len(res.out)-len("\n…(truncated)") {
		t.Fatalf("sink saw %d bytes but %d were retained; chunks were lost",
			len(emitted), len(res.out))
	}
	if emitted != string(res.out) && emitted+"\n…(truncated)" != string(res.out) {
		t.Fatalf("emitted output diverged from retained output")
	}
}

func TestExecStreamStressConcurrentCalls(t *testing.T) {
	// Several streaming calls at once, each with its own command, must not
	// interfere: execStream keeps no package-level state, and this is what
	// would catch it if it ever did.
	if testing.Short() {
		t.Skip("stress test")
	}
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			want := fmt.Sprintf("worker%d", idx)
			cmd := shellCmd(fmt.Sprintf(`printf "%s"`, want))
			res := execStream(context.Background(), cmd, nil, maxOutBytes)
			if string(res.out) != want {
				errs[idx] = fmt.Errorf("got %q, want %q", res.out, want)
			}
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: %v", i, err)
		}
	}
}
