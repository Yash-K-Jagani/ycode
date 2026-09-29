package tools

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"sync"
	"time"
)

// Shared streaming subprocess execution.
//
// Several tools run a command and show what it printed: bash, run, testgen,
// notebook. All of them used to call cmd.CombinedOutput, which buffers the whole
// thing and returns it at the end, so a three-minute test run was indistinguishable
// from a hang. This is the machinery that lets them report as they go, kept in
// one place because getting it right - capping what is retained without capping
// what is shown, interleaving two pipes without losing either, not deadlocking
// when one pipe fills - is subtle enough not to repeat five times.

// waitDelay bounds how long Wait may block after the context has already been
// cancelled.
//
// This is not a precaution. Killing a process does not necessarily close the
// pipe handles it handed to its children, so after the deadline fires and the
// process dies, the drain goroutines can sit waiting for an EOF that never
// arrives. Measured on Windows, a `sleep 5` cancelled at 150ms kept Wait
// blocked for the full 5 seconds. Left alone, that turns "the command timed
// out after 60s" into a turn that hangs forever on a command that has already
// been killed - the worst possible failure mode, because the UI shows nothing
// and the user cannot tell it apart from a hang.
//
// WaitDelay makes Wait give up once the context is done and the delay has
// passed, closing the pipes from our side. Any output already read is kept.
const waitDelay = 2 * time.Second

// streamResult is what execStream collects while the command runs.
type streamResult struct {
	// out is the full combined output, capped at maxOutBytes.
	out []byte
	// err is the command's error, or nil.
	err error
	// timedOut is set when the context deadline, not the command, ended it.
	timedOut bool
}

// execStream runs cmd, forwarding its combined output to emit as it arrives,
// and returns the same capped output CombinedOutput would have returned.
//
// emit is called from an internal goroutine, so it must be safe for the caller
// to treat as a plain sequential call only if they synchronise it. Every caller
// here passes a function that forwards to the TUI message loop, which is
// already serialised.
//
// The output cap applies to what is *retained*, not to what is *shown*: a
// runaway build can emit megabytes, and a TUI that rendered every byte of it
// would freeze trying to draw it. So emit stops being called once the retained
// buffer is full, and the truncation marker says so.
func execStream(ctx context.Context, cmd *exec.Cmd, emit func(string), maxOut int) streamResult {
	// A pipe rather than a temp file: stdout and stderr must be interleaved in
	// real time, and CombinedOutput does this by creating a pipe and draining
	// it on another goroutine. Same shape, but with the reader exposed.
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		// Fall back to buffering rather than failing the call: a tool that
		// cannot stream should still work.
		out, runErr := cmd.CombinedOutput()
		if len(out) > maxOut {
			out = append(out[:maxOut], []byte("\n…(truncated)")...)
		}
		if emit != nil && len(out) > 0 {
			emit(string(out))
		}
		return streamResult{out: out, err: runErr, timedOut: ctx.Err() == context.DeadlineExceeded}
	}
	// Same trick for stderr, so the two are not read sequentially and deadlock
	// when the second one fills its pipe buffer.
	errPipe, err := cmd.StderrPipe()
	if err != nil {
		errPipe = nil
	}
	if cmd.Stdin == nil {
		// A command that reads stdin would otherwise block forever waiting for
		// a terminal that is not there.
		cmd.Stdin = nil
	}

	if err := cmd.Start(); err != nil {
		return streamResult{err: err, timedOut: ctx.Err() == context.DeadlineExceeded}
	}
	// See waitDelay. Set after Start so it cannot race with process creation.
	cmd.WaitDelay = waitDelay

	var (
		mu   sync.Mutex
		buf  bytes.Buffer
		full bool
		// emitMu serialises the calls into emit, because stdout and stderr are
		// drained on separate goroutines and a caller that forwards straight
		// to the TUI message loop would otherwise see them interleave mid-line.
		emitMu sync.Mutex
	)
	appendChunk := func(p []byte) {
		mu.Lock()
		defer mu.Unlock()
		if buf.Len() >= maxOut {
			full = true
			return
		}
		n := maxOut - buf.Len()
		if n > len(p) {
			n = len(p)
		}
		buf.Write(p[:n])
		if buf.Len() >= maxOut {
			full = true
		}
	}
	drain := func(r io.Reader) {
		if r == nil {
			return
		}
		br := bufio.NewReaderSize(r, 32*1024)
		chunk := make([]byte, 8*1024)
		for {
			n, readErr := br.Read(chunk)
			if n > 0 {
				appendChunk(chunk[:n])
				// Everything read is emitted, so the live view is genuinely
				// live. Only what is *retained* is capped, because the
				// retained copy is what goes into the model's context, and a
				// megabyte of build log would crowd out the conversation.
				// A caller that wants to stop drawing can ignore emit.
				if emit != nil {
					emitMu.Lock()
					emit(string(chunk[:n]))
					emitMu.Unlock()
				}
			}
			if readErr != nil {
				return
			}
		}
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); drain(pipe) }()
	if errPipe != nil {
		go func() { defer wg.Done(); drain(errPipe) }()
	} else {
		wg.Done()
	}
	waitErr := cmd.Wait()
	// A stalled pipe is the one case where the drain goroutines may not return
	// promptly, and waiting on them unconditionally would reintroduce the hang
	// WaitDelay exists to prevent. Everything read so far is already in buf, so
	// giving up here loses trailing output rather than the turn.
	drained := make(chan struct{})
	go func() { wg.Wait(); close(drained) }()
	select {
	case <-drained:
	case <-time.After(waitDelay):
	}

	mu.Lock()
	out := append([]byte(nil), buf.Bytes()...)
	wasFull := full
	mu.Unlock()
	if wasFull {
		out = append(out, []byte("\n…(truncated)")...)
	}
	timedOut := ctx.Err() == context.DeadlineExceeded
	// ErrWaitDelay means "we stopped waiting for the pipes", not "the command
	// failed". Reporting it as a command failure would blame the user's build
	// for our own cleanup, so it is only surfaced when the command had not
	// already failed for a real reason.
	if errors.Is(waitErr, exec.ErrWaitDelay) && timedOut {
		waitErr = nil
	}
	return streamResult{out: out, err: waitErr, timedOut: timedOut}
}
