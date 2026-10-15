package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"sim-deck-server/internal/domain"
	"sim-deck-server/internal/store"
)

var _ = Describe("App installation API", func() {
	const installationID = "01900000-0000-7000-8000-000000000101"
	const deviceID = "01900000-0000-7000-8000-000000000001"
	var (
		now        time.Time
		repository *fakeRepository
		handler    http.Handler
	)

	BeforeEach(func() {
		now = time.Date(2026, 10, 16, 13, 0, 0, 0, time.UTC)
		repository = &fakeRepository{ownerID: "owner-1"}
		handler = Handler(repository, Config{Now: func() time.Time { return now }})
	})

	requestBody := func(id string, publicKey []byte, keyAgreementPublicKey []byte, wrappedKey []byte) []byte {
		body, err := json.Marshal(map[string]any{
			"installation_id":          id,
			"identity_public_key":      publicKey,
			"key_agreement_public_key": keyAgreementPublicKey,
			"wrapped_vault_key":        wrappedKey,
		})
		Expect(err).NotTo(HaveOccurred())
		return body
	}

	perform := func(body []byte, accessToken string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/v1/app-installations", bytes.NewReader(body))
		if accessToken != "" {
			request.Header.Set("Authorization", "Bearer "+accessToken)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	It("registers the authenticated installation without exposing private identity data", func() {
		publicKey := bytes.Repeat([]byte{4}, installationPublicKeyBytes)
		keyAgreementPublicKey := bytes.Repeat([]byte{8}, installationPublicKeyBytes)
		wrappedKey := []byte("wrapped vault key")
		approvedAt := now
		repository.installationRecord = store.AppInstallationRecord{
			ID:                    installationID,
			IdentityPublicKey:     publicKey,
			KeyAgreementPublicKey: keyAgreementPublicKey,
			WrappedVaultKey:       wrappedKey,
			ApprovedAt:            &approvedAt,
			CreatedAt:             now,
		}
		repository.installationCreated = true

		response := perform(requestBody(installationID, publicKey, keyAgreementPublicKey, wrappedKey), "owner-token")

		Expect(response.Code).To(Equal(http.StatusCreated))
		Expect(repository.installationOwnerID).To(Equal("owner-1"))
		Expect(repository.installation.ID).To(Equal(installationID))
		Expect(repository.installation.IdentityPublicKey).To(Equal(publicKey))
		Expect(repository.installation.KeyAgreementPublicKey).To(Equal(keyAgreementPublicKey))
		Expect(repository.installation.WrappedVaultKey).To(Equal(wrappedKey))
		Expect(repository.installationNow).To(Equal(now))
		Expect(response.Body.String()).To(ContainSubstring(`"created":true`))
		Expect(response.Body.String()).NotTo(ContainSubstring("private_key"))
	})

	It("returns an idempotent registration without creating a second installation", func() {
		publicKey := bytes.Repeat([]byte{5}, installationPublicKeyBytes)
		keyAgreementPublicKey := bytes.Repeat([]byte{9}, installationPublicKeyBytes)
		repository.installationRecord = store.AppInstallationRecord{ID: installationID, IdentityPublicKey: publicKey, KeyAgreementPublicKey: keyAgreementPublicKey, CreatedAt: now}

		response := perform(requestBody(installationID, publicKey, keyAgreementPublicKey, nil), "owner-token")

		Expect(response.Code).To(Equal(http.StatusOK))
		Expect(response.Body.String()).To(ContainSubstring(`"created":false`))
	})

	It("requires authentication and validates identity material before storage", func() {
		validKey := bytes.Repeat([]byte{6}, installationPublicKeyBytes)
		validAgreementKey := bytes.Repeat([]byte{7}, installationPublicKeyBytes)
		Expect(perform(requestBody(installationID, validKey, validAgreementKey, nil), "").Code).To(Equal(http.StatusUnauthorized))
		Expect(perform(requestBody("not-a-uuid", validKey, validAgreementKey, nil), "owner-token").Code).To(Equal(http.StatusUnprocessableEntity))
		Expect(perform(requestBody(installationID, []byte("short"), validAgreementKey, nil), "owner-token").Code).To(Equal(http.StatusUnprocessableEntity))
		Expect(perform(requestBody(installationID, validKey, []byte("short"), nil), "owner-token").Code).To(Equal(http.StatusUnprocessableEntity))
		Expect(perform(requestBody(installationID, validKey, validAgreementKey, bytes.Repeat([]byte{1}, maxWrappedVaultKeyBytes+1)), "owner-token").Code).To(Equal(http.StatusUnprocessableEntity))
		Expect(repository.installation.ID).To(BeEmpty())
	})

	It("rejects malformed JSON", func() {
		response := perform([]byte("{"), "owner-token")

		Expect(response.Code).To(Equal(http.StatusBadRequest))
		Expect(repository.installation.ID).To(BeEmpty())
	})

	DescribeTable("maps repository failures",
		func(repositoryError error, expectedStatus int, expectedCode string) {
			repository.installationError = repositoryError
			response := perform(
				requestBody(
					installationID,
					bytes.Repeat([]byte{7}, installationPublicKeyBytes),
					bytes.Repeat([]byte{8}, installationPublicKeyBytes),
					nil,
				),
				"owner-token",
			)

			Expect(response.Code).To(Equal(expectedStatus))
			Expect(response.Body.String()).To(ContainSubstring(expectedCode))
		},
		Entry("identity conflict", domain.ErrInstallationConflict, http.StatusConflict, "installation_conflict"),
		Entry("storage failure", errors.New("database unavailable"), http.StatusInternalServerError, "storage_error"),
	)

	It("lists only public trusted-recipient metadata for an authenticated device", func() {
		repository.installationRecipients = []store.AppInstallationRecipient{{
			InstallationID:        installationID,
			KeyAgreementPublicKey: bytes.Repeat([]byte{12}, installationPublicKeyBytes),
		}}
		request := httptest.NewRequest(
			http.MethodGet,
			"/v1/device-installations?device_uuid="+deviceID,
			nil,
		)
		request.Header.Set("Authorization", "Bearer device-secret")
		response := httptest.NewRecorder()

		handler.ServeHTTP(response, request)

		Expect(response.Code).To(Equal(http.StatusOK))
		Expect(repository.authenticatedDeviceID).To(Equal(deviceID))
		Expect(repository.authenticatedCredential).To(Equal("device-secret"))
		Expect(response.Body.String()).To(ContainSubstring(installationID))
		Expect(response.Body.String()).To(ContainSubstring("key_agreement_public_key"))
		Expect(response.Body.String()).NotTo(ContainSubstring("private"))
	})

	It("protects trusted-recipient listing with device authentication", func() {
		missingCredential := httptest.NewRequest(
			http.MethodGet,
			"/v1/device-installations?device_uuid="+deviceID,
			nil,
		)
		missingResponse := httptest.NewRecorder()
		handler.ServeHTTP(missingResponse, missingCredential)
		Expect(missingResponse.Code).To(Equal(http.StatusUnauthorized))

		repository.deviceAuthError = errors.New("revoked")
		revoked := httptest.NewRequest(
			http.MethodGet,
			"/v1/device-installations?device_uuid="+deviceID,
			nil,
		)
		revoked.Header.Set("Authorization", "Bearer revoked")
		revokedResponse := httptest.NewRecorder()
		handler.ServeHTTP(revokedResponse, revoked)
		Expect(revokedResponse.Code).To(Equal(http.StatusUnauthorized))
	})

	It("maps trusted-recipient storage failures", func() {
		repository.installationListError = errors.New("database unavailable")
		request := httptest.NewRequest(
			http.MethodGet,
			"/v1/device-installations?device_uuid="+deviceID,
			nil,
		)
		request.Header.Set("Authorization", "Bearer device-secret")
		response := httptest.NewRecorder()

		handler.ServeHTTP(response, request)

		Expect(response.Code).To(Equal(http.StatusInternalServerError))
		Expect(response.Body.String()).To(ContainSubstring("storage_error"))
	})
})
