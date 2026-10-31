package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"time"

	webauthnlib "github.com/go-webauthn/webauthn/webauthn"
	"github.com/jackc/pgx/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"sim-deck-server/internal/auth"
	"sim-deck-server/internal/domain"
	"sim-deck-server/internal/store"
)

type fakeRepository struct {
	devices                     []store.DeviceRecord
	sims                        []store.SIMRecord
	listDevicesError            error
	listSIMsError               error
	retiredSIMID                string
	retiredSIMAt                time.Time
	retireSIMError              error
	updatedDeviceID             string
	updatedNumericID            int
	updateNumericIDError        error
	updatedProfileDeviceID      string
	updatedProfileCiphertext    []byte
	updateDeviceProfileError    error
	updatedSIMNumericIDTarget   string
	updatedSIMNumericIDValue    int
	updateSIMNumericIDError     error
	updatedSIMProfileTarget     string
	updatedSIMProfileCiphertext []byte
	updateSIMProfileError       error
	revokedDeviceID             string
	revokedDeviceAt             time.Time
	revokeDeviceError           error
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
	listEventsCalls             chan int64
	listEventsMutex             sync.Mutex
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
	ownerAuthToken              string
	historySnapshot             domain.HistorySnapshot
	historySnapshotError        error
	historyWriteExpected        int64
	historyWriteCiphertext      []byte
	historyWriteAt              time.Time
	historyWriteError           error
	pairingOwnerID              string
	pairingTokenHash            []byte
	pairingExpiresAt            time.Time
	pairingTokenError           error
	pairing                     store.DevicePairing
	pairingTokenConsumedHash    []byte
	pairingRecord               store.DeviceRecord
	pairingError                error
	discoveryOwnerID            string
	discoveryOpenedAt           time.Time
	discoveryExpiresAt          time.Time
	discoverySession            store.DeviceDiscoverySession
	openDiscoveryError          error
	closeDiscoveryError         error
	getDiscoveryError           error
	discoveryRequest            store.DeviceDiscoveryRequest
	discoverySubmittedAt        time.Time
	discoveryRecord             store.DeviceDiscoveryRecord
	submitDiscoveryError        error
	discoveryRecords            []store.DeviceDiscoveryRecord
	listDiscoveriesError        error
	discoveryDeviceID           string
	discoveryDecisionAt         time.Time
	discoveryDevice             store.DeviceRecord
	approveDiscoveryError       error
	rejectDiscoveryError        error
	discoveryCredentialHash     []byte
	discoveryStatusError        error
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
	updatedRadioDevice          string
	updatedRadioSlotKey         string
	updatedRadioSlotIndex       int
	updatedRadioTechnology      string
	updatedSignalBars           *int
	updatedRadioAt              time.Time
	updateRadioError            error
	updatedSIMDevice            string
	updatedSIMSlotKey           string
	updatedSIMSlotIndex         int
	updatedSIMFingerprint       []byte
	updatedSIMAt                time.Time
	updatedSIMID                string
	updatedSIMNumericID         int
	updateSIMError              error
	installationOwnerID         string
	installation                store.AppInstallationRegistration
	installationRecord          store.AppInstallationRecord
	installationCreated         bool
	installationNow             time.Time
	installationError           error
	installationRecipients      []store.AppInstallationRecipient
	installationListError       error
	webauthnCredentials         []webauthnlib.Credential
	webauthnSaveError           error
	webauthnDisableError        error
	pushTokenOwnerID            string
	pushTokenInstallationID     string
	pushTokenEnvironment        string
	pushTokenKind               string
	pushToken                   []byte
	pushTokenNow                time.Time
	pushTokenError              error
	deletePushTokenOwnerID      string
	deletePushTokenInstallation string
	deletePushTokenEnvironment  string
	deletePushTokenKind         string
	deletePushTokenError        error
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

func (fake *fakeRepository) OwnerID(context.Context) (string, error) {
	return fake.ownerID, fake.ownerAuthError
}

func (fake *fakeRepository) WebAuthnCredentials(context.Context, string) ([]webauthnlib.Credential, error) {
	return append([]webauthnlib.Credential(nil), fake.webauthnCredentials...), nil
}

func (fake *fakeRepository) SaveWebAuthnCredential(_ context.Context, _ string, credential webauthnlib.Credential) error {
	if fake.webauthnSaveError != nil {
		return fake.webauthnSaveError
	}
	fake.webauthnCredentials = append(fake.webauthnCredentials, credential)
	return nil
}

func (fake *fakeRepository) DisableOwnerPassword(context.Context, string, time.Time) error {
	return fake.webauthnDisableError
}

func (fake *fakeRepository) UpdateDeviceRadioStatus(_ context.Context, deviceID string, slotKey string, slotIndex int, technology string, signalBars *int, now time.Time) error {
	fake.updatedRadioDevice = deviceID
	fake.updatedRadioSlotKey = slotKey
	fake.updatedRadioSlotIndex = slotIndex
	fake.updatedRadioTechnology = technology
	fake.updatedSignalBars = signalBars
	fake.updatedRadioAt = now
	return fake.updateRadioError
}

func (fake *fakeRepository) UpdateDeviceSIMIdentity(_ context.Context, deviceID string, slotKey string, slotIndex int, fingerprint []byte, now time.Time) (store.SIMSlotAssignment, error) {
	fake.updatedSIMDevice = deviceID
	fake.updatedSIMSlotKey = slotKey
	fake.updatedSIMSlotIndex = slotIndex
	fake.updatedSIMFingerprint = append([]byte(nil), fingerprint...)
	fake.updatedSIMAt = now
	if fake.updatedSIMID == "" {
		fake.updatedSIMID = "01900000-0000-7000-8000-000000000099"
	}
	if fake.updatedSIMNumericID == 0 {
		fake.updatedSIMNumericID = 1
	}
	return store.SIMSlotAssignment{
		SlotID:    "01900000-0000-7000-8000-000000000098",
		SIMID:     fake.updatedSIMID,
		NumericID: fake.updatedSIMNumericID,
	}, fake.updateSIMError
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

func (fake *fakeRepository) DeletePushToken(
	_ context.Context,
	ownerID string,
	installationID string,
	environment string,
	kind string,
) error {
	fake.deletePushTokenOwnerID = ownerID
	fake.deletePushTokenInstallation = installationID
	fake.deletePushTokenEnvironment = environment
	fake.deletePushTokenKind = kind
	return fake.deletePushTokenError
}

func (fake *fakeRepository) ListDevices(context.Context) ([]store.DeviceRecord, error) {
	return fake.devices, fake.listDevicesError
}

func (fake *fakeRepository) ListSIMCards(context.Context) ([]store.SIMRecord, error) {
	return fake.sims, fake.listSIMsError
}

func (fake *fakeRepository) RetireSIMCard(_ context.Context, simID string, retiredAt time.Time) error {
	fake.retiredSIMID = simID
	fake.retiredSIMAt = retiredAt
	return fake.retireSIMError
}

func (fake *fakeRepository) UpdateNumericID(_ context.Context, deviceID string, numericID int) error {
	fake.updatedDeviceID = deviceID
	fake.updatedNumericID = numericID
	return fake.updateNumericIDError
}

func (fake *fakeRepository) UpdateDeviceProfile(_ context.Context, deviceID string, profileCiphertext []byte) error {
	fake.updatedProfileDeviceID = deviceID
	fake.updatedProfileCiphertext = append([]byte(nil), profileCiphertext...)
	return fake.updateDeviceProfileError
}

func (fake *fakeRepository) UpdateSIMNumericID(_ context.Context, simID string, numericID int) error {
	fake.updatedSIMNumericIDTarget = simID
	fake.updatedSIMNumericIDValue = numericID
	return fake.updateSIMNumericIDError
}

func (fake *fakeRepository) UpdateSIMProfile(_ context.Context, simID string, profileCiphertext []byte) error {
	fake.updatedSIMProfileTarget = simID
	fake.updatedSIMProfileCiphertext = append([]byte(nil), profileCiphertext...)
	return fake.updateSIMProfileError
}

func (fake *fakeRepository) RevokeDevice(_ context.Context, deviceID string, revokedAt time.Time) error {
	fake.revokedDeviceID = deviceID
	fake.revokedDeviceAt = revokedAt
	return fake.revokeDeviceError
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

func (fake *fakeRepository) ReleaseIncomingCall(_ context.Context, ownerID string, notificationID string, installationID string) error {
	fake.incomingClaimOwnerID = ownerID
	fake.incomingClaimNotificationID = notificationID
	fake.incomingClaimInstallationID = installationID
	return nil
}

func (fake *fakeRepository) ListEvents(_ context.Context, afterSequence int64, limit int, now time.Time) ([]domain.EventEnvelope, error) {
	fake.listEventsMutex.Lock()
	defer fake.listEventsMutex.Unlock()
	fake.listEventsAfter = afterSequence
	fake.listEventsLimit = limit
	fake.listEventsNow = now
	if fake.listEventsCalls != nil {
		select {
		case fake.listEventsCalls <- afterSequence:
		default:
		}
	}
	var events []domain.EventEnvelope
	for _, event := range fake.listedEvents {
		if event.Sequence > afterSequence {
			events = append(events, event)
			if len(events) == limit {
				break
			}
		}
	}
	return events, fake.listEventsError
}

func (fake *fakeRepository) setListedEvents(events []domain.EventEnvelope) {
	fake.listEventsMutex.Lock()
	defer fake.listEventsMutex.Unlock()
	fake.listedEvents = events
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

func (fake *fakeRepository) AuthenticateOwnerSession(_ context.Context, token string, _ time.Time) (string, error) {
	fake.ownerAuthToken = token
	if fake.ownerAuthError != nil {
		return "", fake.ownerAuthError
	}
	if fake.ownerID == "" {
		return "owner-1", nil
	}
	return fake.ownerID, nil
}

func (fake *fakeRepository) GetHistorySnapshot(_ context.Context, ownerID string, kind int) (domain.HistorySnapshot, error) {
	if fake.historySnapshotError != nil {
		return domain.HistorySnapshot{}, fake.historySnapshotError
	}
	snapshot := fake.historySnapshot
	snapshot.OwnerID = ownerID
	snapshot.Kind = kind
	return snapshot, nil
}

func (fake *fakeRepository) PutHistorySnapshot(_ context.Context, ownerID string, kind int, expectedRevision int64, ciphertext []byte, now time.Time) (domain.HistorySnapshot, error) {
	fake.historyWriteExpected = expectedRevision
	fake.historyWriteCiphertext = append([]byte(nil), ciphertext...)
	fake.historyWriteAt = now
	if fake.historyWriteError != nil {
		return domain.HistorySnapshot{}, fake.historyWriteError
	}
	fake.historySnapshot = domain.HistorySnapshot{
		OwnerID: ownerID, Kind: kind, Revision: expectedRevision + 1,
		Ciphertext: append([]byte(nil), ciphertext...), UpdatedAt: now,
	}
	return fake.historySnapshot, nil
}

func (fake *fakeRepository) CreatePairingToken(_ context.Context, ownerID string, tokenHash []byte, expiresAt time.Time) error {
	fake.pairingOwnerID = ownerID
	fake.pairingTokenHash = append([]byte(nil), tokenHash...)
	fake.pairingExpiresAt = expiresAt
	return fake.pairingTokenError
}

func (fake *fakeRepository) ConsumePairingToken(_ context.Context, tokenHash []byte, _ time.Time, pairing store.DevicePairing) (store.DeviceRecord, error) {
	fake.pairingTokenConsumedHash = append([]byte(nil), tokenHash...)
	fake.pairing = pairing
	return fake.pairingRecord, fake.pairingError
}

func (fake *fakeRepository) OpenDeviceDiscovery(_ context.Context, ownerID string, now time.Time, expiresAt time.Time) (store.DeviceDiscoverySession, error) {
	fake.discoveryOwnerID = ownerID
	fake.discoveryOpenedAt = now
	fake.discoveryExpiresAt = expiresAt
	return fake.discoverySession, fake.openDiscoveryError
}

func (fake *fakeRepository) CloseDeviceDiscovery(_ context.Context, ownerID string, now time.Time) error {
	fake.discoveryOwnerID = ownerID
	fake.discoveryDecisionAt = now
	return fake.closeDiscoveryError
}

func (fake *fakeRepository) GetDeviceDiscoverySession(_ context.Context, ownerID string) (store.DeviceDiscoverySession, error) {
	fake.discoveryOwnerID = ownerID
	return fake.discoverySession, fake.getDiscoveryError
}

func (fake *fakeRepository) SubmitDeviceDiscovery(_ context.Context, now time.Time, request store.DeviceDiscoveryRequest) (store.DeviceDiscoveryRecord, error) {
	fake.discoverySubmittedAt = now
	fake.discoveryRequest = request
	return fake.discoveryRecord, fake.submitDiscoveryError
}

func (fake *fakeRepository) ListPendingDeviceDiscoveries(_ context.Context, ownerID string, now time.Time) ([]store.DeviceDiscoveryRecord, error) {
	fake.discoveryOwnerID = ownerID
	fake.discoverySubmittedAt = now
	return fake.discoveryRecords, fake.listDiscoveriesError
}

func (fake *fakeRepository) ApproveDeviceDiscovery(_ context.Context, ownerID string, deviceID string, now time.Time) (store.DeviceRecord, error) {
	fake.discoveryOwnerID = ownerID
	fake.discoveryDeviceID = deviceID
	fake.discoveryDecisionAt = now
	return fake.discoveryDevice, fake.approveDiscoveryError
}

func (fake *fakeRepository) RejectDeviceDiscovery(_ context.Context, ownerID string, deviceID string, now time.Time) error {
	fake.discoveryOwnerID = ownerID
	fake.discoveryDeviceID = deviceID
	fake.discoveryDecisionAt = now
	return fake.rejectDiscoveryError
}

func (fake *fakeRepository) DeviceDiscoveryStatus(_ context.Context, deviceID string, credentialHash []byte, now time.Time) (store.DeviceDiscoveryRecord, error) {
	fake.discoveryDeviceID = deviceID
	fake.discoveryCredentialHash = append([]byte(nil), credentialHash...)
	fake.discoverySubmittedAt = now
	return fake.discoveryRecord, fake.discoveryStatusError
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
			TURNURL:       "turn:turn.example.com:3478?transport=udp",
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
	rpc := func(procedure string, body string, accessToken string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/relay.communication.v1.AuthenticationService/"+procedure, bytes.NewBufferString(body))
		request.Header.Set("Content-Type", "application/json")
		if accessToken != "" {
			request.Header.Set("Authorization", "Bearer "+accessToken)
		}
		handler.ServeHTTP(response, request)
		return response
	}

	It("serves the configured passkey web authorization pages", func() {
		for _, path := range []string{"/passkey/register", "/passkey/login"} {
			response := perform(http.MethodGet, path, "", "")
			Expect(response.Code).To(Equal(http.StatusOK))
			Expect(response.Header().Get("Content-Type")).To(HavePrefix("text/html"))
			Expect(response.Header().Get("Cache-Control")).To(Equal("no-store"))
			Expect(response.Body.String()).To(ContainSubstring("AuthenticationService/"))
			Expect(response.Body.String()).To(ContainSubstring("BeginPasskeyRegistration"))
			Expect(response.Body.String()).To(ContainSubstring("BeginPasskeyLogin"))
			Expect(response.Body.String()).To(ContainSubstring("params.get('callback')"))
			Expect(response.Body.String()).To(ContainSubstring("callback === 'simdeck://passkey'"))
		}
	})

	PIt("reports health and discovery metadata", func() {
		health := perform(http.MethodGet, "/healthz", "", "")
		Expect(health.Code).To(Equal(http.StatusOK))
		Expect(health.Body.String()).To(MatchJSON(`{"status":"ok"}`))

		discovery := perform(http.MethodGet, "/.well-known/sim-deck", "", "")
		Expect(discovery.Code).To(Equal(http.StatusOK))
		var body map[string]any
		Expect(json.Unmarshal(discovery.Body.Bytes(), &body)).To(Succeed())
		Expect(body["api_version"]).To(Equal("v1"))
		Expect(body).NotTo(HaveKey("websocket_url"))
		Expect(body["media"]).To(Equal(map[string]any{"transport": "webrtc", "relay": "turn"}))

		association := perform(http.MethodGet, "/.well-known/apple-app-site-association", "", "")
		Expect(association.Code).To(Equal(http.StatusOK))
		Expect(association.Header().Get("Content-Type")).To(ContainSubstring("application/json"))
		Expect(association.Body.String()).To(MatchJSON(`{"webcredentials":{"apps":["97YACL9S6W.com.nowcent.simdeck"]}}`))
	})

	PIt("starts passkey ceremonies through RPC and validates missing ceremony identifiers", func() {
		login := rpc("BeginPasskeyLogin", `{}`, "")
		Expect(login.Code).To(Equal(http.StatusOK))
		Expect(login.Body.String()).To(ContainSubstring("ceremonyId"))

		registration := rpc("BeginPasskeyRegistration", `{}`, "access-token")
		Expect(registration.Code).To(Equal(http.StatusOK))
		Expect(registration.Body.String()).To(ContainSubstring("ceremonyId"))

		missing := rpc("FinishPasskeyLogin", `{}`, "")
		Expect(missing.Code).To(Equal(http.StatusBadRequest))

		failedLogin := rpc("FinishPasskeyLogin", `{"ceremonyId":"missing","credentialJson":"e30="}`, "")
		Expect(failedLogin.Code).To(Equal(http.StatusUnauthorized))
		failedRegistration := rpc("FinishPasskeyRegistration", `{"ceremonyId":"missing","credentialJson":"e30="}`, "access-token")
		Expect(failedRegistration.Code).To(Equal(http.StatusUnauthorized))
		Expect(perform(http.MethodPost, "/v1/auth/passkey/login/options", `{}`, "").Code).To(Equal(http.StatusNotFound))
	})

	PIt("reports passkeys as unavailable when the public origin is not configured", func() {
		handler = Handler(repository, Config{})

		loginOptions := rpc("BeginPasskeyLogin", `{}`, "")
		Expect(loginOptions.Code).To(Equal(http.StatusNotImplemented))

		loginFinish := rpc("FinishPasskeyLogin", `{"ceremonyId":"ceremony","credentialJson":"e30="}`, "")
		Expect(loginFinish.Code).To(Equal(http.StatusNotImplemented))

		registrationOptions := rpc("BeginPasskeyRegistration", `{}`, "access-token")
		Expect(registrationOptions.Code).To(Equal(http.StatusNotImplemented))

		registrationFinish := rpc("FinishPasskeyRegistration", `{}`, "access-token")
		Expect(registrationFinish.Code).To(Equal(http.StatusNotImplemented))
	})

	PIt("keeps owner-only handlers protected without authentication middleware", func() {
		directRouter := &router{repository: repository, config: Config{}}
		ownerHandlers := []func(http.ResponseWriter, *http.Request){
			directRouter.upsertPushToken,
			directRouter.deletePushToken,
			directRouter.registerAppInstallation,
			directRouter.claimIncomingCall,
			directRouter.createPairingToken,
			directRouter.openDeviceDiscovery,
			directRouter.getDeviceDiscoverySession,
			directRouter.closeDeviceDiscovery,
			directRouter.listDeviceDiscoveries,
			directRouter.approveDeviceDiscovery,
			directRouter.rejectDeviceDiscovery,
		}

		for _, ownerHandler := range ownerHandlers {
			request := httptest.NewRequest(http.MethodPost, "/", nil)
			response := httptest.NewRecorder()

			ownerHandler(response, request)

			Expect(response.Code).To(Equal(http.StatusUnauthorized))
			Expect(response.Body.String()).To(ContainSubstring("invalid_access_token"))
		}
	})

	PIt("issues short-lived TURN credentials to authenticated app and device clients", func() {
		now := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
		handler = Handler(repository, Config{
			PublicBaseURL: "https://deck.example",
			TURNURL:       "turn:turn.example.com:3478?transport=udp",
			TURNSecret:    []byte("a sufficiently long shared secret"),
			TURNTTL:       10 * time.Minute,
			Now:           func() time.Time { return now },
		})

		appResponse := perform(http.MethodGet, "/v1/media/turn-credentials", "", "access-token")
		Expect(appResponse.Code).To(Equal(http.StatusOK))
		var appCredentials TURNCredentials
		Expect(json.Unmarshal(appResponse.Body.Bytes(), &appCredentials)).To(Succeed())
		Expect(appCredentials.URLs).To(Equal([]string{
			"turn:turn.example.com:3478?transport=udp",
			"turn:turn.example.com:3478?transport=tcp",
		}))
		Expect(appCredentials.ExpiresAt).To(Equal(now.Add(10 * time.Minute)))
		Expect(appCredentials.Username).To(Equal(strconv.FormatInt(now.Add(10*time.Minute).Unix(), 10) + ":sim-deck"))

		deviceID := "01900000-0000-7000-8000-000000000001"
		deviceResponse := perform(http.MethodGet, "/v1/devices/"+deviceID+"/media/turn-credentials", "", "device-secret")
		Expect(deviceResponse.Code).To(Equal(http.StatusOK))
		Expect(repository.authenticatedDeviceID).To(Equal(deviceID))
		Expect(repository.authenticatedCredential).To(Equal("device-secret"))
	})

	PIt("rejects unauthenticated TURN credential requests", func() {
		response := perform(http.MethodGet, "/v1/devices/01900000-0000-7000-8000-000000000001/media/turn-credentials", "", "")
		Expect(response.Code).To(Equal(http.StatusUnauthorized))
	})

	PDescribeTable("validates and normalizes public API URLs",
		func(value string, expected string) {
			parsed, err := ParsePublicBaseURL(value)
			Expect(err).NotTo(HaveOccurred())
			Expect(parsed).To(Equal(expected))
		},
		Entry("secure", " https://deck.example/ ", "https://deck.example"),
		Entry("plaintext", "http://127.0.0.1:8080", "http://127.0.0.1:8080"),
		Entry("reverse proxy prefix", "https://deck.example/sim-deck/", "https://deck.example/sim-deck"),
	)

	PDescribeTable("rejects invalid public API URLs",
		func(value string) {
			_, err := ParsePublicBaseURL(value)
			Expect(err).To(HaveOccurred())
		},
		Entry("missing", ""),
		Entry("relative", "deck.example"),
		Entry("unsupported scheme", "wss://deck.example"),
		Entry("missing host", "https:///sim-deck"),
		Entry("credentials", "https://admin:secret@deck.example"),
		Entry("query", "https://deck.example?tenant=owner"),
		Entry("fragment", "https://deck.example#settings"),
	)

	PIt("lists devices for an authenticated owner", func() {
		repository.devices = []store.DeviceRecord{{ID: "device-1", NumericID: 1}}

		response := perform(http.MethodGet, "/v1/devices", "", "access-token")

		Expect(response.Code).To(Equal(http.StatusOK))
		Expect(response.Body.String()).To(ContainSubstring(`"device_uuid":"device-1"`))
	})

	PIt("lists SIM cards independently from their device slots", func() {
		repository.sims = []store.SIMRecord{{ID: "01900000-0000-0000-0000-000000000101", NumericID: 2}}

		response := perform(http.MethodGet, "/v1/sims", "", "access-token")

		Expect(response.Code).To(Equal(http.StatusOK))
		Expect(response.Body.String()).To(ContainSubstring(`"sim_id":"01900000-0000-0000-0000-000000000101"`))
	})

	PDescribeTable("maps SIM deletion outcomes",
		func(repositoryError error, expectedStatus int, expectedCode string) {
			repository.retireSIMError = repositoryError
			response := perform(http.MethodDelete, "/v1/sims/01900000-0000-0000-0000-000000000101", "", "access-token")
			Expect(response.Code).To(Equal(expectedStatus))
			Expect(response.Body.String()).To(ContainSubstring(expectedCode))
		},
		Entry("success", nil, http.StatusNoContent, ""),
		Entry("assigned", domain.ErrSIMAssigned, http.StatusConflict, "sim_online"),
		Entry("missing", pgx.ErrNoRows, http.StatusNotFound, "sim_not_found"),
		Entry("storage failure", errors.New("database unavailable"), http.StatusInternalServerError, "storage_error"),
	)

	PIt("rejects missing and invalid owner sessions", func() {
		Expect(perform(http.MethodGet, "/v1/devices", "", "").Code).To(Equal(http.StatusUnauthorized))
		repository.ownerAuthError = errors.New("expired")
		Expect(perform(http.MethodGet, "/v1/devices", "", "access-token").Code).To(Equal(http.StatusUnauthorized))
	})

	PIt("maps a device-list storage failure", func() {
		repository.listDevicesError = errors.New("database unavailable")

		response := perform(http.MethodGet, "/v1/devices", "", "access-token")

		Expect(response.Code).To(Equal(http.StatusInternalServerError))
		Expect(response.Body.String()).To(ContainSubstring("storage_error"))
	})

	PIt("updates a device numeric ID", func() {
		response := perform(http.MethodPatch, "/v1/devices/device-1/numeric-id", `{"numeric_id":2}`, "access-token")

		Expect(response.Code).To(Equal(http.StatusNoContent))
		Expect(repository.updatedDeviceID).To(Equal("device-1"))
		Expect(repository.updatedNumericID).To(Equal(2))
	})

	PDescribeTable("maps numeric-ID update failures",
		func(body string, repositoryError error, expectedStatus int, expectedCode string) {
			repository.updateNumericIDError = repositoryError
			response := perform(http.MethodPatch, "/v1/devices/device-1/numeric-id", body, "access-token")
			Expect(response.Code).To(Equal(expectedStatus))
			Expect(response.Body.String()).To(ContainSubstring(expectedCode))
		},
		Entry("malformed JSON", `{`, nil, http.StatusBadRequest, "invalid_request"),
		Entry("trailing JSON value", `{"numeric_id":2}{"numeric_id":3}`, nil, http.StatusBadRequest, "invalid_request"),
		Entry("trailing invalid data", `{"numeric_id":2} trailing`, nil, http.StatusBadRequest, "invalid_request"),
		Entry("unknown field", `{"numeric_id":2,"unexpected":true}`, nil, http.StatusBadRequest, "invalid_request"),
		Entry("invalid identifier", `{"numeric_id":0}`, domain.ErrInvalidNumericID, http.StatusUnprocessableEntity, "invalid_numeric_id"),
		Entry("duplicate identifier", `{"numeric_id":2}`, domain.ErrDuplicateNumericID, http.StatusConflict, "duplicate_numeric_id"),
		Entry("missing device", `{"numeric_id":2}`, pgx.ErrNoRows, http.StatusNotFound, "device_not_found"),
		Entry("storage failure", `{"numeric_id":2}`, errors.New("database unavailable"), http.StatusInternalServerError, "storage_error"),
	)

	PIt("updates only an opaque encrypted device profile", func() {
		response := perform(
			http.MethodPut,
			"/v1/devices/01900000-0000-7000-8000-000000000001/profile",
			`{"profile_ciphertext":"ZW5jcnlwdGVkLXByb2ZpbGU="}`,
			"access-token",
		)

		Expect(response.Code).To(Equal(http.StatusNoContent))
		Expect(repository.updatedProfileDeviceID).To(Equal("01900000-0000-7000-8000-000000000001"))
		Expect(repository.updatedProfileCiphertext).To(Equal([]byte("encrypted-profile")))
	})

	PDescribeTable("rejects invalid or unavailable encrypted device profiles",
		func(path string, body string, repositoryError error, expectedStatus int, expectedCode string) {
			repository.updateDeviceProfileError = repositoryError
			response := perform(http.MethodPut, path, body, "access-token")
			Expect(response.Code).To(Equal(expectedStatus))
			Expect(response.Body.String()).To(ContainSubstring(expectedCode))
		},
		Entry("malformed JSON", "/v1/devices/01900000-0000-7000-8000-000000000001/profile", `{`, nil, http.StatusBadRequest, "invalid_request"),
		Entry("invalid device ID", "/v1/devices/not-a-uuid/profile", `{"profile_ciphertext":"YQ=="}`, nil, http.StatusUnprocessableEntity, "invalid_device_profile"),
		Entry("empty ciphertext", "/v1/devices/01900000-0000-7000-8000-000000000001/profile", `{"profile_ciphertext":""}`, nil, http.StatusUnprocessableEntity, "invalid_device_profile"),
		Entry("missing device", "/v1/devices/01900000-0000-7000-8000-000000000001/profile", `{"profile_ciphertext":"YQ=="}`, pgx.ErrNoRows, http.StatusNotFound, "device_not_found"),
		Entry("storage failure", "/v1/devices/01900000-0000-7000-8000-000000000001/profile", `{"profile_ciphertext":"YQ=="}`, errors.New("database unavailable"), http.StatusInternalServerError, "storage_error"),
	)

	PIt("updates one SIM numeric ID", func() {
		response := perform(
			http.MethodPatch,
			"/v1/sims/01900000-0000-7000-8000-000000000101/numeric-id",
			`{"numeric_id":7}`,
			"access-token",
		)

		Expect(response.Code).To(Equal(http.StatusNoContent))
		Expect(repository.updatedSIMNumericIDTarget).To(Equal("01900000-0000-7000-8000-000000000101"))
		Expect(repository.updatedSIMNumericIDValue).To(Equal(7))
	})

	PDescribeTable("maps SIM numeric-ID update failures",
		func(path string, body string, repositoryError error, expectedStatus int, expectedCode string) {
			repository.updateSIMNumericIDError = repositoryError
			response := perform(http.MethodPatch, path, body, "access-token")
			Expect(response.Code).To(Equal(expectedStatus))
			Expect(response.Body.String()).To(ContainSubstring(expectedCode))
		},
		Entry("malformed JSON", "/v1/sims/01900000-0000-7000-8000-000000000101/numeric-id", `{`, nil, http.StatusBadRequest, "invalid_request"),
		Entry("invalid SIM ID", "/v1/sims/not-a-uuid/numeric-id", `{"numeric_id":7}`, nil, http.StatusUnprocessableEntity, "invalid_sim"),
		Entry("invalid identifier", "/v1/sims/01900000-0000-7000-8000-000000000101/numeric-id", `{"numeric_id":0}`, domain.ErrInvalidNumericID, http.StatusUnprocessableEntity, "invalid_numeric_id"),
		Entry("duplicate identifier", "/v1/sims/01900000-0000-7000-8000-000000000101/numeric-id", `{"numeric_id":7}`, domain.ErrDuplicateNumericID, http.StatusConflict, "duplicate_numeric_id"),
		Entry("missing SIM", "/v1/sims/01900000-0000-7000-8000-000000000101/numeric-id", `{"numeric_id":7}`, pgx.ErrNoRows, http.StatusNotFound, "sim_not_found"),
		Entry("storage failure", "/v1/sims/01900000-0000-7000-8000-000000000101/numeric-id", `{"numeric_id":7}`, errors.New("database unavailable"), http.StatusInternalServerError, "storage_error"),
	)

	PIt("updates only one opaque encrypted SIM profile", func() {
		response := perform(
			http.MethodPut,
			"/v1/sims/01900000-0000-7000-8000-000000000101/profile",
			`{"profile_ciphertext":"ZW5jcnlwdGVkLVNJTS1wcm9maWxl"}`,
			"access-token",
		)

		Expect(response.Code).To(Equal(http.StatusNoContent))
		Expect(repository.updatedSIMProfileTarget).To(Equal("01900000-0000-7000-8000-000000000101"))
		Expect(repository.updatedSIMProfileCiphertext).To(Equal([]byte("encrypted-SIM-profile")))
	})

	PDescribeTable("rejects invalid or unavailable encrypted SIM profiles",
		func(path string, body string, repositoryError error, expectedStatus int, expectedCode string) {
			repository.updateSIMProfileError = repositoryError
			response := perform(http.MethodPut, path, body, "access-token")
			Expect(response.Code).To(Equal(expectedStatus))
			Expect(response.Body.String()).To(ContainSubstring(expectedCode))
		},
		Entry("malformed JSON", "/v1/sims/01900000-0000-7000-8000-000000000101/profile", `{`, nil, http.StatusBadRequest, "invalid_request"),
		Entry("invalid SIM ID", "/v1/sims/not-a-uuid/profile", `{"profile_ciphertext":"YQ=="}`, nil, http.StatusUnprocessableEntity, "invalid_sim_profile"),
		Entry("empty ciphertext", "/v1/sims/01900000-0000-7000-8000-000000000101/profile", `{"profile_ciphertext":""}`, nil, http.StatusUnprocessableEntity, "invalid_sim_profile"),
		Entry("missing SIM", "/v1/sims/01900000-0000-7000-8000-000000000101/profile", `{"profile_ciphertext":"YQ=="}`, pgx.ErrNoRows, http.StatusNotFound, "sim_not_found"),
		Entry("storage failure", "/v1/sims/01900000-0000-7000-8000-000000000101/profile", `{"profile_ciphertext":"YQ=="}`, errors.New("database unavailable"), http.StatusInternalServerError, "storage_error"),
	)

	PIt("revokes a device using the owner session", func() {
		response := perform(
			http.MethodDelete,
			"/v1/devices/01900000-0000-7000-8000-000000000001",
			"",
			"access-token",
		)

		Expect(response.Code).To(Equal(http.StatusNoContent))
		Expect(repository.revokedDeviceID).To(Equal("01900000-0000-7000-8000-000000000001"))
		Expect(repository.revokedDeviceAt).NotTo(BeZero())
	})

	PDescribeTable("maps device revocation failures",
		func(path string, repositoryError error, expectedStatus int, expectedCode string) {
			repository.revokeDeviceError = repositoryError
			response := perform(http.MethodDelete, path, "", "access-token")
			Expect(response.Code).To(Equal(expectedStatus))
			Expect(response.Body.String()).To(ContainSubstring(expectedCode))
		},
		Entry("invalid device ID", "/v1/devices/not-a-uuid", nil, http.StatusUnprocessableEntity, "invalid_device"),
		Entry("missing device", "/v1/devices/01900000-0000-7000-8000-000000000001", pgx.ErrNoRows, http.StatusNotFound, "device_not_found"),
		Entry("storage failure", "/v1/devices/01900000-0000-7000-8000-000000000001", errors.New("database unavailable"), http.StatusInternalServerError, "storage_error"),
	)

	PIt("creates a bootstrap session through RPC", func() {
		passwordHash, err := auth.HashPassword("correct horse battery staple")
		Expect(err).NotTo(HaveOccurred())
		repository.credential = store.OwnerCredential{ID: "owner-1", PasswordHash: passwordHash}

		response := rpc("Bootstrap", `{"username":"admin","password":"correct horse battery staple"}`, "")

		Expect(response.Code).To(Equal(http.StatusOK))
		Expect(repository.sessionPair.AccessToken).NotTo(BeEmpty())
		Expect(repository.sessionPair.RefreshToken).NotTo(BeEmpty())
		Expect(perform(http.MethodPost, "/v1/auth/bootstrap", `{}`, "").Code).To(Equal(http.StatusNotFound))
	})

	PDescribeTable("rejects invalid bootstrap logins",
		func(credential store.OwnerCredential, credentialError error, requestBody string) {
			repository.credential = credential
			repository.credentialError = credentialError
			response := rpc("Bootstrap", requestBody, "")
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

	PIt("rejects invalid bootstrap RPC messages and session storage failures", func() {
		Expect(rpc("Bootstrap", `{`, "").Code).To(Equal(http.StatusBadRequest))
		passwordHash, err := auth.HashPassword("correct horse battery staple")
		Expect(err).NotTo(HaveOccurred())
		repository.credential = store.OwnerCredential{ID: "owner-1", PasswordHash: passwordHash}
		repository.createSessionError = errors.New("database unavailable")
		Expect(rpc("Bootstrap", `{"username":"admin","password":"correct horse battery staple"}`, "").Code).To(Equal(http.StatusInternalServerError))
	})

	PIt("refreshes and revokes session credentials through RPC", func() {
		refresh := rpc("Refresh", `{"refreshToken":"old-refresh"}`, "")
		Expect(refresh.Code).To(Equal(http.StatusOK))
		Expect(repository.rotatedRefresh).To(Equal("old-refresh"))

		logout := rpc("Logout", `{}`, "access-token")
		Expect(logout.Code).To(Equal(http.StatusOK))
		Expect(repository.revokedAccess).To(Equal("access-token"))
	})

	PIt("rejects malformed or failed session refreshes", func() {
		Expect(rpc("Refresh", `{`, "").Code).To(Equal(http.StatusBadRequest))
		repository.rotateSessionError = pgx.ErrNoRows
		response := rpc("Refresh", `{"refreshToken":"expired"}`, "")
		Expect(response.Code).To(Equal(http.StatusUnauthorized))
	})

	PIt("requires a valid access token for logout", func() {
		Expect(rpc("Logout", `{}`, "").Code).To(Equal(http.StatusUnauthorized))
		repository.revokeSessionError = pgx.ErrNoRows
		Expect(rpc("Logout", `{}`, "expired").Code).To(Equal(http.StatusUnauthorized))
	})

	PDescribeTable("parses configured ports",
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
