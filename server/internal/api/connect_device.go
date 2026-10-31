package api

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"

	connect "connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/jackc/pgx/v5"
	communicationv1 "github.com/orangeboyChen/relay-sdk/gen/go/relay/communication/v1"
	communicationv1connect "github.com/orangeboyChen/relay-sdk/gen/go/relay/communication/v1/communicationv1connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"sim-deck-server/internal/auth"
	"sim-deck-server/internal/domain"
	"sim-deck-server/internal/e2ee"
	"sim-deck-server/internal/store"
)

type deviceService struct {
	communicationv1connect.UnimplementedDeviceServiceHandler
	router *router
}

func mountDeviceService(mux *http.ServeMux, router *router) {
	server := connect.NewServer(rpcAuthInterceptor(router))
	communicationv1connect.RegisterDeviceServiceHandler(server, &deviceService{router: router})
	connecthttp.Mount(mux, server)
}

func (service *deviceService) requireDeviceCredential(ctx context.Context, deviceID string) error {
	info, ok := connect.CallInfoForServerContext(ctx)
	if !ok {
		return connect.NewError(connect.CodeUnauthenticated, "device request metadata is missing")
	}
	token, ok := bearerToken(info.RequestHeader().Get("Authorization"))
	if !ok || !validUUID(deviceID) {
		return connect.NewError(connect.CodeUnauthenticated, "device credential is required")
	}
	if _, err := service.router.repository.AuthenticateDevice(ctx, deviceID, token, service.router.now()); err != nil {
		return connect.NewError(connect.CodeUnauthenticated, "device credential is invalid or revoked")
	}
	return nil
}

func (service *deviceService) FetchTurnCredentials(ctx context.Context, request *communicationv1.FetchTurnCredentialsRequest) (*communicationv1.FetchTurnCredentialsResponse, error) {
	if request == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, "device UUID is required")
	}
	if err := service.requireDeviceCredential(ctx, request.GetDeviceUuid()); err != nil {
		return nil, err
	}
	credentials, err := service.router.turnCredentialsProto()
	if err != nil {
		return nil, err
	}
	return &communicationv1.FetchTurnCredentialsResponse{Urls: credentials.Urls, Username: credentials.Username, Credential: credentials.Credential, ExpiresAt: credentials.ExpiresAt}, nil
}

func (service *deviceService) UpdateRadioStatus(ctx context.Context, request *communicationv1.UpdateRadioStatusRequest) (*communicationv1.UpdateRadioStatusResponse, error) {
	if request == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, "radio status is required")
	}
	if err := service.requireDeviceCredential(ctx, request.GetDeviceUuid()); err != nil {
		return nil, err
	}
	slotKey, slotIndex := request.GetSlotKey(), int(request.GetSlotIndex())
	if slotKey == "" {
		slotKey = "1"
	}
	if slotIndex == 0 {
		slotIndex = 1
	}
	if len(slotKey) > 64 || slotIndex < 1 || slotIndex > 32 || !validRadioAccessTechnology(request.GetRadioAccessTechnology()) || (request.SignalBars != nil && (*request.SignalBars < 0 || *request.SignalBars > 4)) {
		return nil, connect.NewError(connect.CodeInvalidArgument, "radio status is invalid")
	}
	var signal *int
	if request.SignalBars != nil {
		value := int(*request.SignalBars)
		signal = &value
	}
	err := service.router.repository.UpdateDeviceRadioStatus(ctx, request.GetDeviceUuid(), slotKey, slotIndex, request.GetRadioAccessTechnology(), signal, service.router.now())
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, connect.NewError(connect.CodeNotFound, "device not found")
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "unable to update device radio status")
	}
	return &communicationv1.UpdateRadioStatusResponse{}, nil
}

func (service *deviceService) UpdateSimIdentity(ctx context.Context, request *communicationv1.UpdateSimIdentityRequest) (*communicationv1.UpdateSimIdentityResponse, error) {
	if request == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, "SIM identity is required")
	}
	if err := service.requireDeviceCredential(ctx, request.GetDeviceUuid()); err != nil {
		return nil, err
	}
	slotKey, slotIndex := request.GetSlotKey(), int(request.GetSlotIndex())
	if slotKey == "" {
		slotKey = "1"
	}
	if slotIndex == 0 {
		slotIndex = 1
	}
	if len(slotKey) > 64 || slotIndex < 1 || slotIndex > 32 || len(request.GetSimFingerprint()) != 32 {
		return nil, connect.NewError(connect.CodeInvalidArgument, "SIM identity is invalid")
	}
	assignment, err := service.router.repository.UpdateDeviceSIMIdentity(ctx, request.GetDeviceUuid(), slotKey, slotIndex, request.GetSimFingerprint(), service.router.now())
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, connect.NewError(connect.CodeNotFound, "device slot not found")
	}
	if errors.Is(err, domain.ErrNoNumericID) {
		return nil, connect.NewError(connect.CodeAlreadyExists, "no SIM numeric ID is available")
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "unable to synchronize the SIM identity")
	}
	return &communicationv1.UpdateSimIdentityResponse{SlotId: assignment.SlotID, SimId: assignment.SIMID, NumericId: int32(assignment.NumericID)}, nil
}

func (service *deviceService) ListInstallationRecipients(ctx context.Context, request *communicationv1.ListInstallationRecipientsRequest) (*communicationv1.ListInstallationRecipientsResponse, error) {
	if request == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, "device UUID is required")
	}
	if err := service.requireDeviceCredential(ctx, request.GetDeviceUuid()); err != nil {
		return nil, err
	}
	installations, err := service.router.repository.ListApprovedAppInstallationRecipients(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "unable to list trusted app installations")
	}
	response := &communicationv1.ListInstallationRecipientsResponse{}
	for _, installation := range installations {
		response.Installations = append(response.Installations, &communicationv1.InstallationRecipient{InstallationId: installation.InstallationID, IdentityPublicKey: installation.IdentityPublicKey, KeyAgreementPublicKey: installation.KeyAgreementPublicKey})
	}
	return response, nil
}

func (service *deviceService) UploadIncomingCall(ctx context.Context, request *communicationv1.UploadIncomingCallRequest) (*communicationv1.UploadIncomingCallResponse, error) {
	if request == nil || request.GetNotification() == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, "incoming call notification is required")
	}
	if err := service.requireDeviceCredential(ctx, request.GetNotification().GetDeviceUuid()); err != nil {
		return nil, err
	}
	payload := request.GetNotification()
	if payload.GetOccurredAt() == nil || payload.GetOccurredAt().CheckValid() != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, "incoming call timestamp is invalid")
	}
	notification := domain.IncomingCallNotification{ID: payload.GetId(), Type: payload.GetType(), DeviceUUID: payload.GetDeviceUuid(), OccurredAt: payload.GetOccurredAt().AsTime(), KeyEpoch: int(payload.GetKeyEpoch())}
	for _, recipient := range payload.GetRecipients() {
		notification.Recipients = append(notification.Recipients, domain.IncomingCallRecipient{InstallationID: recipient.GetInstallationId(), SenderPublicKey: recipient.GetSenderPublicKey(), Nonce: recipient.GetNonce(), Ciphertext: recipient.GetCiphertext(), Signature: recipient.GetSignature()})
	}
	publicKey, err := service.router.repository.AuthenticateDevice(ctx, payload.GetDeviceUuid(), rpcBearer(ctx), service.router.now())
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, "device credential is invalid or revoked")
	}
	if err := e2ee.VerifyIncomingCallNotification(ed25519.PublicKey(publicKey), notification); err != nil {
		if errors.Is(err, e2ee.ErrInvalidIncomingCall) {
			return nil, connect.NewError(connect.CodeInvalidArgument, err.Error())
		}
		return nil, connect.NewError(connect.CodePermissionDenied, "incoming call signature is invalid")
	}
	now := service.router.now()
	if notification.OccurredAt.After(now.Add(maxEventFutureSkew)) {
		return nil, connect.NewError(connect.CodeInvalidArgument, "incoming call timestamp is too far in the future")
	}
	expiresAt := notification.OccurredAt.Add(domain.IncomingCallRetention)
	if !expiresAt.After(now) {
		return nil, connect.NewError(connect.CodeInvalidArgument, "incoming call is outside the retention window")
	}
	created, err := service.router.repository.InsertIncomingCall(ctx, notification, expiresAt)
	if errors.Is(err, domain.ErrIdempotencyConflict) || errors.Is(err, domain.ErrUntrustedInstallationRecipient) {
		return nil, connect.NewError(connect.CodeAlreadyExists, err.Error())
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "unable to store the incoming call")
	}
	_ = created
	return &communicationv1.UploadIncomingCallResponse{}, nil
}

func (service *deviceService) ClaimCommand(ctx context.Context, request *communicationv1.ClaimCommandRequest) (*communicationv1.ClaimCommandResponse, error) {
	if request == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, "device UUID is required")
	}
	if err := service.requireDeviceCredential(ctx, request.GetDeviceUuid()); err != nil {
		return nil, err
	}
	now := service.router.now()
	command, err := service.router.repository.ClaimCommand(ctx, request.GetDeviceUuid(), now, now.Add(commandLease))
	if errors.Is(err, pgx.ErrNoRows) {
		return &communicationv1.ClaimCommandResponse{HasCommand: false}, nil
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "unable to claim an encrypted command")
	}
	return &communicationv1.ClaimCommandResponse{HasCommand: true, Command: &communicationv1.EncryptedCommand{Id: command.ID, DeviceUuid: command.DeviceUUID, SenderInstallationId: command.SenderInstallationID, SenderPublicKey: command.SenderPublicKey, Nonce: command.Nonce, Ciphertext: command.Ciphertext, Signature: command.Signature, CreatedAt: timestamppb.New(command.CreatedAt), ExpiresAt: timestamppb.New(command.ExpiresAt)}}, nil
}

func (service *deviceService) CompleteCommand(ctx context.Context, request *communicationv1.CompleteCommandRequest) (*communicationv1.CompleteCommandResponse, error) {
	if request == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, "command result is required")
	}
	if err := service.requireDeviceCredential(ctx, request.GetDeviceUuid()); err != nil {
		return nil, err
	}
	if !validUUID(request.GetCommandId()) || len(request.GetNonce()) != commandNonceBytes || len(request.GetCiphertext()) == 0 || len(request.GetCiphertext()) > maxCommandCiphertextBytes {
		return nil, connect.NewError(connect.CodeInvalidArgument, "encrypted command result is invalid")
	}
	err := service.router.repository.CompleteCommand(ctx, request.GetDeviceUuid(), request.GetCommandId(), domain.EncryptedCommandResult{Nonce: request.GetNonce(), Ciphertext: request.GetCiphertext()}, service.router.now())
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, connect.NewError(connect.CodeNotFound, "encrypted command was not found or has expired")
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "unable to complete the encrypted command")
	}
	return &communicationv1.CompleteCommandResponse{}, nil
}

func (service *deviceService) UpdateCommandKey(ctx context.Context, request *communicationv1.UpdateCommandKeyRequest) (*communicationv1.UpdateCommandKeyResponse, error) {
	if request == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, "command public key is required")
	}
	if err := service.requireDeviceCredential(ctx, request.GetDeviceUuid()); err != nil {
		return nil, err
	}
	if len(request.GetCommandPublicKey()) != commandPublicKeyBytes {
		return nil, connect.NewError(connect.CodeInvalidArgument, "device command public key is invalid")
	}
	err := service.router.repository.UpdateDeviceCommandPublicKey(ctx, request.GetDeviceUuid(), request.GetCommandPublicKey())
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, connect.NewError(connect.CodeNotFound, "device not found")
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "unable to update the device command key")
	}
	return &communicationv1.UpdateCommandKeyResponse{}, nil
}

func (service *deviceService) SubmitDiscovery(ctx context.Context, request *communicationv1.SubmitDiscoveryRequest) (*communicationv1.SubmitDiscoveryResponse, error) {
	if request == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, "device discovery is required")
	}
	slotKey, slotIndex := request.GetSlotKey(), int(request.GetSlotIndex())
	if slotKey == "" {
		slotKey = "1"
	}
	if slotIndex == 0 {
		slotIndex = 1
	}
	if !validUUID(request.GetDeviceUuid()) || slotIndex < 1 || slotIndex > 32 || len(slotKey) > 64 || len(request.GetIdentityPublicKey()) != ed25519.PublicKeySize || len(request.GetCommandPublicKey()) != commandPublicKeyBytes || len(request.GetSignature()) != ed25519.SignatureSize || strings.TrimSpace(request.GetPendingCredential()) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "device discovery identity is invalid")
	}
	var simFingerprint []byte
	if request.GetSimFingerprint() != "" {
		decoded, err := hex.DecodeString(request.GetSimFingerprint())
		if err != nil || len(decoded) != 32 {
			return nil, connect.NewError(connect.CodeInvalidArgument, "SIM fingerprint is invalid")
		}
		simFingerprint = decoded
	}
	credentialHash, err := auth.TokenHash(request.GetPendingCredential())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, "pending device credential is invalid")
	}
	legacyProof := deviceDiscoverySignedData(request.GetDeviceUuid(), request.GetIdentityPublicKey(), request.GetCommandPublicKey(), request.GetPendingCredential())
	proof := deviceDiscoverySignedDataWithSlot(request.GetDeviceUuid(), request.GetIdentityPublicKey(), request.GetCommandPublicKey(), request.GetPendingCredential(), slotKey, slotIndex, request.GetSimFingerprint())
	if !ed25519.Verify(ed25519.PublicKey(request.GetIdentityPublicKey()), proof, request.GetSignature()) && !ed25519.Verify(ed25519.PublicKey(request.GetIdentityPublicKey()), legacyProof, request.GetSignature()) {
		return nil, connect.NewError(connect.CodeUnauthenticated, "device identity proof is invalid")
	}
	record, err := service.router.repository.SubmitDeviceDiscovery(ctx, service.router.now(), store.DeviceDiscoveryRequest{
		DeviceUUID: request.GetDeviceUuid(), SlotKey: slotKey, SlotIndex: slotIndex,
		SIMFingerprint: simFingerprint, IdentityPublicKey: request.GetIdentityPublicKey(),
		CommandPublicKey: request.GetCommandPublicKey(), CredentialHash: credentialHash[:],
	})
	if errors.Is(err, domain.ErrDeviceDiscoveryClosed) || errors.Is(err, domain.ErrDeviceDiscoveryConflict) || errors.Is(err, domain.ErrDeviceExists) {
		return nil, connect.NewError(connect.CodeAlreadyExists, err.Error())
	}
	if errors.Is(err, domain.ErrDeviceDiscoveryLimit) {
		return nil, connect.NewError(connect.CodeResourceExhausted, err.Error())
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "unable to submit device discovery")
	}
	return submitDiscoveryResponse(record), nil
}

func (service *deviceService) GetDiscoveryStatus(ctx context.Context, request *communicationv1.GetDiscoveryStatusRequest) (*communicationv1.GetDiscoveryStatusResponse, error) {
	if request == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, "device UUID is required")
	}
	credential := rpcBearer(ctx)
	if !validUUID(request.GetDeviceUuid()) || credential == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, "pending device credential is required")
	}
	credentialHash, err := auth.TokenHash(credential)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, "pending device credential is invalid")
	}
	record, err := service.router.repository.DeviceDiscoveryStatus(ctx, request.GetDeviceUuid(), credentialHash[:], service.router.now())
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, connect.NewError(connect.CodeUnauthenticated, "pending device credential is invalid or expired")
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "unable to read device discovery status")
	}
	return discoveryStatusResponse(record), nil
}

func (service *deviceService) PairDevice(ctx context.Context, request *communicationv1.PairDeviceRequest) (*communicationv1.PairDeviceResponse, error) {
	if request == nil || strings.TrimSpace(request.GetPairingToken()) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "pairing token is required")
	}
	if !validUUID(request.GetDeviceUuid()) || len(request.GetIdentityPublicKey()) != ed25519.PublicKeySize || len(request.GetCommandPublicKey()) != commandPublicKeyBytes || len(request.GetProfileCiphertext()) > maxProfileCiphertextBytes || len(request.GetWrappedVaultKey()) > maxWrappedVaultKeyBytes || strings.TrimSpace(request.GetDeviceCredential()) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "device identity or encrypted profile is invalid")
	}
	tokenHash, err := auth.TokenHash(request.GetPairingToken())
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, "pairing token is invalid")
	}
	credentialHash, err := auth.TokenHash(request.GetDeviceCredential())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, "device credential is invalid")
	}
	record, err := service.router.repository.ConsumePairingToken(ctx, tokenHash[:], service.router.now(), store.DevicePairing{
		DeviceUUID: request.GetDeviceUuid(), ProfileCiphertext: request.GetProfileCiphertext(), IdentityPublicKey: request.GetIdentityPublicKey(), CommandPublicKey: request.GetCommandPublicKey(), WrappedVaultKey: request.GetWrappedVaultKey(), CredentialHash: credentialHash[:],
	})
	if errors.Is(err, domain.ErrInvalidPairingToken) {
		return nil, connect.NewError(connect.CodeUnauthenticated, err.Error())
	}
	if errors.Is(err, domain.ErrDeviceExists) || errors.Is(err, domain.ErrNoNumericID) {
		return nil, connect.NewError(connect.CodeAlreadyExists, err.Error())
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "unable to pair the device")
	}
	return &communicationv1.PairDeviceResponse{DeviceUuid: record.ID, NumericId: int32(record.NumericID)}, nil
}

func submitDiscoveryResponse(record store.DeviceDiscoveryRecord) *communicationv1.SubmitDiscoveryResponse {
	response := &communicationv1.SubmitDiscoveryResponse{DeviceUuid: record.DeviceUUID, Status: string(record.Status), NumericId: int32(record.NumericID), SlotKey: record.SlotKey, SlotIndex: int32(record.SlotIndex), IdentityPublicKey: record.IdentityPublicKey, CommandPublicKey: record.CommandPublicKey, FirstSeenAt: timestamppb.New(record.FirstSeenAt), LastSeenAt: timestamppb.New(record.LastSeenAt), ExpiresAt: timestamppb.New(record.ExpiresAt)}
	if record.SlotID != nil {
		response.SlotId = record.SlotID
	}
	if record.SIMID != nil {
		response.SimId = record.SIMID
	}
	if record.DecidedAt != nil {
		response.DecidedAt = timestamppb.New(*record.DecidedAt)
	}
	return response
}

func discoveryStatusResponse(record store.DeviceDiscoveryRecord) *communicationv1.GetDiscoveryStatusResponse {
	response := &communicationv1.GetDiscoveryStatusResponse{DeviceUuid: record.DeviceUUID, Status: string(record.Status), NumericId: int32(record.NumericID), SlotKey: record.SlotKey, SlotIndex: int32(record.SlotIndex), IdentityPublicKey: record.IdentityPublicKey, CommandPublicKey: record.CommandPublicKey, FirstSeenAt: timestamppb.New(record.FirstSeenAt), LastSeenAt: timestamppb.New(record.LastSeenAt), ExpiresAt: timestamppb.New(record.ExpiresAt)}
	if record.SlotID != nil {
		response.SlotId = record.SlotID
	}
	if record.SIMID != nil {
		response.SimId = record.SIMID
	}
	if record.DecidedAt != nil {
		response.DecidedAt = timestamppb.New(*record.DecidedAt)
	}
	return response
}
