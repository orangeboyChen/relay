package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"sim-deck-server/internal/domain"
	"sim-deck-server/internal/store"
)

type fakeRepository struct {
	devices          []store.DeviceRecord
	updatedDeviceID  string
	updatedNumericID int
	insertSequence   int64
	insertCreated    bool
}

func (fake *fakeRepository) ListDevices(context.Context) ([]store.DeviceRecord, error) {
	return fake.devices, nil
}

func (fake *fakeRepository) UpdateNumericID(_ context.Context, deviceID string, numericID int) error {
	fake.updatedDeviceID = deviceID
	fake.updatedNumericID = numericID
	return nil
}

func (fake *fakeRepository) InsertEvent(context.Context, domain.EventEnvelope, time.Time) (int64, bool, error) {
	return fake.insertSequence, fake.insertCreated, nil
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
	repository := &fakeRepository{insertSequence: 42, insertCreated: false}
	handler := Handler(repository, Config{})
	body := `{"envelope":{"id":"01900000-0000-7000-8000-000000000001","type":"message.received","device_uuid":"01900000-0000-7000-8000-000000000002","occurred_at":"2026-10-15T10:00:00Z","key_epoch":1,"nonce":"AA==","ciphertext":"AA==","signature":"AA=="},"expires_at":"2027-10-15T10:00:00Z"}`
	request := httptest.NewRequest(http.MethodPost, "/v1/events", bytes.NewBufferString(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", response.Code, response.Body.String())
	}
}
