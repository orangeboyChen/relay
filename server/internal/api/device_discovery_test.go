package api

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/jackc/pgx/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"sim-deck-server/internal/auth"
	"sim-deck-server/internal/domain"
	"sim-deck-server/internal/store"
)

var _ = Describe("App-controlled device discovery API", func() {
	const deviceID = "01900000-0000-7000-8000-000000000001"
	var (
		repository *fakeRepository
		handler    http.Handler
		now        time.Time
		privateKey ed25519.PrivateKey
		publicKey  ed25519.PublicKey
		commandKey []byte
		credential string
	)

	BeforeEach(func() {
		now = time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
		privateKey = ed25519.NewKeyFromSeed(bytes.Repeat([]byte{1}, ed25519.SeedSize))
		publicKey = privateKey.Public().(ed25519.PublicKey)
		commandKey = bytes.Repeat([]byte{2}, commandPublicKeyBytes)
		credential = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32))
		repository = &fakeRepository{ownerID: "owner-1"}
		handler = Handler(repository, Config{PublicBaseURL: "https://deck.example", Now: func() time.Time { return now }})
	})

	perform := func(method string, path string, body any, token string) *httptest.ResponseRecorder {
		var encoded []byte
		if body != nil {
			var err error
			encoded, err = json.Marshal(body)
			Expect(err).NotTo(HaveOccurred())
		}
		request := httptest.NewRequest(method, path, bytes.NewReader(encoded))
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	discoveryBody := func() map[string]any {
		proof := deviceDiscoverySignedData(deviceID, publicKey, commandKey, credential)
		return map[string]any{
			"device_uuid": deviceID, "identity_public_key": publicKey,
			"command_public_key": commandKey, "pending_credential": credential,
			"signature": ed25519.Sign(privateKey, proof),
		}
	}

	It("opens a ten-minute owner-controlled discovery window", func() {
		repository.discoverySession = store.DeviceDiscoverySession{
			OwnerID: "owner-1", OpenedAt: now, ExpiresAt: now.Add(deviceDiscoveryLifetime),
		}

		response := perform(http.MethodPost, "/v1/device-discovery-session", nil, "owner-token")

		Expect(response.Code).To(Equal(http.StatusCreated))
		Expect(response.Body.String()).To(ContainSubstring(`"active":true`))
		Expect(repository.discoveryOwnerID).To(Equal("owner-1"))
		Expect(repository.discoveryOpenedAt).To(Equal(now))
		Expect(repository.discoveryExpiresAt).To(Equal(now.Add(10 * time.Minute)))
	})

	It("reads, closes, and lists discovery state for the owner", func() {
		repository.discoverySession = store.DeviceDiscoverySession{OwnerID: "owner-1", OpenedAt: now, ExpiresAt: now.Add(time.Minute)}
		repository.discoveryRecords = []store.DeviceDiscoveryRecord{{DeviceUUID: deviceID, Status: store.DeviceDiscoveryPending}}

		status := perform(http.MethodGet, "/v1/device-discovery-session", nil, "owner-token")
		list := perform(http.MethodGet, "/v1/device-discoveries", nil, "owner-token")
		closed := perform(http.MethodDelete, "/v1/device-discovery-session", nil, "owner-token")

		Expect(status.Code).To(Equal(http.StatusOK))
		Expect(status.Body.String()).To(ContainSubstring(`"active":true`))
		Expect(list.Code).To(Equal(http.StatusOK))
		Expect(list.Body.String()).To(ContainSubstring(deviceID))
		Expect(closed.Code).To(Equal(http.StatusNoContent))
		Expect(repository.discoveryDecisionAt).To(Equal(now))
	})

	It("reports discovery as inactive before a window has been opened", func() {
		repository.getDiscoveryError = pgx.ErrNoRows

		response := perform(http.MethodGet, "/v1/device-discovery-session", nil, "owner-token")

		Expect(response.Code).To(Equal(http.StatusOK))
		Expect(response.Body.String()).To(MatchJSON(`{"active":false}`))
	})

	DescribeTable("hides owner discovery storage failures",
		func(method string, path string, configure func()) {
			configure()
			response := perform(method, path, nil, "owner-token")
			Expect(response.Code).To(Equal(http.StatusInternalServerError))
			Expect(response.Body.String()).To(ContainSubstring("storage_error"))
			Expect(response.Body.String()).NotTo(ContainSubstring("database unavailable"))
		},
		Entry("open", http.MethodPost, "/v1/device-discovery-session", func() {
			repository.openDiscoveryError = errors.New("database unavailable")
		}),
		Entry("read", http.MethodGet, "/v1/device-discovery-session", func() {
			repository.getDiscoveryError = errors.New("database unavailable")
		}),
		Entry("close", http.MethodDelete, "/v1/device-discovery-session", func() {
			repository.closeDiscoveryError = errors.New("database unavailable")
		}),
		Entry("list", http.MethodGet, "/v1/device-discoveries", func() {
			repository.listDiscoveriesError = errors.New("database unavailable")
		}),
	)

	DescribeTable("protects owner discovery operations",
		func(method string, path string) {
			response := perform(method, path, nil, "")
			Expect(response.Code).To(Equal(http.StatusUnauthorized))
		},
		Entry("open", http.MethodPost, "/v1/device-discovery-session"),
		Entry("read", http.MethodGet, "/v1/device-discovery-session"),
		Entry("close", http.MethodDelete, "/v1/device-discovery-session"),
		Entry("list", http.MethodGet, "/v1/device-discoveries"),
		Entry("approve", http.MethodPost, "/v1/device-discoveries/"+deviceID+"/approve"),
		Entry("reject", http.MethodDelete, "/v1/device-discoveries/"+deviceID),
	)

	It("accepts a signed device candidate without storing its pending secret", func() {
		repository.discoveryRecord = store.DeviceDiscoveryRecord{DeviceUUID: deviceID, Status: store.DeviceDiscoveryPending}

		response := perform(http.MethodPost, "/v1/device-discoveries", discoveryBody(), "")

		Expect(response.Code).To(Equal(http.StatusAccepted))
		Expect(repository.discoveryRequest.DeviceUUID).To(Equal(deviceID))
		Expect(repository.discoveryRequest.IdentityPublicKey).To(Equal([]byte(publicKey)))
		Expect(repository.discoveryRequest.CommandPublicKey).To(Equal(commandKey))
		hash, err := auth.TokenHash(credential)
		Expect(err).NotTo(HaveOccurred())
		Expect(repository.discoveryRequest.CredentialHash).To(Equal(hash[:]))
		Expect(response.Body.String()).NotTo(ContainSubstring(credential))
	})

	It("rejects malformed candidate JSON before repository access", func() {
		request := httptest.NewRequest(http.MethodPost, "/v1/device-discoveries", bytes.NewBufferString("{"))
		response := httptest.NewRecorder()

		handler.ServeHTTP(response, request)

		Expect(response.Code).To(Equal(http.StatusBadRequest))
		Expect(response.Body.String()).To(ContainSubstring("invalid_request"))
		Expect(repository.discoveryRequest.DeviceUUID).To(BeEmpty())
	})

	It("rejects a candidate whose signed identity was modified", func() {
		body := discoveryBody()
		body["command_public_key"] = bytes.Repeat([]byte{9}, commandPublicKeyBytes)

		response := perform(http.MethodPost, "/v1/device-discoveries", body, "")

		Expect(response.Code).To(Equal(http.StatusUnauthorized))
		Expect(response.Body.String()).To(ContainSubstring("invalid_device_proof"))
		Expect(repository.discoveryRequest.DeviceUUID).To(BeEmpty())
	})

	DescribeTable("validates candidate discovery material",
		func(mutate func(map[string]any), expectedStatus int) {
			body := discoveryBody()
			mutate(body)
			response := perform(http.MethodPost, "/v1/device-discoveries", body, "")
			Expect(response.Code).To(Equal(expectedStatus), response.Body.String())
		},
		Entry("device UUID", func(body map[string]any) { body["device_uuid"] = "invalid" }, http.StatusUnprocessableEntity),
		Entry("identity key", func(body map[string]any) { body["identity_public_key"] = []byte{1} }, http.StatusUnprocessableEntity),
		Entry("command key", func(body map[string]any) { body["command_public_key"] = []byte{1} }, http.StatusUnprocessableEntity),
		Entry("signature", func(body map[string]any) { body["signature"] = []byte{1} }, http.StatusUnprocessableEntity),
		Entry("credential", func(body map[string]any) { body["pending_credential"] = "short" }, http.StatusUnprocessableEntity),
	)

	DescribeTable("maps candidate submission failures",
		func(repositoryError error, expectedStatus int, expectedCode string) {
			repository.submitDiscoveryError = repositoryError
			response := perform(http.MethodPost, "/v1/device-discoveries", discoveryBody(), "")
			Expect(response.Code).To(Equal(expectedStatus))
			Expect(response.Body.String()).To(ContainSubstring(expectedCode))
		},
		Entry("closed", domain.ErrDeviceDiscoveryClosed, http.StatusConflict, "device_discovery_closed"),
		Entry("conflict", domain.ErrDeviceDiscoveryConflict, http.StatusConflict, "device_discovery_conflict"),
		Entry("limit", domain.ErrDeviceDiscoveryLimit, http.StatusTooManyRequests, "device_discovery_limit"),
		Entry("paired", domain.ErrDeviceExists, http.StatusConflict, "device_exists"),
		Entry("storage", errors.New("database unavailable"), http.StatusInternalServerError, "storage_error"),
	)

	It("approves and rejects selected candidates", func() {
		repository.discoveryDevice = store.DeviceRecord{ID: deviceID, NumericID: 2}

		approved := perform(http.MethodPost, "/v1/device-discoveries/"+deviceID+"/approve", nil, "owner-token")
		rejected := perform(http.MethodDelete, "/v1/device-discoveries/"+deviceID, nil, "owner-token")

		Expect(approved.Code).To(Equal(http.StatusOK))
		Expect(approved.Body.String()).To(ContainSubstring(`"numeric_id":2`))
		Expect(rejected.Code).To(Equal(http.StatusNoContent))
		Expect(repository.discoveryOwnerID).To(Equal("owner-1"))
		Expect(repository.discoveryDeviceID).To(Equal(deviceID))
		Expect(repository.discoveryDecisionAt).To(Equal(now))
	})

	DescribeTable("validates owner discovery decision identifiers",
		func(method string, suffix string) {
			response := perform(method, "/v1/device-discoveries/not-a-uuid"+suffix, nil, "owner-token")
			Expect(response.Code).To(Equal(http.StatusUnprocessableEntity))
			Expect(response.Body.String()).To(ContainSubstring("invalid_device_discovery"))
		},
		Entry("approve", http.MethodPost, "/approve"),
		Entry("reject", http.MethodDelete, ""),
	)

	DescribeTable("maps approval failures",
		func(repositoryError error, expectedStatus int, expectedCode string) {
			repository.approveDiscoveryError = repositoryError
			response := perform(http.MethodPost, "/v1/device-discoveries/"+deviceID+"/approve", nil, "owner-token")
			Expect(response.Code).To(Equal(expectedStatus))
			Expect(response.Body.String()).To(ContainSubstring(expectedCode))
		},
		Entry("missing", pgx.ErrNoRows, http.StatusNotFound, "device_discovery_not_found"),
		Entry("expired", domain.ErrDeviceDiscoveryExpired, http.StatusGone, "device_discovery_expired"),
		Entry("rejected", domain.ErrDeviceDiscoveryRejected, http.StatusConflict, "device_discovery_rejected"),
		Entry("paired", domain.ErrDeviceExists, http.StatusConflict, "device_exists"),
		Entry("ids exhausted", domain.ErrNoNumericID, http.StatusConflict, "numeric_ids_exhausted"),
		Entry("storage", errors.New("database unavailable"), http.StatusInternalServerError, "storage_error"),
	)

	DescribeTable("maps rejection failures",
		func(repositoryError error, expectedStatus int, expectedCode string) {
			repository.rejectDiscoveryError = repositoryError
			response := perform(http.MethodDelete, "/v1/device-discoveries/"+deviceID, nil, "owner-token")
			Expect(response.Code).To(Equal(expectedStatus))
			Expect(response.Body.String()).To(ContainSubstring(expectedCode))
		},
		Entry("missing", pgx.ErrNoRows, http.StatusNotFound, "device_discovery_not_found"),
		Entry("expired", domain.ErrDeviceDiscoveryExpired, http.StatusGone, "device_discovery_expired"),
		Entry("paired", domain.ErrDeviceExists, http.StatusConflict, "device_exists"),
		Entry("storage", errors.New("database unavailable"), http.StatusInternalServerError, "storage_error"),
	)

	It("authenticates device status polling with only the pending credential hash", func() {
		repository.discoveryRecord = store.DeviceDiscoveryRecord{DeviceUUID: deviceID, Status: store.DeviceDiscoveryApproved, NumericID: 2}

		response := perform(http.MethodGet, "/v1/device-discoveries/"+deviceID+"/status", nil, credential)

		Expect(response.Code).To(Equal(http.StatusOK))
		Expect(response.Body.String()).To(ContainSubstring(`"status":"approved"`))
		hash, err := auth.TokenHash(credential)
		Expect(err).NotTo(HaveOccurred())
		Expect(repository.discoveryCredentialHash).To(Equal(hash[:]))
	})

	DescribeTable("rejects invalid status polling",
		func(path string, token string, repositoryError error, expectedStatus int) {
			repository.discoveryStatusError = repositoryError
			response := perform(http.MethodGet, path, nil, token)
			Expect(response.Code).To(Equal(expectedStatus), response.Body.String())
		},
		Entry("missing credential", "/v1/device-discoveries/"+deviceID+"/status", "", nil, http.StatusUnauthorized),
		Entry("invalid UUID", "/v1/device-discoveries/not-a-uuid/status", base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32)), nil, http.StatusUnauthorized),
		Entry("invalid token", "/v1/device-discoveries/"+deviceID+"/status", "short", nil, http.StatusUnauthorized),
		Entry("unknown token", "/v1/device-discoveries/"+deviceID+"/status", base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32)), pgx.ErrNoRows, http.StatusUnauthorized),
		Entry("storage failure", "/v1/device-discoveries/"+deviceID+"/status", base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32)), errors.New("database unavailable"), http.StatusInternalServerError),
	)
})
