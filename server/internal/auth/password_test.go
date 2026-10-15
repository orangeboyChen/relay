package auth

import (
	"errors"
	"testing"
)

func TestHashAndVerifyPassword(t *testing.T) {
	encoded, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	valid, err := VerifyPassword("correct horse battery staple", encoded)
	if err != nil {
		t.Fatalf("VerifyPassword() error = %v", err)
	}
	if !valid {
		t.Fatal("VerifyPassword() = false, want true")
	}
	valid, err = VerifyPassword("wrong password", encoded)
	if err != nil {
		t.Fatalf("VerifyPassword() wrong password error = %v", err)
	}
	if valid {
		t.Fatal("VerifyPassword() = true for wrong password")
	}
}

func TestVerifyPasswordRejectsMalformedHash(t *testing.T) {
	if _, err := VerifyPassword("password", "$argon2id$broken"); !errors.Is(err, ErrInvalidPasswordHash) {
		t.Fatalf("VerifyPassword() error = %v, want ErrInvalidPasswordHash", err)
	}
}

func TestGenerateInitialPassword(t *testing.T) {
	first, err := GenerateInitialPassword()
	if err != nil {
		t.Fatalf("GenerateInitialPassword() error = %v", err)
	}
	second, err := GenerateInitialPassword()
	if err != nil {
		t.Fatalf("GenerateInitialPassword() error = %v", err)
	}
	if len(first) < 24 || first == second {
		t.Fatalf("generated passwords are not sufficiently distinct: %q and %q", first, second)
	}
}
