package bootstrap

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"

	"sim-deck-server/internal/auth"
)

// OwnerStore contains the persistence operations required during first startup.
type OwnerStore interface {
	OwnerExists(context.Context) (bool, error)
	CreateOwner(context.Context, string, []byte) (bool, error)
}

// Config points to Docker secret files used to create the owner account.
type Config struct {
	PasswordFile          string
	RecoveryPublicKeyFile string
}

// Result reports whether bootstrap created an account and a generated password must be shown.
type Result struct {
	Created           bool
	GeneratedPassword string
}

// Owner creates the single admin account without exposing the Linux recovery private key.
func Owner(ctx context.Context, repository OwnerStore, config Config) (Result, error) {
	exists, err := repository.OwnerExists(ctx)
	if err != nil {
		return Result{}, err
	}
	if exists {
		return Result{}, nil
	}

	recoveryPublicKey, err := readRecoveryPublicKey(config.RecoveryPublicKeyFile)
	if err != nil {
		return Result{}, err
	}
	password, generatedPassword, err := readOrGeneratePassword(config.PasswordFile)
	if err != nil {
		return Result{}, err
	}
	passwordHash, err := auth.HashPassword(password)
	if err != nil {
		return Result{}, fmt.Errorf("hash bootstrap password: %w", err)
	}
	created, err := repository.CreateOwner(ctx, passwordHash, recoveryPublicKey)
	if err != nil {
		return Result{}, err
	}
	if !created {
		return Result{}, nil
	}
	return Result{Created: true, GeneratedPassword: generatedPassword}, nil
}

func readOrGeneratePassword(path string) (string, string, error) {
	if strings.TrimSpace(path) != "" {
		value, err := os.ReadFile(path)
		if err == nil {
			password := strings.TrimSpace(string(value))
			if password == "" {
				return "", "", errors.New("admin password secret is empty")
			}
			return password, "", nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", "", fmt.Errorf("read admin password secret: %w", err)
		}
	}
	password, err := auth.GenerateInitialPassword()
	if err != nil {
		return "", "", err
	}
	return password, password, nil
}

func readRecoveryPublicKey(path string) ([]byte, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("RECOVERY_PUBLIC_KEY_FILE is required for first startup")
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read recovery public key: %w", err)
	}
	publicKey, err := base64.RawStdEncoding.DecodeString(strings.TrimSpace(string(encoded)))
	if err != nil {
		return nil, fmt.Errorf("decode recovery public key: %w", err)
	}
	if len(publicKey) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("recovery public key must contain %d bytes", ed25519.PublicKeySize)
	}
	return publicKey, nil
}
