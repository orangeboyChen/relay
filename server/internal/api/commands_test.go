package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/jackc/pgx/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"sim-deck-server/internal/domain"
)

var _ = Describe("Encrypted device commands", func() {
	const (
		deviceID       = "01900000-0000-7000-8000-000000000001"
		commandID      = "01900000-0000-7000-8000-000000000010"
		installationID = "01900000-0000-7000-8000-000000000101"
	)
	var (
		now        time.Time
		repository *fakeRepository
		handler    http.Handler
	)

	BeforeEach(func() {
		now = time.Date(2026, 10, 16, 14, 0, 0, 0, time.UTC)
		repository = &fakeRepository{ownerID: "owner-1", enqueueCreated: true}
		handler = Handler(repository, Config{Now: func() time.Time { return now }})
	})

	validCommand := func() domain.EncryptedCommand {
		return domain.EncryptedCommand{
			ID:                   commandID,
			SenderInstallationID: installationID,
			SenderPublicKey:      bytes.Repeat([]byte{1}, commandPublicKeyBytes),
			Nonce:                bytes.Repeat([]byte{2}, commandNonceBytes),
			Ciphertext:           []byte("opaque command"),
			Signature:            bytes.Repeat([]byte{3}, commandSignatureBytes),
			CreatedAt:            now,
			ExpiresAt:            now.Add(time.Minute),
		}
	}

	requestJSON := func(method string, path string, body any, token string) *httptest.ResponseRecorder {
		encoded, err := json.Marshal(body)
		Expect(err).NotTo(HaveOccurred())
		request := httptest.NewRequest(method, path, bytes.NewReader(encoded))
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	It("queues only opaque ciphertext for the selected device", func() {
		response := requestJSON(http.MethodPost, "/v1/devices/"+deviceID+"/commands", validCommand(), "owner-token")

		Expect(response.Code).To(Equal(http.StatusCreated))
		Expect(repository.enqueuedCommand.DeviceUUID).To(Equal(deviceID))
		Expect(repository.enqueuedCommand.Ciphertext).To(Equal([]byte("opaque command")))
		Expect(response.Body.String()).NotTo(ContainSubstring("opaque command"))
	})

	DescribeTable("rejects invalid encrypted command metadata",
		func(mutate func(*domain.EncryptedCommand)) {
			encrypted := validCommand()
			mutate(&encrypted)
			response := requestJSON(http.MethodPost, "/v1/devices/"+deviceID+"/commands", encrypted, "owner-token")
			Expect(response.Code).To(Equal(http.StatusUnprocessableEntity))
			Expect(repository.enqueuedCommand.ID).To(BeEmpty())
		},
		Entry("invalid id", func(command *domain.EncryptedCommand) { command.ID = "invalid" }),
		Entry("invalid installation id", func(command *domain.EncryptedCommand) { command.SenderInstallationID = "invalid" }),
		Entry("wrong public key size", func(command *domain.EncryptedCommand) { command.SenderPublicKey = []byte{1} }),
		Entry("wrong nonce size", func(command *domain.EncryptedCommand) { command.Nonce = []byte{1} }),
		Entry("empty ciphertext", func(command *domain.EncryptedCommand) { command.Ciphertext = nil }),
		Entry("wrong signature size", func(command *domain.EncryptedCommand) { command.Signature = []byte{1} }),
		Entry("stale creation", func(command *domain.EncryptedCommand) { command.CreatedAt = now.Add(-6 * time.Minute) }),
		Entry("expired", func(command *domain.EncryptedCommand) { command.ExpiresAt = now }),
		Entry("excessive lifetime", func(command *domain.EncryptedCommand) { command.ExpiresAt = now.Add(6 * time.Minute) }),
	)

	DescribeTable("maps command enqueue outcomes",
		func(repositoryError error, created bool, expectedStatus int, expectedCode string) {
			repository.enqueueError = repositoryError
			repository.enqueueCreated = created
			response := requestJSON(http.MethodPost, "/v1/devices/"+deviceID+"/commands", validCommand(), "owner-token")
			Expect(response.Code).To(Equal(expectedStatus))
			if expectedCode != "" {
				Expect(response.Body.String()).To(ContainSubstring(expectedCode))
			}
		},
		Entry("idempotent retry", nil, false, http.StatusOK, `"created":false`),
		Entry("idempotency conflict", domain.ErrIdempotencyConflict, false, http.StatusConflict, "idempotency_conflict"),
		Entry("missing device", pgx.ErrNoRows, false, http.StatusNotFound, "device_not_found"),
		Entry("storage error", errors.New("down"), false, http.StatusInternalServerError, "storage_error"),
	)

	It("authenticates a device and leases its next command", func() {
		repository.claimedCommand = validCommand()
		repository.claimedCommand.DeviceUUID = deviceID
		request := httptest.NewRequest(http.MethodGet, "/v1/device-commands/next?device_uuid="+deviceID, nil)
		request.Header.Set("Authorization", "Bearer device-token")
		response := httptest.NewRecorder()

		handler.ServeHTTP(response, request)

		Expect(response.Code).To(Equal(http.StatusOK))
		Expect(repository.authenticatedDeviceID).To(Equal(deviceID))
		Expect(repository.authenticatedCredential).To(Equal("device-token"))
		Expect(repository.claimDeviceID).To(Equal(deviceID))
		Expect(repository.claimNow).To(Equal(now))
		Expect(repository.claimLeaseUntil).To(Equal(now.Add(commandLease)))
	})

	It("returns no content when the device queue is empty", func() {
		repository.claimError = pgx.ErrNoRows
		request := httptest.NewRequest(http.MethodGet, "/v1/device-commands/next?device_uuid="+deviceID, nil)
		request.Header.Set("Authorization", "Bearer device-token")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		Expect(response.Code).To(Equal(http.StatusNoContent))
	})

	It("rejects unauthenticated claims and maps claim storage errors", func() {
		missing := httptest.NewRecorder()
		handler.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/v1/device-commands/next?device_uuid="+deviceID, nil))
		Expect(missing.Code).To(Equal(http.StatusUnauthorized))

		repository.deviceAuthError = errors.New("invalid")
		invalidRequest := httptest.NewRequest(http.MethodGet, "/v1/device-commands/next?device_uuid="+deviceID, nil)
		invalidRequest.Header.Set("Authorization", "Bearer invalid")
		invalid := httptest.NewRecorder()
		handler.ServeHTTP(invalid, invalidRequest)
		Expect(invalid.Code).To(Equal(http.StatusUnauthorized))

		repository.deviceAuthError = nil
		repository.claimError = errors.New("down")
		failedRequest := httptest.NewRequest(http.MethodGet, "/v1/device-commands/next?device_uuid="+deviceID, nil)
		failedRequest.Header.Set("Authorization", "Bearer device-token")
		failed := httptest.NewRecorder()
		handler.ServeHTTP(failed, failedRequest)
		Expect(failed.Code).To(Equal(http.StatusInternalServerError))
	})

	It("stores an opaque completion from the authenticated target device", func() {
		body := map[string]any{
			"device_uuid": deviceID,
			"result": domain.EncryptedCommandResult{
				Nonce:      bytes.Repeat([]byte{3}, commandNonceBytes),
				Ciphertext: []byte("opaque result"),
			},
		}
		response := requestJSON(http.MethodPost, "/v1/device-commands/"+commandID+"/complete", body, "device-token")

		Expect(response.Code).To(Equal(http.StatusNoContent))
		Expect(repository.completedDeviceID).To(Equal(deviceID))
		Expect(repository.completedCommandID).To(Equal(commandID))
		Expect(repository.completedResult.Ciphertext).To(Equal([]byte("opaque result")))
		Expect(repository.completedAt).To(Equal(now))
	})

	It("validates completion authentication, ciphertext, and repository outcomes", func() {
		invalidBody := map[string]any{"device_uuid": deviceID, "result": domain.EncryptedCommandResult{Nonce: []byte{1}}}
		Expect(requestJSON(http.MethodPost, "/v1/device-commands/"+commandID+"/complete", invalidBody, "device-token").Code).To(Equal(http.StatusUnprocessableEntity))

		repository.deviceAuthError = errors.New("invalid")
		validBody := map[string]any{"device_uuid": deviceID, "result": domain.EncryptedCommandResult{Nonce: bytes.Repeat([]byte{3}, 12), Ciphertext: []byte{4}}}
		Expect(requestJSON(http.MethodPost, "/v1/device-commands/"+commandID+"/complete", validBody, "bad").Code).To(Equal(http.StatusUnauthorized))

		repository.deviceAuthError = nil
		repository.completeError = pgx.ErrNoRows
		Expect(requestJSON(http.MethodPost, "/v1/device-commands/"+commandID+"/complete", validBody, "device-token").Code).To(Equal(http.StatusNotFound))

		repository.completeError = errors.New("down")
		Expect(requestJSON(http.MethodPost, "/v1/device-commands/"+commandID+"/complete", validBody, "device-token").Code).To(Equal(http.StatusInternalServerError))
	})

	It("returns owner-visible routing status and only an encrypted result", func() {
		completedAt := now
		repository.commandStatus = domain.CommandStatus{
			ID: commandID, DeviceUUID: deviceID, State: "completed", CompletedAt: &completedAt,
			Result: &domain.EncryptedCommandResult{Nonce: bytes.Repeat([]byte{4}, 12), Ciphertext: []byte("sealed")},
		}
		response := requestJSON(http.MethodGet, "/v1/devices/"+deviceID+"/commands/"+commandID, nil, "owner-token")
		Expect(response.Code).To(Equal(http.StatusOK))
		Expect(repository.statusDeviceID).To(Equal(deviceID))
		Expect(repository.statusCommandID).To(Equal(commandID))
		var body struct {
			Command domain.CommandStatus `json:"command"`
		}
		Expect(json.Unmarshal(response.Body.Bytes(), &body)).To(Succeed())
		Expect(body.Command.Result.Ciphertext).To(Equal([]byte("sealed")))
	})

	It("maps missing and failed status reads", func() {
		repository.statusError = pgx.ErrNoRows
		Expect(requestJSON(http.MethodGet, "/v1/devices/"+deviceID+"/commands/"+commandID, nil, "owner-token").Code).To(Equal(http.StatusNotFound))
		repository.statusError = errors.New("down")
		Expect(requestJSON(http.MethodGet, "/v1/devices/"+deviceID+"/commands/"+commandID, nil, "owner-token").Code).To(Equal(http.StatusInternalServerError))
	})

	It("lets an authenticated device register its command encryption key", func() {
		body := map[string]any{"device_uuid": deviceID, "command_public_key": bytes.Repeat([]byte{8}, 32)}
		response := requestJSON(http.MethodPut, "/v1/device-command-key", body, "device-token")
		Expect(response.Code).To(Equal(http.StatusNoContent))
		Expect(repository.updatedCommandKeyDevice).To(Equal(deviceID))
		Expect(repository.updatedCommandKey).To(Equal(bytes.Repeat([]byte{8}, 32)))
	})

	It("validates command key registration authentication and storage outcomes", func() {
		invalidKey := map[string]any{"device_uuid": deviceID, "command_public_key": []byte{1}}
		Expect(requestJSON(http.MethodPut, "/v1/device-command-key", invalidKey, "device-token").Code).To(Equal(http.StatusUnprocessableEntity))

		valid := map[string]any{"device_uuid": deviceID, "command_public_key": bytes.Repeat([]byte{8}, 32)}
		repository.deviceAuthError = errors.New("invalid")
		Expect(requestJSON(http.MethodPut, "/v1/device-command-key", valid, "bad").Code).To(Equal(http.StatusUnauthorized))

		repository.deviceAuthError = nil
		repository.updateCommandKeyError = pgx.ErrNoRows
		Expect(requestJSON(http.MethodPut, "/v1/device-command-key", valid, "device-token").Code).To(Equal(http.StatusNotFound))

		repository.updateCommandKeyError = errors.New("down")
		Expect(requestJSON(http.MethodPut, "/v1/device-command-key", valid, "device-token").Code).To(Equal(http.StatusInternalServerError))
	})
})
