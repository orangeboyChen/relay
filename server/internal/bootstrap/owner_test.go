package bootstrap

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"sim-deck-server/internal/auth"
)

type fakeOwnerStore struct {
	exists            bool
	created           bool
	passwordHash      string
	recoveryPublicKey []byte
}

func (store *fakeOwnerStore) OwnerExists(context.Context) (bool, error) {
	return store.exists, nil
}

func (store *fakeOwnerStore) CreateOwner(_ context.Context, passwordHash string, recoveryPublicKey []byte) (bool, error) {
	store.created = true
	store.passwordHash = passwordHash
	store.recoveryPublicKey = append([]byte(nil), recoveryPublicKey...)
	return true, nil
}

func TestOwnerReadsDockerSecrets(t *testing.T) {
	directory := t.TempDir()
	passwordPath := filepath.Join(directory, "password")
	recoveryPath := filepath.Join(directory, "recovery-public-key")
	if err := os.WriteFile(passwordPath, []byte("a strong bootstrap password\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	publicKey := make([]byte, ed25519.PublicKeySize)
	if err := os.WriteFile(recoveryPath, []byte(base64.RawStdEncoding.EncodeToString(publicKey)), 0o600); err != nil {
		t.Fatal(err)
	}

	repository := &fakeOwnerStore{}
	result, err := Owner(context.Background(), repository, Config{PasswordFile: passwordPath, RecoveryPublicKeyFile: recoveryPath})
	if err != nil {
		t.Fatalf("Owner() error = %v", err)
	}
	if !result.Created || result.GeneratedPassword != "" || !repository.created {
		t.Fatalf("result = %#v", result)
	}
	valid, err := auth.VerifyPassword("a strong bootstrap password", repository.passwordHash)
	if err != nil || !valid {
		t.Fatalf("stored password hash did not verify: valid=%v err=%v", valid, err)
	}
	if len(repository.recoveryPublicKey) != ed25519.PublicKeySize {
		t.Fatalf("recovery public key length = %d", len(repository.recoveryPublicKey))
	}
}

func TestOwnerGeneratesPasswordOnlyWhenCreating(t *testing.T) {
	directory := t.TempDir()
	recoveryPath := filepath.Join(directory, "recovery-public-key")
	publicKey := make([]byte, ed25519.PublicKeySize)
	if err := os.WriteFile(recoveryPath, []byte(base64.RawStdEncoding.EncodeToString(publicKey)), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := Owner(context.Background(), &fakeOwnerStore{}, Config{
		PasswordFile:          filepath.Join(directory, "missing-password"),
		RecoveryPublicKeyFile: recoveryPath,
	})
	if err != nil {
		t.Fatalf("Owner() error = %v", err)
	}
	if !result.Created || result.GeneratedPassword == "" {
		t.Fatalf("result = %#v", result)
	}

	existing := &fakeOwnerStore{exists: true}
	result, err = Owner(context.Background(), existing, Config{})
	if err != nil {
		t.Fatalf("Owner() existing error = %v", err)
	}
	if result.Created || result.GeneratedPassword != "" || existing.created {
		t.Fatalf("existing result = %#v", result)
	}
}
