package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"time"
)

const tokenBytes = 32

var ErrInvalidToken = errors.New("session token is invalid")

// TokenPair contains opaque credentials and their independent expiration times.
type TokenPair struct {
	AccessToken      string    `json:"access_token"`
	RefreshToken     string    `json:"refresh_token"`
	AccessExpiresAt  time.Time `json:"access_expires_at"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at"`
}

// NewTokenPair creates a short-lived access token and a rotating refresh token.
func NewTokenPair(now time.Time) (TokenPair, error) {
	return newTokenPair(now, rand.Reader)
}

// NewOpaqueToken creates a high-entropy credential suitable for one-time pairing or device authentication.
func NewOpaqueToken() (string, error) {
	return randomToken(rand.Reader)
}

func newTokenPair(now time.Time, source io.Reader) (TokenPair, error) {
	accessToken, err := randomToken(source)
	if err != nil {
		return TokenPair{}, err
	}
	refreshToken, err := randomToken(source)
	if err != nil {
		return TokenPair{}, err
	}
	return TokenPair{
		AccessToken:      accessToken,
		RefreshToken:     refreshToken,
		AccessExpiresAt:  now.UTC().Add(15 * time.Minute),
		RefreshExpiresAt: now.UTC().Add(30 * 24 * time.Hour),
	}, nil
}

// TokenHash validates and hashes an opaque token before database lookup.
func TokenHash(token string) ([sha256.Size]byte, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(decoded) != tokenBytes {
		return [sha256.Size]byte{}, ErrInvalidToken
	}
	return sha256.Sum256(decoded), nil
}

func randomToken(source io.Reader) (string, error) {
	value := make([]byte, tokenBytes)
	if _, err := io.ReadFull(source, value); err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}
