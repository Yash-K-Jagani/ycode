package tools

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A child that exits while leaving its stdout open - the background-grandchild
// case - used to be guarded by a bounded wait on the drain goroutines. That guard
// was removed when the pipes became ours, so this pins down what replaced it:
// cmd.Wait waits for Go's copy goroutines, and WaitDelay bounds them, so a
// command whose fds outlive it must still return.
//
// A hang here is the worst failure this file can have, per its own note: the UI
// shows nothing and the user cannot tell it from a wedged command.
func TestExecStreamReturnsWhenChildLeavesPipeOpen(t *testing.T) {
	var cmd = shellCmd(grandchildScript())
	if cmd == nil {
		t.Skip("no shell")
	}
	done := make(chan streamResult, 1)
	start := time.Now()
	go func() { done <- execStream(context.Background(), cmd, nil, maxOutBytes) }()

	select {
	case res := <-done:
		elapsed := time.Since(start)
		if elapsed > 15*time.Second {
			t.Fatalf("took %v; the command should have returned around WaitDelay", elapsed)
		}
		// Whatever it printed before spawning the background job is kept.
		if !strings.Contains(string(res.out), "parent") {
			t.Logf("no parent output retained (%q); acceptable if the copy was cut short", res.out)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("execStream hung waiting for a pipe the child left open")
	}
}
func grandchildScript() string {
	if isWindows() {
		// -NoNewWindow makes the child inherit this process's stdout, so the
		// handle outlives the parent exactly as a background grandchild would.
		return `$p = Start-Process powershell -ArgumentList '-NoProfile','-NonInteractive','-Command','Start-Sleep 20' -NoNewWindow -PassThru; [Console]::Out.Write('parent'); exit 0`
	}
	return `sleep 20 & printf 'parent'; exit 0`
}
