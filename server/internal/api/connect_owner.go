package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	connect "connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/jackc/pgx/v5"
	communicationv1 "github.com/orangeboyChen/relay-sdk/gen/go/relay/communication/v1"
	communicationv1connect "github.com/orangeboyChen/relay-sdk/gen/go/relay/communication/v1/communicationv1connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"sim-deck-server/internal/auth"
	"sim-deck-server/internal/domain"
	"sim-deck-server/internal/store"
)

type ownerService struct {
	communicationv1connect.UnimplementedOwnerServiceHandler
	router *router
}

func mountOwnerService(mux *http.ServeMux, router *router) {
	server := connect.NewServer(rpcAuthInterceptor(router))
	communicationv1connect.RegisterOwnerServiceHandler(server, &ownerService{router: router})
	connecthttp.Mount(mux, server)
}

func (service *ownerService) ListDevices(ctx context.Context, request *communicationv1.ListDevicesRequest) (*communicationv1.ListDevicesResponse, error) {
	if _, err := ownerIDFromRPC(ctx); err != nil {
		return nil, err
	}
	devices, err := service.router.repository.ListDevices(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "unable to list devices")
	}
	response := &communicationv1.ListDevicesResponse{}
	for _, device := range devices {
		item := &communicationv1.DeviceRecord{DeviceUuid: device.ID, NumericId: int32(device.NumericID), ProfileCiphertext: device.ProfileCiphertext, IdentityPublicKey: device.IdentityPublicKey, CommandPublicKey: device.CommandPublicKey, WrappedVaultKey: device.WrappedVaultKey, PairedAt: timestamppb.New(device.PairedAt), RadioAccessTechnology: device.RadioAccessTechnology}
		if device.LastSeenAt != nil {
			item.LastSeenAt = timestamppb.New(*device.LastSeenAt)
		}
		if device.SignalBars != nil {
			value := int32(*device.SignalBars)
			item.SignalBars = &value
		}
		if device.RadioStatusUpdatedAt != nil {
			item.RadioStatusUpdatedAt = timestamppb.New(*device.RadioStatusUpdatedAt)
		}
		for _, slot := range device.Slots {
			s := &communicationv1.SlotRecord{SlotId: slot.ID, SlotKey: slot.SlotKey, SlotIndex: int32(slot.SlotIndex), ProfileCiphertext: slot.ProfileCiphertext, RadioAccessTechnology: slot.RadioAccessTechnology}
			if slot.SIMID != nil {
				s.SimId = slot.SIMID
			}
			if slot.SIMNumericID != nil {
				value := int32(*slot.SIMNumericID)
				s.SimNumericId = &value
			}
			if slot.LastSeenAt != nil {
				s.LastSeenAt = timestamppb.New(*slot.LastSeenAt)
			}
			if slot.SignalBars != nil {
				value := int32(*slot.SignalBars)
				s.SignalBars = &value
			}
			if slot.RadioStatusUpdatedAt != nil {
				s.RadioStatusUpdatedAt = timestamppb.New(*slot.RadioStatusUpdatedAt)
			}
			item.Slots = append(item.Slots, s)
		}
		response.Devices = append(response.Devices, item)
	}
	return response, nil
}

func (service *ownerService) ListSimCards(ctx context.Context, request *communicationv1.ListSimCardsRequest) (*communicationv1.ListSimCardsResponse, error) {
	if _, err := ownerIDFromRPC(ctx); err != nil {
		return nil, err
	}
	cards, err := service.router.repository.ListSIMCards(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "unable to list SIM cards")
	}
	response := &communicationv1.ListSimCardsResponse{}
	for _, card := range cards {
		item := &communicationv1.SimRecord{SimId: card.ID, NumericId: int32(card.NumericID), ProfileCiphertext: card.ProfileCiphertext, FirstSeenAt: timestamppb.New(card.FirstSeenAt), LastSeenAt: timestamppb.New(card.LastSeenAt)}
		if card.AssignedDeviceID != nil {
			item.AssignedDeviceUuid = card.AssignedDeviceID
		}
		if card.AssignedSlotID != nil {
			item.AssignedSlotId = card.AssignedSlotID
		}
		response.Sims = append(response.Sims, item)
	}
	return response, nil
}

func (service *ownerService) RetireSimCard(ctx context.Context, request *communicationv1.RetireSimCardRequest) (*communicationv1.RetireSimCardResponse, error) {
	if _, err := ownerIDFromRPC(ctx); err != nil {
		return nil, err
	}
	if !validUUID(request.GetSimId()) {
		return nil, connect.NewError(connect.CodeInvalidArgument, "SIM identifier is invalid")
	}
	err := service.router.repository.RetireSIMCard(ctx, request.GetSimId(), service.router.now())
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, connect.NewError(connect.CodeNotFound, "SIM card not found or already removed")
	}
	if errors.Is(err, domain.ErrSIMAssigned) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, "remove the SIM card from its device before deleting it")
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "unable to remove the SIM card")
	}
	return &communicationv1.RetireSimCardResponse{}, nil
}

func (service *ownerService) UpdateDeviceNumericId(ctx context.Context, request *communicationv1.UpdateDeviceNumericIdRequest) (*communicationv1.UpdateDeviceNumericIdResponse, error) {
	if _, err := ownerIDFromRPC(ctx); err != nil {
		return nil, err
	}
	if !validUUID(request.GetId()) || request.GetNumericId() < 1 || request.GetNumericId() > 99 {
		return nil, connect.NewError(connect.CodeInvalidArgument, "device numeric ID is invalid")
	}
	err := service.router.repository.UpdateNumericID(ctx, request.GetId(), int(request.GetNumericId()))
	if errors.Is(err, domain.ErrDuplicateNumericID) {
		return nil, connect.NewError(connect.CodeAlreadyExists, err.Error())
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, connect.NewError(connect.CodeNotFound, "device not found")
	}
	if errors.Is(err, domain.ErrInvalidNumericID) {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error())
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "unable to update the device")
	}
	return &communicationv1.UpdateDeviceNumericIdResponse{}, nil
}

func (service *ownerService) UpdateDeviceProfile(ctx context.Context, request *communicationv1.UpdateDeviceProfileRequest) (*communicationv1.UpdateDeviceProfileResponse, error) {
	if _, err := ownerIDFromRPC(ctx); err != nil {
		return nil, err
	}
	if !validUUID(request.GetId()) || len(request.GetProfileCiphertext()) == 0 || len(request.GetProfileCiphertext()) > maxProfileCiphertextBytes {
		return nil, connect.NewError(connect.CodeInvalidArgument, "encrypted device profile is invalid")
	}
	err := service.router.repository.UpdateDeviceProfile(ctx, request.GetId(), request.GetProfileCiphertext())
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, connect.NewError(connect.CodeNotFound, "device not found")
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "unable to update the device profile")
	}
	return &communicationv1.UpdateDeviceProfileResponse{}, nil
}

func (service *ownerService) UpdateSimNumericId(ctx context.Context, request *communicationv1.UpdateSimNumericIdRequest) (*communicationv1.UpdateSimNumericIdResponse, error) {
	if _, err := ownerIDFromRPC(ctx); err != nil {
		return nil, err
	}
	if !validUUID(request.GetId()) || request.GetNumericId() < 1 || request.GetNumericId() > 99 {
		return nil, connect.NewError(connect.CodeInvalidArgument, "SIM numeric ID is invalid")
	}
	err := service.router.repository.UpdateSIMNumericID(ctx, request.GetId(), int(request.GetNumericId()))
	if errors.Is(err, domain.ErrDuplicateNumericID) {
		return nil, connect.NewError(connect.CodeAlreadyExists, err.Error())
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, connect.NewError(connect.CodeNotFound, "SIM not found")
	}
	if errors.Is(err, domain.ErrInvalidNumericID) {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error())
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "unable to update the SIM")
	}
	return &communicationv1.UpdateSimNumericIdResponse{}, nil
}

func (service *ownerService) UpdateSimProfile(ctx context.Context, request *communicationv1.UpdateSimProfileRequest) (*communicationv1.UpdateSimProfileResponse, error) {
	if _, err := ownerIDFromRPC(ctx); err != nil {
		return nil, err
	}
	if !validUUID(request.GetId()) || len(request.GetProfileCiphertext()) == 0 || len(request.GetProfileCiphertext()) > maxProfileCiphertextBytes {
		return nil, connect.NewError(connect.CodeInvalidArgument, "encrypted SIM profile is invalid")
	}
	err := service.router.repository.UpdateSIMProfile(ctx, request.GetId(), request.GetProfileCiphertext())
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, connect.NewError(connect.CodeNotFound, "SIM not found")
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "unable to update the SIM profile")
	}
	return &communicationv1.UpdateSimProfileResponse{}, nil
}

func (service *ownerService) RevokeDevice(ctx context.Context, request *communicationv1.RevokeDeviceRequest) (*communicationv1.RevokeDeviceResponse, error) {
	if _, err := ownerIDFromRPC(ctx); err != nil {
		return nil, err
	}
	if !validUUID(request.GetDeviceUuid()) {
		return nil, connect.NewError(connect.CodeInvalidArgument, "device identifier is invalid")
	}
	err := service.router.repository.RevokeDevice(ctx, request.GetDeviceUuid(), service.router.now())
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, connect.NewError(connect.CodeNotFound, "device not found")
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "unable to remove the device")
	}
	return &communicationv1.RevokeDeviceResponse{}, nil
}

func (service *ownerService) CreatePairingToken(ctx context.Context, request *communicationv1.CreatePairingTokenRequest) (*communicationv1.CreatePairingTokenResponse, error) {
	ownerID, err := ownerIDFromRPC(ctx)
	if err != nil {
		return nil, err
	}
	token, err := auth.NewOpaqueToken()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "unable to create a pairing token")
	}
	hash, err := auth.TokenHash(token)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "unable to create a pairing token")
	}
	expiresAt := service.router.now().Add(pairingTokenLifetime)
	if err := service.router.repository.CreatePairingToken(ctx, ownerID, hash[:], expiresAt); err != nil {
		return nil, connect.NewError(connect.CodeInternal, "unable to store the pairing token")
	}
	return &communicationv1.CreatePairingTokenResponse{PairingToken: token, ExpiresAt: timestamppb.New(expiresAt)}, nil
}

func (service *ownerService) OpenDeviceDiscovery(ctx context.Context, request *communicationv1.OpenDeviceDiscoveryRequest) (*communicationv1.OpenDeviceDiscoveryResponse, error) {
	ownerID, err := ownerIDFromRPC(ctx)
	if err != nil {
		return nil, err
	}
	now := service.router.now()
	session, err := service.router.repository.OpenDeviceDiscovery(ctx, ownerID, now, now.Add(deviceDiscoveryLifetime))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "unable to open device discovery")
	}
	return &communicationv1.OpenDeviceDiscoveryResponse{Active: session.Active(now), Session: discoverySessionProto(session)}, nil
}

func (service *ownerService) GetDeviceDiscoverySession(ctx context.Context, request *communicationv1.GetDeviceDiscoverySessionRequest) (*communicationv1.GetDeviceDiscoverySessionResponse, error) {
	ownerID, err := ownerIDFromRPC(ctx)
	if err != nil {
		return nil, err
	}
	session, err := service.router.repository.GetDeviceDiscoverySession(ctx, ownerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return &communicationv1.GetDeviceDiscoverySessionResponse{Active: false}, nil
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "unable to read device discovery")
	}
	now := service.router.now()
	return &communicationv1.GetDeviceDiscoverySessionResponse{Active: session.Active(now), Session: discoverySessionProto(session)}, nil
}

func (service *ownerService) CloseDeviceDiscovery(ctx context.Context, request *communicationv1.CloseDeviceDiscoveryRequest) (*communicationv1.CloseDeviceDiscoveryResponse, error) {
	ownerID, err := ownerIDFromRPC(ctx)
	if err != nil {
		return nil, err
	}
	if err := service.router.repository.CloseDeviceDiscovery(ctx, ownerID, service.router.now()); err != nil {
		return nil, connect.NewError(connect.CodeInternal, "unable to close device discovery")
	}
	return &communicationv1.CloseDeviceDiscoveryResponse{}, nil
}

func (service *ownerService) ListDeviceDiscoveries(ctx context.Context, request *communicationv1.ListDeviceDiscoveriesRequest) (*communicationv1.ListDeviceDiscoveriesResponse, error) {
	ownerID, err := ownerIDFromRPC(ctx)
	if err != nil {
		return nil, err
	}
	records, err := service.router.repository.ListPendingDeviceDiscoveries(ctx, ownerID, service.router.now())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "unable to list discovered devices")
	}
	response := &communicationv1.ListDeviceDiscoveriesResponse{}
	for _, record := range records {
		response.Discoveries = append(response.Discoveries, discoveryProto(record))
	}
	return response, nil
}

func (service *ownerService) ApproveDeviceDiscovery(ctx context.Context, request *communicationv1.ApproveDeviceDiscoveryRequest) (*communicationv1.ApproveDeviceDiscoveryResponse, error) {
	ownerID, err := ownerIDFromRPC(ctx)
	if err != nil {
		return nil, err
	}
	if request == nil || !validUUID(request.GetDeviceUuid()) {
		return nil, connect.NewError(connect.CodeInvalidArgument, "device identifier is invalid")
	}
	device, err := service.router.repository.ApproveDeviceDiscovery(ctx, ownerID, request.GetDeviceUuid(), service.router.now())
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, connect.NewError(connect.CodeNotFound, "device discovery was not found")
	}
	if errors.Is(err, domain.ErrDeviceDiscoveryExpired) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err.Error())
	}
	if errors.Is(err, domain.ErrDeviceDiscoveryRejected) || errors.Is(err, domain.ErrDeviceExists) || errors.Is(err, domain.ErrNoNumericID) {
		return nil, connect.NewError(connect.CodeAlreadyExists, err.Error())
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "unable to approve device discovery")
	}
	return &communicationv1.ApproveDeviceDiscoveryResponse{Device: &communicationv1.OwnerDeviceDiscovery{DeviceUuid: device.ID, NumericId: int32(device.NumericID)}}, nil
}

func (service *ownerService) RejectDeviceDiscovery(ctx context.Context, request *communicationv1.RejectDeviceDiscoveryRequest) (*communicationv1.RejectDeviceDiscoveryResponse, error) {
	ownerID, err := ownerIDFromRPC(ctx)
	if err != nil {
		return nil, err
	}
	if request == nil || !validUUID(request.GetDeviceUuid()) {
		return nil, connect.NewError(connect.CodeInvalidArgument, "device identifier is invalid")
	}
	err = service.router.repository.RejectDeviceDiscovery(ctx, ownerID, request.GetDeviceUuid(), service.router.now())
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, connect.NewError(connect.CodeNotFound, "device discovery was not found")
	}
	if errors.Is(err, domain.ErrDeviceDiscoveryExpired) || errors.Is(err, domain.ErrDeviceExists) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err.Error())
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "unable to reject device discovery")
	}
	return &communicationv1.RejectDeviceDiscoveryResponse{}, nil
}

func (service *ownerService) RegisterAppInstallation(ctx context.Context, request *communicationv1.RegisterAppInstallationRequest) (*communicationv1.RegisterAppInstallationResponse, error) {
	ownerID, err := ownerIDFromRPC(ctx)
	if err != nil {
		return nil, err
	}
	if request == nil || !validUUID(request.GetInstallationId()) || len(request.GetIdentityPublicKey()) != installationPublicKeyBytes || len(request.GetKeyAgreementPublicKey()) != installationPublicKeyBytes || len(request.GetWrappedVaultKey()) > maxWrappedVaultKeyBytes {
		return nil, connect.NewError(connect.CodeInvalidArgument, "installation identity or wrapped vault key is invalid")
	}
	record, created, err := service.router.repository.RegisterAppInstallation(ctx, ownerID, store.AppInstallationRegistration{ID: request.GetInstallationId(), IdentityPublicKey: request.GetIdentityPublicKey(), KeyAgreementPublicKey: request.GetKeyAgreementPublicKey(), WrappedVaultKey: request.GetWrappedVaultKey()}, service.router.now())
	if errors.Is(err, domain.ErrInstallationConflict) {
		return nil, connect.NewError(connect.CodeAlreadyExists, err.Error())
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "unable to register the app installation")
	}
	return &communicationv1.RegisterAppInstallationResponse{Installation: appInstallationProto(record), Created: created}, nil
}

func (service *ownerService) UpsertPushToken(ctx context.Context, request *communicationv1.UpsertPushTokenRequest) (*communicationv1.UpsertPushTokenResponse, error) {
	ownerID, err := ownerIDFromRPC(ctx)
	if err != nil {
		return nil, err
	}
	if request == nil || !validUUID(request.GetInstallationId()) || (request.GetKind() != "alert" && request.GetKind() != "voip") || (request.GetEnvironment() != "sandbox" && request.GetEnvironment() != "production") || len(request.GetToken()) == 0 || len(request.GetToken()) > maxPushTokenBytes {
		return nil, connect.NewError(connect.CodeInvalidArgument, "push token registration is invalid")
	}
	err = service.router.repository.UpsertPushToken(ctx, ownerID, request.GetInstallationId(), request.GetEnvironment(), request.GetKind(), request.GetToken(), service.router.now())
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, connect.NewError(connect.CodeNotFound, "the app installation is not approved")
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "unable to register the push token")
	}
	return &communicationv1.UpsertPushTokenResponse{}, nil
}

func (service *ownerService) DeletePushToken(ctx context.Context, request *communicationv1.DeletePushTokenRequest) (*communicationv1.DeletePushTokenResponse, error) {
	ownerID, err := ownerIDFromRPC(ctx)
	if err != nil {
		return nil, err
	}
	if request == nil || !validUUID(request.GetInstallationId()) || (request.GetKind() != "alert" && request.GetKind() != "voip") || (request.GetEnvironment() != "sandbox" && request.GetEnvironment() != "production") {
		return nil, connect.NewError(connect.CodeInvalidArgument, "push token route is invalid")
	}
	err = service.router.repository.DeletePushToken(ctx, ownerID, request.GetInstallationId(), request.GetEnvironment(), request.GetKind())
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, connect.NewError(connect.CodeNotFound, "the push token route does not exist")
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "unable to delete the push token")
	}
	return &communicationv1.DeletePushTokenResponse{}, nil
}

func (service *ownerService) ClaimIncomingCall(ctx context.Context, request *communicationv1.ClaimIncomingCallRequest) (*communicationv1.ClaimIncomingCallResponse, error) {
	ownerID, err := ownerIDFromRPC(ctx)
	if err != nil {
		return nil, err
	}
	if request == nil || !validUUID(request.GetNotificationId()) || !validUUID(request.GetInstallationId()) {
		return nil, connect.NewError(connect.CodeInvalidArgument, "notification and installation identities are invalid")
	}
	claim, err := service.router.repository.ClaimIncomingCall(ctx, ownerID, request.GetNotificationId(), request.GetInstallationId(), service.router.now())
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, connect.NewError(connect.CodeNotFound, "incoming call is unavailable to this installation")
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "unable to claim the incoming call")
	}
	return &communicationv1.ClaimIncomingCallResponse{NotificationId: claim.NotificationID, InstallationId: claim.InstallationID, ClaimedAt: timestamppb.New(claim.ClaimedAt), Won: claim.Won}, nil
}

func (service *ownerService) EnqueueCommand(ctx context.Context, request *communicationv1.EnqueueCommandRequest) (*communicationv1.EnqueueCommandResponse, error) {
	command := request.GetCommand()
	if command == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, "command is required")
	}
	if _, err := ownerIDFromRPC(ctx); err != nil {
		return nil, err
	}
	if command.GetCreatedAt() == nil || command.GetExpiresAt() == nil || command.GetCreatedAt().CheckValid() != nil || command.GetExpiresAt().CheckValid() != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, "encrypted command timestamps are invalid")
	}
	encrypted := domain.EncryptedCommand{ID: command.GetId(), DeviceUUID: command.GetDeviceUuid(), SenderInstallationID: command.GetSenderInstallationId(), SenderPublicKey: command.GetSenderPublicKey(), Nonce: command.GetNonce(), Ciphertext: command.GetCiphertext(), Signature: command.GetSignature(), CreatedAt: command.GetCreatedAt().AsTime(), ExpiresAt: command.GetExpiresAt().AsTime()}
	now := service.router.now()
	if !validUUID(encrypted.ID) || !validUUID(encrypted.DeviceUUID) || !validUUID(encrypted.SenderInstallationID) || len(encrypted.SenderPublicKey) != commandPublicKeyBytes || len(encrypted.Nonce) != commandNonceBytes || len(encrypted.Ciphertext) == 0 || len(encrypted.Ciphertext) > maxCommandCiphertextBytes || len(encrypted.Signature) != commandSignatureBytes || encrypted.CreatedAt.After(now.Add(maxEventFutureSkew)) || encrypted.CreatedAt.Before(now.Add(-maxEventFutureSkew)) || !encrypted.ExpiresAt.After(now) || encrypted.ExpiresAt.After(encrypted.CreatedAt.Add(maxCommandLifetime)) {
		return nil, connect.NewError(connect.CodeInvalidArgument, "encrypted command metadata is invalid")
	}
	created, err := service.router.repository.EnqueueCommand(ctx, encrypted)
	if errors.Is(err, domain.ErrIdempotencyConflict) {
		return nil, connect.NewError(connect.CodeAlreadyExists, err.Error())
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, connect.NewError(connect.CodeNotFound, "device not found")
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "unable to queue the encrypted command")
	}
	return &communicationv1.EnqueueCommandResponse{CommandId: encrypted.ID, Created: created}, nil
}

func (service *ownerService) GetCommandStatus(ctx context.Context, request *communicationv1.GetCommandStatusRequest) (*communicationv1.GetCommandStatusResponse, error) {
	if _, err := ownerIDFromRPC(ctx); err != nil {
		return nil, err
	}
	if request == nil || !validUUID(request.GetDeviceUuid()) || !validUUID(request.GetCommandId()) {
		return nil, connect.NewError(connect.CodeInvalidArgument, "command identity is invalid")
	}
	status, err := service.router.repository.CommandStatus(ctx, request.GetDeviceUuid(), request.GetCommandId(), service.router.now())
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, connect.NewError(connect.CodeNotFound, "encrypted command was not found or has expired")
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "unable to read encrypted command status")
	}
	response := &communicationv1.GetCommandStatusResponse{CommandId: status.ID, DeviceUuid: status.DeviceUUID, State: status.State}
	if status.Result != nil {
		response.ResultNonce = status.Result.Nonce
		response.ResultCiphertext = status.Result.Ciphertext
	}
	if status.CompletedAt != nil {
		response.CompletedAt = timestamppb.New(*status.CompletedAt)
	}
	return response, nil
}

func (service *ownerService) GetCommandRecipient(ctx context.Context, request *communicationv1.GetCommandRecipientRequest) (*communicationv1.GetCommandRecipientResponse, error) {
	response := new(communicationv1.GetCommandRecipientResponse)
	devices, err := service.router.repository.ListDevices(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "unable to list devices")
	}
	for _, device := range devices {
		if device.ID == request.GetDeviceUuid() {
			if len(device.CommandPublicKey) != commandPublicKeyBytes {
				return nil, connect.NewError(connect.CodeFailedPrecondition, "device command recipient is unavailable")
			}
			response.CommandPublicKey = device.CommandPublicKey
			return response, nil
		}
	}
	return nil, connect.NewError(connect.CodeNotFound, "device command recipient was not found")
}

func (service *ownerService) GetTurnCredentials(ctx context.Context, request *communicationv1.GetTurnCredentialsRequest) (*communicationv1.GetTurnCredentialsResponse, error) {
	if _, err := ownerIDFromRPC(ctx); err != nil {
		return nil, err
	}
	return service.router.turnCredentialsProto()
}

func appInstallationProto(record store.AppInstallationRecord) *communicationv1.AppInstallation {
	item := &communicationv1.AppInstallation{InstallationId: record.ID, IdentityPublicKey: record.IdentityPublicKey, KeyAgreementPublicKey: record.KeyAgreementPublicKey, WrappedVaultKey: record.WrappedVaultKey, CreatedAt: timestamppb.New(record.CreatedAt)}
	if record.ApprovedAt != nil {
		item.ApprovedAt = timestamppb.New(*record.ApprovedAt)
	}
	if record.RevokedAt != nil {
		item.RevokedAt = timestamppb.New(*record.RevokedAt)
	}
	return item
}

func discoverySessionProto(session store.DeviceDiscoverySession) *communicationv1.DeviceDiscoverySession {
	item := &communicationv1.DeviceDiscoverySession{OpenedAt: timestamppb.New(session.OpenedAt), ExpiresAt: timestamppb.New(session.ExpiresAt)}
	if session.ClosedAt != nil {
		item.ClosedAt = timestamppb.New(*session.ClosedAt)
	}
	return item
}

func discoveryProto(record store.DeviceDiscoveryRecord) *communicationv1.OwnerDeviceDiscovery {
	item := &communicationv1.OwnerDeviceDiscovery{DeviceUuid: record.DeviceUUID, IdentityPublicKey: record.IdentityPublicKey, CommandPublicKey: record.CommandPublicKey, Status: string(record.Status), FirstSeenAt: timestamppb.New(record.FirstSeenAt), LastSeenAt: timestamppb.New(record.LastSeenAt), ExpiresAt: timestamppb.New(record.ExpiresAt), NumericId: int32(record.NumericID), SlotKey: record.SlotKey, SlotIndex: int32(record.SlotIndex)}
	if record.SlotID != nil {
		item.SlotId = record.SlotID
	}
	if record.SIMID != nil {
		item.SimId = record.SIMID
	}
	return item
}

func (router *router) turnCredentialsProto() (*communicationv1.GetTurnCredentialsResponse, error) {
	if router.config.TURNURL == "" || len(router.config.TURNSecret) == 0 {
		return nil, connect.NewError(connect.CodeUnavailable, "TURN media relay is not configured")
	}
	ttl := router.config.TURNTTL
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	credentials := NewTURNCredentials(router.config.TURNURL, router.config.TURNSecret, router.now(), ttl)
	return &communicationv1.GetTurnCredentialsResponse{Urls: credentials.URLs, Username: credentials.Username, Credential: credentials.Credential, ExpiresAt: timestamppb.New(credentials.ExpiresAt)}, nil
}
