package api

import (
	"bytes"
	"context"
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

type fakeRepository struct {
	devices                     []store.DeviceRecord
	listDevicesError            error
	updatedDeviceID             string
	updatedNumericID            int
	updateNumericIDError        error
	insertSequence              int64
	insertCreated               bool
	insertError                 error
	insertedEnvelope            domain.EventEnvelope
	insertedExpiry              time.Time
	listedEvents                []domain.EventEnvelope
	listEventsError             error
	listEventsAfter             int64
	listEventsLimit             int
	listEventsNow               time.Time
	credential                  store.OwnerCredential
	credentialError             error
	sessionPair                 auth.TokenPair
	createSessionError          error
	rotatedRefresh              string
	rotateSessionError          error
	revokedAccess               string
	revokeSessionError          error
	ownerID                     string
	ownerAuthError              error
	pairingOwnerID              string
	pairingTokenHash            []byte
	pairingExpiresAt            time.Time
	pairingTokenError           error
	pairing                     store.DevicePairing
	pairingRecord               store.DeviceRecord
	pairingError                error
	devicePublicKey             []byte
	deviceAuthError             error
	authenticatedDeviceID       string
	authenticatedCredential     string
	enqueuedCommand             domain.EncryptedCommand
	enqueueCreated              bool
	enqueueError                error
	claimedCommand              domain.EncryptedCommand
	claimDeviceID               string
	claimNow                    time.Time
	claimLeaseUntil             time.Time
	claimError                  error
	completedDeviceID           string
	completedCommandID          string
	completedResult             domain.EncryptedCommandResult
	completedAt                 time.Time
	completeError               error
	commandStatus               domain.CommandStatus
	statusDeviceID              string
	statusCommandID             string
	statusError                 error
	updatedCommandKeyDevice     string
	updatedCommandKey           []byte
	updateCommandKeyError       error
	installationOwnerID         string
	installation                store.AppInstallationRegistration
	installationRecord          store.AppInstallationRecord
	installationCreated         bool
	installationNow             time.Time
	installationError           error
	installationRecipients      []store.AppInstallationRecipient
	installationListError       error
	pushTokenOwnerID            string
	pushTokenInstallationID     string
	pushTokenEnvironment        string
	pushTokenKind               string
	pushToken                   []byte
	pushTokenNow                time.Time
	pushTokenError              error
	incomingCall                domain.IncomingCallNotification
	incomingCallExpiry          time.Time
	incomingCallCreated         bool
	incomingCallError           error
	incomingClaim               domain.IncomingCallClaim
	incomingClaimOwnerID        string
	incomingClaimNotificationID string
	incomingClaimInstallationID string
	incomingClaimNow            time.Time
	incomingClaimError          error
}

func (fake *fakeRepository) UpsertPushToken(
	_ context.Context,
	ownerID string,
	installationID string,
	environment string,
	kind string,
	token []byte,
	now time.Time,
) error {
	fake.pushTokenOwnerID = ownerID
	fake.pushTokenInstallationID = installationID
	fake.pushTokenEnvironment = environment
	fake.pushTokenKind = kind
	fake.pushToken = append([]byte(nil), token...)
	fake.pushTokenNow = now
	return fake.pushTokenError
}

func (fake *fakeRepository) ListDevices(context.Context) ([]store.DeviceRecord, error) {
	return fake.devices, fake.listDevicesError
}

func (fake *fakeRepository) UpdateNumericID(_ context.Context, deviceID string, numericID int) error {
	fake.updatedDeviceID = deviceID
	fake.updatedNumericID = numericID
	return fake.updateNumericIDError
}

func (fake *fakeRepository) InsertEvent(_ context.Context, envelope domain.EventEnvelope, expiresAt time.Time) (int64, bool, error) {
	fake.insertedEnvelope = envelope
	fake.insertedExpiry = expiresAt
	return fake.insertSequence, fake.insertCreated, fake.insertError
}

func (fake *fakeRepository) InsertIncomingCall(_ context.Context, notification domain.IncomingCallNotification, expiresAt time.Time) (bool, error) {
	fake.incomingCall = notification
	fake.incomingCallExpiry = expiresAt
	return fake.incomingCallCreated, fake.incomingCallError
}

func (fake *fakeRepository) ClaimIncomingCall(_ context.Context, ownerID string, notificationID string, installationID string, now time.Time) (domain.IncomingCallClaim, error) {
	fake.incomingClaimOwnerID = ownerID
	fake.incomingClaimNotificationID = notificationID
	fake.incomingClaimInstallationID = installationID
	fake.incomingClaimNow = now
	return fake.incomingClaim, fake.incomingClaimError
}

func (fake *fakeRepository) ListEvents(_ context.Context, afterSequence int64, limit int, now time.Time) ([]domain.EventEnvelope, error) {
	fake.listEventsAfter = afterSequence
	fake.listEventsLimit = limit
	fake.listEventsNow = now
	return fake.listedEvents, fake.listEventsError
}

func (fake *fakeRepository) OwnerCredentialByUsername(context.Context, string) (store.OwnerCredential, error) {
	return fake.credential, fake.credentialError
}

func (fake *fakeRepository) CreateSession(_ context.Context, _ string, pair auth.TokenPair) error {
	fake.sessionPair = pair
	return fake.createSessionError
}

func (fake *fakeRepository) RotateSession(_ context.Context, refresh string, pair auth.TokenPair, _ time.Time) error {
	fake.rotatedRefresh = refresh
	fake.sessionPair = pair
	return fake.rotateSessionError
}

func (fake *fakeRepository) RevokeSession(_ context.Context, access string, _ time.Time) error {
	fake.revokedAccess = access
	return fake.revokeSessionError
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
	return fake.pairingTokenError
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

func (fake *fakeRepository) EnqueueCommand(_ context.Context, encrypted domain.EncryptedCommand) (bool, error) {
	fake.enqueuedCommand = encrypted
	return fake.enqueueCreated, fake.enqueueError
}

func (fake *fakeRepository) ClaimCommand(_ context.Context, deviceID string, now time.Time, leaseUntil time.Time) (domain.EncryptedCommand, error) {
	fake.claimDeviceID = deviceID
	fake.claimNow = now
	fake.claimLeaseUntil = leaseUntil
	return fake.claimedCommand, fake.claimError
}

func (fake *fakeRepository) CompleteCommand(_ context.Context, deviceID string, commandID string, result domain.EncryptedCommandResult, completedAt time.Time) error {
	fake.completedDeviceID = deviceID
	fake.completedCommandID = commandID
	fake.completedResult = result
	fake.completedAt = completedAt
	return fake.completeError
}

func (fake *fakeRepository) CommandStatus(_ context.Context, deviceID string, commandID string, _ time.Time) (domain.CommandStatus, error) {
	fake.statusDeviceID = deviceID
	fake.statusCommandID = commandID
	return fake.commandStatus, fake.statusError
}

func (fake *fakeRepository) UpdateDeviceCommandPublicKey(_ context.Context, deviceID string, publicKey []byte) error {
	fake.updatedCommandKeyDevice = deviceID
	fake.updatedCommandKey = append([]byte(nil), publicKey...)
	return fake.updateCommandKeyError
}

func (fake *fakeRepository) RegisterAppInstallation(_ context.Context, ownerID string, registration store.AppInstallationRegistration, now time.Time) (store.AppInstallationRecord, bool, error) {
	fake.installationOwnerID = ownerID
	fake.installation = registration
	fake.installationNow = now
	return fake.installationRecord, fake.installationCreated, fake.installationError
}

func (fake *fakeRepository) ListApprovedAppInstallationRecipients(context.Context) ([]store.AppInstallationRecipient, error) {
	return fake.installationRecipients, fake.installationListError
}

var _ = Describe("HTTP API", func() {
	var (
		repository *fakeRepository
		handler    http.Handler
	)

	BeforeEach(func() {
		repository = &fakeRepository{ownerID: "owner-1"}
		handler = Handler(repository, Config{
			PublicBaseURL: "https://deck.example/",
			MediaUDPPort:  49152,
			MediaTCPPort:  49153,
		})
	})

	perform := func(method string, path string, body string, accessToken string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		if accessToken != "" {
			request.Header.Set("Authorization", "Bearer "+accessToken)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	It("reports health and discovery metadata", func() {
		health := perform(http.MethodGet, "/healthz", "", "")
		Expect(health.Code).To(Equal(http.StatusOK))
		Expect(health.Body.String()).To(MatchJSON(`{"status":"ok"}`))

		discovery := perform(http.MethodGet, "/.well-known/sim-deck", "", "")
		Expect(discovery.Code).To(Equal(http.StatusOK))
		var body map[string]any
		Expect(json.Unmarshal(discovery.Body.Bytes(), &body)).To(Succeed())
		Expect(body["api_version"]).To(Equal("v1"))
		Expect(body["websocket_url"]).To(Equal("https://deck.example/v1/events/ws"))
		Expect(body["media"]).To(Equal(map[string]any{"udp_port": float64(49152), "tcp_port": float64(49153)}))
	})

	It("lists devices for an authenticated owner", func() {
		repository.devices = []store.DeviceRecord{{ID: "device-1", NumericID: 1}}

		response := perform(http.MethodGet, "/v1/devices", "", "access-token")

		Expect(response.Code).To(Equal(http.StatusOK))
		Expect(response.Body.String()).To(ContainSubstring(`"device_uuid":"device-1"`))
	})

	It("rejects missing and invalid owner sessions", func() {
		Expect(perform(http.MethodGet, "/v1/devices", "", "").Code).To(Equal(http.StatusUnauthorized))
		repository.ownerAuthError = errors.New("expired")
		Expect(perform(http.MethodGet, "/v1/devices", "", "access-token").Code).To(Equal(http.StatusUnauthorized))
	})

	It("maps a device-list storage failure", func() {
		repository.listDevicesError = errors.New("database unavailable")

		response := perform(http.MethodGet, "/v1/devices", "", "access-token")

		Expect(response.Code).To(Equal(http.StatusInternalServerError))
		Expect(response.Body.String()).To(ContainSubstring("storage_error"))
	})

	It("updates a device numeric ID", func() {
		response := perform(http.MethodPatch, "/v1/devices/device-1/numeric-id", `{"numeric_id":2}`, "access-token")

		Expect(response.Code).To(Equal(http.StatusNoContent))
		Expect(repository.updatedDeviceID).To(Equal("device-1"))
		Expect(repository.updatedNumericID).To(Equal(2))
	})

	DescribeTable("maps numeric-ID update failures",
		func(body string, repositoryError error, expectedStatus int, expectedCode string) {
			repository.updateNumericIDError = repositoryError
			response := perform(http.MethodPatch, "/v1/devices/device-1/numeric-id", body, "access-token")
			Expect(response.Code).To(Equal(expectedStatus))
			Expect(response.Body.String()).To(ContainSubstring(expectedCode))
		},
		Entry("malformed JSON", `{`, nil, http.StatusBadRequest, "invalid_request"),
		Entry("invalid identifier", `{"numeric_id":0}`, domain.ErrInvalidNumericID, http.StatusUnprocessableEntity, "invalid_numeric_id"),
		Entry("duplicate identifier", `{"numeric_id":2}`, domain.ErrDuplicateNumericID, http.StatusConflict, "duplicate_numeric_id"),
		Entry("missing device", `{"numeric_id":2}`, pgx.ErrNoRows, http.StatusNotFound, "device_not_found"),
		Entry("storage failure", `{"numeric_id":2}`, errors.New("database unavailable"), http.StatusInternalServerError, "storage_error"),
	)

	It("creates a bootstrap session", func() {
		passwordHash, err := auth.HashPassword("correct horse battery staple")
		Expect(err).NotTo(HaveOccurred())
		repository.credential = store.OwnerCredential{ID: "owner-1", PasswordHash: passwordHash}

		response := perform(http.MethodPost, "/v1/auth/bootstrap", `{"username":"admin","password":"correct horse battery staple"}`, "")

		Expect(response.Code).To(Equal(http.StatusCreated))
		Expect(repository.sessionPair.AccessToken).NotTo(BeEmpty())
		Expect(repository.sessionPair.RefreshToken).NotTo(BeEmpty())
	})

	DescribeTable("rejects invalid bootstrap logins",
		func(credential store.OwnerCredential, credentialError error, requestBody string) {
			repository.credential = credential
			repository.credentialError = credentialError
			response := perform(http.MethodPost, "/v1/auth/bootstrap", requestBody, "")
			Expect(response.Code).To(Equal(http.StatusUnauthorized))
			Expect(repository.sessionPair.AccessToken).To(BeEmpty())
		},
		Entry("unknown owner", store.OwnerCredential{}, pgx.ErrNoRows, `{"username":"admin","password":"password"}`),
		Entry("missing password hash", store.OwnerCredential{ID: "owner-1"}, nil, `{"username":"admin","password":"password"}`),
		Entry("malformed password hash", store.OwnerCredential{ID: "owner-1", PasswordHash: "invalid"}, nil, `{"username":"admin","password":"password"}`),
		Entry("wrong password", store.OwnerCredential{ID: "owner-1", PasswordHash: "$argon2id$v=19$m=8192,t=1,p=1$MTIzNDU2Nzg$MTIzNDU2Nzg5MDEyMzQ1Ng"}, nil, `{"username":"admin","password":"wrong"}`),
		Entry("disabled password", func() store.OwnerCredential {
			disabledAt := time.Now()
			return store.OwnerCredential{ID: "owner-1", PasswordHash: "unused", PasswordDisabledAt: &disabledAt}
		}(), nil, `{"username":"admin","password":"password"}`),
	)

	It("rejects malformed bootstrap JSON and session storage failures", func() {
		Expect(perform(http.MethodPost, "/v1/auth/bootstrap", `{`, "").Code).To(Equal(http.StatusBadRequest))
		passwordHash, err := auth.HashPassword("correct horse battery staple")
		Expect(err).NotTo(HaveOccurred())
		repository.credential = store.OwnerCredential{ID: "owner-1", PasswordHash: passwordHash}
		repository.createSessionError = errors.New("database unavailable")
		Expect(perform(http.MethodPost, "/v1/auth/bootstrap", `{"username":"admin","password":"correct horse battery staple"}`, "").Code).To(Equal(http.StatusInternalServerError))
	})

	It("refreshes and revokes session credentials", func() {
		refresh := perform(http.MethodPost, "/v1/auth/refresh", `{"refresh_token":"old-refresh"}`, "")
		Expect(refresh.Code).To(Equal(http.StatusOK))
		Expect(repository.rotatedRefresh).To(Equal("old-refresh"))

		logout := perform(http.MethodPost, "/v1/auth/logout", "", "access-token")
		Expect(logout.Code).To(Equal(http.StatusNoContent))
		Expect(repository.revokedAccess).To(Equal("access-token"))
	})

	It("rejects malformed or failed session refreshes", func() {
		Expect(perform(http.MethodPost, "/v1/auth/refresh", `{`, "").Code).To(Equal(http.StatusBadRequest))
		repository.rotateSessionError = pgx.ErrNoRows
		response := perform(http.MethodPost, "/v1/auth/refresh", `{"refresh_token":"expired"}`, "")
		Expect(response.Code).To(Equal(http.StatusUnauthorized))
		Expect(response.Body.String()).To(ContainSubstring("invalid_refresh_token"))
	})

	It("requires a valid access token for logout", func() {
		Expect(perform(http.MethodPost, "/v1/auth/logout", "", "").Code).To(Equal(http.StatusUnauthorized))
		repository.revokeSessionError = pgx.ErrNoRows
		Expect(perform(http.MethodPost, "/v1/auth/logout", "", "expired").Code).To(Equal(http.StatusUnauthorized))
	})

	DescribeTable("parses configured ports",
		func(value string, fallback int, expected int, succeeds bool) {
			port, err := ParsePort(value, fallback)
			if succeeds {
				Expect(err).NotTo(HaveOccurred())
				Expect(port).To(Equal(expected))
				return
			}
			Expect(err).To(HaveOccurred())
			Expect(port).To(BeZero())
		},
		Entry("fallback", " ", 8080, 8080, true),
		Entry("configured", "49152", 0, 49152, true),
		Entry("not numeric", "udp", 0, 0, false),
		Entry("below range", "0", 0, 0, false),
		Entry("above range", "65536", 0, 0, false),
	)
})
