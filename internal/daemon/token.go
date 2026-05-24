package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
)

// EnsureToken returns the bearer token at path, creating it (32 random
// bytes hex-encoded, mode 0600) when absent. Idempotent — safe to call on
// every daemon start. Any trailing whitespace (e.g. an "echo" appended
// newline) is stripped before the token is returned.
func EnsureToken(path string) (string, error) {
	if data, err := os.ReadFile(path); err == nil {
		return strings.TrimSpace(string(data)), nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("read token: %w", err)
	}
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	tok := hex.EncodeToString(buf[:])
	if err := os.WriteFile(path, []byte(tok), 0o600); err != nil {
		return "", fmt.Errorf("write token: %w", err)
	}
	return tok, nil
}
