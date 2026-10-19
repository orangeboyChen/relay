package webauthn

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	webauthnlib "github.com/go-webauthn/webauthn/webauthn"
)

type memoryStore struct {
	owner            string
	credentials      []webauthnlib.Credential
	disabled         bool
	credentialsError error
	saveError        error
	disableError     error
}

func (store *memoryStore) OwnerID(context.Context) (string, error) { return store.owner, nil }
func (store *memoryStore) WebAuthnCredentials(context.Context, string) ([]webauthnlib.Credential, error) {
	if store.credentialsError != nil {
		return nil, store.credentialsError
	}
	return append([]webauthnlib.Credential(nil), store.credentials...), nil
}
func (store *memoryStore) SaveWebAuthnCredential(_ context.Context, _ string, credential webauthnlib.Credential) error {
	if store.saveError != nil {
		return store.saveError
	}
	store.credentials = append(store.credentials, credential)
	return nil
}
func (store *memoryStore) DisableOwnerPassword(context.Context, string, time.Time) error {
	if store.disableError != nil {
		return store.disableError
	}
	store.disabled = true
	return nil
}

func TestNewValidatesInputs(t *testing.T) {
	if _, err := New(nil, "https://relay.example.test"); err == nil {
		t.Fatal("expected nil store to fail")
	}
	store := &memoryStore{owner: "01900000-0000-7000-8000-000000000001"}
	if _, err := New(store, ""); err == nil {
		t.Fatal("expected empty base URL to fail")
	}
	if _, err := New(store, "not a URL"); err == nil {
		t.Fatal("expected malformed base URL to fail")
	}
}

func TestNewUsesHostnameAsRPIDAndAcceptsNativeOrigin(t *testing.T) {
	store := &memoryStore{owner: "01900000-0000-7000-8000-000000000001"}
	service, err := New(store, "https://sim-deck.home.nowcent.cn:54443")
	if err != nil {
		t.Fatal(err)
	}
	if service.rp.Config.RPID != "sim-deck.home.nowcent.cn" {
		t.Fatalf("unexpected RP ID %q", service.rp.Config.RPID)
	}
	want := map[string]bool{
		"https://sim-deck.home.nowcent.cn:54443": true,
		"https://sim-deck.home.nowcent.cn":       true,
	}
	for _, origin := range service.rp.Config.RPOrigins {
		delete(want, origin)
	}
	if len(want) != 0 {
		t.Fatalf("missing native or API origin: %#v", want)
	}
}

func TestBeginRegistrationPropagatesCredentialLookupFailure(t *testing.T) {
	lookupError := errors.New("credential lookup failed")
	store := &memoryStore{owner: "01900000-0000-7000-8000-000000000001", credentialsError: lookupError}
	service, err := New(store, "https://relay.example.test")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.BeginRegistration(context.Background(), store.owner); !errors.Is(err, lookupError) {
		t.Fatalf("expected lookup error, got %v", err)
	}
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

func TestUserMetadataIsStable(t *testing.T) {
	user := User{ID: "01900000-0000-7000-8000-000000000001"}
	if len(user.WebAuthnID()) != 16 {
		t.Fatalf("expected UUID bytes, got %d", len(user.WebAuthnID()))
	}
	if user.WebAuthnName() != "admin" || user.WebAuthnDisplayName() != "Sim Deck owner" {
		t.Fatalf("unexpected user metadata")
	}
}

func TestCeremoniesRejectExpiredOrMissingSessions(t *testing.T) {
	store := &memoryStore{owner: "01900000-0000-7000-8000-000000000001"}
	service, err := New(store, "https://relay.example.test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.takeSession("missing"); err == nil {
		t.Fatal("expected missing ceremony to fail")
	}
	service.sessions["expired"] = webauthnlib.SessionData{Expires: time.Now().Add(-time.Minute)}
	if _, err := service.takeSession("expired"); err == nil {
		t.Fatal("expected expired ceremony to fail")
	}
}

func TestFinishCeremoniesRejectMalformedRequests(t *testing.T) {
	store := &memoryStore{owner: "01900000-0000-7000-8000-000000000001"}
	service, err := New(store, "https://relay.example.test")
	if err != nil {
		t.Fatal(err)
	}
	registrationID, _, err := service.BeginRegistration(context.Background(), store.owner)
	if err != nil {
		t.Fatal(err)
	}
	registrationRequest, err := http.NewRequest(http.MethodPost, "https://relay.example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.FinishRegistration(context.Background(), store.owner, registrationID, registrationRequest); err == nil {
		t.Fatal("expected malformed registration to fail")
	}

	loginID, _, err := service.BeginLogin()
	if err != nil {
		t.Fatal(err)
	}
	loginRequest, err := http.NewRequest(http.MethodPost, "https://relay.example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.FinishLogin(context.Background(), loginID, loginRequest); err == nil {
		t.Fatal("expected malformed login to fail")
	}
}
