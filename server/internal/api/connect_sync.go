package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	connect "connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/jackc/pgx/v5"
	communicationv1 "github.com/orangeboyChen/relay-sdk/gen/go/relay/communication/v1"
	communicationv1connect "github.com/orangeboyChen/relay-sdk/gen/go/relay/communication/v1/communicationv1connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"sim-deck-server/internal/domain"
	"sim-deck-server/internal/e2ee"
)

type rpcOwnerContextKey struct{}

type historyRepository interface {
	GetHistorySnapshot(context.Context, string, int) (domain.HistorySnapshot, error)
	PutHistorySnapshot(context.Context, string, int, int64, []byte, time.Time) (domain.HistorySnapshot, error)
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
			token := strings.TrimSpace(strings.TrimPrefix(info.RequestHeader().Get("Authorization"), "Bearer "))
			if token == "" {
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

func (service *syncService) WatchEvents(ctx context.Context, request *communicationv1.WatchEventsRequest, stream communicationv1connect.CommunicationSyncServiceWatchEventsServerStream) error {
	after := int64(0)
	if request != nil {
		after = request.GetAfterSequence()
	}
	if after < 0 {
		return connect.NewError(connect.CodeInvalidArgument, "event cursor must be non-negative")
	}
	ticker := time.NewTicker(eventStreamPollInterval)
	defer ticker.Stop()
	for {
		events, err := service.router.repository.ListEvents(ctx, after, defaultEventPageSize, service.router.now())
		if err != nil {
			return connect.NewError(connect.CodeInternal, "unable to read encrypted events")
		}
		if len(events) > 0 {
			after = nextSequence(after, events)
			if err := stream.Send(&communicationv1.WatchEventsResponse{Events: eventProtos(events), NextSequence: after}); err != nil {
				return err
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
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

func (service *syncService) PutHistorySnapshot(ctx context.Context, request *communicationv1.PutHistorySnapshotRequest) (*communicationv1.PutHistorySnapshotResponse, error) {
	if request == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, "history snapshot is required")
	}
	kind, err := historyKind(request.GetKind())
	if err != nil || len(request.GetCiphertext()) == 0 || len(request.GetCiphertext()) > 16<<20 {
		return nil, connect.NewError(connect.CodeInvalidArgument, "history snapshot is invalid")
	}
	ownerID, ok := ctx.Value(rpcOwnerContextKey{}).(string)
	if !ok || ownerID == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, "owner credential is required")
	}
	repository, ok := service.router.repository.(historyRepository)
	if !ok {
		return nil, connect.NewError(connect.CodeUnimplemented, "history snapshots are not configured")
	}
	snapshot, err := repository.PutHistorySnapshot(ctx, ownerID, kind, request.GetExpectedRevision(), request.GetCiphertext(), service.router.now())
	if err != nil {
		if errors.Is(err, domain.ErrHistoryRevisionConflict) {
			return nil, connect.NewError(connect.CodeAborted, "history snapshot revision has changed")
		}
		return nil, connect.NewError(connect.CodeInternal, "unable to write encrypted history")
	}
	return &communicationv1.PutHistorySnapshotResponse{Snapshot: snapshotProto(snapshot)}, nil
}

func rpcBearer(ctx context.Context) string {
	info, _ := connect.CallInfoForServerContext(ctx)
	return strings.TrimSpace(strings.TrimPrefix(info.RequestHeader().Get("Authorization"), "Bearer "))
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
