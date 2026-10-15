package api

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"sim-deck-server/internal/auth"
	"sim-deck-server/internal/domain"
	"sim-deck-server/internal/e2ee"
	"sim-deck-server/internal/store"
)

type fakeRepository struct {
	devices                 []store.DeviceRecord
	updatedDeviceID         string
	updatedNumericID        int
	insertSequence          int64
	insertCreated           bool
	insertError             error
	insertedEnvelope        domain.EventEnvelope
	insertedExpiry          time.Time
	credential              store.OwnerCredential
	sessionPair             auth.TokenPair
	rotatedRefresh          string
	revokedAccess           string
	ownerID                 string
	ownerAuthError          error
	pairingOwnerID          string
	pairingTokenHash        []byte
	pairingExpiresAt        time.Time
	pairing                 store.DevicePairing
	pairingRecord           store.DeviceRecord
	pairingError            error
	devicePublicKey         []byte
	deviceAuthError         error
	authenticatedDeviceID   string
	authenticatedCredential string
}

func (fake *fakeRepository) ListDevices(context.Context) ([]store.DeviceRecord, error) {
	return fake.devices, nil
}

func (fake *fakeRepository) UpdateNumericID(_ context.Context, deviceID string, numericID int) error {
	fake.updatedDeviceID = deviceID
	fake.updatedNumericID = numericID
	return nil
}

func (fake *fakeRepository) InsertEvent(_ context.Context, envelope domain.EventEnvelope, expiresAt time.Time) (int64, bool, error) {
	fake.insertedEnvelope = envelope
	fake.insertedExpiry = expiresAt
	return fake.insertSequence, fake.insertCreated, fake.insertError
}

func (fake *fakeRepository) OwnerCredentialByUsername(context.Context, string) (store.OwnerCredential, error) {
	return fake.credential, nil
}

func (fake *fakeRepository) CreateSession(_ context.Context, _ string, pair auth.TokenPair) error {
	fake.sessionPair = pair
	return nil
}

func (fake *fakeRepository) RotateSession(_ context.Context, refresh string, pair auth.TokenPair, _ time.Time) error {
	fake.rotatedRefresh = refresh
	fake.sessionPair = pair
	return nil
}

func (fake *fakeRepository) RevokeSession(_ context.Context, access string, _ time.Time) error {
	fake.revokedAccess = access
	return nil
}

func (fake *fakeRepository) AuthenticateOwnerSession(_ context.Context, _ string, _ time.Time) (string, error) {
	if fake.ownerAuthError != nil {
		return "", fake.ownerAuthError
	}
	if fake.ownerID == "" {
		return "owner-1", nil
	}
	return fake.ownerID, nil
}

func (fake *fakeRepository) CreatePairingToken(_ context.Context, ownerID string, tokenHash []byte, expiresAt time.Time) error {
	fake.pairingOwnerID = ownerID
	fake.pairingTokenHash = append([]byte(nil), tokenHash...)
	fake.pairingExpiresAt = expiresAt
	return nil
}

func (fake *fakeRepository) ConsumePairingToken(_ context.Context, _ []byte, _ time.Time, pairing store.DevicePairing) (store.DeviceRecord, error) {
	fake.pairing = pairing
	return fake.pairingRecord, fake.pairingError
}

func (fake *fakeRepository) AuthenticateDevice(_ context.Context, deviceID string, credential string, _ time.Time) ([]byte, error) {
	fake.authenticatedDeviceID = deviceID
	fake.authenticatedCredential = credential
	if fake.deviceAuthError != nil {
		return nil, fake.deviceAuthError
	}
	return append([]byte(nil), fake.devicePublicKey...), nil
}

func TestDiscovery(t *testing.T) {
	handler := Handler(&fakeRepository{}, Config{PublicBaseURL: "https://deck.example", MediaUDPPort: 49152, MediaTCPPort: 49153})
	request := httptest.NewRequest(http.MethodGet, "/.well-known/sim-deck", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if body["api_version"] != "v1" {
		t.Fatalf("api_version = %v, want v1", body["api_version"])
	}
}

func TestUpdateNumericID(t *testing.T) {
	repository := &fakeRepository{}
	handler := Handler(repository, Config{})
	request := httptest.NewRequest(http.MethodPatch, "/v1/devices/device-1/numeric-id", bytes.NewBufferString(`{"numeric_id":2}`))
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", response.Code)
	}
	if repository.updatedDeviceID != "device-1" || repository.updatedNumericID != 2 {
		t.Fatalf("update = (%q, %d), want (device-1, 2)", repository.updatedDeviceID, repository.updatedNumericID)
	}
}

func TestInsertEventIsIdempotent(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	vaultKey := make([]byte, 32)
	eventTime := time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)
	envelope, err := e2ee.Seal(vaultKey, privateKey, e2ee.NewEnvelope(
		"01900000-0000-7000-8000-000000000001",
		99,
		"message.received",
		"01900000-0000-7000-8000-000000000002",
		eventTime,
		1,
	), []byte("encrypted payload"))
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{"envelope": envelope})
	if err != nil {
		t.Fatal(err)
	}
	repository := &fakeRepository{insertSequence: 42, insertCreated: false, devicePublicKey: publicKey}
	handler := Handler(repository, Config{Now: func() time.Time { return eventTime.Add(time.Hour) }})
	request := httptest.NewRequest(http.MethodPost, "/v1/events", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer device-credential")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", response.Code, response.Body.String())
	}
	if repository.insertedEnvelope.Sequence != 0 {
		t.Fatalf("stored sequence = %d, want 0", repository.insertedEnvelope.Sequence)
	}
	if repository.insertedExpiry != eventTime.Add(365*24*time.Hour) {
		t.Fatalf("stored expiry = %s", repository.insertedExpiry)
	}
}

func TestBootstrapLoginCreatesSession(t *testing.T) {
	passwordHash, err := auth.HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	repository := &fakeRepository{credential: store.OwnerCredential{ID: "owner-1", PasswordHash: passwordHash}}
	handler := Handler(repository, Config{})
	request := httptest.NewRequest(http.MethodPost, "/v1/auth/bootstrap", bytes.NewBufferString(`{"username":"admin","password":"correct horse battery staple"}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusCreated || repository.sessionPair.AccessToken == "" {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
}

func TestBootstrapLoginRejectsDisabledPassword(t *testing.T) {
	disabledAt := time.Now()
	repository := &fakeRepository{credential: store.OwnerCredential{ID: "owner-1", PasswordHash: "unused", PasswordDisabledAt: &disabledAt}}
	handler := Handler(repository, Config{})
	request := httptest.NewRequest(http.MethodPost, "/v1/auth/bootstrap", bytes.NewBufferString(`{"username":"admin","password":"password"}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized || repository.sessionPair.AccessToken != "" {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
}

func TestRefreshAndLogoutRotateCredentials(t *testing.T) {
	repository := &fakeRepository{}
	handler := Handler(repository, Config{})
	refreshRequest := httptest.NewRequest(http.MethodPost, "/v1/auth/refresh", bytes.NewBufferString(`{"refresh_token":"old-refresh"}`))
	refreshResponse := httptest.NewRecorder()
	handler.ServeHTTP(refreshResponse, refreshRequest)
	if refreshResponse.Code != http.StatusOK || repository.rotatedRefresh != "old-refresh" {
		t.Fatalf("refresh status = %d body = %s", refreshResponse.Code, refreshResponse.Body.String())
	}

	logoutRequest := httptest.NewRequest(http.MethodPost, "/v1/auth/logout", nil)
	logoutRequest.Header.Set("Authorization", "Bearer access-token")
	logoutResponse := httptest.NewRecorder()
	handler.ServeHTTP(logoutResponse, logoutRequest)
	if logoutResponse.Code != http.StatusNoContent || repository.revokedAccess != "access-token" {
		t.Fatalf("logout status = %d body = %s", logoutResponse.Code, logoutResponse.Body.String())
	}
}
