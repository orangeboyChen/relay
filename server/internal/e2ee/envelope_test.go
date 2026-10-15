package e2ee

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
	"time"
)

func TestSealAndOpen(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	vaultKey := make([]byte, 32)
	if _, err := rand.Read(vaultKey); err != nil {
		t.Fatalf("rand.Read() error = %v", err)
	}

	envelope := NewEnvelope("event-1", 7, "message.received", "device-1", time.Unix(1_700_000_000, 0), 2)
	sealed, err := Seal(vaultKey, privateKey, envelope, []byte("private message"))
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	opened, err := Open(vaultKey, publicKey, sealed)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if string(opened) != "private message" {
		t.Fatalf("Open() = %q, want private message", opened)
	}
}

func TestOpenRejectsTamperedMetadata(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	vaultKey := make([]byte, 32)
	envelope := NewEnvelope("event-1", 7, "message.received", "device-1", time.Unix(1_700_000_000, 0), 2)
	sealed, err := Seal(vaultKey, privateKey, envelope, []byte("private message"))
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}

	sealed.DeviceUUID = "device-2"
	if _, err := Open(vaultKey, publicKey, sealed); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("Open() error = %v, want ErrInvalidSignature", err)
	}
}

func TestSealRejectsInvalidVaultKey(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	envelope := NewEnvelope("event-1", 1, "message.received", "device-1", time.Now(), 1)
	if _, err := Seal([]byte("short"), privateKey, envelope, nil); !errors.Is(err, ErrInvalidVaultKey) {
		t.Fatalf("Seal() error = %v, want ErrInvalidVaultKey", err)
	}
}
