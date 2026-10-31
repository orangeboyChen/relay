package webauthn

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"

	"sim-deck-server/internal/store"
)

type Store interface {
	OwnerID(context.Context) (string, error)
	WebAuthnCredentials(context.Context, string) ([]webauthn.Credential, error)
	SaveWebAuthnCredential(context.Context, string, webauthn.Credential) error
	DisableOwnerPassword(context.Context, string, time.Time) error
}

type CredentialSummary struct {
	CreatedAt  time.Time
	LastUsedAt *time.Time
}

type CredentialRecordStore interface {
	WebAuthnCredentialRecords(context.Context, string) ([]store.WebAuthnCredentialRecord, error)
}

type User struct {
	ID          string
	Credentials []webauthn.Credential
}

func (user User) WebAuthnID() []byte                         { parsed, _ := uuid.Parse(user.ID); return parsed[:] }
func (user User) WebAuthnName() string                       { return "admin" }
func (user User) WebAuthnDisplayName() string                { return "Sim Deck owner" }
func (user User) WebAuthnCredentials() []webauthn.Credential { return user.Credentials }

type Service struct {
	rp       *webauthn.WebAuthn
	store    Store
	mu       sync.Mutex
	sessions map[string]webauthn.SessionData
}

func New(store Store, baseURL string) (*Service, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if store == nil || baseURL == "" {
		return nil, errors.New("webauthn store and base URL are required")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return nil, errors.New("webauthn base URL must be an absolute HTTP or HTTPS URL")
	}
	rpID := parsed.Hostname()
	origins := []string{baseURL}
	nativeOrigin := "https://" + rpID
	if nativeOrigin != baseURL {
		origins = append(origins, nativeOrigin)
	}
	rp := &webauthn.Config{RPID: rpID, RPDisplayName: "Sim Deck", RPOrigins: origins}
	instance, err := webauthn.New(rp)
	if err != nil {
		return nil, err
	}
	return &Service{rp: instance, store: store, sessions: make(map[string]webauthn.SessionData)}, nil
}

func (service *Service) user(ctx context.Context, ownerID string) (User, error) {
	credentials, err := service.store.WebAuthnCredentials(ctx, ownerID)
	if err != nil {
		return User{}, err
	}
	return User{ID: ownerID, Credentials: credentials}, nil
}

func (service *Service) ListCredentialSummaries(ctx context.Context, ownerID string) ([]CredentialSummary, error) {
	if recordStore, ok := service.store.(CredentialRecordStore); ok {
		records, err := recordStore.WebAuthnCredentialRecords(ctx, ownerID)
		if err != nil {
			return nil, err
		}
		summaries := make([]CredentialSummary, 0, len(records))
		for _, record := range records {
			summaries = append(summaries, CredentialSummary{
				CreatedAt:  record.CreatedAt,
				LastUsedAt: record.LastUsedAt,
			})
		}
		return summaries, nil
	}

	credentials, err := service.store.WebAuthnCredentials(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	return make([]CredentialSummary, len(credentials)), nil
}

func (service *Service) BeginRegistration(ctx context.Context, ownerID string) (string, json.RawMessage, error) {
	user, err := service.user(ctx, ownerID)
	if err != nil {
		return "", nil, err
	}
	creation, session, err := service.rp.BeginRegistration(user)
	if err != nil {
		return "", nil, err
	}
	return service.saveSession(session, creation)
}

func (service *Service) FinishRegistration(ctx context.Context, ownerID, ceremonyID string, request *http.Request) error {
	user, err := service.user(ctx, ownerID)
	if err != nil {
		return err
	}
	session, err := service.takeSession(ceremonyID)
	if err != nil {
		return err
	}
	credential, err := service.rp.FinishRegistration(user, session, request)
	if err != nil {
		return err
	}
	if err := service.store.SaveWebAuthnCredential(ctx, ownerID, *credential); err != nil {
		return err
	}
	return service.store.DisableOwnerPassword(ctx, ownerID, time.Now().UTC())
}

func (service *Service) BeginLogin() (string, json.RawMessage, error) {
	assertion, session, err := service.rp.BeginDiscoverableLogin()
	if err != nil {
		return "", nil, err
	}
	return service.saveSession(session, assertion)
}

func (service *Service) FinishLogin(ctx context.Context, ceremonyID string, request *http.Request) (string, error) {
	session, err := service.takeSession(ceremonyID)
	if err != nil {
		return "", err
	}
	ownerID, err := service.store.OwnerID(ctx)
	if err != nil {
		return "", err
	}
	_, _, err = service.rp.FinishPasskeyLogin(func(rawID, _ []byte) (webauthn.User, error) {
		parsed, parseErr := uuid.FromBytes(rawID)
		if parseErr != nil || parsed.String() != ownerID {
			return nil, errors.New("unknown passkey owner")
		}
		return service.user(ctx, ownerID)
	}, session, request)
	return ownerID, err
}

func (service *Service) saveSession(session *webauthn.SessionData, response any) (string, json.RawMessage, error) {
	ceremonyID := uuid.NewString()
	encoded, err := json.Marshal(response)
	if err != nil {
		return "", nil, err
	}
	service.mu.Lock()
	service.sessions[ceremonyID] = *session
	service.mu.Unlock()
	return ceremonyID, encoded, nil
}

func (service *Service) takeSession(id string) (webauthn.SessionData, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	session, ok := service.sessions[id]
	if !ok || (!session.Expires.IsZero() && time.Now().After(session.Expires)) {
		delete(service.sessions, id)
		return webauthn.SessionData{}, errors.New("webauthn ceremony is expired")
	}
	delete(service.sessions, id)
	return session, nil
}
