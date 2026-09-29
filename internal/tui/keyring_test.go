package tui

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Yash-K-Jagani/ycode/internal/keys"
)

// Saving a credential runs an external program, and on macOS that program
// blocks on an interactive keychain prompt. applyConnect runs on the UI
// thread, so an unbounded keyring write means a TUI that stops drawing - and a
// test runner, which can never answer the prompt, hangs until the suite
// times out. That is not hypothetical: it is what a CI run did.

func TestApplyConnectNeverBlocksOnTheKeyring(t *testing.T) {
	m := realModel(t)
	// A keyring that never answers, which is what an unanswered prompt looks
	// like from here. The block is released in cleanup, and the goroutine is
	// waited for, so nothing outlives the test and races the seam's restore.
	release := make(chan struct{})
	var finished sync.WaitGroup
	finished.Add(1)
	restore := keys.SetForTest(func(service, user, password string) error {
		defer finished.Done()
		<-release
		return nil
	})
	t.Cleanup(func() {
		close(release)
		finished.Wait()
		restore()
	})

	res := connectResult{
		Provider: "gemini", Model: "gemini-2.5-pro",
		Key: "gemini-secret", KeyEnv: "GEMINI_API_KEY", SaveKey: true,
	}
	done := make(chan string, 1)
	go func() { done <- m.applyConnect(res) }()

	select {
	case out := <-done:
		// The session key is set regardless, so a timeout costs nothing that
		// was not already applied, and the message must say how to persist it.
		if !strings.Contains(out, "persist with") {
			t.Fatalf("a keyring timeout should say how to persist the key: %q", out)
		}
		if m.cfg.KeyFor("gemini") != "gemini-secret" {
			t.Fatal("the session key should still be set after a keyring timeout")
		}
	case <-time.After(keyringTimeout + 10*time.Second):
		t.Fatal("applyConnect blocked on the keyring")
	}
}

func TestApplyConnectReportsKeyringOutcomes(t *testing.T) {
	for name, tc := range map[string]struct {
		keyringErr error
		want       string
	}{
		"saved":  {nil, "saved to OS keyring"},
		"failed": {errors.New("access denied"), "keyring save failed"},
	} {
		t.Run(name, func(t *testing.T) {
			restore := keys.SetForTest(func(service, user, password string) error {
				return tc.keyringErr
			})
			defer restore()
			m := realModel(t)
			out := m.applyConnect(connectResult{
				Provider: "groq", Model: "llama-3.3-70b-versatile",
				Key: "groq-secret", KeyEnv: "GROQ_API_KEY", SaveKey: true,
			})
			if !strings.Contains(out, tc.want) {
				t.Fatalf("applyConnect = %q, want it to mention %q", out, tc.want)
			}
			// Whatever happened, the key must not be echoed.
			if strings.Contains(out, "groq-secret") {
				t.Fatalf("applyConnect leaked the key: %q", out)
			}
		})
	}
}

// Declining the keyring must not touch it at all.
func TestDecliningTheKeyringDoesNotWriteToIt(t *testing.T) {
	writes := 0
	restore := keys.SetForTest(func(service, user, password string) error {
		writes++
		return nil
	})
	defer restore()

	m := realModel(t)
	out := m.applyConnect(connectResult{
		Provider: "gemini", Model: "gemini-2.5-pro",
		Key: "gemini-secret", KeyEnv: "GEMINI_API_KEY", SaveKey: false,
	})
	if writes != 0 {
		t.Fatalf("declining the keyring still wrote to it %d times", writes)
	}
	if !strings.Contains(out, "persist with") {
		t.Fatalf("declining should say how to persist the key: %q", out)
	}
	// The session key is still active, which is the point of declining.
	if m.cfg.KeyFor("gemini") != "gemini-secret" {
		t.Fatal("the session key was not set")
	}
}

func TestKeyringTimeoutIsSane(t *testing.T) {
	if keyringTimeout <= 0 || keyringTimeout > 30*time.Second {
		t.Fatalf("keyringTimeout = %s, which either never fires or looks hung", keyringTimeout)
	}
}
