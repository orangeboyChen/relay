package auth

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

func TestNewTokenPairUsesIndependentTokensAndExpirations(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	randomBytes := append(bytes.Repeat([]byte{1}, tokenBytes), bytes.Repeat([]byte{2}, tokenBytes)...)
	pair, err := newTokenPair(now, bytes.NewReader(randomBytes))
	if err != nil {
		t.Fatalf("newTokenPair() error = %v", err)
	}
	if pair.AccessToken == pair.RefreshToken {
		t.Fatal("access and refresh tokens are equal")
	}
	if pair.AccessExpiresAt != now.Add(15*time.Minute) || pair.RefreshExpiresAt != now.Add(30*24*time.Hour) {
		t.Fatalf("token expirations = %s and %s", pair.AccessExpiresAt, pair.RefreshExpiresAt)
	}
	if _, err := TokenHash(pair.AccessToken); err != nil {
		t.Fatalf("TokenHash() error = %v", err)
	}
}

func TestTokenHashRejectsMalformedToken(t *testing.T) {
	if _, err := TokenHash("short"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("TokenHash() error = %v, want ErrInvalidToken", err)
	}
}

func TestNewOpaqueTokenProducesHashableIndependentCredentials(t *testing.T) {
	first, err := NewOpaqueToken()
	if err != nil {
		t.Fatalf("NewOpaqueToken() error = %v", err)
	}
	second, err := NewOpaqueToken()
	if err != nil {
		t.Fatalf("NewOpaqueToken() error = %v", err)
	}
	if first == second {
		t.Fatal("opaque credentials are equal")
	}
	if _, err := TokenHash(first); err != nil {
		t.Fatalf("TokenHash() error = %v", err)
	}
}
