package store

import (
	"bytes"
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	webauthnlib "github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"

	"sim-deck-server/internal/auth"
	"sim-deck-server/internal/domain"
)

//go:embed schema.sql
var schemaFS embed.FS

// Postgres stores only routing metadata and encrypted application payloads.
type Postgres struct {
	pool *pgxpool.Pool
	db   *gorm.DB
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

// SIMSlotAssignment is the current server-owned identity for a physical slot and inserted SIM.
type SIMSlotAssignment struct {
	SlotID    string `json:"slot_id"`
	SIMID     string `json:"sim_id"`
	NumericID int    `json:"numeric_id"`
}

// SIMRecord identifies a physical SIM independently from any device slot.
type SIMRecord struct {
	ID                string    `json:"sim_id"`
	NumericID         int       `json:"numeric_id"`
	ProfileCiphertext []byte    `json:"profile_ciphertext,omitempty"`
	FirstSeenAt       time.Time `json:"first_seen_at"`
	LastSeenAt        time.Time `json:"last_seen_at"`
	AssignedDeviceID  *string   `json:"assigned_device_uuid,omitempty"`
	AssignedSlotID    *string   `json:"assigned_slot_id,omitempty"`
}

// ESIMProfileRecord contains the server-owned metadata for one installed eSIM
// profile. Provisioning itself remains a device capability.
type ESIMProfileRecord struct {
	SIMID        string    `json:"sim_id"`
	ProfileID    string    `json:"profile_id"`
	Name         string    `json:"name"`
	ICCID        string    `json:"iccid"`
	OperatorName string    `json:"operator_name"`
	IsEnabled    bool      `json:"is_enabled"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
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
	database, err := gorm.Open(postgres.Open(databaseURL), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("open database: %w", err)
	}
	return &Postgres{pool: pool, db: database}, nil
}

// Close releases all PostgreSQL connections.
func (store *Postgres) Close() {
	store.pool.Close()
	if store.db != nil {
		if sqlDB, err := store.db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}
}

// InitializeSchema creates the complete database schema for a new deployment.
func (store *Postgres) InitializeSchema(ctx context.Context) error {
	return store.runMigrations(ctx)
}

// OwnerExists reports whether the single owner account has already been created.
func (store *Postgres) OwnerExists(ctx context.Context) (bool, error) {
	var count int64
	if err := store.database(ctx).Model(&ownerAccountModel{}).Count(&count).Error; err != nil {
		return false, fmt.Errorf("check owner account: %w", err)
	}
	return count > 0, nil
}

// CreateOwner creates the single owner while serializing concurrent bootstrap attempts.
func (store *Postgres) CreateOwner(ctx context.Context, passwordHash string, recoveryPublicKey []byte) (bool, error) {
	owner := ownerAccountModel{
		ID: uuid.NewString(), Username: "admin", PasswordHash: []byte(passwordHash),
		RecoveryPublicKey: append([]byte(nil), recoveryPublicKey...), CreatedAt: time.Now().UTC(),
	}
	result := store.database(ctx).Create(&owner)
	if result.Error != nil {
		var existing ownerAccountModel
		if findErr := store.database(ctx).Where("username = ?", owner.Username).First(&existing).Error; findErr == nil {
			return false, nil
		}
		return false, fmt.Errorf("create owner account: %w", result.Error)
	}
	return true, nil
}

// OwnerCredentialByUsername loads bootstrap credentials without returning recovery material.
func (store *Postgres) OwnerCredentialByUsername(ctx context.Context, username string) (OwnerCredential, error) {
	var model ownerAccountModel
	if err := store.database(ctx).Where("username = ?", username).First(&model).Error; err != nil {
		return OwnerCredential{}, databaseNotFound(err)
	}
	return OwnerCredential{ID: model.ID, PasswordHash: string(model.PasswordHash), PasswordDisabledAt: model.PasswordDisabledAt}, nil
}

// OwnerID returns the singleton owner identifier used by passkey ceremonies.
func (store *Postgres) OwnerID(ctx context.Context) (string, error) {
	var model ownerAccountModel
	if err := store.database(ctx).Where("username = ?", "admin").First(&model).Error; err != nil {
		return "", databaseNotFound(err)
	}
	return model.ID, nil
}

// WebAuthnCredentials loads persisted passkey records.
func (store *Postgres) WebAuthnCredentials(ctx context.Context, ownerID string) ([]webauthnlib.Credential, error) {
	var models []webauthnCredentialModel
	if err := store.database(ctx).Where("owner_id = ?", ownerID).Order("created_at").Find(&models).Error; err != nil {
		return nil, fmt.Errorf("list passkey credentials: %w", err)
	}
	credentials := make([]webauthnlib.Credential, 0)
	for _, model := range models {
		var credential webauthnlib.Credential
		if err := json.Unmarshal(model.PublicKey, &credential); err != nil {
			return nil, fmt.Errorf("decode passkey credential: %w", err)
		}
		credentials = append(credentials, credential)
	}
	return credentials, nil
}

// SaveWebAuthnCredential stores the complete credential while keeping the lookup ID indexed.
func (store *Postgres) SaveWebAuthnCredential(ctx context.Context, ownerID string, credential webauthnlib.Credential) error {
	encoded, err := json.Marshal(credential)
	if err != nil {
		return err
	}
	model := webauthnCredentialModel{ID: append([]byte(nil), credential.ID...), OwnerID: ownerID, PublicKey: encoded, SignCount: credential.Authenticator.SignCount, LastUsedAt: ptrTime(time.Now().UTC())}
	result := store.database(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.Assignments(map[string]any{"public_key": encoded, "sign_count": credential.Authenticator.SignCount, "last_used_at": time.Now().UTC()})}).Create(&model)
	return result.Error
}

// DisableOwnerPassword permanently disables the bootstrap password after passkey enrollment.
func (store *Postgres) DisableOwnerPassword(ctx context.Context, ownerID string, now time.Time) error {
	result := store.database(ctx).Model(&ownerAccountModel{}).Where("id = ?", ownerID).Updates(map[string]any{"password_hash": nil, "password_disabled_at": now.UTC()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return pgx.ErrNoRows
	}
	return nil
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
	model := authSessionModel{ID: uuid.NewString(), OwnerID: ownerID, AccessTokenHash: accessHash[:], RefreshTokenHash: refreshHash[:], AccessExpiresAt: pair.AccessExpiresAt, RefreshExpiresAt: pair.RefreshExpiresAt, CreatedAt: time.Now().UTC()}
	if err := store.database(ctx).Create(&model).Error; err != nil {
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
	err = store.database(ctx).Transaction(func(tx *gorm.DB) error {
		var session authSessionModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("refresh_token_hash = ? AND revoked_at IS NULL AND refresh_expires_at > ?", refreshHash[:], now.UTC()).First(&session).Error; err != nil {
			return databaseNotFound(err)
		}
		revokedAt := now.UTC()
		if err := tx.Model(&session).Updates(map[string]any{"revoked_at": revokedAt}).Error; err != nil {
			return err
		}
		replacement := authSessionModel{ID: uuid.NewString(), OwnerID: session.OwnerID, AccessTokenHash: accessHash[:], RefreshTokenHash: newRefreshHash[:], AccessExpiresAt: pair.AccessExpiresAt, RefreshExpiresAt: pair.RefreshExpiresAt, CreatedAt: time.Now().UTC()}
		if err := tx.Create(&replacement).Error; err != nil {
			return fmt.Errorf("insert rotated session: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("rotate session: %w", err)
	}
	return nil
}

// RevokeSession invalidates the session matching an access token.
func (store *Postgres) RevokeSession(ctx context.Context, accessToken string, now time.Time) error {
	accessHash, err := auth.TokenHash(accessToken)
	if err != nil {
		return err
	}
	result := store.database(ctx).Model(&authSessionModel{}).Where("access_token_hash = ? AND revoked_at IS NULL AND access_expires_at > ?", accessHash[:], now.UTC()).Updates(map[string]any{"revoked_at": now.UTC()})
	if result.Error != nil {
		return fmt.Errorf("revoke auth session: %w", result.Error)
	}
	if result.RowsAffected == 0 {
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
	var session authSessionModel
	if err := store.database(ctx).Where("access_token_hash = ? AND revoked_at IS NULL AND access_expires_at > ?", accessHash[:], now.UTC()).First(&session).Error; err != nil {
		return "", databaseNotFound(err)
	}
	return session.OwnerID, nil
}

// CreatePairingToken stores only the hash of a short-lived one-time pairing secret.
func (store *Postgres) CreatePairingToken(ctx context.Context, ownerID string, tokenHash []byte, expiresAt time.Time) error {
	owner := ownerID
	if err := store.database(ctx).Create(&pairingTokenModel{TokenHash: append([]byte(nil), tokenHash...), OwnerID: &owner, ExpiresAt: expiresAt.UTC(), CreatedAt: time.Now().UTC()}).Error; err != nil {
		return fmt.Errorf("create pairing token: %w", err)
	}
	return nil
}

// RegisterAppInstallation atomically approves the first iPhone and leaves later installs pending.
func (store *Postgres) RegisterAppInstallation(ctx context.Context, ownerID string, registration AppInstallationRegistration, now time.Time) (AppInstallationRecord, bool, error) {
	var record AppInstallationRecord
	created := false
	err := store.database(ctx).Transaction(func(tx *gorm.DB) error {
		var existing appInstallationModel
		findErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", registration.ID).First(&existing).Error
		if findErr == nil {
			if existing.OwnerID != ownerID || existing.RevokedAt != nil || !bytes.Equal(existing.IdentityPublicKey, registration.IdentityPublicKey) || !bytes.Equal(existing.WrappedVaultKey, registration.WrappedVaultKey) {
				return domain.ErrInstallationConflict
			}
			if len(existing.KeyAgreementPublicKey) == 0 {
				if err := tx.Model(&existing).Updates(map[string]any{"key_agreement_public_key": registration.KeyAgreementPublicKey}).Error; err != nil {
					return err
				}
				existing.KeyAgreementPublicKey = append([]byte(nil), registration.KeyAgreementPublicKey...)
			} else if !bytes.Equal(existing.KeyAgreementPublicKey, registration.KeyAgreementPublicKey) {
				return domain.ErrInstallationConflict
			}
			record = appInstallationRecordFromModel(existing)
			return nil
		}
		if !errors.Is(findErr, gorm.ErrRecordNotFound) {
			return findErr
		}
		var approvedCount int64
		if err := tx.Model(&appInstallationModel{}).Where("owner_id = ? AND approved_at IS NOT NULL AND revoked_at IS NULL", ownerID).Count(&approvedCount).Error; err != nil {
			return err
		}
		var approvedAt *time.Time
		if approvedCount == 0 {
			approvedAt = ptrTime(now.UTC())
		}
		model := appInstallationModel{ID: registration.ID, OwnerID: ownerID, IdentityPublicKey: append([]byte(nil), registration.IdentityPublicKey...), KeyAgreementPublicKey: append([]byte(nil), registration.KeyAgreementPublicKey...), WrappedVaultKey: append([]byte(nil), registration.WrappedVaultKey...), ApprovedAt: approvedAt, CreatedAt: now.UTC()}
		if err := tx.Create(&model).Error; err != nil {
			return domain.ErrInstallationConflict
		}
		record = appInstallationRecordFromModel(model)
		created = true
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return AppInstallationRecord{}, false, fmt.Errorf("register app installation: %w", err)
	}
	return record, created, nil
}

func appInstallationRecordFromModel(model appInstallationModel) AppInstallationRecord {
	return AppInstallationRecord{ID: model.ID, IdentityPublicKey: append([]byte(nil), model.IdentityPublicKey...), KeyAgreementPublicKey: append([]byte(nil), model.KeyAgreementPublicKey...), WrappedVaultKey: append([]byte(nil), model.WrappedVaultKey...), ApprovedAt: model.ApprovedAt, RevokedAt: model.RevokedAt, CreatedAt: model.CreatedAt}
}

// ListApprovedAppInstallationRecipients returns only active public encryption identities.
func (store *Postgres) ListApprovedAppInstallationRecipients(ctx context.Context) ([]AppInstallationRecipient, error) {
	var models []appInstallationModel
	if err := store.database(ctx).Where("approved_at IS NOT NULL AND revoked_at IS NULL AND key_agreement_public_key IS NOT NULL").Order("created_at, id").Find(&models).Error; err != nil {
		return nil, fmt.Errorf("list approved app installation recipients: %w", err)
	}
	recipients := make([]AppInstallationRecipient, 0)
	for _, model := range models {
		recipients = append(recipients, AppInstallationRecipient{InstallationID: model.ID, IdentityPublicKey: append([]byte(nil), model.IdentityPublicKey...), KeyAgreementPublicKey: append([]byte(nil), model.KeyAgreementPublicKey...)})
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
	err := store.database(ctx).Transaction(func(tx *gorm.DB) error {
		var installation appInstallationModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND owner_id = ? AND approved_at IS NOT NULL AND revoked_at IS NULL", installationID, ownerID).First(&installation).Error; err != nil {
			return databaseNotFound(err)
		}
		model := pushTokenModel{InstallationID: installation.ID, Environment: environment, Kind: kind, TokenCiphertext: append([]byte(nil), token...), UpdatedAt: now.UTC()}
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "installation_id"}, {Name: "environment"}, {Name: "kind"}}, DoUpdates: clause.AssignmentColumns([]string{"token_ciphertext", "updated_at"})}).Create(&model).Error
	}, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("upsert push token: %w", err)
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
	var removed int64
	err := store.database(ctx).Transaction(func(tx *gorm.DB) error {
		var installation appInstallationModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND owner_id = ?", installationID, ownerID).First(&installation).Error; err != nil {
			return databaseNotFound(err)
		}
		result := tx.Where("installation_id = ? AND environment = ? AND kind = ?", installationID, environment, kind).Delete(&pushTokenModel{})
		removed = result.RowsAffected
		return result.Error
	}, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("delete push token: %w", err)
	}
	if removed == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// InsertIncomingCall stores recipient ciphertexts and queues one opaque VoIP delivery per approved iPhone.
func (store *Postgres) InsertIncomingCall(ctx context.Context, notification domain.IncomingCallNotification, expiresAt time.Time) (bool, error) {
	created := false
	err := store.database(ctx).Transaction(func(tx *gorm.DB) error {
		model := incomingCallNotificationModel{ID: notification.ID, Type: notification.Type, DeviceID: notification.DeviceUUID, OccurredAt: notification.OccurredAt, KeyEpoch: notification.KeyEpoch, ExpiresAt: expiresAt, CreatedAt: time.Now().UTC()}
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&model)
		if result.Error != nil {
			return fmt.Errorf("insert incoming call notification: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			matches, err := incomingCallMatches(ctx, tx, notification, expiresAt)
			if err != nil {
				return err
			}
			if !matches {
				return domain.ErrIdempotencyConflict
			}
			return nil
		}
		created = true
		for _, recipient := range notification.Recipients {
			var installation appInstallationModel
			if err := tx.Where("id = ? AND approved_at IS NOT NULL AND revoked_at IS NULL", recipient.InstallationID).First(&installation).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return domain.ErrUntrustedInstallationRecipient
				}
				return err
			}
			if err := tx.Create(&incomingCallRecipientModel{NotificationID: notification.ID, InstallationID: installation.ID, SenderPublicKey: recipient.SenderPublicKey, Nonce: recipient.Nonce, Ciphertext: recipient.Ciphertext, Signature: recipient.Signature}).Error; err != nil {
				return err
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
				return fmt.Errorf("encode incoming call delivery: %w", err)
			}
			expires := notification.OccurredAt.Add(2 * time.Minute)
			if err := tx.Create(&outboxJobModel{Kind: "voip.incoming_call", RoutingKey: recipient.InstallationID, Payload: delivery, AvailableAt: time.Now().UTC(), ExpiresAt: &expires}).Error; err != nil {
				return fmt.Errorf("enqueue incoming call delivery: %w", err)
			}
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return false, fmt.Errorf("insert incoming call: %w", err)
	}
	return created, nil
}

func incomingCallMatches(ctx context.Context, tx *gorm.DB, notification domain.IncomingCallNotification, expiresAt time.Time) (bool, error) {
	var model incomingCallNotificationModel
	if err := tx.Where("id = ?", notification.ID).First(&model).Error; err != nil {
		return false, databaseNotFound(err)
	}
	if model.Type != notification.Type || model.DeviceID != notification.DeviceUUID || !model.OccurredAt.Equal(notification.OccurredAt) || model.KeyEpoch != notification.KeyEpoch || !model.ExpiresAt.Equal(expiresAt) {
		return false, nil
	}
	var recipients []incomingCallRecipientModel
	if err := tx.Where("notification_id = ?", notification.ID).Order("installation_id").Find(&recipients).Error; err != nil {
		return false, err
	}
	if len(recipients) != len(notification.Recipients) {
		return false, nil
	}
	for index, row := range recipients {
		left := domain.IncomingCallRecipient{InstallationID: row.InstallationID, SenderPublicKey: row.SenderPublicKey, Nonce: row.Nonce, Ciphertext: row.Ciphertext, Signature: row.Signature}
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
	var claim domain.IncomingCallClaim
	err := store.database(ctx).Transaction(func(tx *gorm.DB) error {
		var notification incomingCallNotificationModel
		if err := tx.Where("id = ? AND expires_at > ?", notificationID, now.UTC()).First(&notification).Error; err != nil {
			return databaseNotFound(err)
		}
		var installation appInstallationModel
		if err := tx.Where("id = ? AND owner_id = ? AND approved_at IS NOT NULL AND revoked_at IS NULL", installationID, ownerID).First(&installation).Error; err != nil {
			return databaseNotFound(err)
		}
		var recipient incomingCallRecipientModel
		if err := tx.Where("notification_id = ? AND installation_id = ?", notificationID, installationID).First(&recipient).Error; err != nil {
			return databaseNotFound(err)
		}
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&incomingCallClaimModel{NotificationID: notificationID, InstallationID: installationID, ClaimedAt: now.UTC()})
		if result.Error != nil {
			return result.Error
		}
		created := result.RowsAffected == 1
		var stored incomingCallClaimModel
		if err := tx.Where("notification_id = ?", notificationID).First(&stored).Error; err != nil {
			return err
		}
		claim = domain.IncomingCallClaim{NotificationID: notificationID, InstallationID: stored.InstallationID, ClaimedAt: stored.ClaimedAt, Won: stored.InstallationID == installationID}
		if !created {
			return nil
		}
		payload, err := json.Marshal(domain.IncomingCallClaimDelivery{NotificationID: notificationID, Reason: "answered_elsewhere"})
		if err != nil {
			return fmt.Errorf("encode incoming call claim delivery: %w", err)
		}
		var recipients []incomingCallRecipientModel
		if err := tx.Where("notification_id = ? AND installation_id <> ?", notificationID, installationID).Find(&recipients).Error; err != nil {
			return err
		}
		expires := now.UTC().Add(30 * time.Second)
		for _, row := range recipients {
			var approved appInstallationModel
			if err := tx.Where("id = ? AND approved_at IS NOT NULL AND revoked_at IS NULL", row.InstallationID).First(&approved).Error; errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			} else if err != nil {
				return err
			}
			if err := tx.Create(&outboxJobModel{Kind: "voip.incoming_call_claimed", RoutingKey: row.InstallationID, Payload: payload, AvailableAt: now.UTC(), ExpiresAt: &expires}).Error; err != nil {
				return err
			}
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return domain.IncomingCallClaim{}, fmt.Errorf("claim incoming call: %w", err)
	}
	return claim, nil
}

// ClaimVoIPPushJob leases the next time-sensitive VoIP outbox job without blocking another worker.
func (store *Postgres) ClaimVoIPPushJob(ctx context.Context, now time.Time, leaseDuration time.Duration) (OutboxPushJob, error) {
	var output OutboxPushJob
	err := store.database(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&outboxJobModel{}).Where("completed_at IS NULL AND expires_at IS NOT NULL AND expires_at <= ? AND kind LIKE ?", now.UTC(), "voip.%").Updates(map[string]any{"completed_at": now.UTC(), "last_error": "expired before delivery"}).Error; err != nil {
			return fmt.Errorf("expire outbox jobs: %w", err)
		}
		var model outboxJobModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).Where("completed_at IS NULL AND available_at <= ? AND (expires_at IS NULL OR expires_at > ?) AND kind IN ?", now.UTC(), now.UTC(), []string{"voip.incoming_call", "voip.incoming_call_claimed", "alert.incoming_message"}).Order("available_at, id").First(&model).Error; err != nil {
			return databaseNotFound(err)
		}
		kind := "alert"
		if strings.HasPrefix(model.Kind, "voip.") {
			kind = "voip"
		}
		var environment string
		var deviceToken []byte
		var installation appInstallationModel
		if err := tx.Where("id = ? AND approved_at IS NOT NULL AND revoked_at IS NULL", model.RoutingKey).First(&installation).Error; err == nil {
			var token pushTokenModel
			if err := tx.Where("installation_id = ? AND kind = ?", installation.ID, kind).Order("updated_at DESC").First(&token).Error; err == nil {
				environment = token.Environment
				deviceToken = append([]byte(nil), token.TokenCiphertext...)
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		leaseExpires := now.UTC().Add(leaseDuration)
		model.Attempts++
		if err := tx.Model(&model).Updates(map[string]any{"attempts": model.Attempts, "available_at": leaseExpires}).Error; err != nil {
			return fmt.Errorf("lease outbox job: %w", err)
		}
		output = OutboxPushJob{ID: model.ID, Kind: model.Kind, RoutingKey: model.RoutingKey, Payload: append([]byte(nil), model.Payload...), Attempt: model.Attempts, Environment: environment, DeviceToken: deviceToken, LeaseExpiresAt: leaseExpires}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return OutboxPushJob{}, fmt.Errorf("claim outbox job: %w", err)
	}
	return output, nil
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
	var lastError *string
	if reason != "" {
		lastError = &reason
	}
	result := store.database(ctx).Model(&outboxJobModel{}).Where("id = ? AND attempts = ? AND completed_at IS NULL", id, attempt).Updates(map[string]any{"completed_at": now.UTC(), "last_error": lastError})
	if result.Error != nil {
		return fmt.Errorf("finish push job: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// RetryPushJob releases a leased delivery at the requested retry time.
func (store *Postgres) RetryPushJob(ctx context.Context, id int64, attempt int, availableAt time.Time, reason string) error {
	result := store.database(ctx).Model(&outboxJobModel{}).Where("id = ? AND attempts = ? AND completed_at IS NULL", id, attempt).Updates(map[string]any{"available_at": availableAt.UTC(), "last_error": reason})
	if result.Error != nil {
		return fmt.Errorf("retry push job: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// DeletePushTokenForDelivery removes only the failed route that APNs rejected.
func (store *Postgres) DeletePushTokenForDelivery(ctx context.Context, installationID string, environment string, kind string, token []byte) error {
	if err := store.database(ctx).Where("installation_id = ? AND environment = ? AND kind = ? AND token_ciphertext = ?", installationID, environment, kind, token).Delete(&pushTokenModel{}).Error; err != nil {
		return fmt.Errorf("delete push token: %w", err)
	}
	return nil
}

// ClaimPushJob is the generalized push lease used for VoIP and alert deliveries.
func (store *Postgres) ClaimPushJob(ctx context.Context, now time.Time, leaseDuration time.Duration) (OutboxPushJob, error) {
	return store.ClaimVoIPPushJob(ctx, now, leaseDuration)
}

// DeleteVoIPPushToken preserves the legacy repository contract for VoIP callers.
func (store *Postgres) DeleteVoIPPushToken(ctx context.Context, installationID string, environment string, token []byte) error {
	return store.DeletePushTokenForDelivery(ctx, installationID, environment, "voip", token)
}

// ConsumePairingToken atomically consumes a pairing token, allocates a SIM ID, and stores a device credential hash.
func (store *Postgres) ConsumePairingToken(ctx context.Context, tokenHash []byte, now time.Time, pairing DevicePairing) (DeviceRecord, error) {
	var record DeviceRecord
	err := store.database(ctx).Transaction(func(tx *gorm.DB) error {
		var token pairingTokenModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("token_hash = ? AND owner_id IS NOT NULL AND (expires_at > ? OR consumed_at IS NOT NULL)", tokenHash, now.UTC()).First(&token).Error; err != nil {
			return domain.ErrInvalidPairingToken
		}
		if token.ConsumedAt != nil {
			if token.PairedDeviceID == nil || *token.PairedDeviceID != pairing.DeviceUUID {
				return domain.ErrInvalidPairingToken
			}
			if err := tx.Model(&deviceModel{}).Joins("JOIN device_credentials ON device_credentials.device_id = devices.id").Where("devices.id = ? AND device_credentials.credential_hash = ? AND devices.revoked_at IS NULL AND device_credentials.revoked_at IS NULL", pairing.DeviceUUID, pairing.CredentialHash).First(&record).Error; err != nil {
				return domain.ErrInvalidPairingToken
			}
			var slots []SlotRecord
			if err := listDeviceSlotsDB(tx, record.ID, &slots); err != nil {
				return err
			}
			record.Slots = slots
			return nil
		}
		numericID, err := allocateNumericID(tx)
		if err != nil {
			return err
		}
		model := deviceModel{ID: pairing.DeviceUUID, NumericID: numericID, ProfileCiphertext: append([]byte{}, pairing.ProfileCiphertext...), IdentityPublicKey: append([]byte(nil), pairing.IdentityPublicKey...), CommandPublicKey: append([]byte(nil), pairing.CommandPublicKey...), WrappedVaultKey: append([]byte(nil), pairing.WrappedVaultKey...), PairedAt: now.UTC()}
		if err := tx.Create(&model).Error; err != nil {
			return domain.ErrDeviceExists
		}
		if err := tx.Create(&deviceCredentialModel{DeviceID: pairing.DeviceUUID, CredentialHash: append([]byte(nil), pairing.CredentialHash...), CreatedAt: now.UTC()}).Error; err != nil {
			return fmt.Errorf("insert device credential: %w", err)
		}
		record = deviceRecordFromModel(model)
		if err := createInitialSIMSlotDB(tx, pairing.DeviceUUID, numericID, pairing.ProfileCiphertext, now, &record); err != nil {
			return err
		}
		if err := tx.Model(&token).Updates(map[string]any{"consumed_at": now.UTC(), "paired_device_id": pairing.DeviceUUID}).Error; err != nil {
			return fmt.Errorf("complete pairing token: %w", err)
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return DeviceRecord{}, fmt.Errorf("device pairing: %w", err)
	}
	return record, nil
}

// AuthenticateDevice validates a device credential and records liveness without exposing encrypted profile data.
func (store *Postgres) AuthenticateDevice(ctx context.Context, deviceID string, credential string, now time.Time) ([]byte, error) {
	credentialHash, err := auth.TokenHash(credential)
	if err != nil {
		return nil, err
	}
	var model deviceModel
	if err := store.database(ctx).Joins("JOIN device_credentials ON device_credentials.device_id = devices.id").Where("devices.id = ? AND device_credentials.credential_hash = ? AND devices.revoked_at IS NULL AND device_credentials.revoked_at IS NULL", deviceID, credentialHash[:]).First(&model).Error; err != nil {
		return nil, databaseNotFound(err)
	}
	if err := store.database(ctx).Model(&model).Updates(map[string]any{"last_seen_at": now.UTC()}).Error; err != nil {
		return nil, err
	}
	return append([]byte(nil), model.IdentityPublicKey...), nil
}

// CreateDevice atomically allocates the smallest free numeric identifier.
func (store *Postgres) CreateDevice(ctx context.Context, id string, profileCiphertext []byte, identityPublicKey []byte, wrappedVaultKey []byte) (DeviceRecord, error) {
	var record DeviceRecord
	err := store.database(ctx).Transaction(func(tx *gorm.DB) error {
		numericID, err := allocateNumericID(tx)
		if err != nil {
			return err
		}
		model := deviceModel{ID: id, NumericID: numericID, ProfileCiphertext: append([]byte{}, profileCiphertext...), IdentityPublicKey: append([]byte(nil), identityPublicKey...), WrappedVaultKey: append([]byte(nil), wrappedVaultKey...), PairedAt: time.Now().UTC()}
		if err := tx.Create(&model).Error; err != nil {
			return fmt.Errorf("insert device: %w", err)
		}
		record = deviceRecordFromModel(model)
		return createInitialSIMSlotDB(tx, id, numericID, profileCiphertext, model.PairedAt, &record)
	}, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return DeviceRecord{}, fmt.Errorf("create device: %w", err)
	}
	return record, nil
}

func allocateNumericID(tx *gorm.DB) (int, error) {
	var used []int
	if err := tx.Model(&simCardModel{}).Where("retired_at IS NULL").Pluck("numeric_id", &used).Error; err != nil {
		return 0, err
	}
	for candidate := domain.MinNumericID; candidate <= domain.MaxNumericID; candidate++ {
		found := false
		for _, value := range used {
			if value == candidate {
				found = true
				break
			}
		}
		if !found {
			return candidate, nil
		}
	}
	return 0, domain.ErrNoNumericID
}

func deviceRecordFromModel(model deviceModel) DeviceRecord {
	return DeviceRecord{ID: model.ID, NumericID: model.NumericID, ProfileCiphertext: append([]byte(nil), model.ProfileCiphertext...), IdentityPublicKey: append([]byte(nil), model.IdentityPublicKey...), CommandPublicKey: append([]byte(nil), model.CommandPublicKey...), WrappedVaultKey: append([]byte(nil), model.WrappedVaultKey...), PairedAt: model.PairedAt, LastSeenAt: model.LastSeenAt, RadioAccessTechnology: model.RadioAccessTechnology, SignalBars: model.SignalBars, RadioStatusUpdatedAt: model.RadioStatusUpdatedAt}
}

func createInitialSIMSlotDB(tx *gorm.DB, deviceID string, numericID int, profileCiphertext []byte, now time.Time, record *DeviceRecord) error {
	if profileCiphertext == nil {
		profileCiphertext = []byte{}
	}
	sim := simCardModel{ID: deviceID, NumericID: numericID, ProfileCiphertext: append([]byte{}, profileCiphertext...), FirstSeenAt: now.UTC(), LastSeenAt: now.UTC(), CreatedAt: now.UTC()}
	if err := tx.Create(&sim).Error; err != nil {
		return fmt.Errorf("insert initial SIM identity: %w", err)
	}
	slot := deviceSlotModel{ID: uuid.NewString(), DeviceID: deviceID, SlotKey: "1", SlotIndex: 1, CurrentSIMID: &sim.ID, CreatedAt: now.UTC(), LastSeenAt: ptrTime(now.UTC())}
	if err := tx.Create(&slot).Error; err != nil {
		return fmt.Errorf("insert initial device slot: %w", err)
	}
	if err := tx.Create(&slotSIMAssignmentModel{SlotID: slot.ID, SIMID: sim.ID, InsertedAt: now.UTC()}).Error; err != nil {
		return fmt.Errorf("record initial SIM slot assignment: %w", err)
	}
	value := numericID
	lastSeen := now.UTC()
	record.Slots = []SlotRecord{{ID: slot.ID, SlotKey: slot.SlotKey, SlotIndex: slot.SlotIndex, SIMID: &sim.ID, SIMNumericID: &value, ProfileCiphertext: append([]byte(nil), profileCiphertext...), LastSeenAt: &lastSeen}}
	return nil
}

func listDeviceSlotsDB(tx *gorm.DB, deviceID string, output *[]SlotRecord) error {
	var slots []deviceSlotModel
	if err := tx.Where("device_id = ? AND removed_at IS NULL", deviceID).Order("slot_index").Find(&slots).Error; err != nil {
		return fmt.Errorf("list device slots: %w", err)
	}
	result := make([]SlotRecord, 0, len(slots))
	for _, slot := range slots {
		record := SlotRecord{ID: slot.ID, SlotKey: slot.SlotKey, SlotIndex: slot.SlotIndex, SIMID: slot.CurrentSIMID, LastSeenAt: slot.LastSeenAt, RadioAccessTechnology: slot.RadioAccessTechnology, SignalBars: slot.SignalBars, RadioStatusUpdatedAt: slot.RadioStatusUpdatedAt}
		if slot.CurrentSIMID != nil {
			var sim simCardModel
			if err := tx.Where("id = ? AND retired_at IS NULL", *slot.CurrentSIMID).First(&sim).Error; err == nil {
				record.SIMNumericID = &sim.NumericID
				record.ProfileCiphertext = append([]byte(nil), sim.ProfileCiphertext...)
			}
		}
		result = append(result, record)
	}
	*output = result
	return nil
}

// UpdateNumericID assigns a user-selected compact identifier.
func (store *Postgres) UpdateNumericID(ctx context.Context, deviceID string, numericID int) error {
	if numericID < domain.MinNumericID || numericID > domain.MaxNumericID {
		return domain.ErrInvalidNumericID
	}
	err := store.database(ctx).Transaction(func(tx *gorm.DB) error {
		var slot deviceSlotModel
		if err := tx.Where("device_id = ? AND removed_at IS NULL AND current_sim_id IS NOT NULL", deviceID).Order("slot_index").First(&slot).Error; err != nil {
			return databaseNotFound(err)
		}
		result := tx.Model(&simCardModel{}).Where("id = ? AND retired_at IS NULL", *slot.CurrentSIMID).Updates(map[string]any{"numeric_id": numericID})
		if result.Error != nil {
			if isUniqueViolation(result.Error) {
				return domain.ErrDuplicateNumericID
			}
			return fmt.Errorf("update numeric id: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return pgx.ErrNoRows
		}
		result = tx.Model(&deviceModel{}).Where("id = ? AND revoked_at IS NULL", deviceID).Updates(map[string]any{"numeric_id": numericID})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return pgx.ErrNoRows
		}
		return nil
	})
	return err
}

// UpdateDeviceProfile replaces only the opaque end-to-end encrypted line profile.
func (store *Postgres) UpdateDeviceProfile(ctx context.Context, deviceID string, profileCiphertext []byte) error {
	err := store.database(ctx).Transaction(func(tx *gorm.DB) error {
		var device deviceModel
		if err := tx.Where("id = ? AND revoked_at IS NULL", deviceID).First(&device).Error; err != nil {
			return databaseNotFound(err)
		}
		var slot deviceSlotModel
		if err := tx.Where("device_id = ? AND removed_at IS NULL AND current_sim_id IS NOT NULL", deviceID).Order("slot_index").First(&slot).Error; err != nil {
			return databaseNotFound(err)
		}
		result := tx.Model(&simCardModel{}).Where("id = ? AND retired_at IS NULL", *slot.CurrentSIMID).Updates(map[string]any{"profile_ciphertext": profileCiphertext})
		if result.Error != nil {
			return fmt.Errorf("update device profile: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return pgx.ErrNoRows
		}
		return tx.Model(&device).Updates(map[string]any{"profile_ciphertext": profileCiphertext}).Error
	})
	return err
}

// UpdateSIMNumericID assigns a user-selected compact identifier to one retained SIM card.
func (store *Postgres) UpdateSIMNumericID(ctx context.Context, simID string, numericID int) error {
	if numericID < domain.MinNumericID || numericID > domain.MaxNumericID {
		return domain.ErrInvalidNumericID
	}
	err := store.database(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&simCardModel{}).Where("id = ? AND retired_at IS NULL", simID).Updates(map[string]any{"numeric_id": numericID})
		if result.Error != nil {
			if isUniqueViolation(result.Error) {
				return domain.ErrDuplicateNumericID
			}
			return fmt.Errorf("update SIM numeric id: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return pgx.ErrNoRows
		}
		var slots []deviceSlotModel
		if err := tx.Where("current_sim_id = ? AND removed_at IS NULL", simID).Find(&slots).Error; err != nil {
			return err
		}
		for _, slot := range slots {
			if err := tx.Model(&deviceModel{}).Where("id = ? AND revoked_at IS NULL", slot.DeviceID).Updates(map[string]any{"numeric_id": numericID}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	return err
}

// UpdateSIMProfile replaces the opaque end-to-end encrypted profile for one retained SIM card.
func (store *Postgres) UpdateSIMProfile(ctx context.Context, simID string, profileCiphertext []byte) error {
	err := store.database(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&simCardModel{}).Where("id = ? AND retired_at IS NULL", simID).Updates(map[string]any{"profile_ciphertext": profileCiphertext})
		if result.Error != nil {
			return fmt.Errorf("update SIM profile: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return pgx.ErrNoRows
		}
		var slots []deviceSlotModel
		if err := tx.Where("current_sim_id = ? AND removed_at IS NULL", simID).Find(&slots).Error; err != nil {
			return err
		}
		for _, slot := range slots {
			if err := tx.Model(&deviceModel{}).Where("id = ? AND revoked_at IS NULL", slot.DeviceID).Updates(map[string]any{"profile_ciphertext": profileCiphertext}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	return err
}

// ListESIMProfiles returns the profiles recorded for an active SIM.
func (store *Postgres) ListESIMProfiles(ctx context.Context, simID string) ([]ESIMProfileRecord, error) {
	var sim simCardModel
	if err := store.database(ctx).Where("id = ? AND retired_at IS NULL", simID).First(&sim).Error; err != nil {
		return nil, databaseNotFound(err)
	}
	var models []esimProfileModel
	if err := store.database(ctx).Where("sim_id = ?", simID).Order("updated_at DESC, profile_id").Find(&models).Error; err != nil {
		return nil, fmt.Errorf("list eSIM profiles: %w", err)
	}
	profiles := make([]ESIMProfileRecord, 0, len(models))
	for _, model := range models {
		profiles = append(profiles, esimProfileRecordFromModel(model))
	}
	return profiles, nil
}

// UpsertESIMProfile records metadata after a profile is installed on a modem.
func (store *Postgres) UpsertESIMProfile(ctx context.Context, profile ESIMProfileRecord) (ESIMProfileRecord, error) {
	if profile.SIMID == "" || profile.ProfileID == "" || len(profile.ProfileID) > 256 || len(profile.Name) > 128 || len(profile.ICCID) > 32 || len(profile.OperatorName) > 128 {
		return ESIMProfileRecord{}, domain.ErrInvalidESIMProfile
	}
	var sim simCardModel
	if err := store.database(ctx).Where("id = ? AND retired_at IS NULL", profile.SIMID).First(&sim).Error; err != nil {
		return ESIMProfileRecord{}, databaseNotFound(err)
	}
	model := esimProfileModel{SIMID: profile.SIMID, ProfileID: profile.ProfileID, Name: profile.Name, ICCID: profile.ICCID, OperatorName: profile.OperatorName, IsEnabled: profile.IsEnabled, CreatedAt: profile.CreatedAt, UpdatedAt: time.Now().UTC()}
	if model.CreatedAt.IsZero() {
		model.CreatedAt = model.UpdatedAt
	}
	if err := store.database(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "sim_id"}, {Name: "profile_id"}}, DoUpdates: clause.AssignmentColumns([]string{"name", "iccid", "operator_name", "updated_at"})}).Create(&model).Error; err != nil {
		return ESIMProfileRecord{}, fmt.Errorf("upsert eSIM profile: %w", err)
	}
	if err := store.database(ctx).Where("sim_id = ? AND profile_id = ?", profile.SIMID, profile.ProfileID).First(&model).Error; err != nil {
		return ESIMProfileRecord{}, databaseNotFound(err)
	}
	return esimProfileRecordFromModel(model), nil
}

func esimProfileRecordFromModel(model esimProfileModel) ESIMProfileRecord {
	return ESIMProfileRecord{SIMID: model.SIMID, ProfileID: model.ProfileID, Name: model.Name, ICCID: model.ICCID, OperatorName: model.OperatorName, IsEnabled: model.IsEnabled, CreatedAt: model.CreatedAt, UpdatedAt: model.UpdatedAt}
}

// RenameESIMProfile changes only the user-visible label of an installed profile.
func (store *Postgres) RenameESIMProfile(ctx context.Context, simID, profileID, name string) (ESIMProfileRecord, error) {
	name = strings.TrimSpace(name)
	if simID == "" || profileID == "" || len(profileID) > 256 || len(name) > 128 {
		return ESIMProfileRecord{}, domain.ErrInvalidESIMProfile
	}
	result := store.database(ctx).Model(&esimProfileModel{}).Where("sim_id = ? AND profile_id = ?", simID, profileID).Updates(map[string]any{"name": name, "updated_at": time.Now().UTC()})
	if result.Error != nil {
		return ESIMProfileRecord{}, fmt.Errorf("rename eSIM profile: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ESIMProfileRecord{}, pgx.ErrNoRows
	}
	var model esimProfileModel
	if err := store.database(ctx).Where("sim_id = ? AND profile_id = ?", simID, profileID).First(&model).Error; err != nil {
		return ESIMProfileRecord{}, databaseNotFound(err)
	}
	return esimProfileRecordFromModel(model), nil
}

// SetESIMProfileEnabled toggles one profile and disables other profiles on the same SIM.
func (store *Postgres) SetESIMProfileEnabled(ctx context.Context, simID, profileID string, enabled bool) (ESIMProfileRecord, error) {
	var model esimProfileModel
	err := store.database(ctx).Transaction(func(tx *gorm.DB) error {
		if enabled {
			if err := tx.Model(&esimProfileModel{}).Where("sim_id = ?", simID).Updates(map[string]any{"is_enabled": false, "updated_at": time.Now().UTC()}).Error; err != nil {
				return fmt.Errorf("disable other eSIM profiles: %w", err)
			}
		}
		result := tx.Model(&esimProfileModel{}).Where("sim_id = ? AND profile_id = ?", simID, profileID).Updates(map[string]any{"is_enabled": enabled, "updated_at": time.Now().UTC()})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return pgx.ErrNoRows
		}
		return tx.Where("sim_id = ? AND profile_id = ?", simID, profileID).First(&model).Error
	}, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return ESIMProfileRecord{}, fmt.Errorf("set eSIM profile state: %w", err)
	}
	return esimProfileRecordFromModel(model), nil
}

// DeleteESIMProfile removes server metadata after the modem has removed a profile.
func (store *Postgres) DeleteESIMProfile(ctx context.Context, simID, profileID string) error {
	result := store.database(ctx).Where("sim_id = ? AND profile_id = ?", simID, profileID).Delete(&esimProfileModel{})
	if result.Error != nil {
		return fmt.Errorf("delete eSIM profile: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// RevokeDevice atomically disables the device and all credentials issued to it.
func (store *Postgres) RevokeDevice(ctx context.Context, deviceID string, revokedAt time.Time) error {
	err := store.database(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&deviceModel{}).Where("id = ? AND revoked_at IS NULL", deviceID).Updates(map[string]any{"revoked_at": revokedAt.UTC()})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return pgx.ErrNoRows
		}
		if err := tx.Model(&deviceCredentialModel{}).Where("device_id = ? AND revoked_at IS NULL", deviceID).Updates(map[string]any{"revoked_at": revokedAt.UTC()}).Error; err != nil {
			return err
		}
		var slots []deviceSlotModel
		if err := tx.Where("device_id = ? AND removed_at IS NULL", deviceID).Find(&slots).Error; err != nil {
			return err
		}
		for _, slot := range slots {
			if err := tx.Model(&slotSIMAssignmentModel{}).Where("slot_id = ? AND removed_at IS NULL", slot.ID).Updates(map[string]any{"removed_at": revokedAt.UTC()}).Error; err != nil {
				return err
			}
		}
		return tx.Where("device_id = ?", deviceID).Delete(&deviceSlotModel{}).Error
	})
	return err
}

// ListSIMCards returns active physical SIMs, including cards currently offline.
func (store *Postgres) ListSIMCards(ctx context.Context) ([]SIMRecord, error) {
	var models []simCardModel
	if err := store.database(ctx).Where("retired_at IS NULL").Order("numeric_id").Find(&models).Error; err != nil {
		return nil, fmt.Errorf("list SIM cards: %w", err)
	}
	result := make([]SIMRecord, 0, len(models))
	for _, model := range models {
		record := SIMRecord{ID: model.ID, NumericID: model.NumericID, ProfileCiphertext: append([]byte(nil), model.ProfileCiphertext...), FirstSeenAt: model.FirstSeenAt, LastSeenAt: model.LastSeenAt}
		var slot deviceSlotModel
		if err := store.database(ctx).Where("current_sim_id = ? AND removed_at IS NULL", model.ID).First(&slot).Error; err == nil {
			record.AssignedDeviceID = &slot.DeviceID
			record.AssignedSlotID = &slot.ID
		}
		result = append(result, record)
	}
	return result, nil
}

// RetireSIMCard permanently removes an offline SIM from the owner's inventory.
func (store *Postgres) RetireSIMCard(ctx context.Context, simID string, retiredAt time.Time) error {
	var removed int64
	err := store.database(ctx).Transaction(func(tx *gorm.DB) error {
		var sim simCardModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND retired_at IS NULL", simID).First(&sim).Error; err != nil {
			return databaseNotFound(err)
		}
		var slotCount int64
		if err := tx.Model(&deviceSlotModel{}).Where("current_sim_id = ? AND removed_at IS NULL", simID).Count(&slotCount).Error; err != nil {
			return err
		}
		if slotCount > 0 {
			return domain.ErrSIMAssigned
		}
		result := tx.Delete(&sim)
		removed = result.RowsAffected
		return result.Error
	}, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("retire SIM card: %w", err)
	}
	if removed == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// ListDevices returns active devices without decrypting their line profiles.
func (store *Postgres) ListDevices(ctx context.Context) ([]DeviceRecord, error) {
	var models []deviceModel
	if err := store.database(ctx).Where("revoked_at IS NULL").Order("numeric_id").Find(&models).Error; err != nil {
		return nil, fmt.Errorf("list devices: %w", err)
	}
	records := make([]DeviceRecord, 0, len(models))
	for _, model := range models {
		record := deviceRecordFromModel(model)
		var slots []SlotRecord
		if err := listDeviceSlotsDB(store.database(ctx), model.ID, &slots); err != nil {
			return nil, err
		}
		record.Slots = slots
		records = append(records, record)
	}
	return records, nil
}

func createSIMSlotDB(tx *gorm.DB, deviceID string, slotKey string, slotIndex int, simID string, numericID int, profileCiphertext []byte, now time.Time, record *DeviceRecord) error {
	if profileCiphertext == nil {
		profileCiphertext = []byte{}
	}
	sim := simCardModel{ID: simID, NumericID: numericID, ProfileCiphertext: append([]byte{}, profileCiphertext...), FirstSeenAt: now.UTC(), LastSeenAt: now.UTC(), CreatedAt: now.UTC()}
	if err := tx.Create(&sim).Error; err != nil {
		return fmt.Errorf("insert initial SIM identity: %w", err)
	}
	slot := deviceSlotModel{ID: uuid.NewString(), DeviceID: deviceID, SlotKey: slotKey, SlotIndex: slotIndex, CurrentSIMID: &sim.ID, CreatedAt: now.UTC(), LastSeenAt: ptrTime(now.UTC())}
	if err := tx.Create(&slot).Error; err != nil {
		return fmt.Errorf("insert initial device slot: %w", err)
	}
	if err := tx.Create(&slotSIMAssignmentModel{SlotID: slot.ID, SIMID: sim.ID, InsertedAt: now.UTC()}).Error; err != nil {
		return fmt.Errorf("record initial SIM slot assignment: %w", err)
	}
	value := numericID
	lastSeen := now.UTC()
	record.Slots = []SlotRecord{{ID: slot.ID, SlotKey: slot.SlotKey, SlotIndex: slot.SlotIndex, SIMID: &sim.ID, SIMNumericID: &value, ProfileCiphertext: append([]byte(nil), profileCiphertext...), LastSeenAt: &lastSeen}}
	return nil
}

// UpdateDeviceRadioStatus stores the latest non-sensitive modem telemetry.
func (store *Postgres) UpdateDeviceRadioStatus(ctx context.Context, deviceID string, slotKey string, slotIndex int, technology string, signalBars *int, now time.Time) error {
	err := store.database(ctx).Transaction(func(tx *gorm.DB) error {
		var device deviceModel
		if err := tx.Where("id = ? AND revoked_at IS NULL", deviceID).First(&device).Error; err != nil {
			return databaseNotFound(err)
		}
		result := tx.Model(&deviceSlotModel{}).Where("device_id = ? AND slot_key = ? AND slot_index = ? AND removed_at IS NULL", deviceID, slotKey, slotIndex).Updates(map[string]any{"radio_access_technology": technology, "signal_bars": signalBars, "radio_status_updated_at": now.UTC(), "last_seen_at": now.UTC()})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return pgx.ErrNoRows
		}
		return tx.Model(&device).Updates(map[string]any{"radio_access_technology": technology, "signal_bars": signalBars, "radio_status_updated_at": now.UTC(), "last_seen_at": now.UTC()}).Error
	})
	if err != nil {
		return fmt.Errorf("update device radio status: %w", err)
	}
	return nil
}

// UpdateDeviceSIMIdentity assigns the current card fingerprint to its physical modem slot.
func (store *Postgres) UpdateDeviceSIMIdentity(ctx context.Context, deviceID string, slotKey string, slotIndex int, fingerprint []byte, now time.Time) (SIMSlotAssignment, error) {
	if len(fingerprint) != 32 || slotKey == "" || slotIndex < 1 || slotIndex > 32 {
		return SIMSlotAssignment{}, errors.New("invalid slot or SIM fingerprint")
	}
	var output SIMSlotAssignment
	err := store.database(ctx).Transaction(func(tx *gorm.DB) error {
		var slot deviceSlotModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("device_id = ? AND slot_key = ? AND slot_index = ? AND removed_at IS NULL", deviceID, slotKey, slotIndex).First(&slot).Error; err != nil {
			return databaseNotFound(err)
		}
		oldSIMID := slot.CurrentSIMID
		var sim simCardModel
		found := false
		findErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("iccid_fingerprint = ?", fingerprint).Order("created_at").First(&sim).Error
		if findErr == nil {
			found = true
		} else if !errors.Is(findErr, gorm.ErrRecordNotFound) {
			return fmt.Errorf("find SIM card identity: %w", findErr)
		}
		if !found && oldSIMID != nil {
			if err := tx.Where("id = ? AND iccid_fingerprint IS NULL AND fingerprint_scope IS NULL AND retired_at IS NULL", *oldSIMID).First(&sim).Error; err == nil {
				found = true
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("identify current SIM card: %w", err)
			}
		}
		if !found {
			numericID, err := allocateNumericID(tx)
			if err != nil {
				return err
			}
			sim = simCardModel{ID: uuid.NewString(), NumericID: numericID, ICCIDFingerprint: append([]byte(nil), fingerprint...), FingerprintScope: &deviceID, ProfileCiphertext: []byte{}, FirstSeenAt: now.UTC(), LastSeenAt: now.UTC(), CreatedAt: now.UTC()}
			if err := tx.Create(&sim).Error; err != nil {
				return fmt.Errorf("create SIM card identity: %w", err)
			}
		} else if err := tx.Model(&sim).Updates(map[string]any{"iccid_fingerprint": fingerprint, "fingerprint_scope": deviceID, "retired_at": nil, "last_seen_at": now.UTC()}).Error; err != nil {
			return fmt.Errorf("reactivate SIM card identity: %w", err)
		}
		closeAssignments := func(simID, excludedSlotID string) error {
			var assignments []slotSIMAssignmentModel
			if err := tx.Where("sim_id = ? AND slot_id <> ? AND removed_at IS NULL", simID, excludedSlotID).Find(&assignments).Error; err != nil {
				return err
			}
			for _, assignment := range assignments {
				removed := now.UTC()
				if assignment.InsertedAt.After(removed) {
					removed = assignment.InsertedAt
				}
				if err := tx.Model(&assignment).Updates(map[string]any{"removed_at": removed}).Error; err != nil {
					return err
				}
			}
			return tx.Model(&deviceSlotModel{}).Where("current_sim_id = ? AND id <> ?", simID, excludedSlotID).Updates(map[string]any{"current_sim_id": nil}).Error
		}
		if oldSIMID != nil && *oldSIMID != sim.ID {
			var assignments []slotSIMAssignmentModel
			if err := tx.Where("slot_id = ? AND removed_at IS NULL", slot.ID).Find(&assignments).Error; err != nil {
				return err
			}
			for _, assignment := range assignments {
				removed := now.UTC()
				if assignment.InsertedAt.After(removed) {
					removed = assignment.InsertedAt
				}
				if err := tx.Model(&assignment).Updates(map[string]any{"removed_at": removed}).Error; err != nil {
					return err
				}
			}
		}
		if err := closeAssignments(sim.ID, slot.ID); err != nil {
			return err
		}
		if err := tx.Model(&slot).Updates(map[string]any{"current_sim_id": sim.ID, "last_seen_at": now.UTC()}).Error; err != nil {
			return err
		}
		if result := tx.Model(&deviceModel{}).Where("id = ? AND revoked_at IS NULL", deviceID).Updates(map[string]any{"numeric_id": sim.NumericID, "last_seen_at": now.UTC()}); result.Error != nil {
			return fmt.Errorf("update device SIM identity: %w", result.Error)
		} else if result.RowsAffected == 0 {
			return pgx.ErrNoRows
		}
		var current slotSIMAssignmentModel
		if err := tx.Where("slot_id = ? AND sim_id = ? AND removed_at IS NULL", slot.ID, sim.ID).First(&current).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			if err := tx.Create(&slotSIMAssignmentModel{SlotID: slot.ID, SIMID: sim.ID, InsertedAt: now.UTC()}).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		output = SIMSlotAssignment{SlotID: slot.ID, SIMID: sim.ID, NumericID: sim.NumericID}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return SIMSlotAssignment{}, fmt.Errorf("synchronize SIM identity: %w", err)
	}
	return output, nil
}

// InsertEvent stores an encrypted event once and returns its server sequence.
func (store *Postgres) InsertEvent(ctx context.Context, envelope domain.EventEnvelope, expiresAt time.Time) (int64, bool, error) {
	var sequence int64
	created := false
	err := store.database(ctx).Transaction(func(tx *gorm.DB) error {
		var recipient *string
		if envelope.RecipientInstallationID != "" {
			recipient = &envelope.RecipientInstallationID
		}
		model := eventModel{ID: envelope.ID, Type: envelope.Type, DeviceID: envelope.DeviceUUID, RecipientInstallationID: recipient, OccurredAt: envelope.OccurredAt, KeyEpoch: envelope.KeyEpoch, Nonce: append([]byte(nil), envelope.Nonce...), Ciphertext: append([]byte(nil), envelope.Ciphertext...), Signature: append([]byte(nil), envelope.Signature...), ExpiresAt: expiresAt, CreatedAt: time.Now().UTC()}
		result := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoNothing: true}).Create(&model)
		if result.Error != nil {
			return fmt.Errorf("insert encrypted event: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			var existing eventModel
			if err := tx.Where("id = ?", envelope.ID).First(&existing).Error; err != nil {
				return fmt.Errorf("read existing event: %w", err)
			}
			if existing.Type != envelope.Type || existing.DeviceID != envelope.DeviceUUID || existing.RecipientInstallationID == nil && recipient != nil || existing.RecipientInstallationID != nil && recipient == nil || existing.RecipientInstallationID != nil && recipient != nil && *existing.RecipientInstallationID != *recipient || !existing.OccurredAt.Equal(envelope.OccurredAt) || existing.KeyEpoch != envelope.KeyEpoch || !bytes.Equal(existing.Nonce, envelope.Nonce) || !bytes.Equal(existing.Ciphertext, envelope.Ciphertext) || !bytes.Equal(existing.Signature, envelope.Signature) || !existing.ExpiresAt.Equal(expiresAt) {
				return domain.ErrIdempotencyConflict
			}
			sequence = existing.Sequence
			return nil
		}
		sequence = model.Sequence
		created = true
		if envelope.Type == "message.received" && envelope.RecipientInstallationID != "" {
			payload, err := json.Marshal(envelope)
			if err != nil {
				return fmt.Errorf("encode SMS push envelope: %w", err)
			}
			var installation appInstallationModel
			if err := tx.Where("id = ? AND approved_at IS NOT NULL AND revoked_at IS NULL", envelope.RecipientInstallationID).First(&installation).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return errors.New("SMS event recipient installation is not approved")
				}
				return err
			}
			if err := tx.Create(&outboxJobModel{Kind: "alert.incoming_message", RoutingKey: envelope.RecipientInstallationID, Payload: payload, Attempts: 0, AvailableAt: time.Now().UTC(), ExpiresAt: &expiresAt}).Error; err != nil {
				return fmt.Errorf("enqueue SMS alert push: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return 0, false, fmt.Errorf("insert encrypted event: %w", err)
	}
	return sequence, created, nil
}

// ListEvents returns unexpired opaque events strictly after the supplied cursor.
func (store *Postgres) ListEvents(ctx context.Context, afterSequence int64, limit int, now time.Time) ([]domain.EventEnvelope, error) {
	var models []eventModel
	if err := store.database(ctx).Where("sequence > ? AND expires_at > ?", afterSequence, now.UTC()).Order("sequence ASC").Limit(limit).Find(&models).Error; err != nil {
		return nil, fmt.Errorf("list encrypted events: %w", err)
	}
	events := make([]domain.EventEnvelope, 0, len(models))
	for _, model := range models {
		envelope := domain.EventEnvelope{ID: model.ID, Sequence: model.Sequence, Type: model.Type, DeviceUUID: model.DeviceID, OccurredAt: model.OccurredAt, KeyEpoch: model.KeyEpoch, Nonce: append([]byte(nil), model.Nonce...), Ciphertext: append([]byte(nil), model.Ciphertext...), Signature: append([]byte(nil), model.Signature...)}
		if model.RecipientInstallationID != nil {
			envelope.RecipientInstallationID = *model.RecipientInstallationID
		}
		events = append(events, envelope)
	}
	return events, nil
}

// GetHistorySnapshot reads an encrypted archive for its owner and kind.
func (store *Postgres) GetHistorySnapshot(ctx context.Context, ownerID string, kind int) (domain.HistorySnapshot, error) {
	var model encryptedHistorySnapshotModel
	if err := store.database(ctx).Where("owner_id = ? AND kind = ?", ownerID, kind).First(&model).Error; err != nil {
		return domain.HistorySnapshot{}, databaseNotFound(err)
	}
	return domain.HistorySnapshot{OwnerID: model.OwnerID, Kind: model.Kind, Revision: model.Revision, Ciphertext: append([]byte(nil), model.Ciphertext...), UpdatedAt: model.UpdatedAt}, nil
}

// PutHistorySnapshot replaces an archive only when its current revision matches expectedRevision.
func (store *Postgres) PutHistorySnapshot(ctx context.Context, ownerID string, kind int, expectedRevision int64, ciphertext []byte, now time.Time) (domain.HistorySnapshot, error) {
	var model encryptedHistorySnapshotModel
	err := store.database(ctx).Transaction(func(tx *gorm.DB) error {
		var current encryptedHistorySnapshotModel
		result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("owner_id = ? AND kind = ?", ownerID, kind).First(&current)
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			if expectedRevision != 0 {
				return domain.ErrHistoryRevisionConflict
			}
			model = encryptedHistorySnapshotModel{OwnerID: ownerID, Kind: kind, Revision: 1, Ciphertext: append([]byte(nil), ciphertext...), UpdatedAt: now.UTC()}
			return tx.Create(&model).Error
		}
		if result.Error != nil {
			return result.Error
		}
		if current.Revision != expectedRevision {
			return domain.ErrHistoryRevisionConflict
		}
		current.Revision++
		current.Ciphertext = append([]byte(nil), ciphertext...)
		current.UpdatedAt = now.UTC()
		if err := tx.Save(&current).Error; err != nil {
			return err
		}
		model = current
		return nil
	})
	if err != nil {
		if errors.Is(err, domain.ErrHistoryRevisionConflict) {
			return domain.HistorySnapshot{}, err
		}
		return domain.HistorySnapshot{}, fmt.Errorf("write encrypted history snapshot: %w", err)
	}
	return domain.HistorySnapshot{OwnerID: model.OwnerID, Kind: model.Kind, Revision: model.Revision, Ciphertext: append([]byte(nil), model.Ciphertext...), UpdatedAt: model.UpdatedAt}, nil
}

// EnqueueCommand stores one opaque command while preserving client idempotency.
func (store *Postgres) EnqueueCommand(ctx context.Context, encrypted domain.EncryptedCommand) (bool, error) {
	var device deviceModel
	if err := store.database(ctx).Where("id = ? AND revoked_at IS NULL", encrypted.DeviceUUID).First(&device).Error; err != nil {
		return false, databaseNotFound(err)
	}
	var installation appInstallationModel
	if err := store.database(ctx).Where("id = ? AND approved_at IS NOT NULL AND revoked_at IS NULL", encrypted.SenderInstallationID).First(&installation).Error; err != nil {
		return false, databaseNotFound(err)
	}
	model := encryptedDeviceCommandModel{ID: encrypted.ID, DeviceID: encrypted.DeviceUUID, SenderInstallationID: encrypted.SenderInstallationID, SenderPublicKey: append([]byte(nil), encrypted.SenderPublicKey...), Nonce: append([]byte(nil), encrypted.Nonce...), Ciphertext: append([]byte(nil), encrypted.Ciphertext...), Signature: append([]byte(nil), encrypted.Signature...), CreatedAt: encrypted.CreatedAt.UTC(), ExpiresAt: encrypted.ExpiresAt.UTC()}
	result := store.database(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoNothing: true}).Create(&model)
	if result.Error != nil {
		return false, fmt.Errorf("enqueue encrypted command: %w", result.Error)
	}
	if result.RowsAffected == 1 {
		return true, nil
	}
	var existing encryptedDeviceCommandModel
	if err := store.database(ctx).Where("id = ?", encrypted.ID).First(&existing).Error; err != nil {
		return false, databaseNotFound(err)
	}
	if existing.DeviceID != encrypted.DeviceUUID || existing.SenderInstallationID != encrypted.SenderInstallationID || !bytes.Equal(existing.SenderPublicKey, encrypted.SenderPublicKey) || !bytes.Equal(existing.Nonce, encrypted.Nonce) || !bytes.Equal(existing.Ciphertext, encrypted.Ciphertext) || !bytes.Equal(existing.Signature, encrypted.Signature) || !existing.CreatedAt.Equal(encrypted.CreatedAt.UTC()) || !existing.ExpiresAt.Equal(encrypted.ExpiresAt.UTC()) {
		return false, domain.ErrIdempotencyConflict
	}
	return false, nil
}

// ClaimCommand leases the oldest deliverable command to one authenticated device.
func (store *Postgres) ClaimCommand(ctx context.Context, deviceID string, now time.Time, leaseUntil time.Time) (domain.EncryptedCommand, error) {
	var model encryptedDeviceCommandModel
	err := store.database(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).Where("device_id = ? AND completed_at IS NULL AND expires_at > ? AND (leased_until IS NULL OR leased_until <= ?)", deviceID, now.UTC(), now.UTC()).Order("created_at, id").First(&model)
		if err := result.Error; err != nil {
			return databaseNotFound(err)
		}
		model.LeasedUntil = &leaseUntil
		model.DeliveryAttempts++
		return tx.Model(&model).Updates(map[string]any{"leased_until": leaseUntil.UTC(), "delivery_attempts": model.DeliveryAttempts}).Error
	})
	if err != nil {
		return domain.EncryptedCommand{}, err
	}
	return domain.EncryptedCommand{ID: model.ID, DeviceUUID: model.DeviceID, SenderInstallationID: model.SenderInstallationID, SenderPublicKey: append([]byte(nil), model.SenderPublicKey...), Nonce: append([]byte(nil), model.Nonce...), Ciphertext: append([]byte(nil), model.Ciphertext...), Signature: append([]byte(nil), model.Signature...), CreatedAt: model.CreatedAt, ExpiresAt: model.ExpiresAt}, nil
}

// CompleteCommand stores only the encrypted device response and closes the lease.
func (store *Postgres) CompleteCommand(ctx context.Context, deviceID string, commandID string, result domain.EncryptedCommandResult, completedAt time.Time) error {
	updated := store.database(ctx).Model(&encryptedDeviceCommandModel{}).Where("id = ? AND device_id = ? AND completed_at IS NULL AND expires_at > ?", commandID, deviceID, completedAt.UTC()).Updates(map[string]any{"result_nonce": result.Nonce, "result_ciphertext": result.Ciphertext, "completed_at": completedAt.UTC(), "leased_until": nil})
	if updated.Error != nil {
		return fmt.Errorf("complete encrypted command: %w", updated.Error)
	}
	if updated.RowsAffected == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// CommandStatus returns routing state and an opaque result to the authenticated owner.
func (store *Postgres) CommandStatus(ctx context.Context, deviceID string, commandID string, now time.Time) (domain.CommandStatus, error) {
	var model encryptedDeviceCommandModel
	if err := store.database(ctx).Where("id = ? AND device_id = ? AND expires_at > ?", commandID, deviceID, now.UTC()).First(&model).Error; err != nil {
		return domain.CommandStatus{}, databaseNotFound(err)
	}
	status := domain.CommandStatus{ID: model.ID, DeviceUUID: model.DeviceID, CompletedAt: model.CompletedAt}
	status.State = "pending"
	if model.LeasedUntil != nil && model.LeasedUntil.After(now) {
		status.State = "processing"
	}
	if status.CompletedAt != nil {
		status.State = "completed"
		status.Result = &domain.EncryptedCommandResult{
			Nonce:      append([]byte(nil), model.ResultNonce...),
			Ciphertext: append([]byte(nil), model.ResultCiphertext...),
		}
	}
	return status, nil
}

// UpdateDeviceCommandPublicKey registers the X25519 key used for opaque commands.
func (store *Postgres) UpdateDeviceCommandPublicKey(ctx context.Context, deviceID string, publicKey []byte) error {
	result := store.database(ctx).Model(&deviceModel{}).Where("id = ? AND revoked_at IS NULL", deviceID).Updates(map[string]any{"command_public_key": publicKey})
	if result.Error != nil {
		return fmt.Errorf("update device command public key: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// Schema returns the embedded schema for validation and packaging tests.
func Schema() ([]byte, error) {
	return schemaFS.ReadFile("schema.sql")
}
