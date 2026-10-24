//go:build legacy_rest_tests

package api

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"sim-deck-server/internal/domain"
)

var _ = Describe("Encrypted incoming call ingestion", func() {
	var (
		now          time.Time
		privateKey   ed25519.PrivateKey
		repository   *fakeRepository
		handler      http.Handler
		notification domain.IncomingCallNotification
	)

	BeforeEach(func() {
		now = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
		publicKey, generatedPrivateKey, err := ed25519.GenerateKey(rand.Reader)
		Expect(err).NotTo(HaveOccurred())
		privateKey = generatedPrivateKey
		notification = domain.IncomingCallNotification{
			ID:         "01900000-0000-7000-8000-000000000010",
			Type:       "call.incoming",
			DeviceUUID: "01900000-0000-7000-8000-000000000001",
			OccurredAt: now.Add(-time.Minute),
			KeyEpoch:   1,
			Recipients: []domain.IncomingCallRecipient{{
				InstallationID:  "01900000-0000-7000-8000-000000000101",
				SenderPublicKey: bytes.Repeat([]byte{1}, 32),
				Nonce:           bytes.Repeat([]byte{2}, 12),
				Ciphertext:      bytes.Repeat([]byte{3}, 48),
			}},
		}
		signIncomingCall(privateKey, &notification)
		repository = &fakeRepository{devicePublicKey: publicKey, incomingCallCreated: true}
		handler = Handler(repository, Config{Now: func() time.Time { return now }})
	})

	request := func(credential string, value domain.IncomingCallNotification) *http.Request {
		body, err := json.Marshal(map[string]any{"notification": value})
		Expect(err).NotTo(HaveOccurred())
		result := httptest.NewRequest(http.MethodPost, "/v1/incoming-calls", bytes.NewReader(body))
		if credential != "" {
			result.Header.Set("Authorization", "Bearer "+credential)
		}
		return result
	}

	It("authenticates the device and stores only signed recipient ciphertexts", func() {
		response := httptest.NewRecorder()

		handler.ServeHTTP(response, request("device-secret", notification))

		Expect(response.Code).To(Equal(http.StatusCreated))
		Expect(repository.authenticatedDeviceID).To(Equal(notification.DeviceUUID))
		Expect(repository.authenticatedCredential).To(Equal("device-secret"))
		Expect(repository.incomingCall).To(Equal(notification))
		Expect(repository.incomingCallExpiry).To(Equal(notification.OccurredAt.Add(365 * 24 * time.Hour)))
		Expect(response.Body.String()).To(ContainSubstring(`"created":true`))
	})

	It("returns an idempotent success without creating duplicate deliveries", func() {
		repository.incomingCallCreated = false
		response := httptest.NewRecorder()

		handler.ServeHTTP(response, request("device-secret", notification))

		Expect(response.Code).To(Equal(http.StatusOK))
		Expect(response.Body.String()).To(ContainSubstring(`"created":false`))
	})

	It("requires an authenticated device before signature verification", func() {
		missing := httptest.NewRecorder()
		handler.ServeHTTP(missing, request("", notification))
		Expect(missing.Code).To(Equal(http.StatusUnauthorized))

		repository.deviceAuthError = errors.New("revoked")
		revoked := httptest.NewRecorder()
		handler.ServeHTTP(revoked, request("revoked", notification))
		Expect(revoked.Code).To(Equal(http.StatusUnauthorized))
		Expect(repository.incomingCall.ID).To(BeEmpty())
	})

	It("rejects malformed JSON, malformed envelopes, and tampering", func() {
		malformedJSON := httptest.NewRequest(http.MethodPost, "/v1/incoming-calls", bytes.NewBufferString(`{`))
		malformedJSON.Header.Set("Authorization", "Bearer device-secret")
		malformedResponse := httptest.NewRecorder()
		handler.ServeHTTP(malformedResponse, malformedJSON)
		Expect(malformedResponse.Code).To(Equal(http.StatusBadRequest))

		invalid := notification
		invalid.Recipients = append([]domain.IncomingCallRecipient(nil), notification.Recipients...)
		invalid.Recipients[0].Nonce = []byte("short")
		invalidResponse := httptest.NewRecorder()
		handler.ServeHTTP(invalidResponse, request("device-secret", invalid))
		Expect(invalidResponse.Code).To(Equal(http.StatusUnprocessableEntity))

		tampered := notification
		tampered.Recipients = append([]domain.IncomingCallRecipient(nil), notification.Recipients...)
		tampered.Recipients[0].Ciphertext = append([]byte(nil), notification.Recipients[0].Ciphertext...)
		tampered.Recipients[0].Ciphertext[0] ^= 0xff
		tamperedResponse := httptest.NewRecorder()
		handler.ServeHTTP(tamperedResponse, request("device-secret", tampered))
		Expect(tamperedResponse.Code).To(Equal(http.StatusUnauthorized))
		Expect(repository.incomingCall.ID).To(BeEmpty())
	})

	It("rejects notifications outside the accepted time window", func() {
		future := notification
		future.OccurredAt = now.Add(6 * time.Minute)
		signIncomingCall(privateKey, &future)
		futureResponse := httptest.NewRecorder()
		handler.ServeHTTP(futureResponse, request("device-secret", future))
		Expect(futureResponse.Code).To(Equal(http.StatusUnprocessableEntity))

		old := notification
		old.OccurredAt = now.Add(-365 * 24 * time.Hour)
		signIncomingCall(privateKey, &old)
		oldResponse := httptest.NewRecorder()
		handler.ServeHTTP(oldResponse, request("device-secret", old))
		Expect(oldResponse.Code).To(Equal(http.StatusUnprocessableEntity))
	})

	DescribeTable("maps persistence failures without exposing storage details",
		func(repositoryError error, expectedCode string) {
			repository.incomingCallError = repositoryError
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request("device-secret", notification))
			Expect(response.Code).To(BeNumerically(">=", http.StatusConflict))
			Expect(response.Body.String()).To(ContainSubstring(expectedCode))
			Expect(response.Body.String()).NotTo(ContainSubstring("database unavailable"))
		},
		Entry("idempotency conflict", domain.ErrIdempotencyConflict, "idempotency_conflict"),
		Entry("revoked recipient", domain.ErrUntrustedInstallationRecipient, "untrusted_installation_recipient"),
		Entry("database failure", errors.New("database unavailable"), "storage_error"),
	)
})

func signIncomingCall(privateKey ed25519.PrivateKey, notification *domain.IncomingCallNotification) {
	for index := range notification.Recipients {
		recipient := &notification.Recipients[index]
		recipient.Signature = ed25519.Sign(privateKey, incomingCallRecipientSigningData(*notification, *recipient))
	}
}

func incomingCallRecipientSigningData(notification domain.IncomingCallNotification, recipient domain.IncomingCallRecipient) []byte {
	buffer := make([]byte, 0, 256)
	buffer = appendSignedBytes(buffer, []byte(notification.ID))
	buffer = appendSignedBytes(buffer, []byte(notification.Type))
	buffer = appendSignedBytes(buffer, []byte(notification.DeviceUUID))
	buffer = binary.BigEndian.AppendUint64(buffer, uint64(notification.OccurredAt.UTC().UnixNano()))
	buffer = binary.BigEndian.AppendUint64(buffer, uint64(notification.KeyEpoch))
	buffer = appendSignedBytes(buffer, []byte(recipient.InstallationID))
	buffer = appendSignedBytes(buffer, recipient.SenderPublicKey)
	buffer = appendSignedBytes(buffer, recipient.Nonce)
	return appendSignedBytes(buffer, recipient.Ciphertext)
}

func appendSignedBytes(buffer []byte, value []byte) []byte {
	buffer = binary.BigEndian.AppendUint32(buffer, uint32(len(value)))
	return append(buffer, value...)
}
