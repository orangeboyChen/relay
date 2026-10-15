package agent

import (
	"fmt"
	"io"
	"os"
	"strings"
)

const maxSecretBytes = 4096

// ReadSecret loads a small Docker secret without returning whitespace or file contents in errors.
func ReadSecret(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open pairing token secret: %w", err)
	}
	defer file.Close()

	buffer, err := io.ReadAll(io.LimitReader(file, maxSecretBytes+1))
	if err != nil {
		return "", fmt.Errorf("read pairing token secret: %w", err)
	}
	if len(buffer) > maxSecretBytes {
		return "", fmt.Errorf("pairing token secret exceeds %d bytes", maxSecretBytes)
	}
	secret := strings.TrimSpace(string(buffer))
	if secret == "" {
		return "", fmt.Errorf("pairing token secret is empty")
	}
	return secret, nil
}
