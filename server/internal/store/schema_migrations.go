package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
)

const currentSchemaVersion = 1

type schemaMigrationModel struct {
	Version int `gorm:"column:version;primaryKey"`
}

func (schemaMigrationModel) TableName() string { return "schema_migrations" }

// runMigrations applies the versioned database schema. The database mapper owns DDL generation;
// application code never embeds or executes SQL statements.
func (store *Postgres) runMigrations(ctx context.Context) error {
	if store.db == nil {
		return fmt.Errorf("database is not initialized")
	}
	db := store.db.WithContext(ctx)
	if err := db.AutoMigrate(&schemaMigrationModel{}); err != nil {
		return fmt.Errorf("create schema migration table: %w", err)
	}
	var applied schemaMigrationModel
	result := db.Order("version DESC").First(&applied)
	if result.Error != nil && result.Error != gorm.ErrRecordNotFound {
		return fmt.Errorf("read schema version: %w", result.Error)
	}
	if result.Error == nil && applied.Version >= currentSchemaVersion {
		return nil
	}

	models := []any{
		&ownerAccountModel{}, &webauthnCredentialModel{}, &appInstallationModel{},
		&authSessionModel{}, &pushTokenModel{}, &deviceModel{},
		&deviceCertificateModel{}, &pairingTokenModel{}, &deviceCredentialModel{},
		&simCardModel{}, &esimProfileModel{}, &deviceSlotModel{},
		&slotSIMAssignmentModel{}, &eventModel{}, &encryptedHistorySnapshotModel{},
		&messageModel{}, &attachmentModel{}, &callModel{}, &outboxJobModel{},
		&auditEventModel{}, &encryptedDeviceCommandModel{},
		&incomingCallNotificationModel{}, &incomingCallRecipientModel{},
		&incomingCallClaimModel{}, &deviceDiscoverySessionModel{},
		&deviceDiscoveryModel{},
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.AutoMigrate(models...); err != nil {
			return err
		}
		return tx.Create(&schemaMigrationModel{Version: currentSchemaVersion}).Error
	}); err != nil {
		return fmt.Errorf("initialize database schema (migration %d): %w", currentSchemaVersion, err)
	}
	return nil
}

func (store *Postgres) database(ctx context.Context) *gorm.DB {
	return store.db.WithContext(ctx)
}

func databaseNotFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return pgx.ErrNoRows
	}
	return err
}

func isUniqueViolation(err error) bool {
	var postgresError *pgconn.PgError
	return errors.As(err, &postgresError) && postgresError.Code == "23505"
}

func ptrTime(value time.Time) *time.Time { return &value }
