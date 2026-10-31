package api

import (
	"bytes"
	"context"
	"io"
	"net/http"

	connect "connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	communicationv1 "github.com/orangeboyChen/relay-sdk/gen/go/relay/communication/v1"
	communicationv1connect "github.com/orangeboyChen/relay-sdk/gen/go/relay/communication/v1/communicationv1connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"sim-deck-server/internal/auth"
)

type authenticationService struct {
	communicationv1connect.UnimplementedAuthenticationServiceHandler
	router *router
}

func mountAuthenticationService(mux *http.ServeMux, router *router) {
	server := connect.NewServer(rpcAuthInterceptor(router))
	communicationv1connect.RegisterAuthenticationServiceHandler(server, &authenticationService{router: router})
	connecthttp.Mount(mux, server)
}

func (service *authenticationService) Bootstrap(ctx context.Context, request *communicationv1.BootstrapRequest) (*communicationv1.BootstrapResponse, error) {
	if request == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, "credentials are required")
	}
	credential, err := service.router.repository.OwnerCredentialByUsername(ctx, request.GetUsername())
	if err != nil || credential.PasswordDisabledAt != nil || credential.PasswordHash == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, "invalid credentials")
	}
	valid, err := auth.VerifyPassword(request.GetPassword(), credential.PasswordHash)
	if err != nil || !valid {
		return nil, connect.NewError(connect.CodeUnauthenticated, "invalid credentials")
	}
	pair, err := auth.NewTokenPair(service.router.now())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "unable to create a session")
	}
	if err := service.router.repository.CreateSession(ctx, credential.ID, pair); err != nil {
		return nil, connect.NewError(connect.CodeInternal, "unable to store the session")
	}
	return tokenPairProto(pair), nil
}

func (service *authenticationService) Refresh(ctx context.Context, request *communicationv1.RefreshRequest) (*communicationv1.RefreshResponse, error) {
	if request == nil || request.GetRefreshToken() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "refresh token is required")
	}
	pair, err := auth.NewTokenPair(service.router.now())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "unable to refresh the session")
	}
	if err := service.router.repository.RotateSession(ctx, request.GetRefreshToken(), pair, service.router.now()); err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, "refresh token is invalid or expired")
	}
	return &communicationv1.RefreshResponse{
		AccessToken: pair.AccessToken, RefreshToken: pair.RefreshToken,
		AccessExpiresAt: timestamppb.New(pair.AccessExpiresAt), RefreshExpiresAt: timestamppb.New(pair.RefreshExpiresAt),
	}, nil
}

func (service *authenticationService) Logout(ctx context.Context, _ *communicationv1.LogoutRequest) (*communicationv1.LogoutResponse, error) {
	token := rpcBearer(ctx)
	if token == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, "access token is required")
	}
	if err := service.router.repository.RevokeSession(ctx, token, service.router.now()); err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, "access token is invalid or expired")
	}
	return &communicationv1.LogoutResponse{}, nil
}

func (service *authenticationService) BeginPasskeyRegistration(ctx context.Context, _ *communicationv1.BeginPasskeyRegistrationRequest) (*communicationv1.BeginPasskeyRegistrationResponse, error) {
	ownerID, ok := ctx.Value(rpcOwnerContextKey{}).(string)
	if !ok || service.router.webAuthn == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, "passkey authentication is unavailable")
	}
	id, options, err := service.router.webAuthn.BeginRegistration(ctx, ownerID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "unable to start passkey registration")
	}
	return &communicationv1.BeginPasskeyRegistrationResponse{CeremonyId: id, OptionsJson: options}, nil
}

func (service *authenticationService) FinishPasskeyRegistration(ctx context.Context, request *communicationv1.FinishPasskeyRegistrationRequest) (*communicationv1.FinishPasskeyRegistrationResponse, error) {
	ownerID, ok := ctx.Value(rpcOwnerContextKey{}).(string)
	if service.router.webAuthn == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, "passkey authentication is unavailable")
	}
	if !ok || request == nil || request.GetCeremonyId() == "" || len(request.GetCredentialJson()) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, "passkey ceremony is invalid")
	}
	httpRequest := &http.Request{
		Method: http.MethodPost,
		Header: make(http.Header),
		Body:   io.NopCloser(bytes.NewReader(request.GetCredentialJson())),
	}
	if err := service.router.webAuthn.FinishRegistration(ctx, ownerID, request.GetCeremonyId(), httpRequest); err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, "passkey registration failed")
	}
	return &communicationv1.FinishPasskeyRegistrationResponse{}, nil
}

func (service *authenticationService) ListPasskeys(ctx context.Context, _ *communicationv1.ListPasskeysRequest) (*communicationv1.ListPasskeysResponse, error) {
	ownerID, ok := ctx.Value(rpcOwnerContextKey{}).(string)
	if !ok || service.router.webAuthn == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, "passkey authentication is unavailable")
	}
	summaries, err := service.router.webAuthn.ListCredentialSummaries(ctx, ownerID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "unable to list passkeys")
	}
	response := &communicationv1.ListPasskeysResponse{}
	for _, summary := range summaries {
		item := &communicationv1.PasskeyRecord{}
		if !summary.CreatedAt.IsZero() {
			item.CreatedAt = timestamppb.New(summary.CreatedAt)
		}
		if summary.LastUsedAt != nil {
			item.LastUsedAt = timestamppb.New(*summary.LastUsedAt)
		}
		response.Passkeys = append(response.Passkeys, item)
	}
	return response, nil
}

func (service *authenticationService) BeginPasskeyLogin(context.Context, *communicationv1.BeginPasskeyLoginRequest) (*communicationv1.BeginPasskeyLoginResponse, error) {
	if service.router.webAuthn == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, "passkey authentication is unavailable")
	}
	id, options, err := service.router.webAuthn.BeginLogin()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "unable to start passkey login")
	}
	return &communicationv1.BeginPasskeyLoginResponse{CeremonyId: id, OptionsJson: options}, nil
}

func (service *authenticationService) FinishPasskeyLogin(ctx context.Context, request *communicationv1.FinishPasskeyLoginRequest) (*communicationv1.FinishPasskeyLoginResponse, error) {
	if service.router.webAuthn == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, "passkey authentication is unavailable")
	}
	if request == nil || request.GetCeremonyId() == "" || len(request.GetCredentialJson()) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, "passkey ceremony is invalid")
	}
	httpRequest := &http.Request{
		Method: http.MethodPost,
		Header: make(http.Header),
		Body:   io.NopCloser(bytes.NewReader(request.GetCredentialJson())),
	}
	ownerID, err := service.router.webAuthn.FinishLogin(ctx, request.GetCeremonyId(), httpRequest)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, "passkey login failed")
	}
	pair, err := auth.NewTokenPair(service.router.now())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "unable to create a session")
	}
	if err := service.router.repository.CreateSession(ctx, ownerID, pair); err != nil {
		return nil, connect.NewError(connect.CodeInternal, "unable to store the session")
	}
	return &communicationv1.FinishPasskeyLoginResponse{
		AccessToken: pair.AccessToken, RefreshToken: pair.RefreshToken,
		AccessExpiresAt: timestamppb.New(pair.AccessExpiresAt), RefreshExpiresAt: timestamppb.New(pair.RefreshExpiresAt),
	}, nil
}

func tokenPairProto(pair auth.TokenPair) *communicationv1.BootstrapResponse {
	return &communicationv1.BootstrapResponse{
		AccessToken: pair.AccessToken, RefreshToken: pair.RefreshToken,
		AccessExpiresAt: timestamppb.New(pair.AccessExpiresAt), RefreshExpiresAt: timestamppb.New(pair.RefreshExpiresAt),
	}
}
