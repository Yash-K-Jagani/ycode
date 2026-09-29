package keys

import (
	"fmt"
	"sync"

	"github.com/zalando/go-keyring"
)

const service = "ycode"

// setFn is the seam tests replace. The real keyring is not something a test
// should touch: on macOS it shells out to `security`, which blocks on an
// interactive keychain prompt that a CI runner can never answer, and on a
// developer's machine it prompts too.
//
// It is guarded because the UI calls Set from a bounded background goroutine
// (see tui.saveToKeyring), so a test restoring the seam can race with a write
// still in flight. A plain var here raced under -race, which is a good
// argument for a test seam being as careful as the code it stands in for.
var (
	mu    sync.RWMutex
	setFn = keyring.Set
)

func currentSet() func(service, user, password string) error {
	mu.RLock()
	defer mu.RUnlock()
	return setFn
}

// SetForTest replaces the keyring writer and returns a function restoring it.
func SetForTest(f func(service, user, password string) error) func() {
	mu.Lock()
	prev := setFn
	setFn = f
	mu.Unlock()
	return func() {
		mu.Lock()
		setFn = prev
		mu.Unlock()
	}
}

// Set stores a secret in the OS keyring (Credential Manager/Keychain/Secret Service).
func Set(account, secret string) error {
	if account == "" {
		return fmt.Errorf("account required")
	}
	return currentSet()(service, account, secret)
}

// Get retrieves a secret; reports ok=false when absent.
func Get(account string) (string, bool) {
	secret, err := keyring.Get(service, account)
	if err != nil {
		return "", false
	}
	return secret, true
}

// Delete removes a secret (missing is not an error).
func Delete(account string) error {
	if err := keyring.Delete(service, account); err != nil {
		if _, ok := Get(account); !ok {
			return nil
		}
		return err
	}
	return nil
}
