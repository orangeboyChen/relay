package webauthn

import (
	"context"
	"testing"
	"time"

	webauthnlib "github.com/go-webauthn/webauthn/webauthn"
)

type memoryStore struct {
	owner       string
	credentials []webauthnlib.Credential
	disabled    bool
}

func (store *memoryStore) OwnerID(context.Context) (string, error) { return store.owner, nil }
func (store *memoryStore) WebAuthnCredentials(context.Context, string) ([]webauthnlib.Credential, error) {
	return append([]webauthnlib.Credential(nil), store.credentials...), nil
}
func (store *memoryStore) SaveWebAuthnCredential(_ context.Context, _ string, credential webauthnlib.Credential) error {
	store.credentials = append(store.credentials, credential)
	return nil
}
func (store *memoryStore) DisableOwnerPassword(context.Context, string, time.Time) error {
	store.disabled = true
	return nil
}

func TestBeginRegistrationStoresOpaqueCeremony(t *testing.T) {
	store := &memoryStore{owner: "01900000-0000-7000-8000-000000000001"}
	service, err := New(store, "https://relay.example.test")
	if err != nil {
		t.Fatal(err)
	}
	ceremonyID, options, err := service.BeginRegistration(context.Background(), store.owner)
	if err != nil {
		t.Fatal(err)
	}
	if ceremonyID == "" || len(options) == 0 {
		t.Fatal("expected ceremony ID and creation options")
	}
	if len(service.sessions) != 1 {
		t.Fatalf("expected one server-side ceremony, got %d", len(service.sessions))
	}
}

func TestTakeSessionConsumesAndRejectsReuse(t *testing.T) {
	store := &memoryStore{owner: "01900000-0000-7000-8000-000000000001"}
	service, err := New(store, "https://relay.example.test")
	if err != nil {
		t.Fatal(err)
	}
	ceremonyID, _, err := service.BeginLogin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.takeSession(ceremonyID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.takeSession(ceremonyID); err == nil {
		t.Fatal("expected ceremony reuse to fail")
	}
}
