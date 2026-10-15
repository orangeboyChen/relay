package api

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"sim-deck-server/internal/domain"
	"sim-deck-server/internal/e2ee"
)

var _ = Describe("Encrypted event ingestion", func() {
	var (
		now        time.Time
		publicKey  ed25519.PublicKey
		privateKey ed25519.PrivateKey
		vaultKey   []byte
		repository *fakeRepository
		handler    http.Handler
		envelope   domain.EventEnvelope
	)

	BeforeEach(func() {
		now = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
		var err error
		publicKey, privateKey, err = ed25519.GenerateKey(rand.Reader)
		Expect(err).NotTo(HaveOccurred())
		vaultKey = make([]byte, 32)
		envelope, err = e2ee.Seal(vaultKey, privateKey, e2ee.NewEnvelope(
			"01900000-0000-7000-8000-000000000001",
			88,
			"message.received",
			"01900000-0000-7000-8000-000000000002",
			now.Add(-time.Minute),
			1,
		), []byte("encrypted payload"))
		Expect(err).NotTo(HaveOccurred())
		repository = &fakeRepository{devicePublicKey: publicKey, insertSequence: 42, insertCreated: true}
		handler = Handler(repository, Config{Now: func() time.Time { return now }})
	})

	request := func(credential string, event domain.EventEnvelope) *http.Request {
		body, err := json.Marshal(map[string]any{"envelope": event})
		Expect(err).NotTo(HaveOccurred())
		result := httptest.NewRequest(http.MethodPost, "/v1/events", bytes.NewReader(body))
		if credential != "" {
			result.Header.Set("Authorization", "Bearer "+credential)
		}
		return result
	}

	It("requires a device credential before checking the signature", func() {
		response := httptest.NewRecorder()

		handler.ServeHTTP(response, request("", envelope))

		Expect(response.Code).To(Equal(http.StatusUnauthorized))
		Expect(repository.insertedEnvelope.ID).To(BeEmpty())
	})

	It("rejects a malformed request before device authentication", func() {
		malformed := httptest.NewRequest(http.MethodPost, "/v1/events", bytes.NewBufferString(`{`))
		malformed.Header.Set("Authorization", "Bearer device-credential")
		response := httptest.NewRecorder()

		handler.ServeHTTP(response, malformed)

		Expect(response.Code).To(Equal(http.StatusBadRequest))
		Expect(repository.authenticatedDeviceID).To(BeEmpty())
	})

	It("authenticates the device, verifies its signature, and assigns retention metadata", func() {
		response := httptest.NewRecorder()

		handler.ServeHTTP(response, request("device-credential", envelope))

		Expect(response.Code).To(Equal(http.StatusCreated))
		Expect(repository.authenticatedDeviceID).To(Equal(envelope.DeviceUUID))
		Expect(repository.authenticatedCredential).To(Equal("device-credential"))
		Expect(repository.insertedEnvelope.Sequence).To(BeZero())
		Expect(repository.insertedExpiry).To(Equal(envelope.OccurredAt.Add(365 * 24 * time.Hour)))
	})

	It("returns the existing sequence for an idempotent retry", func() {
		repository.insertCreated = false
		response := httptest.NewRecorder()

		handler.ServeHTTP(response, request("device-credential", envelope))

		Expect(response.Code).To(Equal(http.StatusOK))
		Expect(response.Body.String()).To(ContainSubstring(`"sequence":42`))
		Expect(response.Body.String()).To(ContainSubstring(`"created":false`))
	})

	It("rejects a credential belonging to another or revoked device", func() {
		repository.deviceAuthError = errors.New("not found")
		response := httptest.NewRecorder()

		handler.ServeHTTP(response, request("wrong-credential", envelope))

		Expect(response.Code).To(Equal(http.StatusUnauthorized))
		Expect(repository.insertedEnvelope.ID).To(BeEmpty())
	})

	It("rejects a validly shaped envelope with a tampered ciphertext", func() {
		envelope.Ciphertext[0] ^= 0xff
		response := httptest.NewRecorder()

		handler.ServeHTTP(response, request("device-credential", envelope))

		Expect(response.Code).To(Equal(http.StatusUnauthorized))
		Expect(repository.insertedEnvelope.ID).To(BeEmpty())
	})

	It("rejects malformed signed fields before storage", func() {
		envelope.Nonce = []byte("short")
		response := httptest.NewRecorder()

		handler.ServeHTTP(response, request("device-credential", envelope))

		Expect(response.Code).To(Equal(http.StatusUnprocessableEntity))
		Expect(repository.insertedEnvelope.ID).To(BeEmpty())
	})

	It("rejects future timestamps and events outside the retention window", func() {
		future, err := e2ee.Seal(vaultKey, privateKey, e2ee.NewEnvelope(
			envelope.ID,
			0,
			envelope.Type,
			envelope.DeviceUUID,
			now.Add(6*time.Minute),
			envelope.KeyEpoch,
		), []byte("future"))
		Expect(err).NotTo(HaveOccurred())
		futureResponse := httptest.NewRecorder()
		handler.ServeHTTP(futureResponse, request("device-credential", future))
		Expect(futureResponse.Code).To(Equal(http.StatusUnprocessableEntity))

		old, err := e2ee.Seal(vaultKey, privateKey, e2ee.NewEnvelope(
			envelope.ID,
			0,
			envelope.Type,
			envelope.DeviceUUID,
			now.Add(-365*24*time.Hour),
			envelope.KeyEpoch,
		), []byte("old"))
		Expect(err).NotTo(HaveOccurred())
		oldResponse := httptest.NewRecorder()
		handler.ServeHTTP(oldResponse, request("device-credential", old))
		Expect(oldResponse.Code).To(Equal(http.StatusUnprocessableEntity))
	})

	It("returns a conflict when an idempotency key is reused with different content", func() {
		repository.insertError = domain.ErrIdempotencyConflict
		response := httptest.NewRecorder()

		handler.ServeHTTP(response, request("device-credential", envelope))

		Expect(response.Code).To(Equal(http.StatusConflict))
		Expect(response.Body.String()).To(ContainSubstring("idempotency_conflict"))
	})

	It("maps an event storage failure without exposing its details", func() {
		repository.insertError = errors.New("database unavailable")
		response := httptest.NewRecorder()

		handler.ServeHTTP(response, request("device-credential", envelope))

		Expect(response.Code).To(Equal(http.StatusInternalServerError))
		Expect(response.Body.String()).To(ContainSubstring("storage_error"))
		Expect(response.Body.String()).NotTo(ContainSubstring("database unavailable"))
	})
})

var _ = Describe("Encrypted event synchronization", func() {
	var (
		now        time.Time
		repository *fakeRepository
		handler    http.Handler
	)

	BeforeEach(func() {
		now = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
		repository = &fakeRepository{
			listedEvents: []domain.EventEnvelope{
				{ID: "01900000-0000-7000-8000-000000000011", Sequence: 42, Type: "call.state", DeviceUUID: "01900000-0000-7000-8000-000000000002"},
				{ID: "01900000-0000-7000-8000-000000000012", Sequence: 43, Type: "call.state", DeviceUUID: "01900000-0000-7000-8000-000000000002"},
			},
		}
		handler = Handler(repository, Config{Now: func() time.Time { return now }})
	})

	request := func(path string) *http.Request {
		result := httptest.NewRequest(http.MethodGet, path, nil)
		result.Header.Set("Authorization", "Bearer owner-access-token")
		return result
	}

	It("returns opaque events after the requested cursor", func() {
		response := httptest.NewRecorder()

		handler.ServeHTTP(response, request("/v1/events?after_sequence=41&limit=2"))

		Expect(response.Code).To(Equal(http.StatusOK))
		Expect(repository.listEventsAfter).To(Equal(int64(41)))
		Expect(repository.listEventsLimit).To(Equal(2))
		Expect(repository.listEventsNow).To(Equal(now))
		Expect(response.Body.String()).To(ContainSubstring(`"next_sequence":43`))
		Expect(response.Body.String()).To(ContainSubstring(`"ciphertext":null`))
	})

	It("uses bounded defaults and preserves the cursor for an empty page", func() {
		repository.listedEvents = nil
		response := httptest.NewRecorder()

		handler.ServeHTTP(response, request("/v1/events?after_sequence=9"))

		Expect(response.Code).To(Equal(http.StatusOK))
		Expect(repository.listEventsLimit).To(Equal(100))
		Expect(response.Body.String()).To(ContainSubstring(`"next_sequence":9`))
	})

	DescribeTable("rejects invalid cursor pagination before storage",
		func(path string, code string) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request(path))
			Expect(response.Code).To(Equal(http.StatusBadRequest))
			Expect(response.Body.String()).To(ContainSubstring(code))
			Expect(repository.listEventsLimit).To(BeZero())
		},
		Entry("negative cursor", "/v1/events?after_sequence=-1", "invalid_cursor"),
		Entry("non-numeric cursor", "/v1/events?after_sequence=nope", "invalid_cursor"),
		Entry("zero page", "/v1/events?limit=0", "invalid_limit"),
		Entry("oversized page", "/v1/events?limit=501", "invalid_limit"),
	)

	It("requires owner authentication", func() {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/events", nil))
		Expect(response.Code).To(Equal(http.StatusUnauthorized))
		Expect(repository.listEventsLimit).To(BeZero())
	})

	It("maps storage failures without exposing details", func() {
		repository.listEventsError = errors.New("database unavailable")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request("/v1/events"))
		Expect(response.Code).To(Equal(http.StatusInternalServerError))
		Expect(response.Body.String()).To(ContainSubstring("storage_error"))
		Expect(response.Body.String()).NotTo(ContainSubstring("database unavailable"))
	})
})
