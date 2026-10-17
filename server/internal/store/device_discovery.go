package store

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"sim-deck-server/internal/domain"
)

// DeviceDiscoveryStatus is the server-readable lifecycle state for a candidate modem.
type DeviceDiscoveryStatus string

const (
	maxPendingDeviceDiscoveries   = 32
	deviceDiscoveryResultLifetime = 24 * time.Hour
)

const (
	DeviceDiscoveryPending  DeviceDiscoveryStatus = "pending"
	DeviceDiscoveryApproved DeviceDiscoveryStatus = "approved"
	DeviceDiscoveryRejected DeviceDiscoveryStatus = "rejected"
)

// DeviceDiscoverySession is the owner's time-limited device admission window.
type DeviceDiscoverySession struct {
	OwnerID   string     `json:"-"`
	OpenedAt  time.Time  `json:"opened_at"`
	ExpiresAt time.Time  `json:"expires_at"`
	ClosedAt  *time.Time `json:"closed_at,omitempty"`
}

// OpenDeviceDiscovery opens or replaces the owner's time-limited admission window.
func (store *Postgres) OpenDeviceDiscovery(ctx context.Context, ownerID string, now time.Time, expiresAt time.Time) (DeviceDiscoverySession, error) {
	if !expiresAt.After(now) {
		return DeviceDiscoverySession{}, errors.New("device discovery expiration must be after its opening time")
	}
	var session DeviceDiscoverySession
	err := store.pool.QueryRow(ctx, `
		INSERT INTO device_discovery_sessions (owner_id, opened_at, expires_at, closed_at)
		VALUES ($1, $2, $3, NULL)
		ON CONFLICT (owner_id) DO UPDATE
		SET opened_at = EXCLUDED.opened_at,
		    expires_at = EXCLUDED.expires_at,
		    closed_at = NULL
		RETURNING owner_id::text, opened_at, expires_at, closed_at`, ownerID, now.UTC(), expiresAt.UTC()).Scan(
		&session.OwnerID, &session.OpenedAt, &session.ExpiresAt, &session.ClosedAt,
	)
	if err != nil {
		return DeviceDiscoverySession{}, fmt.Errorf("open device discovery: %w", err)
	}
	return session, nil
}

// CloseDeviceDiscovery stops accepting new candidates without deleting pending results.
func (store *Postgres) CloseDeviceDiscovery(ctx context.Context, ownerID string, now time.Time) error {
	_, err := store.pool.Exec(ctx, `
		UPDATE device_discovery_sessions
		SET closed_at = $2
		WHERE owner_id = $1 AND closed_at IS NULL`, ownerID, now.UTC())
	if err != nil {
		return fmt.Errorf("close device discovery: %w", err)
	}
	return nil
}

// GetDeviceDiscoverySession returns the latest admission window for the owner.
func (store *Postgres) GetDeviceDiscoverySession(ctx context.Context, ownerID string) (DeviceDiscoverySession, error) {
	var session DeviceDiscoverySession
	err := store.pool.QueryRow(ctx, `
		SELECT owner_id::text, opened_at, expires_at, closed_at
		FROM device_discovery_sessions
		WHERE owner_id = $1`, ownerID).Scan(&session.OwnerID, &session.OpenedAt, &session.ExpiresAt, &session.ClosedAt)
	if err != nil {
		return DeviceDiscoverySession{}, err
	}
	return session, nil
}

// SubmitDeviceDiscovery records a candidate only while the owner's discovery window is active.
func (store *Postgres) SubmitDeviceDiscovery(ctx context.Context, now time.Time, request DeviceDiscoveryRequest) (DeviceDiscoveryRecord, error) {
	if request.SlotKey == "" {
		request.SlotKey = "1"
	}
	if request.SlotIndex < 1 {
		request.SlotIndex = 1
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return DeviceDiscoveryRecord{}, fmt.Errorf("begin device discovery submission: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var ownerID string
	var expiresAt time.Time
	err = tx.QueryRow(ctx, `
		SELECT owner_id::text, expires_at
		FROM device_discovery_sessions
		WHERE closed_at IS NULL AND expires_at > $1
		ORDER BY opened_at DESC
		LIMIT 1
		FOR SHARE`, now.UTC()).Scan(&ownerID, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return DeviceDiscoveryRecord{}, domain.ErrDeviceDiscoveryClosed
	}
	if err != nil {
		return DeviceDiscoveryRecord{}, fmt.Errorf("read active device discovery: %w", err)
	}

	var deviceExists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM devices WHERE id = $1 AND revoked_at IS NULL
	)`, request.DeviceUUID).Scan(&deviceExists); err != nil {
		return DeviceDiscoveryRecord{}, fmt.Errorf("check existing device discovery: %w", err)
	}
	if deviceExists {
		return DeviceDiscoveryRecord{}, domain.ErrDeviceExists
	}

	var existing DeviceDiscoveryRecord
	var existingOwnerID string
	var existingCredentialHash []byte
	err = tx.QueryRow(ctx, `
		SELECT owner_id::text, device_id::text, identity_public_key, command_public_key,
		       credential_hash, status, first_seen_at, last_seen_at, expires_at, decided_at,
		       slot_key, slot_index, sim_fingerprint
		FROM device_discoveries
		WHERE device_id = $1
		FOR UPDATE`, request.DeviceUUID).Scan(
		&existingOwnerID, &existing.DeviceUUID, &existing.IdentityPublicKey, &existing.CommandPublicKey,
		&existingCredentialHash, &existing.Status, &existing.FirstSeenAt, &existing.LastSeenAt,
		&existing.ExpiresAt, &existing.DecidedAt, &existing.SlotKey, &existing.SlotIndex, &existing.SIMFingerprint,
	)
	if err == nil {
		matches := existingOwnerID == ownerID &&
			existing.SlotKey == request.SlotKey && existing.SlotIndex == request.SlotIndex &&
			bytes.Equal(existing.IdentityPublicKey, request.IdentityPublicKey) &&
			bytes.Equal(existing.CommandPublicKey, request.CommandPublicKey) &&
			bytes.Equal(existing.SIMFingerprint, request.SIMFingerprint) &&
			bytes.Equal(existingCredentialHash, request.CredentialHash)
		if existing.ExpiresAt.After(now) || existing.Status == DeviceDiscoveryApproved {
			if !matches {
				return DeviceDiscoveryRecord{}, domain.ErrDeviceDiscoveryConflict
			}
			if existing.Status == DeviceDiscoveryPending {
				existing.LastSeenAt = now.UTC()
				if _, err := tx.Exec(ctx, `
					UPDATE device_discoveries SET last_seen_at = $2 WHERE device_id = $1`, request.DeviceUUID, existing.LastSeenAt); err != nil {
					return DeviceDiscoveryRecord{}, fmt.Errorf("refresh device discovery: %w", err)
				}
			}
			if err := tx.Commit(ctx); err != nil {
				return DeviceDiscoveryRecord{}, fmt.Errorf("commit device discovery retry: %w", err)
			}
			return existing, nil
		}
		if _, err := tx.Exec(ctx, `DELETE FROM device_discoveries WHERE device_id = $1`, request.DeviceUUID); err != nil {
			return DeviceDiscoveryRecord{}, fmt.Errorf("replace expired device discovery: %w", err)
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return DeviceDiscoveryRecord{}, fmt.Errorf("read device discovery retry: %w", err)
	}

	var pendingCount int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM device_discoveries
		WHERE owner_id = $1 AND status = 'pending' AND expires_at > $2`, ownerID, now.UTC()).Scan(&pendingCount); err != nil {
		return DeviceDiscoveryRecord{}, fmt.Errorf("count pending device discoveries: %w", err)
	}
	if pendingCount >= maxPendingDeviceDiscoveries {
		return DeviceDiscoveryRecord{}, domain.ErrDeviceDiscoveryLimit
	}

	record := DeviceDiscoveryRecord{
		DeviceUUID: request.DeviceUUID, IdentityPublicKey: request.IdentityPublicKey,
		CommandPublicKey: request.CommandPublicKey, Status: DeviceDiscoveryPending,
		FirstSeenAt: now.UTC(), LastSeenAt: now.UTC(), ExpiresAt: expiresAt,
		SlotKey: request.SlotKey, SlotIndex: request.SlotIndex, SIMFingerprint: request.SIMFingerprint,
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO device_discoveries (
			device_id, owner_id, identity_public_key, command_public_key, credential_hash,
			status, first_seen_at, last_seen_at, expires_at, slot_key, slot_index, sim_fingerprint
		) VALUES ($1, $2, $3, $4, $5, 'pending', $6, $6, $7, $8, $9, NULLIF($10, '')::text)`,
		request.DeviceUUID, ownerID, request.IdentityPublicKey, request.CommandPublicKey,
		request.CredentialHash, now.UTC(), expiresAt, request.SlotKey, request.SlotIndex, hex.EncodeToString(request.SIMFingerprint))
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) && postgresError.Code == "23505" {
			return DeviceDiscoveryRecord{}, domain.ErrDeviceDiscoveryConflict
		}
		return DeviceDiscoveryRecord{}, fmt.Errorf("insert device discovery: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return DeviceDiscoveryRecord{}, fmt.Errorf("commit device discovery submission: %w", err)
	}
	return record, nil
}

// ListPendingDeviceDiscoveries returns active candidates in first-seen order.
func (store *Postgres) ListPendingDeviceDiscoveries(ctx context.Context, ownerID string, now time.Time) ([]DeviceDiscoveryRecord, error) {
	rows, err := store.pool.Query(ctx, `
		SELECT device_id::text, identity_public_key, command_public_key, status,
		       first_seen_at, last_seen_at, expires_at, decided_at, slot_key, slot_index
		FROM device_discoveries
		WHERE owner_id = $1 AND status = 'pending' AND expires_at > $2
		ORDER BY first_seen_at, device_id`, ownerID, now.UTC())
	if err != nil {
		return nil, fmt.Errorf("list pending device discoveries: %w", err)
	}
	defer rows.Close()
	records := make([]DeviceDiscoveryRecord, 0)
	for rows.Next() {
		var record DeviceDiscoveryRecord
		if err := rows.Scan(
			&record.DeviceUUID, &record.IdentityPublicKey, &record.CommandPublicKey, &record.Status,
			&record.FirstSeenAt, &record.LastSeenAt, &record.ExpiresAt, &record.DecidedAt,
			&record.SlotKey, &record.SlotIndex,
		); err != nil {
			return nil, fmt.Errorf("scan pending device discovery: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending device discoveries: %w", err)
	}
	return records, nil
}

// ApproveDeviceDiscovery atomically allocates a SIM ID and activates the candidate credential.
func (store *Postgres) ApproveDeviceDiscovery(ctx context.Context, ownerID string, deviceID string, now time.Time) (DeviceRecord, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return DeviceRecord{}, fmt.Errorf("begin device discovery approval: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var identityPublicKey []byte
	var commandPublicKey []byte
	var credentialHash []byte
	var status DeviceDiscoveryStatus
	var expiresAt time.Time
	var slotKey string
	var slotIndex int
	var simFingerprintHex string
	err = tx.QueryRow(ctx, `
		SELECT identity_public_key, command_public_key, credential_hash, status, expires_at,
		       slot_key, slot_index, COALESCE(sim_fingerprint, '')
		FROM device_discoveries
		WHERE device_id = $1 AND owner_id = $2
		FOR UPDATE`, deviceID, ownerID).Scan(&identityPublicKey, &commandPublicKey, &credentialHash, &status, &expiresAt, &slotKey, &slotIndex, &simFingerprintHex)
	if err != nil {
		return DeviceRecord{}, err
	}
	if status == DeviceDiscoveryRejected {
		return DeviceRecord{}, domain.ErrDeviceDiscoveryRejected
	}
	if status == DeviceDiscoveryApproved {
		var record DeviceRecord
		err := tx.QueryRow(ctx, `
			SELECT id::text, numeric_id, profile_ciphertext, identity_public_key,
			       command_public_key, wrapped_vault_key, paired_at, last_seen_at
			FROM devices WHERE id = $1 AND revoked_at IS NULL`, deviceID).Scan(
			&record.ID, &record.NumericID, &record.ProfileCiphertext, &record.IdentityPublicKey,
			&record.CommandPublicKey, &record.WrappedVaultKey, &record.PairedAt, &record.LastSeenAt,
		)
		if err != nil {
			return DeviceRecord{}, err
		}
		record.Slots, err = store.listDeviceSlots(ctx, record.ID)
		if err != nil {
			return DeviceRecord{}, err
		}
		return record, nil
	}
	if !expiresAt.After(now) {
		return DeviceRecord{}, domain.ErrDeviceDiscoveryExpired
	}
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", int64(0x53494d44)); err != nil {
		return DeviceRecord{}, fmt.Errorf("lock discovery numeric id allocation: %w", err)
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
		return DeviceRecord{}, fmt.Errorf("allocate discovery numeric id: %w", err)
	}
	record := DeviceRecord{
		ID: deviceID, NumericID: numericID, ProfileCiphertext: []byte{},
		IdentityPublicKey: identityPublicKey, CommandPublicKey: commandPublicKey,
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO devices (id, numeric_id, profile_ciphertext, identity_public_key, command_public_key)
		VALUES ($1, $2, ''::bytea, $3, $4)
		RETURNING paired_at`, deviceID, numericID, identityPublicKey, commandPublicKey).Scan(&record.PairedAt)
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) && postgresError.Code == "23505" {
			return DeviceRecord{}, domain.ErrDeviceExists
		}
		return DeviceRecord{}, fmt.Errorf("insert discovered device: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO device_credentials (device_id, credential_hash) VALUES ($1, $2)`, deviceID, credentialHash); err != nil {
		return DeviceRecord{}, fmt.Errorf("insert discovered device credential: %w", err)
	}
	if slotKey == "" {
		slotKey = "1"
	}
	if slotIndex < 1 {
		slotIndex = 1
	}
	if err := createSIMSlot(ctx, tx, deviceID, slotKey, slotIndex, deviceID, numericID, nil, now, &record); err != nil {
		return DeviceRecord{}, err
	}
	if simFingerprintHex != "" {
		simFingerprint, decodeError := hex.DecodeString(simFingerprintHex)
		if decodeError != nil || len(simFingerprint) != 32 {
			return DeviceRecord{}, errors.New("stored SIM fingerprint is invalid")
		}
		if _, err := tx.Exec(ctx, `UPDATE sim_cards SET iccid_fingerprint = $2, fingerprint_scope = $3 WHERE id = $1`, deviceID, simFingerprint, deviceID); err != nil {
			return DeviceRecord{}, fmt.Errorf("store SIM fingerprint: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE device_discoveries
		SET status = 'approved', decided_at = $3, expires_at = $4, paired_device_id = $1
		WHERE device_id = $1 AND owner_id = $2`, deviceID, ownerID, now.UTC(), now.Add(deviceDiscoveryResultLifetime).UTC()); err != nil {
		return DeviceRecord{}, fmt.Errorf("complete device discovery approval: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return DeviceRecord{}, fmt.Errorf("commit device discovery approval: %w", err)
	}
	return record, nil
}

// RejectDeviceDiscovery denies a candidate while preserving a short-lived device-visible result.
func (store *Postgres) RejectDeviceDiscovery(ctx context.Context, ownerID string, deviceID string, now time.Time) error {
	command, err := store.pool.Exec(ctx, `
		UPDATE device_discoveries
		SET status = 'rejected', decided_at = $3, expires_at = $4
		WHERE device_id = $1 AND owner_id = $2 AND status = 'pending' AND expires_at > $3`,
		deviceID, ownerID, now.UTC(), now.Add(deviceDiscoveryResultLifetime).UTC())
	if err != nil {
		return fmt.Errorf("reject device discovery: %w", err)
	}
	if command.RowsAffected() == 0 {
		var status DeviceDiscoveryStatus
		var expiresAt time.Time
		err := store.pool.QueryRow(ctx, `
			SELECT status, expires_at FROM device_discoveries
			WHERE device_id = $1 AND owner_id = $2`, deviceID, ownerID).Scan(&status, &expiresAt)
		if err != nil {
			return err
		}
		if status == DeviceDiscoveryRejected {
			return nil
		}
		if status == DeviceDiscoveryApproved {
			return domain.ErrDeviceExists
		}
		return domain.ErrDeviceDiscoveryExpired
	}
	return nil
}

// DeviceDiscoveryStatus authenticates a candidate with the hash of its private pending credential.
func (store *Postgres) DeviceDiscoveryStatus(ctx context.Context, deviceID string, credentialHash []byte, now time.Time) (DeviceDiscoveryRecord, error) {
	var record DeviceDiscoveryRecord
	err := store.pool.QueryRow(ctx, `
		SELECT discovery.device_id::text, discovery.status, discovery.first_seen_at,
		       discovery.last_seen_at, discovery.expires_at, discovery.decided_at,
		       COALESCE(sim.numeric_id, 0),
		       slot.id::text, sim.id::text, discovery.slot_key, discovery.slot_index
		FROM device_discoveries AS discovery
		LEFT JOIN devices ON devices.id = discovery.paired_device_id AND devices.revoked_at IS NULL
		LEFT JOIN device_slots AS slot ON slot.device_id = devices.id AND slot.removed_at IS NULL
		LEFT JOIN sim_cards AS sim ON sim.id = slot.current_sim_id AND sim.retired_at IS NULL
		WHERE discovery.device_id = $1
		  AND discovery.credential_hash = $2
		  AND discovery.expires_at > $3`, deviceID, credentialHash, now.UTC()).Scan(
		&record.DeviceUUID, &record.Status, &record.FirstSeenAt, &record.LastSeenAt,
		&record.ExpiresAt, &record.DecidedAt, &record.NumericID, &record.SlotID, &record.SIMID,
		&record.SlotKey, &record.SlotIndex,
	)
	if err != nil {
		return DeviceDiscoveryRecord{}, err
	}
	return record, nil
}

// Active reports whether the discovery window accepts new device candidates.
func (session DeviceDiscoverySession) Active(now time.Time) bool {
	return session.ClosedAt == nil && session.ExpiresAt.After(now)
}

// DeviceDiscoveryRequest contains verified public identity material from one candidate modem.
type DeviceDiscoveryRequest struct {
	DeviceUUID        string
	SlotKey           string
	SlotIndex         int
	SIMFingerprint    []byte
	IdentityPublicKey []byte
	CommandPublicKey  []byte
	CredentialHash    []byte
}

// DeviceDiscoveryRecord is safe to return to the owner or the authenticated candidate.
type DeviceDiscoveryRecord struct {
	DeviceUUID        string                `json:"device_uuid"`
	IdentityPublicKey []byte                `json:"identity_public_key,omitempty"`
	CommandPublicKey  []byte                `json:"command_public_key,omitempty"`
	Status            DeviceDiscoveryStatus `json:"status"`
	FirstSeenAt       time.Time             `json:"first_seen_at"`
	LastSeenAt        time.Time             `json:"last_seen_at"`
	ExpiresAt         time.Time             `json:"expires_at"`
	DecidedAt         *time.Time            `json:"decided_at,omitempty"`
	NumericID         int                   `json:"numeric_id,omitempty"`
	SlotID            *string               `json:"slot_id,omitempty"`
	SIMID             *string               `json:"sim_id,omitempty"`
	SlotKey           string                `json:"slot_key,omitempty"`
	SlotIndex         int                   `json:"slot_index,omitempty"`
	SIMFingerprint    []byte                `json:"-"`
}
