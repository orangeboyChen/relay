package api

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"sim-deck-server/internal/auth"
	"sim-deck-server/internal/domain"
	"sim-deck-server/internal/store"
)

var _ = Describe("Device pairing API", func() {
	var (
		now        time.Time
		repository *fakeRepository
		handler    http.Handler
	)

	BeforeEach(func() {
		now = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
		repository = &fakeRepository{ownerID: "owner-1"}
		handler = Handler(repository, Config{Now: func() time.Time { return now }})
	})

	It("requires an owner session before issuing a one-time pairing token", func() {
		request := httptest.NewRequest(http.MethodPost, "/v1/device-pairing-tokens", nil)
		response := httptest.NewRecorder()

		handler.ServeHTTP(response, request)

		Expect(response.Code).To(Equal(http.StatusUnauthorized))
		Expect(repository.pairingTokenHash).To(BeEmpty())
	})

	It("stores only a hash of the pairing token with a short expiration", func() {
		request := httptest.NewRequest(http.MethodPost, "/v1/device-pairing-tokens", nil)
		request.Header.Set("Authorization", "Bearer owner-access-token")
		response := httptest.NewRecorder()

		handler.ServeHTTP(response, request)

		Expect(response.Code).To(Equal(http.StatusCreated))
		var body struct {
			PairingToken string    `json:"pairing_token"`
			ExpiresAt    time.Time `json:"expires_at"`
		}
		Expect(json.Unmarshal(response.Body.Bytes(), &body)).To(Succeed())
		Expect(body.PairingToken).NotTo(BeEmpty())
		Expect(body.ExpiresAt).To(Equal(now.Add(10 * time.Minute)))
		Expect(repository.pairingOwnerID).To(Equal("owner-1"))
		Expect(repository.pairingExpiresAt).To(Equal(body.ExpiresAt))

		hash, err := auth.TokenHash(body.PairingToken)
		Expect(err).NotTo(HaveOccurred())
		Expect(repository.pairingTokenHash).To(Equal(hash[:]))
		Expect(repository.pairingTokenHash).To(HaveLen(sha256.Size))
		Expect(repository.pairingTokenHash).NotTo(Equal([]byte(body.PairingToken)))
	})

	It("does not expose a token when storage fails", func() {
		repository.pairingTokenError = errors.New("database unavailable")
		request := httptest.NewRequest(http.MethodPost, "/v1/device-pairing-tokens", nil)
		request.Header.Set("Authorization", "Bearer owner-access-token")
		response := httptest.NewRecorder()

		handler.ServeHTTP(response, request)

		Expect(response.Code).To(Equal(http.StatusInternalServerError))
		Expect(response.Body.String()).To(ContainSubstring("storage_error"))
		Expect(response.Body.String()).NotTo(ContainSubstring("pairing_token"))
	})

	It("pairs one device while the repository sees only its credential hash", func() {
		pairingToken, err := auth.NewOpaqueToken()
		Expect(err).NotTo(HaveOccurred())
		credential, err := auth.NewOpaqueToken()
		Expect(err).NotTo(HaveOccurred())
		repository.pairingRecord = store.DeviceRecord{ID: "01900000-0000-7000-8000-000000000001", NumericID: 1, PairedAt: now}
		requestBody, err := json.Marshal(map[string]any{
			"pairing_token":       pairingToken,
			"device_uuid":         repository.pairingRecord.ID,
			"profile_ciphertext":  []byte{},
			"identity_public_key": bytes.Repeat([]byte{7}, ed25519.PublicKeySize),
			"wrapped_vault_key":   []byte("wrapped vault key"),
			"device_credential":   credential,
		})
		Expect(err).NotTo(HaveOccurred())
		request := httptest.NewRequest(http.MethodPost, "/v1/device-pairings", bytes.NewReader(requestBody))
		response := httptest.NewRecorder()

		handler.ServeHTTP(response, request)

		Expect(response.Code).To(Equal(http.StatusCreated))
		var body struct {
			Device store.DeviceRecord `json:"device"`
		}
		Expect(json.Unmarshal(response.Body.Bytes(), &body)).To(Succeed())
		Expect(body.Device.NumericID).To(Equal(1))
		credentialHash, err := auth.TokenHash(credential)
		Expect(err).NotTo(HaveOccurred())
		Expect(repository.pairing.CredentialHash).To(Equal(credentialHash[:]))
		Expect(repository.pairing.IdentityPublicKey).To(HaveLen(ed25519.PublicKeySize))
		Expect(repository.pairing.ProfileCiphertext).To(BeEmpty())
	})

	It("rejects malformed device identity before consuming a token", func() {
		pairingToken, err := auth.NewOpaqueToken()
		Expect(err).NotTo(HaveOccurred())
		requestBody, err := json.Marshal(map[string]any{
			"pairing_token":       pairingToken,
			"device_uuid":         "device-1",
			"profile_ciphertext":  []byte("encrypted"),
			"identity_public_key": []byte("short"),
		})
		Expect(err).NotTo(HaveOccurred())
		request := httptest.NewRequest(http.MethodPost, "/v1/device-pairings", bytes.NewReader(requestBody))
		response := httptest.NewRecorder()

		handler.ServeHTTP(response, request)

		Expect(response.Code).To(Equal(http.StatusUnprocessableEntity))
		Expect(repository.pairing.DeviceUUID).To(BeEmpty())
	})

	It("rejects malformed JSON and opaque credentials", func() {
		malformed := httptest.NewRequest(http.MethodPost, "/v1/device-pairings", bytes.NewBufferString(`{`))
		malformedResponse := httptest.NewRecorder()
		handler.ServeHTTP(malformedResponse, malformed)
		Expect(malformedResponse.Code).To(Equal(http.StatusBadRequest))

		invalidTokenBody, err := json.Marshal(map[string]any{
			"pairing_token":       "not-an-opaque-token",
			"device_uuid":         "01900000-0000-7000-8000-000000000001",
			"identity_public_key": bytes.Repeat([]byte{7}, ed25519.PublicKeySize),
		})
		Expect(err).NotTo(HaveOccurred())
		invalidToken := httptest.NewRequest(http.MethodPost, "/v1/device-pairings", bytes.NewReader(invalidTokenBody))
		invalidTokenResponse := httptest.NewRecorder()
		handler.ServeHTTP(invalidTokenResponse, invalidToken)
		Expect(invalidTokenResponse.Code).To(Equal(http.StatusUnauthorized))

		pairingToken, err := auth.NewOpaqueToken()
		Expect(err).NotTo(HaveOccurred())
		invalidCredentialBody, err := json.Marshal(map[string]any{
			"pairing_token":       pairingToken,
			"device_uuid":         "01900000-0000-7000-8000-000000000001",
			"identity_public_key": bytes.Repeat([]byte{7}, ed25519.PublicKeySize),
			"device_credential":   "invalid",
		})
		Expect(err).NotTo(HaveOccurred())
		invalidCredential := httptest.NewRequest(http.MethodPost, "/v1/device-pairings", bytes.NewReader(invalidCredentialBody))
		invalidCredentialResponse := httptest.NewRecorder()
		handler.ServeHTTP(invalidCredentialResponse, invalidCredential)
		Expect(invalidCredentialResponse.Code).To(Equal(http.StatusUnprocessableEntity))
		Expect(repository.pairing.DeviceUUID).To(BeEmpty())
	})

	DescribeTable("maps pairing conflicts without leaking credentials",
		func(repositoryError error, expectedStatus int) {
			pairingToken, err := auth.NewOpaqueToken()
			Expect(err).NotTo(HaveOccurred())
			credential, err := auth.NewOpaqueToken()
			Expect(err).NotTo(HaveOccurred())
			repository.pairingError = repositoryError
			requestBody, err := json.Marshal(map[string]any{
				"pairing_token":       pairingToken,
				"device_uuid":         "01900000-0000-7000-8000-000000000001",
				"profile_ciphertext":  []byte("encrypted"),
				"identity_public_key": bytes.Repeat([]byte{7}, ed25519.PublicKeySize),
				"device_credential":   credential,
			})
			Expect(err).NotTo(HaveOccurred())
			request := httptest.NewRequest(http.MethodPost, "/v1/device-pairings", bytes.NewReader(requestBody))
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			Expect(response.Code).To(Equal(expectedStatus))
			Expect(response.Body.String()).NotTo(ContainSubstring("credential"))
		},
		Entry("used token", domain.ErrInvalidPairingToken, http.StatusUnauthorized),
		Entry("duplicate device", domain.ErrDeviceExists, http.StatusConflict),
		Entry("exhausted IDs", domain.ErrNoNumericID, http.StatusConflict),
		Entry("storage failure", errors.New("database unavailable"), http.StatusInternalServerError),
	)
})
