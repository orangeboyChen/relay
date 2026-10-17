package store

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"sim-deck-server/internal/auth"
	"sim-deck-server/internal/domain"
)

//go:embed schema.sql
var schemaFS embed.FS

// Postgres stores only routing metadata and encrypted application payloads.
type Postgres struct {
	pool *pgxpool.Pool
}

// DeviceRecord is the server-readable portion of a paired device.
type DeviceRecord struct {
	ID                    string       `json:"device_uuid"`
	NumericID             int          `json:"numeric_id"`
	ProfileCiphertext     []byte       `json:"profile_ciphertext"`
	IdentityPublicKey     []byte       `json:"identity_public_key"`
	CommandPublicKey      []byte       `json:"command_public_key,omitempty"`
	WrappedVaultKey       []byte       `json:"wrapped_vault_key,omitempty"`
	PairedAt              time.Time    `json:"paired_at"`
	LastSeenAt            *time.Time   `json:"last_seen_at,omitempty"`
	RadioAccessTechnology string       `json:"radio_access_technology,omitempty"`
	SignalBars            *int         `json:"signal_bars,omitempty"`
	RadioStatusUpdatedAt  *time.Time   `json:"radio_status_updated_at,omitempty"`
	Slots                 []SlotRecord `json:"slots"`
}

// SlotRecord identifies a physical modem slot and the SIM currently inserted in it.
// Slot IDs are stable for a device; SIM IDs remain stable when a card moves slots.
type SlotRecord struct {
	ID                    string     `json:"slot_id"`
	SlotKey               string     `json:"slot_key"`
	SlotIndex             int        `json:"slot_index"`
	SIMID                 *string    `json:"sim_id,omitempty"`
	SIMNumericID          *int       `json:"numeric_id,omitempty"`
	ProfileCiphertext     []byte     `json:"profile_ciphertext,omitempty"`
	LastSeenAt            *time.Time `json:"last_seen_at,omitempty"`
	RadioAccessTechnology string     `json:"radio_access_technology,omitempty"`
	SignalBars            *int       `json:"signal_bars,omitempty"`
	RadioStatusUpdatedAt  *time.Time `json:"radio_status_updated_at,omitempty"`
}

// OwnerCredential is the password-login state for the single owner.
type OwnerCredential struct {
	ID                 string
	PasswordHash       string
	PasswordDisabledAt *time.Time
}

// DevicePairing contains the public-key material and encrypted profile supplied by a new agent.
type DevicePairing struct {
	DeviceUUID        string
	ProfileCiphertext []byte
	IdentityPublicKey []byte
	CommandPublicKey  []byte
	WrappedVaultKey   []byte
	CredentialHash    []byte
}

// AppInstallationRegistration contains public identity material supplied by one iPhone install.
type AppInstallationRegistration struct {
	ID                    string
	IdentityPublicKey     []byte
	KeyAgreementPublicKey []byte
	WrappedVaultKey       []byte
}

// AppInstallationRecord is the server-readable trust state for one iPhone install.
type AppInstallationRecord struct {
	ID                    string     `json:"installation_id"`
	IdentityPublicKey     []byte     `json:"identity_public_key"`
	KeyAgreementPublicKey []byte     `json:"key_agreement_public_key"`
	WrappedVaultKey       []byte     `json:"wrapped_vault_key,omitempty"`
	ApprovedAt            *time.Time `json:"approved_at,omitempty"`
	RevokedAt             *time.Time `json:"revoked_at,omitempty"`
	CreatedAt             time.Time  `json:"created_at"`
}

// AppInstallationRecipient is the public routing identity of one approved iPhone.
type AppInstallationRecipient struct {
	InstallationID        string `json:"installation_id"`
	IdentityPublicKey     []byte `json:"identity_public_key"`
	KeyAgreementPublicKey []byte `json:"key_agreement_public_key"`
}

// OutboxPushJob is one leased encrypted push delivery and its current APNs route.
type OutboxPushJob struct {
	ID             int64
	Kind           string
	RoutingKey     string
	Payload        []byte
	Attempt        int
	Environment    string
	DeviceToken    []byte
	LeaseExpiresAt time.Time
}

// Open connects to PostgreSQL and verifies that the server is reachable.
func Open(ctx context.Context, databaseURL string) (*Postgres, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("configure postgres: %w", err)
	}
	config.AfterConnect = func(_ context.Context, connection *pgx.Conn) error {
		connection.TypeMap().RegisterType(&pgtype.Type{
			Name:  "timestamptz",
			OID:   pgtype.TimestamptzOID,
			Codec: &pgtype.TimestamptzCodec{ScanLocation: time.UTC},
		})
		return nil
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("configure postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return &Postgres{pool: pool}, nil
}

// Close releases all PostgreSQL connections.
func (store *Postgres) Close() {
	store.pool.Close()
}

// InitializeSchema creates the complete database schema for a new deployment.
func (store *Postgres) InitializeSchema(ctx context.Context) error {
	schema, err := Schema()
	if err != nil {
		return err
	}
	if _, err := store.pool.Exec(ctx, string(schema)); err != nil {
		return fmt.Errorf("initialize database schema: %w", err)
	}
	return nil
}

// OwnerExists reports whether the single owner account has already been created.
func (store *Postgres) OwnerExists(ctx context.Context) (bool, error) {
	var exists bool
	if err := store.pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM owner_accounts)").Scan(&exists); err != nil {
		return false, fmt.Errorf("check owner account: %w", err)
	}
	return exists, nil
}

// CreateOwner creates the single owner while serializing concurrent bootstrap attempts.
func (store *Postgres) CreateOwner(ctx context.Context, passwordHash string, recoveryPublicKey []byte) (bool, error) {
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin owner bootstrap: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", int64(0x53494d4f)); err != nil {
		return false, fmt.Errorf("lock owner bootstrap: %w", err)
	}
	var exists bool
	if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM owner_accounts)").Scan(&exists); err != nil {
		return false, fmt.Errorf("check owner during bootstrap: %w", err)
	}
	if exists {
		return false, nil
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO owner_accounts (id, username, password_hash, recovery_public_key)
		VALUES (gen_random_uuid(), 'admin', $1, $2)`, []byte(passwordHash), recoveryPublicKey); err != nil {
		return false, fmt.Errorf("create owner account: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit owner bootstrap: %w", err)
	}
	return true, nil
}

// OwnerCredentialByUsername loads bootstrap credentials without returning recovery material.
func (store *Postgres) OwnerCredentialByUsername(ctx context.Context, username string) (OwnerCredential, error) {
	var credential OwnerCredential
	var passwordHash []byte
	err := store.pool.QueryRow(ctx, `
		SELECT id, password_hash, password_disabled_at
		FROM owner_accounts
		WHERE username = $1`, username).Scan(&credential.ID, &passwordHash, &credential.PasswordDisabledAt)
	if err != nil {
		return OwnerCredential{}, err
	}
	credential.PasswordHash = string(passwordHash)
	return credential, nil
}

// CreateSession stores only hashes of newly issued bearer credentials.
func (store *Postgres) CreateSession(ctx context.Context, ownerID string, pair auth.TokenPair) error {
	accessHash, err := auth.TokenHash(pair.AccessToken)
	if err != nil {
		return err
	}
	refreshHash, err := auth.TokenHash(pair.RefreshToken)
	if err != nil {
		return err
	}
	_, err = store.pool.Exec(ctx, `
		INSERT INTO auth_sessions (
			owner_id, access_token_hash, refresh_token_hash, access_expires_at, refresh_expires_at
		) VALUES ($1, $2, $3, $4, $5)`, ownerID, accessHash[:], refreshHash[:], pair.AccessExpiresAt, pair.RefreshExpiresAt)
	if err != nil {
		return fmt.Errorf("create auth session: %w", err)
	}
	return nil
}

// RotateSession atomically consumes a refresh token and creates its replacement.
func (store *Postgres) RotateSession(ctx context.Context, refreshToken string, pair auth.TokenPair, now time.Time) error {
	refreshHash, err := auth.TokenHash(refreshToken)
	if err != nil {
		return err
	}
	accessHash, err := auth.TokenHash(pair.AccessToken)
	if err != nil {
		return err
	}
	newRefreshHash, err := auth.TokenHash(pair.RefreshToken)
	if err != nil {
		return err
	}
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin session rotation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var ownerID string
	err = tx.QueryRow(ctx, `
		UPDATE auth_sessions
		SET revoked_at = $2
		WHERE refresh_token_hash = $1 AND revoked_at IS NULL AND refresh_expires_at > $2
		RETURNING owner_id`, refreshHash[:], now.UTC()).Scan(&ownerID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO auth_sessions (
			owner_id, access_token_hash, refresh_token_hash, access_expires_at, refresh_expires_at
		) VALUES ($1, $2, $3, $4, $5)`, ownerID, accessHash[:], newRefreshHash[:], pair.AccessExpiresAt, pair.RefreshExpiresAt)
	if err != nil {
		return fmt.Errorf("insert rotated session: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit session rotation: %w", err)
	}
	return nil
}

// RevokeSession invalidates the session matching an access token.
func (store *Postgres) RevokeSession(ctx context.Context, accessToken string, now time.Time) error {
	accessHash, err := auth.TokenHash(accessToken)
	if err != nil {
		return err
	}
	command, err := store.pool.Exec(ctx, `
		UPDATE auth_sessions SET revoked_at = $2
		WHERE access_token_hash = $1 AND revoked_at IS NULL AND access_expires_at > $2`, accessHash[:], now.UTC())
	if err != nil {
		return fmt.Errorf("revoke auth session: %w", err)
	}
	if command.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// AuthenticateOwnerSession validates an unexpired owner access token.
func (store *Postgres) AuthenticateOwnerSession(ctx context.Context, accessToken string, now time.Time) (string, error) {
	accessHash, err := auth.TokenHash(accessToken)
	if err != nil {
		return "", err
	}
	var ownerID string
	err = store.pool.QueryRow(ctx, `
		SELECT owner_id
		FROM auth_sessions
		WHERE access_token_hash = $1 AND revoked_at IS NULL AND access_expires_at > $2`, accessHash[:], now.UTC()).Scan(&ownerID)
	if err != nil {
		return "", err
	}
	return ownerID, nil
}

// CreatePairingToken stores only the hash of a short-lived one-time pairing secret.
func (store *Postgres) CreatePairingToken(ctx context.Context, ownerID string, tokenHash []byte, expiresAt time.Time) error {
	_, err := store.pool.Exec(ctx, `
		INSERT INTO pairing_tokens (token_hash, owner_id, expires_at)
		VALUES ($1, $2, $3)`, tokenHash, ownerID, expiresAt.UTC())
	if err != nil {
		return fmt.Errorf("create pairing token: %w", err)
	}
	return nil
}

// RegisterAppInstallation atomically approves the first iPhone and leaves later installs pending.
func (store *Postgres) RegisterAppInstallation(ctx context.Context, ownerID string, registration AppInstallationRegistration, now time.Time) (AppInstallationRecord, bool, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return AppInstallationRecord{}, false, fmt.Errorf("begin app installation registration: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", int64(0x53494d49)); err != nil {
		return AppInstallationRecord{}, false, fmt.Errorf("lock app installation registration: %w", err)
	}

	var existing AppInstallationRecord
	var existingOwnerID string
	err = tx.QueryRow(ctx, `
		SELECT id, owner_id, identity_public_key, key_agreement_public_key, wrapped_vault_key,
		       approved_at, revoked_at, created_at
		FROM app_installations
		WHERE id = $1`, registration.ID).Scan(
		&existing.ID,
		&existingOwnerID,
		&existing.IdentityPublicKey,
		&existing.KeyAgreementPublicKey,
		&existing.WrappedVaultKey,
		&existing.ApprovedAt,
		&existing.RevokedAt,
		&existing.CreatedAt,
	)
	if err == nil {
		if existingOwnerID != ownerID || existing.RevokedAt != nil ||
			!bytes.Equal(existing.IdentityPublicKey, registration.IdentityPublicKey) ||
			!bytes.Equal(existing.WrappedVaultKey, registration.WrappedVaultKey) {
			return AppInstallationRecord{}, false, domain.ErrInstallationConflict
		}
		if len(existing.KeyAgreementPublicKey) == 0 {
			if _, err := tx.Exec(ctx, `
				UPDATE app_installations
				SET key_agreement_public_key = $2
				WHERE id = $1`, registration.ID, registration.KeyAgreementPublicKey); err != nil {
				return AppInstallationRecord{}, false, fmt.Errorf("upgrade app installation key agreement identity: %w", err)
			}
			existing.KeyAgreementPublicKey = append([]byte(nil), registration.KeyAgreementPublicKey...)
			if err := tx.Commit(ctx); err != nil {
				return AppInstallationRecord{}, false, fmt.Errorf("commit app installation identity upgrade: %w", err)
			}
			return existing, false, nil
		}
		if !bytes.Equal(existing.KeyAgreementPublicKey, registration.KeyAgreementPublicKey) {
			return AppInstallationRecord{}, false, domain.ErrInstallationConflict
		}
		return existing, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return AppInstallationRecord{}, false, fmt.Errorf("read app installation retry: %w", err)
	}

	var hasApprovedInstallation bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM app_installations
			WHERE owner_id = $1 AND approved_at IS NOT NULL AND revoked_at IS NULL
		)`, ownerID).Scan(&hasApprovedInstallation); err != nil {
		return AppInstallationRecord{}, false, fmt.Errorf("check approved app installations: %w", err)
	}

	record := AppInstallationRecord{
		ID:                    registration.ID,
		IdentityPublicKey:     append([]byte(nil), registration.IdentityPublicKey...),
		KeyAgreementPublicKey: append([]byte(nil), registration.KeyAgreementPublicKey...),
		WrappedVaultKey:       append([]byte(nil), registration.WrappedVaultKey...),
	}
	if !hasApprovedInstallation {
		approvedAt := now.UTC()
		record.ApprovedAt = &approvedAt
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO app_installations (
			id, owner_id, identity_public_key, key_agreement_public_key,
			wrapped_vault_key, approved_at
		) VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING created_at`,
		registration.ID,
		ownerID,
		registration.IdentityPublicKey,
		registration.KeyAgreementPublicKey,
		registration.WrappedVaultKey,
		record.ApprovedAt,
	).Scan(&record.CreatedAt)
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) && postgresError.Code == "23505" {
			return AppInstallationRecord{}, false, domain.ErrInstallationConflict
		}
		return AppInstallationRecord{}, false, fmt.Errorf("insert app installation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return AppInstallationRecord{}, false, fmt.Errorf("commit app installation registration: %w", err)
	}
	return record, true, nil
}

// ListApprovedAppInstallationRecipients returns only active public encryption identities.
func (store *Postgres) ListApprovedAppInstallationRecipients(ctx context.Context) ([]AppInstallationRecipient, error) {
	rows, err := store.pool.Query(ctx, `
		SELECT id, identity_public_key, key_agreement_public_key
		FROM app_installations
		WHERE approved_at IS NOT NULL
		  AND revoked_at IS NULL
		  AND key_agreement_public_key IS NOT NULL
		ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list approved app installation recipients: %w", err)
	}
	defer rows.Close()

	recipients := make([]AppInstallationRecipient, 0)
	for rows.Next() {
		var recipient AppInstallationRecipient
		if err := rows.Scan(&recipient.InstallationID, &recipient.IdentityPublicKey, &recipient.KeyAgreementPublicKey); err != nil {
			return nil, fmt.Errorf("scan approved app installation recipient: %w", err)
		}
		recipients = append(recipients, recipient)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate approved app installation recipients: %w", err)
	}
	return recipients, nil
}

// UpsertPushToken binds one APNs route to an approved installation owned by the caller.
func (store *Postgres) UpsertPushToken(
	ctx context.Context,
	ownerID string,
	installationID string,
	environment string,
	kind string,
	token []byte,
	now time.Time,
) error {
	command, err := store.pool.Exec(ctx, `
		INSERT INTO push_tokens (installation_id, environment, kind, token_ciphertext, updated_at)
		SELECT id, $3, $4, $5, $6
		FROM app_installations
		WHERE id = $1
		  AND owner_id = $2
		  AND approved_at IS NOT NULL
		  AND revoked_at IS NULL
		ON CONFLICT (installation_id, environment, kind)
		DO UPDATE SET token_ciphertext = EXCLUDED.token_ciphertext,
		              updated_at = EXCLUDED.updated_at`,
		installationID, ownerID, environment, kind, token, now.UTC(),
	)
	if err != nil {
		return fmt.Errorf("upsert push token: %w", err)
	}
	if command.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// DeletePushToken removes one owner-controlled APNs route after local invalidation.
func (store *Postgres) DeletePushToken(
	ctx context.Context,
	ownerID string,
	installationID string,
	environment string,
	kind string,
) error {
	command, err := store.pool.Exec(ctx, `
		DELETE FROM push_tokens AS token
		USING app_installations AS installation
		WHERE token.installation_id = installation.id
		  AND token.installation_id = $1
		  AND installation.owner_id = $2
		  AND token.environment = $3
		  AND token.kind = $4`, installationID, ownerID, environment, kind)
	if err != nil {
		return fmt.Errorf("delete push token: %w", err)
	}
	if command.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// InsertIncomingCall stores recipient ciphertexts and queues one opaque VoIP delivery per approved iPhone.
func (store *Postgres) InsertIncomingCall(ctx context.Context, notification domain.IncomingCallNotification, expiresAt time.Time) (bool, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return false, fmt.Errorf("begin incoming call insert: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	command, err := tx.Exec(ctx, `
		INSERT INTO incoming_call_notifications (id, type, device_id, occurred_at, key_epoch, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (id) DO NOTHING`,
		notification.ID,
		notification.Type,
		notification.DeviceUUID,
		notification.OccurredAt,
		notification.KeyEpoch,
		expiresAt,
	)
	if err != nil {
		return false, fmt.Errorf("insert incoming call notification: %w", err)
	}
	if command.RowsAffected() == 0 {
		matches, err := incomingCallMatches(ctx, tx, notification, expiresAt)
		if err != nil {
			return false, err
		}
		if !matches {
			return false, domain.ErrIdempotencyConflict
		}
		return false, nil
	}

	for _, recipient := range notification.Recipients {
		command, err := tx.Exec(ctx, `
			INSERT INTO incoming_call_recipients (
				notification_id, installation_id, sender_public_key, nonce, ciphertext, signature
			)
			SELECT $1, id, $3, $4, $5, $6
			FROM app_installations
			WHERE id = $2 AND approved_at IS NOT NULL AND revoked_at IS NULL`,
			notification.ID,
			recipient.InstallationID,
			recipient.SenderPublicKey,
			recipient.Nonce,
			recipient.Ciphertext,
			recipient.Signature,
		)
		if err != nil {
			return false, fmt.Errorf("insert incoming call recipient: %w", err)
		}
		if command.RowsAffected() == 0 {
			return false, domain.ErrUntrustedInstallationRecipient
		}
		delivery, err := json.Marshal(domain.IncomingCallDelivery{
			NotificationID: notification.ID,
			Type:           notification.Type,
			DeviceUUID:     notification.DeviceUUID,
			OccurredAt:     notification.OccurredAt,
			KeyEpoch:       notification.KeyEpoch,
			Recipient:      recipient,
		})
		if err != nil {
			return false, fmt.Errorf("encode incoming call delivery: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO outbox_jobs (kind, routing_key, payload, available_at, expires_at)
			VALUES ('voip.incoming_call', $1, $2, now(), $3)`,
			recipient.InstallationID, delivery, notification.OccurredAt.Add(2*time.Minute)); err != nil {
			return false, fmt.Errorf("enqueue incoming call delivery: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit incoming call insert: %w", err)
	}
	return true, nil
}

func incomingCallMatches(ctx context.Context, tx pgx.Tx, notification domain.IncomingCallNotification, expiresAt time.Time) (bool, error) {
	var existing domain.IncomingCallNotification
	var existingExpiry time.Time
	err := tx.QueryRow(ctx, `
		SELECT id, type, device_id, occurred_at, key_epoch, expires_at
		FROM incoming_call_notifications
		WHERE id = $1`, notification.ID).Scan(
		&existing.ID,
		&existing.Type,
		&existing.DeviceUUID,
		&existing.OccurredAt,
		&existing.KeyEpoch,
		&existingExpiry,
	)
	if err != nil {
		return false, fmt.Errorf("read existing incoming call notification: %w", err)
	}
	if existing.Type != notification.Type ||
		existing.DeviceUUID != notification.DeviceUUID ||
		!existing.OccurredAt.Equal(notification.OccurredAt) ||
		existing.KeyEpoch != notification.KeyEpoch ||
		!existingExpiry.Equal(expiresAt) {
		return false, nil
	}
	rows, err := tx.Query(ctx, `
		SELECT installation_id, sender_public_key, nonce, ciphertext, signature
		FROM incoming_call_recipients
		WHERE notification_id = $1
		ORDER BY installation_id`, notification.ID)
	if err != nil {
		return false, fmt.Errorf("list existing incoming call recipients: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var recipient domain.IncomingCallRecipient
		if err := rows.Scan(
			&recipient.InstallationID,
			&recipient.SenderPublicKey,
			&recipient.Nonce,
			&recipient.Ciphertext,
			&recipient.Signature,
		); err != nil {
			return false, fmt.Errorf("scan existing incoming call recipient: %w", err)
		}
		existing.Recipients = append(existing.Recipients, recipient)
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("iterate existing incoming call recipients: %w", err)
	}
	if len(existing.Recipients) != len(notification.Recipients) {
		return false, nil
	}
	for index := range existing.Recipients {
		left := existing.Recipients[index]
		right := notification.Recipients[index]
		if left.InstallationID != right.InstallationID ||
			!bytes.Equal(left.SenderPublicKey, right.SenderPublicKey) ||
			!bytes.Equal(left.Nonce, right.Nonce) ||
			!bytes.Equal(left.Ciphertext, right.Ciphertext) ||
			!bytes.Equal(left.Signature, right.Signature) {
			return false, nil
		}
	}
	return true, nil
}

// ClaimIncomingCall atomically selects the first approved iPhone and notifies the others.
func (store *Postgres) ClaimIncomingCall(ctx context.Context, ownerID string, notificationID string, installationID string, now time.Time) (domain.IncomingCallClaim, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return domain.IncomingCallClaim{}, fmt.Errorf("begin incoming call claim: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var eligible bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM incoming_call_notifications AS notification
			JOIN incoming_call_recipients AS recipient
			  ON recipient.notification_id = notification.id AND recipient.installation_id = $3
			JOIN app_installations AS installation
			  ON installation.id = recipient.installation_id
			WHERE notification.id = $1
			  AND notification.expires_at > $4
			  AND installation.owner_id = $2
			  AND installation.approved_at IS NOT NULL
			  AND installation.revoked_at IS NULL
		)`, notificationID, ownerID, installationID, now.UTC()).Scan(&eligible); err != nil {
		return domain.IncomingCallClaim{}, fmt.Errorf("authorize incoming call claim: %w", err)
	}
	if !eligible {
		return domain.IncomingCallClaim{}, pgx.ErrNoRows
	}
	command, err := tx.Exec(ctx, `
		INSERT INTO incoming_call_claims (notification_id, installation_id, claimed_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (notification_id) DO NOTHING`, notificationID, installationID, now.UTC())
	if err != nil {
		return domain.IncomingCallClaim{}, fmt.Errorf("insert incoming call claim: %w", err)
	}
	created := command.RowsAffected() == 1
	claim := domain.IncomingCallClaim{NotificationID: notificationID}
	if err := tx.QueryRow(ctx, `
		SELECT claim.installation_id, claim.claimed_at
		FROM incoming_call_claims AS claim
		JOIN incoming_call_notifications AS notification ON notification.id = claim.notification_id
		WHERE claim.notification_id = $1 AND notification.expires_at > $2`, notificationID, now.UTC()).Scan(
		&claim.InstallationID,
		&claim.ClaimedAt,
	); err != nil {
		return domain.IncomingCallClaim{}, err
	}
	claim.Won = claim.InstallationID == installationID
	if created {
		payload, err := json.Marshal(domain.IncomingCallClaimDelivery{
			NotificationID: notificationID,
			Reason:         "answered_elsewhere",
		})
		if err != nil {
			return domain.IncomingCallClaim{}, fmt.Errorf("encode incoming call claim delivery: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO outbox_jobs (kind, routing_key, payload, available_at, expires_at)
			SELECT 'voip.incoming_call_claimed', recipient.installation_id::text, $2,
			       $3::timestamptz, $3::timestamptz + INTERVAL '30 seconds'
			FROM incoming_call_recipients AS recipient
			JOIN app_installations AS installation ON installation.id = recipient.installation_id
			WHERE recipient.notification_id = $1
			  AND recipient.installation_id <> $4
			  AND installation.approved_at IS NOT NULL
			  AND installation.revoked_at IS NULL`, notificationID, payload, now.UTC(), installationID); err != nil {
			return domain.IncomingCallClaim{}, fmt.Errorf("enqueue incoming call claim deliveries: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.IncomingCallClaim{}, fmt.Errorf("commit incoming call claim: %w", err)
	}
	return claim, nil
}

// ClaimVoIPPushJob leases the next time-sensitive VoIP outbox job without blocking another worker.
func (store *Postgres) ClaimVoIPPushJob(ctx context.Context, now time.Time, leaseDuration time.Duration) (OutboxPushJob, error) {
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return OutboxPushJob{}, fmt.Errorf("begin outbox claim: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		UPDATE outbox_jobs
		SET completed_at = $1, last_error = 'expired before delivery'
		WHERE completed_at IS NULL
		  AND expires_at IS NOT NULL
		  AND expires_at <= $1
		  AND kind LIKE 'voip.%'`, now.UTC()); err != nil {
		return OutboxPushJob{}, fmt.Errorf("expire outbox jobs: %w", err)
	}

	var job OutboxPushJob
	var environment *string
	job.LeaseExpiresAt = now.UTC().Add(leaseDuration)
	err = tx.QueryRow(ctx, `
		SELECT job.id, job.kind, job.routing_key, job.payload, job.attempts,
		       route.environment, route.token_ciphertext
		FROM outbox_jobs AS job
		LEFT JOIN LATERAL (
			SELECT token.environment, token.token_ciphertext
			FROM push_tokens AS token
			JOIN app_installations AS installation ON installation.id = token.installation_id
			WHERE token.installation_id::text = job.routing_key
			  AND token.kind = 'voip'
			  AND installation.approved_at IS NOT NULL
			  AND installation.revoked_at IS NULL
			ORDER BY token.updated_at DESC
			LIMIT 1
		) AS route ON true
		WHERE job.completed_at IS NULL
		  AND job.available_at <= $1
		  AND (job.expires_at IS NULL OR job.expires_at > $1)
		  AND job.kind IN ('voip.incoming_call', 'voip.incoming_call_claimed')
		ORDER BY job.available_at, job.id
		FOR UPDATE OF job SKIP LOCKED
		LIMIT 1`, now.UTC()).Scan(
		&job.ID,
		&job.Kind,
		&job.RoutingKey,
		&job.Payload,
		&job.Attempt,
		&environment,
		&job.DeviceToken,
	)
	if err != nil {
		return OutboxPushJob{}, err
	}
	job.Attempt++
	if environment != nil {
		job.Environment = *environment
	}
	if _, err := tx.Exec(ctx, `
		UPDATE outbox_jobs
		SET attempts = $2, available_at = $3
		WHERE id = $1`, job.ID, job.Attempt, job.LeaseExpiresAt); err != nil {
		return OutboxPushJob{}, fmt.Errorf("lease outbox job: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return OutboxPushJob{}, fmt.Errorf("commit outbox claim: %w", err)
	}
	return job, nil
}

// CompletePushJob marks a leased push delivery as successful.
func (store *Postgres) CompletePushJob(ctx context.Context, id int64, attempt int, now time.Time) error {
	return store.finishPushJob(ctx, id, attempt, now, "")
}

// DiscardPushJob records a permanent delivery failure without retrying it.
func (store *Postgres) DiscardPushJob(ctx context.Context, id int64, attempt int, now time.Time, reason string) error {
	return store.finishPushJob(ctx, id, attempt, now, reason)
}

func (store *Postgres) finishPushJob(ctx context.Context, id int64, attempt int, now time.Time, reason string) error {
	command, err := store.pool.Exec(ctx, `
		UPDATE outbox_jobs
		SET completed_at = $3, last_error = NULLIF($4, '')
		WHERE id = $1 AND attempts = $2 AND completed_at IS NULL`, id, attempt, now.UTC(), reason)
	if err != nil {
		return fmt.Errorf("finish push job: %w", err)
	}
	if command.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// RetryPushJob releases a leased delivery at the requested retry time.
func (store *Postgres) RetryPushJob(ctx context.Context, id int64, attempt int, availableAt time.Time, reason string) error {
	command, err := store.pool.Exec(ctx, `
		UPDATE outbox_jobs
		SET available_at = $3, last_error = $4
		WHERE id = $1 AND attempts = $2 AND completed_at IS NULL`, id, attempt, availableAt.UTC(), reason)
	if err != nil {
		return fmt.Errorf("retry push job: %w", err)
	}
	if command.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// DeleteVoIPPushToken removes only the failed route that APNs rejected.
func (store *Postgres) DeleteVoIPPushToken(ctx context.Context, installationID string, environment string, token []byte) error {
	_, err := store.pool.Exec(ctx, `
		DELETE FROM push_tokens
		WHERE installation_id = $1
		  AND environment = $2
		  AND kind = 'voip'
		  AND token_ciphertext = $3`, installationID, environment, token)
	if err != nil {
		return fmt.Errorf("delete VoIP push token: %w", err)
	}
	return nil
}

// ConsumePairingToken atomically consumes a pairing token, allocates a SIM ID, and stores a device credential hash.
func (store *Postgres) ConsumePairingToken(ctx context.Context, tokenHash []byte, now time.Time, pairing DevicePairing) (DeviceRecord, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return DeviceRecord{}, fmt.Errorf("begin device pairing: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var consumedAt *time.Time
	var pairedDeviceID *string
	err = tx.QueryRow(ctx, `
		SELECT consumed_at, paired_device_id::text
		FROM pairing_tokens
		WHERE token_hash = $1
		  AND owner_id IS NOT NULL
		  AND (expires_at > $2 OR consumed_at IS NOT NULL)
		FOR UPDATE`, tokenHash, now.UTC()).Scan(&consumedAt, &pairedDeviceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return DeviceRecord{}, domain.ErrInvalidPairingToken
	}
	if err != nil {
		return DeviceRecord{}, fmt.Errorf("lock pairing token: %w", err)
	}
	if consumedAt != nil {
		if pairedDeviceID == nil || *pairedDeviceID != pairing.DeviceUUID {
			return DeviceRecord{}, domain.ErrInvalidPairingToken
		}
		var record DeviceRecord
		err := tx.QueryRow(ctx, `
			SELECT devices.id, devices.numeric_id, devices.profile_ciphertext,
			       devices.identity_public_key, devices.command_public_key, devices.wrapped_vault_key,
			       devices.paired_at, devices.last_seen_at
			FROM devices
			JOIN device_credentials ON device_credentials.device_id = devices.id
			WHERE devices.id = $1
			  AND device_credentials.credential_hash = $2
			  AND devices.revoked_at IS NULL
			  AND device_credentials.revoked_at IS NULL`, pairing.DeviceUUID, pairing.CredentialHash).Scan(
			&record.ID,
			&record.NumericID,
			&record.ProfileCiphertext,
			&record.IdentityPublicKey,
			&record.CommandPublicKey,
			&record.WrappedVaultKey,
			&record.PairedAt,
			&record.LastSeenAt,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return DeviceRecord{}, domain.ErrInvalidPairingToken
		}
		if err != nil {
			return DeviceRecord{}, fmt.Errorf("read paired device retry: %w", err)
		}
		record.Slots, err = store.listDeviceSlots(ctx, record.ID)
		if err != nil {
			return DeviceRecord{}, err
		}
		return record, nil
	}

	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", int64(0x53494d44)); err != nil {
		return DeviceRecord{}, fmt.Errorf("lock numeric id allocation: %w", err)
	}

	var numericID int
	err = tx.QueryRow(ctx, `
		SELECT candidate
		FROM generate_series(1, 99) AS candidate
		LEFT JOIN sim_cards ON sim_cards.numeric_id = candidate AND sim_cards.retired_at IS NULL
		WHERE sim_cards.id IS NULL
		ORDER BY candidate
		LIMIT 1`).Scan(&numericID)
	if errors.Is(err, pgx.ErrNoRows) {
		return DeviceRecord{}, domain.ErrNoNumericID
	}
	if err != nil {
		return DeviceRecord{}, fmt.Errorf("allocate numeric id: %w", err)
	}

	record := DeviceRecord{
		ID:                pairing.DeviceUUID,
		NumericID:         numericID,
		ProfileCiphertext: pairing.ProfileCiphertext,
		IdentityPublicKey: pairing.IdentityPublicKey,
		CommandPublicKey:  pairing.CommandPublicKey,
		WrappedVaultKey:   pairing.WrappedVaultKey,
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO devices (id, numeric_id, profile_ciphertext, identity_public_key, command_public_key, wrapped_vault_key)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING paired_at`, pairing.DeviceUUID, numericID, pairing.ProfileCiphertext, pairing.IdentityPublicKey, pairing.CommandPublicKey, pairing.WrappedVaultKey).Scan(&record.PairedAt)
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) && postgresError.Code == "23505" {
			return DeviceRecord{}, domain.ErrDeviceExists
		}
		return DeviceRecord{}, fmt.Errorf("insert paired device: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO device_credentials (device_id, credential_hash)
		VALUES ($1, $2)`, pairing.DeviceUUID, pairing.CredentialHash); err != nil {
		return DeviceRecord{}, fmt.Errorf("insert device credential: %w", err)
	}
	if err := createInitialSIMSlot(ctx, tx, pairing.DeviceUUID, numericID, pairing.ProfileCiphertext, now, &record); err != nil {
		return DeviceRecord{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE pairing_tokens
		SET consumed_at = $2, paired_device_id = $3
		WHERE token_hash = $1`, tokenHash, now.UTC(), pairing.DeviceUUID); err != nil {
		return DeviceRecord{}, fmt.Errorf("complete pairing token: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return DeviceRecord{}, fmt.Errorf("commit device pairing: %w", err)
	}
	return record, nil
}

// AuthenticateDevice validates a device credential and records liveness without exposing encrypted profile data.
func (store *Postgres) AuthenticateDevice(ctx context.Context, deviceID string, credential string, now time.Time) ([]byte, error) {
	credentialHash, err := auth.TokenHash(credential)
	if err != nil {
		return nil, err
	}
	var identityPublicKey []byte
	err = store.pool.QueryRow(ctx, `
		UPDATE devices
		SET last_seen_at = $3
		FROM device_credentials
		WHERE devices.id = $1
		  AND device_credentials.device_id = devices.id
		  AND device_credentials.credential_hash = $2
		  AND devices.revoked_at IS NULL
		  AND device_credentials.revoked_at IS NULL
		RETURNING devices.identity_public_key`, deviceID, credentialHash[:], now.UTC()).Scan(&identityPublicKey)
	if err != nil {
		return nil, err
	}
	return identityPublicKey, nil
}

// CreateDevice atomically allocates the smallest free numeric identifier.
func (store *Postgres) CreateDevice(ctx context.Context, id string, profileCiphertext []byte, identityPublicKey []byte, wrappedVaultKey []byte) (DeviceRecord, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return DeviceRecord{}, fmt.Errorf("begin device transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", int64(0x53494d44)); err != nil {
		return DeviceRecord{}, fmt.Errorf("lock numeric id allocation: %w", err)
	}

	var numericID int
	err = tx.QueryRow(ctx, `
		SELECT candidate
		FROM generate_series(1, 99) AS candidate
		LEFT JOIN sim_cards ON sim_cards.numeric_id = candidate AND sim_cards.retired_at IS NULL
		WHERE sim_cards.id IS NULL
		ORDER BY candidate
		LIMIT 1`).Scan(&numericID)
	if errors.Is(err, pgx.ErrNoRows) {
		return DeviceRecord{}, domain.ErrNoNumericID
	}
	if err != nil {
		return DeviceRecord{}, fmt.Errorf("allocate numeric id: %w", err)
	}

	record := DeviceRecord{
		ID:                id,
		NumericID:         numericID,
		ProfileCiphertext: profileCiphertext,
		IdentityPublicKey: identityPublicKey,
		WrappedVaultKey:   wrappedVaultKey,
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO devices (id, numeric_id, profile_ciphertext, identity_public_key, wrapped_vault_key)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING paired_at`, id, numericID, profileCiphertext, identityPublicKey, wrappedVaultKey).Scan(&record.PairedAt)
	if err != nil {
		return DeviceRecord{}, fmt.Errorf("insert device: %w", err)
	}
	if err := createInitialSIMSlot(ctx, tx, id, numericID, profileCiphertext, record.PairedAt, &record); err != nil {
		return DeviceRecord{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return DeviceRecord{}, fmt.Errorf("commit device: %w", err)
	}
	return record, nil
}

// UpdateNumericID assigns a user-selected compact identifier.
func (store *Postgres) UpdateNumericID(ctx context.Context, deviceID string, numericID int) error {
	if numericID < domain.MinNumericID || numericID > domain.MaxNumericID {
		return domain.ErrInvalidNumericID
	}
	command, err := store.pool.Exec(ctx, `
		UPDATE sim_cards
		SET numeric_id = $2
		WHERE id = (
			SELECT current_sim_id FROM device_slots
			WHERE device_id = $1 AND removed_at IS NULL AND current_sim_id IS NOT NULL
			ORDER BY slot_index LIMIT 1
		) AND retired_at IS NULL`, deviceID, numericID)
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) && postgresError.Code == "23505" {
			return domain.ErrDuplicateNumericID
		}
		return fmt.Errorf("update numeric id: %w", err)
	}
	if command.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	if _, err := store.pool.Exec(ctx, `UPDATE devices SET numeric_id = $2 WHERE id = $1 AND revoked_at IS NULL`, deviceID, numericID); err != nil {
		return fmt.Errorf("update legacy numeric id: %w", err)
	}
	return nil
}

// UpdateDeviceProfile replaces only the opaque end-to-end encrypted line profile.
func (store *Postgres) UpdateDeviceProfile(ctx context.Context, deviceID string, profileCiphertext []byte) error {
	command, err := store.pool.Exec(ctx, `
		UPDATE sim_cards
		SET profile_ciphertext = $2
		WHERE id = (
			SELECT slot.current_sim_id
			FROM device_slots AS slot
			JOIN devices ON devices.id = slot.device_id
			WHERE slot.device_id = $1
			  AND devices.revoked_at IS NULL
			  AND slot.removed_at IS NULL
			  AND slot.current_sim_id IS NOT NULL
			ORDER BY slot.slot_index LIMIT 1
		) AND retired_at IS NULL`, deviceID, profileCiphertext)
	if err != nil {
		return fmt.Errorf("update device profile: %w", err)
	}
	if command.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	if _, err := store.pool.Exec(ctx, `UPDATE devices SET profile_ciphertext = $2 WHERE id = $1 AND revoked_at IS NULL`, deviceID, profileCiphertext); err != nil {
		return fmt.Errorf("update legacy device profile: %w", err)
	}
	return nil
}

// RevokeDevice atomically disables the device and all credentials issued to it.
func (store *Postgres) RevokeDevice(ctx context.Context, deviceID string, revokedAt time.Time) error {
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin device revocation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	command, err := tx.Exec(ctx, `
		UPDATE devices SET revoked_at = $2
		WHERE id = $1 AND revoked_at IS NULL`, deviceID, revokedAt.UTC())
	if err != nil {
		return fmt.Errorf("revoke device: %w", err)
	}
	if command.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	if _, err := tx.Exec(ctx, `
		UPDATE device_credentials SET revoked_at = $2
		WHERE device_id = $1 AND revoked_at IS NULL`, deviceID, revokedAt.UTC()); err != nil {
		return fmt.Errorf("revoke device credentials: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit device revocation: %w", err)
	}
	return nil
}

// ListDevices returns active devices without decrypting their line profiles.
func (store *Postgres) ListDevices(ctx context.Context) ([]DeviceRecord, error) {
	rows, err := store.pool.Query(ctx, `
		SELECT id, numeric_id, profile_ciphertext, identity_public_key, command_public_key,
		       wrapped_vault_key, paired_at, last_seen_at, COALESCE(radio_access_technology, ''),
		       signal_bars, radio_status_updated_at
		FROM devices
		WHERE revoked_at IS NULL
		ORDER BY numeric_id`)
	if err != nil {
		return nil, fmt.Errorf("list devices: %w", err)
	}
	defer rows.Close()

	records := make([]DeviceRecord, 0)
	for rows.Next() {
		var record DeviceRecord
		if err := rows.Scan(
			&record.ID,
			&record.NumericID,
			&record.ProfileCiphertext,
			&record.IdentityPublicKey,
			&record.CommandPublicKey,
			&record.WrappedVaultKey,
			&record.PairedAt,
			&record.LastSeenAt,
			&record.RadioAccessTechnology,
			&record.SignalBars,
			&record.RadioStatusUpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan device: %w", err)
		}
		slots, err := store.listDeviceSlots(ctx, record.ID)
		if err != nil {
			return nil, err
		}
		record.Slots = slots
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate devices: %w", err)
	}
	return records, nil
}

func createInitialSIMSlot(ctx context.Context, tx pgx.Tx, deviceID string, numericID int, profileCiphertext []byte, now time.Time, record *DeviceRecord) error {
	return createSIMSlot(ctx, tx, deviceID, "1", 1, deviceID, numericID, profileCiphertext, now, record)
}

func createSIMSlot(ctx context.Context, tx pgx.Tx, deviceID string, slotKey string, slotIndex int, simID string, numericID int, profileCiphertext []byte, now time.Time, record *DeviceRecord) error {
	if profileCiphertext == nil {
		profileCiphertext = []byte{}
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO sim_cards (id, numeric_id, profile_ciphertext, first_seen_at, last_seen_at)
		VALUES ($1, $2, $3, $4, $4)`, simID, numericID, profileCiphertext, now.UTC())
	if err != nil {
		return fmt.Errorf("insert initial SIM identity: %w", err)
	}
	var slot SlotRecord
	err = tx.QueryRow(ctx, `
		INSERT INTO device_slots (device_id, slot_key, slot_index, current_sim_id, created_at, last_seen_at)
		VALUES ($1, $2, $3, $4, $5, $5)
		RETURNING id::text, slot_key, slot_index, current_sim_id::text`, deviceID, slotKey, slotIndex, simID, now.UTC()).Scan(
		&slot.ID, &slot.SlotKey, &slot.SlotIndex, &slot.SIMID,
	)
	if err != nil {
		return fmt.Errorf("insert initial device slot: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO slot_sim_assignments (slot_id, sim_id, inserted_at)
		VALUES ($1, $2, $3)`, slot.ID, simID, now.UTC()); err != nil {
		return fmt.Errorf("record initial SIM slot assignment: %w", err)
	}
	value := numericID
	slot.SIMNumericID = &value
	slot.ProfileCiphertext = append([]byte(nil), profileCiphertext...)
	lastSeen := now.UTC()
	slot.LastSeenAt = &lastSeen
	record.Slots = []SlotRecord{slot}
	return nil
}

func (store *Postgres) listDeviceSlots(ctx context.Context, deviceID string) ([]SlotRecord, error) {
	rows, err := store.pool.Query(ctx, `
		SELECT slot.id::text, slot.slot_key, slot.slot_index,
		       slot.current_sim_id::text, sim.numeric_id, sim.profile_ciphertext,
		       slot.last_seen_at, COALESCE(slot.radio_access_technology, ''),
		       slot.signal_bars, slot.radio_status_updated_at
		FROM device_slots AS slot
		LEFT JOIN sim_cards AS sim ON sim.id = slot.current_sim_id AND sim.retired_at IS NULL
		WHERE slot.device_id = $1 AND slot.removed_at IS NULL
		ORDER BY slot.slot_index`, deviceID)
	if err != nil {
		return nil, fmt.Errorf("list device slots: %w", err)
	}
	defer rows.Close()

	slots := make([]SlotRecord, 0)
	for rows.Next() {
		var slot SlotRecord
		if err := rows.Scan(
			&slot.ID, &slot.SlotKey, &slot.SlotIndex, &slot.SIMID, &slot.SIMNumericID,
			&slot.ProfileCiphertext, &slot.LastSeenAt, &slot.RadioAccessTechnology,
			&slot.SignalBars, &slot.RadioStatusUpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan device slot: %w", err)
		}
		slots = append(slots, slot)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate device slots: %w", err)
	}
	return slots, nil
}

// UpdateDeviceRadioStatus stores the latest non-sensitive modem telemetry.
func (store *Postgres) UpdateDeviceRadioStatus(ctx context.Context, deviceID string, technology string, signalBars *int, now time.Time) error {
	command, err := store.pool.Exec(ctx, `
		UPDATE devices
		SET radio_access_technology = $2,
		    signal_bars = $3,
		    radio_status_updated_at = $4
		WHERE id = $1 AND revoked_at IS NULL`, deviceID, technology, signalBars, now.UTC())
	if err != nil {
		return fmt.Errorf("update device radio status: %w", err)
	}
	if command.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// UpdateDeviceSIMIdentity assigns the current card fingerprint to its physical modem slot.
func (store *Postgres) UpdateDeviceSIMIdentity(ctx context.Context, deviceID string, slotKey string, slotIndex int, fingerprint []byte, now time.Time) (string, int, error) {
	if len(fingerprint) != 32 || slotKey == "" || slotIndex < 1 || slotIndex > 32 {
		return "", 0, errors.New("invalid slot or SIM fingerprint")
	}
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return "", 0, fmt.Errorf("begin SIM identity synchronization: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var slotID string
	var oldSIMID *string
	if err := tx.QueryRow(ctx, `
		SELECT id::text, current_sim_id::text FROM device_slots
		WHERE device_id = $1 AND slot_key = $2 AND slot_index = $3 AND removed_at IS NULL
		FOR UPDATE`, deviceID, slotKey, slotIndex).Scan(&slotID, &oldSIMID); err != nil {
		return "", 0, err
	}
	var simID string
	var numericID int
	err = tx.QueryRow(ctx, `
		SELECT id::text, numeric_id FROM sim_cards
		WHERE fingerprint_scope = $1 AND iccid_fingerprint = $2
		ORDER BY created_at LIMIT 1 FOR UPDATE`, deviceID, fingerprint).Scan(&simID, &numericID)
	if errors.Is(err, pgx.ErrNoRows) {
		if oldSIMID != nil {
			err = tx.QueryRow(ctx, `
				UPDATE sim_cards
				SET iccid_fingerprint = $2, fingerprint_scope = $3, last_seen_at = $4
				WHERE id = $1
				  AND iccid_fingerprint IS NULL
				  AND fingerprint_scope IS NULL
				  AND retired_at IS NULL
				RETURNING id::text, numeric_id`, *oldSIMID, fingerprint, deviceID, now.UTC()).Scan(&simID, &numericID)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return "", 0, fmt.Errorf("identify current SIM card: %w", err)
			}
		}
	}
	if errors.Is(err, pgx.ErrNoRows) {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(0x53494d44)); err != nil {
			return "", 0, fmt.Errorf("lock SIM numeric ID allocation: %w", err)
		}
		err = tx.QueryRow(ctx, `
			SELECT candidate FROM generate_series(1, 99) AS candidate
			LEFT JOIN sim_cards ON sim_cards.numeric_id = candidate AND sim_cards.retired_at IS NULL
			WHERE sim_cards.id IS NULL ORDER BY candidate LIMIT 1`).Scan(&numericID)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", 0, domain.ErrNoNumericID
		}
		if err != nil {
			return "", 0, fmt.Errorf("allocate SIM numeric ID: %w", err)
		}
		err = tx.QueryRow(ctx, `
			INSERT INTO sim_cards (id, numeric_id, iccid_fingerprint, fingerprint_scope, profile_ciphertext, first_seen_at, last_seen_at)
			VALUES (gen_random_uuid(), $1, $2, $3, ''::bytea, $4, $4)
			RETURNING id::text`, numericID, fingerprint, deviceID, now.UTC()).Scan(&simID)
		if err != nil {
			return "", 0, fmt.Errorf("create SIM card identity: %w", err)
		}
	} else if err != nil {
		return "", 0, fmt.Errorf("find SIM card identity: %w", err)
	} else if _, err := tx.Exec(ctx, `UPDATE sim_cards SET retired_at = NULL, last_seen_at = $2 WHERE id = $1`, simID, now.UTC()); err != nil {
		return "", 0, fmt.Errorf("reactivate SIM card identity: %w", err)
	}
	if oldSIMID != nil && *oldSIMID != simID {
		if _, err := tx.Exec(ctx, `
			UPDATE slot_sim_assignments
			SET removed_at = GREATEST($2, inserted_at)
			WHERE slot_id = $1 AND removed_at IS NULL`, slotID, now.UTC()); err != nil {
			return "", 0, err
		}
		if _, err := tx.Exec(ctx, `UPDATE device_slots SET current_sim_id = NULL WHERE id = $1`, slotID); err != nil {
			return "", 0, err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE device_slots SET current_sim_id = NULL WHERE current_sim_id = $1 AND id <> $2`, simID, slotID); err != nil {
		return "", 0, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE slot_sim_assignments
		SET removed_at = GREATEST($3, inserted_at)
		WHERE sim_id = $1 AND slot_id <> $2 AND removed_at IS NULL`, simID, slotID, now.UTC()); err != nil {
		return "", 0, err
	}
	if _, err := tx.Exec(ctx, `UPDATE device_slots SET current_sim_id = $2, last_seen_at = $3 WHERE id = $1`, slotID, simID, now.UTC()); err != nil {
		return "", 0, err
	}
	if _, err := tx.Exec(ctx, `UPDATE devices SET numeric_id = $2, last_seen_at = $3 WHERE id = $1 AND revoked_at IS NULL`, deviceID, numericID, now.UTC()); err != nil {
		return "", 0, fmt.Errorf("update legacy device SIM identity: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO slot_sim_assignments (slot_id, sim_id, inserted_at)
		SELECT $1, $2, $3
		WHERE NOT EXISTS (SELECT 1 FROM slot_sim_assignments WHERE slot_id = $1 AND sim_id = $2 AND removed_at IS NULL)`, slotID, simID, now.UTC()); err != nil {
		return "", 0, err
	}
	if oldSIMID != nil && *oldSIMID != simID {
		if _, err := tx.Exec(ctx, `UPDATE sim_cards SET retired_at = $2 WHERE id = $1 AND NOT EXISTS (SELECT 1 FROM device_slots WHERE current_sim_id = $1)`, *oldSIMID, now.UTC()); err != nil {
			return "", 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return "", 0, fmt.Errorf("commit SIM identity synchronization: %w", err)
	}
	return simID, numericID, nil
}

// InsertEvent stores an encrypted event once and returns its server sequence.
func (store *Postgres) InsertEvent(ctx context.Context, envelope domain.EventEnvelope, expiresAt time.Time) (int64, bool, error) {
	var sequence int64
	err := store.pool.QueryRow(ctx, `
		INSERT INTO events (id, type, device_id, occurred_at, key_epoch, nonce, ciphertext, signature, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (id) DO NOTHING
		RETURNING sequence`, envelope.ID, envelope.Type, envelope.DeviceUUID, envelope.OccurredAt, envelope.KeyEpoch, envelope.Nonce, envelope.Ciphertext, envelope.Signature, expiresAt).Scan(&sequence)
	if errors.Is(err, pgx.ErrNoRows) {
		err = store.pool.QueryRow(ctx, `
			SELECT sequence
			FROM events
			WHERE id = $1
			  AND type = $2
			  AND device_id = $3
			  AND occurred_at = $4
			  AND key_epoch = $5
			  AND nonce = $6
			  AND ciphertext = $7
			  AND signature = $8
			  AND expires_at = $9`, envelope.ID, envelope.Type, envelope.DeviceUUID, envelope.OccurredAt, envelope.KeyEpoch, envelope.Nonce, envelope.Ciphertext, envelope.Signature, expiresAt).Scan(&sequence)
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, false, domain.ErrIdempotencyConflict
		}
		if err != nil {
			return 0, false, fmt.Errorf("read existing event: %w", err)
		}
		return sequence, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("insert event: %w", err)
	}
	return sequence, true, nil
}

// ListEvents returns unexpired opaque events strictly after the supplied cursor.
func (store *Postgres) ListEvents(ctx context.Context, afterSequence int64, limit int, now time.Time) ([]domain.EventEnvelope, error) {
	rows, err := store.pool.Query(ctx, `
		SELECT id, sequence, type, device_id, occurred_at, key_epoch, nonce, ciphertext, signature
		FROM events
		WHERE sequence > $1 AND expires_at > $2
		ORDER BY sequence ASC
		LIMIT $3`, afterSequence, now.UTC(), limit)
	if err != nil {
		return nil, fmt.Errorf("list encrypted events: %w", err)
	}
	defer rows.Close()

	events := make([]domain.EventEnvelope, 0)
	for rows.Next() {
		var envelope domain.EventEnvelope
		if err := rows.Scan(
			&envelope.ID,
			&envelope.Sequence,
			&envelope.Type,
			&envelope.DeviceUUID,
			&envelope.OccurredAt,
			&envelope.KeyEpoch,
			&envelope.Nonce,
			&envelope.Ciphertext,
			&envelope.Signature,
		); err != nil {
			return nil, fmt.Errorf("scan encrypted event: %w", err)
		}
		events = append(events, envelope)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate encrypted events: %w", err)
	}
	return events, nil
}

// EnqueueCommand stores one opaque command while preserving client idempotency.
func (store *Postgres) EnqueueCommand(ctx context.Context, encrypted domain.EncryptedCommand) (bool, error) {
	command, err := store.pool.Exec(ctx, `
		INSERT INTO encrypted_device_commands (
			id, device_id, sender_installation_id, sender_public_key, nonce,
			ciphertext, signature, created_at, expires_at
		)
		SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9
		FROM devices, app_installations
		WHERE devices.id = $2 AND devices.revoked_at IS NULL
		  AND app_installations.id = $3
		  AND app_installations.approved_at IS NOT NULL
		  AND app_installations.revoked_at IS NULL
		ON CONFLICT (id) DO NOTHING`,
		encrypted.ID,
		encrypted.DeviceUUID,
		encrypted.SenderInstallationID,
		encrypted.SenderPublicKey,
		encrypted.Nonce,
		encrypted.Ciphertext,
		encrypted.Signature,
		encrypted.CreatedAt.UTC(),
		encrypted.ExpiresAt.UTC(),
	)
	if err != nil {
		return false, fmt.Errorf("enqueue encrypted command: %w", err)
	}
	if command.RowsAffected() == 1 {
		return true, nil
	}

	var matches bool
	err = store.pool.QueryRow(ctx, `
		SELECT device_id = $2
		   AND sender_installation_id = $3
		   AND sender_public_key = $4
		   AND nonce = $5
		   AND ciphertext = $6
		   AND signature = $7
		   AND created_at = $8
		   AND expires_at = $9
		FROM encrypted_device_commands
		WHERE id = $1`,
		encrypted.ID,
		encrypted.DeviceUUID,
		encrypted.SenderInstallationID,
		encrypted.SenderPublicKey,
		encrypted.Nonce,
		encrypted.Ciphertext,
		encrypted.Signature,
		encrypted.CreatedAt.UTC(),
		encrypted.ExpiresAt.UTC(),
	).Scan(&matches)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, pgx.ErrNoRows
	}
	if err != nil {
		return false, fmt.Errorf("read existing encrypted command: %w", err)
	}
	if !matches {
		return false, domain.ErrIdempotencyConflict
	}
	return false, nil
}

// ClaimCommand leases the oldest deliverable command to one authenticated device.
func (store *Postgres) ClaimCommand(ctx context.Context, deviceID string, now time.Time, leaseUntil time.Time) (domain.EncryptedCommand, error) {
	var encrypted domain.EncryptedCommand
	err := store.pool.QueryRow(ctx, `
		WITH next_command AS (
			SELECT id
			FROM encrypted_device_commands
			WHERE device_id = $1
			  AND completed_at IS NULL
			  AND expires_at > $2
			  AND (leased_until IS NULL OR leased_until <= $2)
			ORDER BY created_at, id
			LIMIT 1
			FOR UPDATE SKIP LOCKED
		)
		UPDATE encrypted_device_commands
		SET leased_until = $3, delivery_attempts = delivery_attempts + 1
		FROM next_command
		WHERE encrypted_device_commands.id = next_command.id
		RETURNING encrypted_device_commands.id,
		          encrypted_device_commands.device_id,
		          encrypted_device_commands.sender_installation_id,
		          encrypted_device_commands.sender_public_key,
		          encrypted_device_commands.nonce,
		          encrypted_device_commands.ciphertext,
		          encrypted_device_commands.signature,
		          encrypted_device_commands.created_at,
		          encrypted_device_commands.expires_at`,
		deviceID,
		now.UTC(),
		leaseUntil.UTC(),
	).Scan(
		&encrypted.ID,
		&encrypted.DeviceUUID,
		&encrypted.SenderInstallationID,
		&encrypted.SenderPublicKey,
		&encrypted.Nonce,
		&encrypted.Ciphertext,
		&encrypted.Signature,
		&encrypted.CreatedAt,
		&encrypted.ExpiresAt,
	)
	if err != nil {
		return domain.EncryptedCommand{}, err
	}
	return encrypted, nil
}

// CompleteCommand stores only the encrypted device response and closes the lease.
func (store *Postgres) CompleteCommand(ctx context.Context, deviceID string, commandID string, result domain.EncryptedCommandResult, completedAt time.Time) error {
	command, err := store.pool.Exec(ctx, `
		UPDATE encrypted_device_commands
		SET result_nonce = $3,
		    result_ciphertext = $4,
		    completed_at = $5,
		    leased_until = NULL
		WHERE id = $1
		  AND device_id = $2
		  AND completed_at IS NULL
		  AND expires_at > $5`,
		commandID,
		deviceID,
		result.Nonce,
		result.Ciphertext,
		completedAt.UTC(),
	)
	if err != nil {
		return fmt.Errorf("complete encrypted command: %w", err)
	}
	if command.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// CommandStatus returns routing state and an opaque result to the authenticated owner.
func (store *Postgres) CommandStatus(ctx context.Context, deviceID string, commandID string, now time.Time) (domain.CommandStatus, error) {
	var status domain.CommandStatus
	var leasedUntil *time.Time
	var resultNonce []byte
	var resultCiphertext []byte
	err := store.pool.QueryRow(ctx, `
		SELECT id, device_id, leased_until, result_nonce, result_ciphertext, completed_at
		FROM encrypted_device_commands
		WHERE id = $1 AND device_id = $2 AND expires_at > $3`,
		commandID,
		deviceID,
		now.UTC(),
	).Scan(
		&status.ID,
		&status.DeviceUUID,
		&leasedUntil,
		&resultNonce,
		&resultCiphertext,
		&status.CompletedAt,
	)
	if err != nil {
		return domain.CommandStatus{}, err
	}
	status.State = "pending"
	if leasedUntil != nil && leasedUntil.After(now) {
		status.State = "processing"
	}
	if status.CompletedAt != nil {
		status.State = "completed"
		status.Result = &domain.EncryptedCommandResult{
			Nonce:      resultNonce,
			Ciphertext: resultCiphertext,
		}
	}
	return status, nil
}

// UpdateDeviceCommandPublicKey registers the X25519 key used for opaque commands.
func (store *Postgres) UpdateDeviceCommandPublicKey(ctx context.Context, deviceID string, publicKey []byte) error {
	command, err := store.pool.Exec(ctx, `
		UPDATE devices
		SET command_public_key = $2
		WHERE id = $1 AND revoked_at IS NULL`, deviceID, publicKey)
	if err != nil {
		return fmt.Errorf("update device command public key: %w", err)
	}
	if command.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// Schema returns the embedded schema for validation and packaging tests.
func Schema() ([]byte, error) {
	return schemaFS.ReadFile("schema.sql")
}
