package keys

import (
	"fmt"

	"github.com/zalando/go-keyring"
)

const service = "ycode"

// Set stores a secret in the OS keyring (Credential Manager/Keychain/Secret Service).
func Set(account, secret string) error {
	if account == "" {
		return fmt.Errorf("account required")
	}
	return keyring.Set(service, account, secret)
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
