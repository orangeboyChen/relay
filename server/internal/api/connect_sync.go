package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	connect "connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/jackc/pgx/v5"
	communicationv1 "github.com/orangeboyChen/relay-sdk/gen/go/relay/communication/v1"
	communicationv1connect "github.com/orangeboyChen/relay-sdk/gen/go/relay/communication/v1/communicationv1connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"sim-deck-server/internal/domain"
	"sim-deck-server/internal/e2ee"
)

const eventStreamPollInterval = 500 * time.Millisecond
const deviceStatusEventRetention = 2 * time.Minute

type rpcOwnerContextKey struct{}

func ownerIDFromRPC(ctx context.Context) (string, error) {
	ownerID, ok := ctx.Value(rpcOwnerContextKey{}).(string)
	if !ok || ownerID == "" {
		return "", connect.NewError(connect.CodeUnauthenticated, "owner credential is required")
	}
	return ownerID, nil
}

type historyRepository interface {
	GetHistorySnapshot(context.Context, string, int) (domain.HistorySnapshot, error)
}

type syncService struct {
	communicationv1connect.UnimplementedCommunicationSyncServiceHandler
	router *router
}

func mountSyncService(mux *http.ServeMux, router *router) {
	server := connect.NewServer(rpcAuthInterceptor(router))
	communicationv1connect.RegisterCommunicationSyncServiceHandler(server, &syncService{router: router})
	connecthttp.Mount(mux, server)
}

func rpcAuthInterceptor(router *router) connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			info, ok := connect.CallInfoForServerContext(ctx)
			if !ok {
				return connect.NewError(connect.CodeUnauthenticated, "missing request metadata")
			}
			if isPublicAuthProcedure(spec.Procedure) {
				return next(ctx, spec, stream)
			}
			if isDeviceProcedure(spec.Procedure) {
				if deviceProcedureUsesCeremonyCredential(spec.Procedure) {
					return next(ctx, spec, stream)
				}
				return authenticateDeviceRPC(ctx, spec, stream, router, next)
			}
			token, ok := bearerToken(info.RequestHeader().Get("Authorization"))
			if !ok {
				return connect.NewError(connect.CodeUnauthenticated, "bearer credential is required")
			}
			if spec.Procedure == communicationv1connect.CommunicationSyncServicePublishEventProcedure {
				return next(ctx, spec, stream)
			}
			ownerID, err := router.repository.AuthenticateOwnerSession(ctx, token, router.now())
			if err != nil {
				return connect.NewError(connect.CodeUnauthenticated, "access token is invalid or expired")
			}
			return next(context.WithValue(ctx, rpcOwnerContextKey{}, ownerID), spec, stream)
		}
	}
}

func isDeviceProcedure(procedure string) bool {
	const prefix = "/relay.communication.v1.DeviceService/"
	return len(procedure) >= len(prefix) && procedure[:len(prefix)] == prefix
}

func deviceProcedureUsesCeremonyCredential(procedure string) bool {
	switch procedure {
	case communicationv1connect.DeviceServicePairDeviceProcedure,
		communicationv1connect.DeviceServiceSubmitDiscoveryProcedure,
		communicationv1connect.DeviceServiceGetDiscoveryStatusProcedure:
		return true
	default:
		return false
	}
}

type replayServerStream struct {
	connect.ServerStream
	request  proto.Message
	consumed bool
}

func (stream *replayServerStream) Receive(message any) error {
	if stream.consumed {
		return stream.ServerStream.Receive(message)
	}
	stream.consumed = true
	target, ok := message.(proto.Message)
	if !ok {
		return connect.NewError(connect.CodeInternal, "RPC request is not a protobuf message")
	}
	proto.Merge(target, stream.request)
	return nil
}

func authenticateDeviceRPC(ctx context.Context, spec connect.Spec, stream connect.ServerStream, router *router, next connect.ServerFunc) error {
	info, ok := connect.CallInfoForServerContext(ctx)
	if !ok {
		return connect.NewError(connect.CodeUnauthenticated, "missing request metadata")
	}
	token, ok := bearerToken(info.RequestHeader().Get("Authorization"))
	if !ok {
		return connect.NewError(connect.CodeUnauthenticated, "device credential is required")
	}
	method, ok := spec.Schema.(protoreflect.MethodDescriptor)
	if !ok {
		return connect.NewError(connect.CodeInternal, "device RPC schema is unavailable")
	}
	input := dynamicpb.NewMessage(method.Input())
	if err := stream.Receive(input); err != nil {
		return err
	}
	deviceUUID := requestDeviceUUID(input)
	if deviceUUID == "" {
		return connect.NewError(connect.CodeInvalidArgument, "device UUID is required")
	}
	if _, err := router.repository.AuthenticateDevice(ctx, deviceUUID, token, router.now()); err != nil {
		return connect.NewError(connect.CodeUnauthenticated, "device credential is invalid or revoked")
	}
	return next(ctx, spec, &replayServerStream{ServerStream: stream, request: input})
}

func requestDeviceUUID(message protoreflect.Message) string {
	field := message.Descriptor().Fields().ByName("device_uuid")
	if field != nil {
		return message.Get(field).String()
	}
	notificationField := message.Descriptor().Fields().ByName("notification")
	if notificationField == nil || !message.Has(notificationField) {
		return ""
	}
	notification := message.Get(notificationField).Message()
	field = notification.Descriptor().Fields().ByName("device_uuid")
	if field == nil {
		return ""
	}
	return notification.Get(field).String()
}

func isPublicAuthProcedure(procedure string) bool {
	switch procedure {
	case communicationv1connect.AuthenticationServiceBootstrapProcedure,
		communicationv1connect.AuthenticationServiceRefreshProcedure,
		communicationv1connect.AuthenticationServiceBeginPasskeyLoginProcedure,
		communicationv1connect.AuthenticationServiceFinishPasskeyLoginProcedure:
		return true
	default:
		return false
	}
}

func (service *syncService) PublishEvent(ctx context.Context, request *communicationv1.PublishEventRequest) (*communicationv1.PublishEventResponse, error) {
	if request == nil || request.GetEvent() == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, "event is required")
	}
	event, err := eventFromProto(request.GetEvent())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error())
	}
	token := rpcBearer(ctx)
	publicKey, err := service.router.repository.AuthenticateDevice(ctx, event.DeviceUUID, token, service.router.now())
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, "device credential is invalid or revoked")
	}
	if err := e2ee.VerifyEnvelope(publicKey, event); err != nil {
		if errors.Is(err, e2ee.ErrInvalidEnvelope) {
			return nil, connect.NewError(connect.CodeInvalidArgument, err.Error())
		}
		return nil, connect.NewError(connect.CodePermissionDenied, "event signature is invalid")
	}
	now := service.router.now()
	if event.OccurredAt.After(now.Add(maxEventFutureSkew)) {
		return nil, connect.NewError(connect.CodeInvalidArgument, "event timestamp is too far in the future")
	}
	expiresAt := event.OccurredAt.Add(eventRetention)
	if event.Type == "device.status" {
		expiresAt = now.Add(deviceStatusEventRetention)
	}
	if !expiresAt.After(now) {
		return nil, connect.NewError(connect.CodeInvalidArgument, "event is outside the retention window")
	}
	event.Sequence = 0
	sequence, created, err := service.router.repository.InsertEvent(ctx, event, expiresAt)
	if err != nil {
		if errors.Is(err, domain.ErrIdempotencyConflict) {
			return nil, connect.NewError(connect.CodeAlreadyExists, err.Error())
		}
		return nil, connect.NewError(connect.CodeInternal, "unable to store the event")
	}
	return &communicationv1.PublishEventResponse{Sequence: sequence, Created: created}, nil
}

func (service *syncService) PullEvents(ctx context.Context, request *communicationv1.PullEventsRequest) (*communicationv1.PullEventsResponse, error) {
	if request == nil {
		request = &communicationv1.PullEventsRequest{}
	}
	after := request.GetAfterSequence()
	limit := int(request.GetPageSize())
	if after < 0 || (limit != 0 && (limit < 1 || limit > maxEventPageSize)) {
		return nil, connect.NewError(connect.CodeInvalidArgument, "event cursor or page size is invalid")
	}
	if limit == 0 {
		limit = defaultEventPageSize
	}
	events, err := service.router.repository.ListEvents(ctx, after, limit, service.router.now())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "unable to read encrypted events")
	}
	return &communicationv1.PullEventsResponse{Events: eventProtos(events), NextSequence: nextSequence(after, events)}, nil
}

type watchEventsStream interface {
	Receive() (*communicationv1.WatchEventsRequest, error)
	Send(*communicationv1.WatchEventsResponse) error
}

func (service *syncService) WatchEvents(ctx context.Context, stream communicationv1connect.CommunicationSyncServiceWatchEventsServerStream) error {
	return service.watchEvents(ctx, stream)
}

func (service *syncService) watchEvents(ctx context.Context, stream watchEventsStream) error {
	initial, err := stream.Receive()
	if err != nil {
		return err
	}
	if initial == nil {
		return connect.NewError(connect.CodeInvalidArgument, "initial event cursor is required")
	}
	cursor := initial.GetAfterSequence()
	if cursor < 0 || initial.GetAcknowledgedSequence() != 0 {
		return connect.NewError(connect.CodeInvalidArgument, "event cursor must be non-negative")
	}
	acknowledged := cursor
	streamContext, cancel := context.WithCancel(ctx)
	defer cancel()
	requests := make(chan watchEventsRequestResult, 1)
	go func() {
		for {
			request, receiveErr := stream.Receive()
			select {
			case requests <- watchEventsRequestResult{request: request, err: receiveErr}:
			case <-streamContext.Done():
				return
			}
			if receiveErr != nil {
				return
			}
		}
	}()
	ticker := time.NewTicker(eventStreamPollInterval)
	defer ticker.Stop()
	for {
		if cursor == acknowledged {
			events, listErr := service.router.repository.ListEvents(ctx, cursor, defaultEventPageSize, service.router.now())
			if listErr != nil {
				return connect.NewError(connect.CodeInternal, "unable to read encrypted events")
			}
			if len(events) > 0 {
				next := nextSequence(cursor, events)
				if sendErr := stream.Send(&communicationv1.WatchEventsResponse{Events: eventProtos(events), NextSequence: next}); sendErr != nil {
					return sendErr
				}
				cursor = next
			}
		}
		select {
		case <-streamContext.Done():
			return streamContext.Err()
		case result := <-requests:
			if errors.Is(result.err, io.EOF) {
				return nil
			}
			if result.err != nil {
				return result.err
			}
			request := result.request
			if request.GetAfterSequence() != 0 || request.GetAcknowledgedSequence() != cursor || cursor < acknowledged {
				return connect.NewError(connect.CodeInvalidArgument, "event acknowledgement does not match the delivered cursor")
			}
			acknowledged = cursor
		case <-ticker.C:
		}
	}
}

type watchEventsRequestResult struct {
	request *communicationv1.WatchEventsRequest
	err     error
}

func (service *syncService) GetHistorySnapshot(ctx context.Context, request *communicationv1.GetHistorySnapshotRequest) (*communicationv1.GetHistorySnapshotResponse, error) {
	if request == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, "history kind is required")
	}
	kind, err := historyKind(request.GetKind())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error())
	}
	ownerID, ok := ctx.Value(rpcOwnerContextKey{}).(string)
	if !ok || ownerID == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, "owner credential is required")
	}
	repository, ok := service.router.repository.(historyRepository)
	if !ok {
		return nil, connect.NewError(connect.CodeUnimplemented, "history snapshots are not configured")
	}
	snapshot, err := repository.GetHistorySnapshot(ctx, ownerID, kind)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return &communicationv1.GetHistorySnapshotResponse{}, nil
		}
		return nil, connect.NewError(connect.CodeInternal, "unable to read encrypted history")
	}
	return &communicationv1.GetHistorySnapshotResponse{Snapshot: snapshotProto(snapshot)}, nil
}

func rpcBearer(ctx context.Context) string {
	info, _ := connect.CallInfoForServerContext(ctx)
	token, _ := bearerToken(info.RequestHeader().Get("Authorization"))
	return token
}

func eventFromProto(event *communicationv1.EncryptedEvent) (domain.EventEnvelope, error) {
	if event.GetOccurredAt() == nil || event.GetOccurredAt().CheckValid() != nil {
		return domain.EventEnvelope{}, errors.New("event timestamp is invalid")
	}
	return domain.EventEnvelope{
		ID: event.GetId(), Sequence: event.GetSequence(), Type: event.GetType(), DeviceUUID: event.GetDeviceUuid(),
		RecipientInstallationID: event.GetRecipientInstallationId(), OccurredAt: event.GetOccurredAt().AsTime(), KeyEpoch: int(event.GetKeyEpoch()),
		Nonce: event.GetNonce(), Ciphertext: event.GetCiphertext(), Signature: event.GetSignature(),
	}, nil
}

func eventProto(event domain.EventEnvelope) *communicationv1.EncryptedEvent {
	return &communicationv1.EncryptedEvent{Id: event.ID, Sequence: event.Sequence, Type: event.Type, DeviceUuid: event.DeviceUUID,
		RecipientInstallationId: stringPtr(event.RecipientInstallationID), OccurredAt: timestamppb.New(event.OccurredAt), KeyEpoch: int32(event.KeyEpoch),
		Nonce: event.Nonce, Ciphertext: event.Ciphertext, Signature: event.Signature}
}

func stringPtr(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func eventProtos(events []domain.EventEnvelope) []*communicationv1.EncryptedEvent {
	result := make([]*communicationv1.EncryptedEvent, 0, len(events))
	for _, event := range events {
		result = append(result, eventProto(event))
	}
	return result
}

func nextSequence(after int64, events []domain.EventEnvelope) int64 {
	if len(events) == 0 {
		return after
	}
	return events[len(events)-1].Sequence
}

func historyKind(kind communicationv1.HistoryKind) (int, error) {
	switch kind {
	case communicationv1.HistoryKind_HISTORY_KIND_MESSAGES, communicationv1.HistoryKind_HISTORY_KIND_CALLS:
		return int(kind), nil
	default:
		return 0, errors.New("history kind is required")
	}
}

func snapshotProto(snapshot domain.HistorySnapshot) *communicationv1.HistorySnapshot {
	return &communicationv1.HistorySnapshot{Kind: communicationv1.HistoryKind(snapshot.Kind), Revision: snapshot.Revision,
		Ciphertext: snapshot.Ciphertext, UpdatedAt: timestamppb.New(snapshot.UpdatedAt)}
}
