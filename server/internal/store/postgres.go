package store

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"sim-deck-server/internal/domain"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Postgres stores only routing metadata and encrypted application payloads.
type Postgres struct {
	pool *pgxpool.Pool
}

// DeviceRecord is the server-readable portion of a paired device.
type DeviceRecord struct {
	ID                string
	NumericID         int
	ProfileCiphertext []byte
	IdentityPublicKey []byte
	WrappedVaultKey   []byte
	PairedAt          time.Time
	LastSeenAt        *time.Time
}

// Open connects to PostgreSQL and verifies that the server is reachable.
func Open(ctx context.Context, databaseURL string) (*Postgres, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
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

// Migrate applies the embedded idempotent schema.
func (store *Postgres) Migrate(ctx context.Context) error {
	schema, err := migrations.ReadFile("migrations/001_initial.sql")
	if err != nil {
		return fmt.Errorf("read initial migration: %w", err)
	}
	if _, err := store.pool.Exec(ctx, string(schema)); err != nil {
		return fmt.Errorf("apply initial migration: %w", err)
	}
	return nil
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
		LEFT JOIN devices ON devices.numeric_id = candidate AND devices.revoked_at IS NULL
		WHERE devices.id IS NULL
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
		UPDATE devices SET numeric_id = $2
		WHERE id = $1 AND revoked_at IS NULL`, deviceID, numericID)
	if err != nil {
		return fmt.Errorf("update numeric id: %w", err)
	}
	if command.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
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
		err = store.pool.QueryRow(ctx, "SELECT sequence FROM events WHERE id = $1", envelope.ID).Scan(&sequence)
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

// InitialMigration returns the schema for validation and packaging tests.
func InitialMigration() ([]byte, error) {
	return migrations.ReadFile("migrations/001_initial.sql")
}
