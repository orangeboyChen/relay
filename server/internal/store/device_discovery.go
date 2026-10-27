package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

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
	model := deviceDiscoverySessionModel{OwnerID: ownerID, OpenedAt: now.UTC(), ExpiresAt: expiresAt.UTC()}
	result := store.database(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "owner_id"}}, DoUpdates: clause.Assignments(map[string]any{"opened_at": now.UTC(), "expires_at": expiresAt.UTC(), "closed_at": nil})}).Create(&model)
	if result.Error != nil {
		return DeviceDiscoverySession{}, fmt.Errorf("open device discovery: %w", result.Error)
	}
	return DeviceDiscoverySession{OwnerID: model.OwnerID, OpenedAt: model.OpenedAt, ExpiresAt: model.ExpiresAt, ClosedAt: model.ClosedAt}, nil
}

// CloseDeviceDiscovery stops accepting new candidates without deleting pending results.
func (store *Postgres) CloseDeviceDiscovery(ctx context.Context, ownerID string, now time.Time) error {
	result := store.database(ctx).Model(&deviceDiscoverySessionModel{}).Where("owner_id = ? AND closed_at IS NULL", ownerID).Updates(map[string]any{"closed_at": now.UTC()})
	if result.Error != nil {
		return fmt.Errorf("close device discovery: %w", result.Error)
	}
	return nil
}

// GetDeviceDiscoverySession returns the latest admission window for the owner.
func (store *Postgres) GetDeviceDiscoverySession(ctx context.Context, ownerID string) (DeviceDiscoverySession, error) {
	var model deviceDiscoverySessionModel
	if err := store.database(ctx).Where("owner_id = ?", ownerID).First(&model).Error; err != nil {
		return DeviceDiscoverySession{}, databaseNotFound(err)
	}
	return DeviceDiscoverySession{OwnerID: model.OwnerID, OpenedAt: model.OpenedAt, ExpiresAt: model.ExpiresAt, ClosedAt: model.ClosedAt}, nil
}

// SubmitDeviceDiscovery records a candidate only while the owner's discovery window is active.
func (store *Postgres) SubmitDeviceDiscovery(ctx context.Context, now time.Time, request DeviceDiscoveryRequest) (DeviceDiscoveryRecord, error) {
	if request.SlotKey == "" {
		request.SlotKey = "1"
	}
	if request.SlotIndex < 1 {
		request.SlotIndex = 1
	}
	var record DeviceDiscoveryRecord
	err := store.database(ctx).Transaction(func(tx *gorm.DB) error {
		var session deviceDiscoverySessionModel
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("closed_at IS NULL AND expires_at > ?", now.UTC()).Order("opened_at DESC").First(&session).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return domain.ErrDeviceDiscoveryClosed
			}
			return fmt.Errorf("read active device discovery: %w", err)
		}
		var device deviceModel
		if err := tx.Where("id = ? AND revoked_at IS NULL", request.DeviceUUID).First(&device).Error; err == nil {
			return domain.ErrDeviceExists
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("check existing device discovery: %w", err)
		}
		var existing deviceDiscoveryModel
		result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("device_id = ?", request.DeviceUUID).First(&existing)
		if result.Error == nil {
			existingFingerprint := []byte{}
			if existing.SIMFingerprint != nil {
				existingFingerprint, _ = hex.DecodeString(*existing.SIMFingerprint)
			}
			matches := existing.OwnerID == session.OwnerID && existing.SlotKey == request.SlotKey && existing.SlotIndex == request.SlotIndex && bytes.Equal(existing.IdentityPublicKey, request.IdentityPublicKey) && bytes.Equal(existing.CommandPublicKey, request.CommandPublicKey) && bytes.Equal(existingFingerprint, request.SIMFingerprint) && bytes.Equal(existing.CredentialHash, request.CredentialHash)
			if existing.ExpiresAt.After(now) || existing.Status == string(DeviceDiscoveryApproved) {
				if !matches {
					var revoked deviceModel
					revokedErr := tx.Where("id = ? AND revoked_at IS NOT NULL", request.DeviceUUID).First(&revoked).Error
					if existing.Status != string(DeviceDiscoveryApproved) || revokedErr != nil || existing.OwnerID != session.OwnerID || !bytes.Equal(revoked.IdentityPublicKey, request.IdentityPublicKey) {
						return domain.ErrDeviceDiscoveryConflict
					}
					fingerprint := ""
					if len(request.SIMFingerprint) > 0 {
						fingerprint = hex.EncodeToString(request.SIMFingerprint)
					}
					updates := map[string]any{
						"identity_public_key": append([]byte(nil), request.IdentityPublicKey...),
						"command_public_key":  append([]byte(nil), request.CommandPublicKey...),
						"credential_hash":     append([]byte(nil), request.CredentialHash...),
						"slot_key":            request.SlotKey,
						"slot_index":          request.SlotIndex,
						"sim_fingerprint":     nil,
						"status":              string(DeviceDiscoveryPending),
						"first_seen_at":       now.UTC(),
						"last_seen_at":        now.UTC(),
						"expires_at":          session.ExpiresAt,
						"decided_at":          nil,
						"paired_device_id":    nil,
					}
					if fingerprint != "" {
						updates["sim_fingerprint"] = fingerprint
					}
					if err := tx.Model(&existing).Updates(updates).Error; err != nil {
						return fmt.Errorf("refresh revoked device discovery: %w", err)
					}
					existing.IdentityPublicKey = append([]byte(nil), request.IdentityPublicKey...)
					existing.CommandPublicKey = append([]byte(nil), request.CommandPublicKey...)
					existing.CredentialHash = append([]byte(nil), request.CredentialHash...)
					existing.SlotKey = request.SlotKey
					existing.SlotIndex = request.SlotIndex
					existing.SIMFingerprint = nil
					if fingerprint != "" {
						existing.SIMFingerprint = &fingerprint
					}
					existing.Status = string(DeviceDiscoveryPending)
					existing.FirstSeenAt = now.UTC()
					existing.LastSeenAt = now.UTC()
					existing.ExpiresAt = session.ExpiresAt
					existing.DecidedAt = nil
					existing.PairedDeviceID = nil
					record = discoveryRecordFromModel(existing, request.SIMFingerprint)
					return nil
				}
				if existing.Status == string(DeviceDiscoveryPending) {
					if err := tx.Model(&existing).Update("last_seen_at", now.UTC()).Error; err != nil {
						return fmt.Errorf("refresh device discovery: %w", err)
					}
				}
				record = discoveryRecordFromModel(existing, existingFingerprint)
				return nil
			}
			if err := tx.Delete(&existing).Error; err != nil {
				return fmt.Errorf("replace expired device discovery: %w", err)
			}
		} else if !errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return fmt.Errorf("read device discovery retry: %w", result.Error)
		}
		var pendingCount int64
		if err := tx.Model(&deviceDiscoveryModel{}).Where("owner_id = ? AND status = ? AND expires_at > ?", session.OwnerID, string(DeviceDiscoveryPending), now.UTC()).Count(&pendingCount).Error; err != nil {
			return fmt.Errorf("count pending device discoveries: %w", err)
		}
		if pendingCount >= maxPendingDeviceDiscoveries {
			return domain.ErrDeviceDiscoveryLimit
		}
		fingerprint := ""
		if len(request.SIMFingerprint) > 0 {
			fingerprint = hex.EncodeToString(request.SIMFingerprint)
		}
		model := deviceDiscoveryModel{DeviceID: request.DeviceUUID, OwnerID: session.OwnerID, IdentityPublicKey: append([]byte(nil), request.IdentityPublicKey...), CommandPublicKey: append([]byte(nil), request.CommandPublicKey...), CredentialHash: append([]byte(nil), request.CredentialHash...), SlotKey: request.SlotKey, SlotIndex: request.SlotIndex, Status: string(DeviceDiscoveryPending), FirstSeenAt: now.UTC(), LastSeenAt: now.UTC(), ExpiresAt: session.ExpiresAt}
		if fingerprint != "" {
			model.SIMFingerprint = &fingerprint
		}
		if err := tx.Create(&model).Error; err != nil {
			var postgresError *pgconn.PgError
			if errors.As(err, &postgresError) && postgresError.Code == "23505" && postgresError.ConstraintName == "idx_device_discoveries_credential_hash" {
				return domain.ErrDeviceDiscoveryConflict
			}
			return fmt.Errorf("insert device discovery: %w", err)
		}
		record = discoveryRecordFromModel(model, request.SIMFingerprint)
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return DeviceDiscoveryRecord{}, err
	}
	return record, nil
}

func discoveryRecordFromModel(model deviceDiscoveryModel, fingerprint []byte) DeviceDiscoveryRecord {
	return DeviceDiscoveryRecord{DeviceUUID: model.DeviceID, IdentityPublicKey: append([]byte(nil), model.IdentityPublicKey...), CommandPublicKey: append([]byte(nil), model.CommandPublicKey...), Status: DeviceDiscoveryStatus(model.Status), FirstSeenAt: model.FirstSeenAt, LastSeenAt: model.LastSeenAt, ExpiresAt: model.ExpiresAt, DecidedAt: model.DecidedAt, SlotKey: model.SlotKey, SlotIndex: model.SlotIndex, SIMFingerprint: append([]byte(nil), fingerprint...)}
}

// ListPendingDeviceDiscoveries returns active candidates in first-seen order.
func (store *Postgres) ListPendingDeviceDiscoveries(ctx context.Context, ownerID string, now time.Time) ([]DeviceDiscoveryRecord, error) {
	var models []deviceDiscoveryModel
	if err := store.database(ctx).Where("owner_id = ? AND status = ? AND expires_at > ?", ownerID, string(DeviceDiscoveryPending), now.UTC()).Order("first_seen_at, device_id").Find(&models).Error; err != nil {
		return nil, fmt.Errorf("list pending device discoveries: %w", err)
	}
	records := make([]DeviceDiscoveryRecord, 0, len(models))
	for _, model := range models {
		fingerprint := []byte{}
		if model.SIMFingerprint != nil {
			fingerprint, _ = hex.DecodeString(*model.SIMFingerprint)
		}
		records = append(records, discoveryRecordFromModel(model, fingerprint))
	}
	return records, nil
}

// ApproveDeviceDiscovery atomically allocates a SIM ID and activates the candidate credential.
func (store *Postgres) ApproveDeviceDiscovery(ctx context.Context, ownerID string, deviceID string, now time.Time) (DeviceRecord, error) {
	var record DeviceRecord
	err := store.database(ctx).Transaction(func(tx *gorm.DB) error {
		var discovery deviceDiscoveryModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("device_id = ? AND owner_id = ?", deviceID, ownerID).First(&discovery).Error; err != nil {
			return err
		}
		if discovery.Status == string(DeviceDiscoveryRejected) {
			return domain.ErrDeviceDiscoveryRejected
		}
		if discovery.Status == string(DeviceDiscoveryApproved) {
			var model deviceModel
			if err := tx.Where("id = ? AND revoked_at IS NULL", deviceID).First(&model).Error; err != nil {
				return err
			}
			record = deviceRecordFromModel(model)
			return listDeviceSlotsDB(tx, record.ID, &record.Slots)
		}
		if !discovery.ExpiresAt.After(now) {
			return domain.ErrDeviceDiscoveryExpired
		}
		pairedAt := now.UTC()
		slotKey, slotIndex := discovery.SlotKey, discovery.SlotIndex
		if slotKey == "" {
			slotKey = "1"
		}
		if slotIndex < 1 {
			slotIndex = 1
		}
		var model deviceModel
		restored := false
		if err := tx.Where("id = ? AND revoked_at IS NOT NULL", deviceID).First(&model).Error; err == nil {
			if !bytes.Equal(model.IdentityPublicKey, discovery.IdentityPublicKey) {
				return domain.ErrDeviceExists
			}
			var sim simCardModel
			fingerprint := []byte(nil)
			if discovery.SIMFingerprint != nil && *discovery.SIMFingerprint != "" {
				decoded, decodeError := hex.DecodeString(*discovery.SIMFingerprint)
				if decodeError != nil || len(decoded) != 32 {
					return errors.New("stored SIM fingerprint is invalid")
				}
				fingerprint = decoded
			}
			query := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND retired_at IS NULL", deviceID)
			if len(fingerprint) > 0 {
				query = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("iccid_fingerprint = ? AND retired_at IS NULL", fingerprint)
			}
			if err := query.First(&sim).Error; err != nil {
				return fmt.Errorf("restore revoked device SIM identity: %w", err)
			}
			var activeSlot deviceSlotModel
			if err := tx.Where("current_sim_id = ? AND removed_at IS NULL AND device_id <> ?", sim.ID, deviceID).First(&activeSlot).Error; err == nil {
				return domain.ErrDeviceDiscoveryConflict
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("check active SIM assignment during restore: %w", err)
			}
			model.NumericID = sim.NumericID
			model.IdentityPublicKey = append([]byte(nil), discovery.IdentityPublicKey...)
			model.CommandPublicKey = append([]byte(nil), discovery.CommandPublicKey...)
			model.RevokedAt = nil
			model.PairedAt = pairedAt
			if err := tx.Model(&model).Updates(map[string]any{
				"numeric_id": model.NumericID, "identity_public_key": model.IdentityPublicKey,
				"command_public_key": model.CommandPublicKey, "revoked_at": nil, "paired_at": pairedAt,
			}).Error; err != nil {
				return fmt.Errorf("restore revoked device: %w", err)
			}
			if len(fingerprint) > 0 && !bytes.Equal(sim.ICCIDFingerprint, fingerprint) {
				return domain.ErrDeviceDiscoveryConflict
			}
			if err := tx.Create(&deviceSlotModel{ID: uuid.NewString(), DeviceID: deviceID, SlotKey: discovery.SlotKey, SlotIndex: discovery.SlotIndex, CurrentSIMID: &sim.ID, CreatedAt: pairedAt, LastSeenAt: ptrTime(pairedAt)}).Error; err != nil {
				return fmt.Errorf("restore device slot: %w", err)
			}
			var slot deviceSlotModel
			if err := tx.Where("device_id = ? AND slot_key = ? AND slot_index = ? AND removed_at IS NULL", deviceID, discovery.SlotKey, discovery.SlotIndex).First(&slot).Error; err != nil {
				return fmt.Errorf("read restored device slot: %w", err)
			}
			if err := tx.Create(&slotSIMAssignmentModel{SlotID: slot.ID, SIMID: sim.ID, InsertedAt: pairedAt}).Error; err != nil {
				return fmt.Errorf("restore SIM slot assignment: %w", err)
			}
			credentialUpdate := tx.Model(&deviceCredentialModel{}).Where("device_id = ?", deviceID).Updates(map[string]any{"credential_hash": append([]byte(nil), discovery.CredentialHash...), "created_at": pairedAt, "revoked_at": nil})
			if credentialUpdate.Error != nil {
				return fmt.Errorf("rotate restored device credential: %w", credentialUpdate.Error)
			}
			if credentialUpdate.RowsAffected == 0 {
				if err := tx.Create(&deviceCredentialModel{DeviceID: deviceID, CredentialHash: append([]byte(nil), discovery.CredentialHash...), CreatedAt: pairedAt}).Error; err != nil {
					return fmt.Errorf("rotate restored device credential: %w", err)
				}
			}
			restored = true
		} else if errors.Is(err, gorm.ErrRecordNotFound) {
			numericID, err := allocateNumericID(tx)
			if err != nil {
				return err
			}
			model = deviceModel{ID: deviceID, NumericID: numericID, ProfileCiphertext: []byte{}, IdentityPublicKey: append([]byte(nil), discovery.IdentityPublicKey...), CommandPublicKey: append([]byte(nil), discovery.CommandPublicKey...), PairedAt: pairedAt}
			if err := tx.Create(&model).Error; err != nil {
				if isUniqueViolation(err) {
					return domain.ErrDeviceExists
				}
				return fmt.Errorf("insert discovered device: %w", err)
			}
			if err := tx.Create(&deviceCredentialModel{DeviceID: deviceID, CredentialHash: append([]byte(nil), discovery.CredentialHash...), CreatedAt: pairedAt}).Error; err != nil {
				return fmt.Errorf("insert discovered device credential: %w", err)
			}
		} else {
			return fmt.Errorf("read revoked device: %w", err)
		}
		record = deviceRecordFromModel(model)
		if !restored {
			if err := createSIMSlotDB(tx, deviceID, slotKey, slotIndex, deviceID, model.NumericID, nil, now, &record); err != nil {
				return err
			}
		} else {
			if err := listDeviceSlotsDB(tx, deviceID, &record.Slots); err != nil {
				return err
			}
		}
		if !restored && discovery.SIMFingerprint != nil && *discovery.SIMFingerprint != "" {
			fingerprint, decodeError := hex.DecodeString(*discovery.SIMFingerprint)
			if decodeError != nil || len(fingerprint) != 32 {
				return errors.New("stored SIM fingerprint is invalid")
			}
			var existing simCardModel
			if err := tx.Where("id <> ? AND iccid_fingerprint = ? AND retired_at IS NULL", deviceID, fingerprint).First(&existing).Error; errors.Is(err, gorm.ErrRecordNotFound) {
				if err := tx.Model(&simCardModel{}).Where("id = ?", deviceID).Updates(map[string]any{"iccid_fingerprint": fingerprint, "fingerprint_scope": deviceID}).Error; err != nil {
					return fmt.Errorf("store SIM fingerprint: %w", err)
				}
			} else if err != nil {
				return fmt.Errorf("check SIM fingerprint: %w", err)
			}
		}
		if err := tx.Model(&discovery).Updates(map[string]any{"status": string(DeviceDiscoveryApproved), "decided_at": now.UTC(), "expires_at": now.Add(deviceDiscoveryResultLifetime).UTC(), "paired_device_id": deviceID}).Error; err != nil {
			return fmt.Errorf("complete device discovery approval: %w", err)
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return DeviceRecord{}, err
	}
	return record, nil
}

// RejectDeviceDiscovery denies a candidate while preserving a short-lived device-visible result.
func (store *Postgres) RejectDeviceDiscovery(ctx context.Context, ownerID string, deviceID string, now time.Time) error {
	result := store.database(ctx).Model(&deviceDiscoveryModel{}).Where("device_id = ? AND owner_id = ? AND status = ? AND expires_at > ?", deviceID, ownerID, string(DeviceDiscoveryPending), now.UTC()).Updates(map[string]any{"status": string(DeviceDiscoveryRejected), "decided_at": now.UTC(), "expires_at": now.Add(deviceDiscoveryResultLifetime).UTC()})
	if result.Error != nil {
		return fmt.Errorf("reject device discovery: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		var model deviceDiscoveryModel
		if err := store.database(ctx).Where("device_id = ? AND owner_id = ?", deviceID, ownerID).First(&model).Error; err != nil {
			return databaseNotFound(err)
		}
		status := DeviceDiscoveryStatus(model.Status)
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
	var discovery deviceDiscoveryModel
	if err := store.database(ctx).Where("device_id = ? AND credential_hash = ? AND expires_at > ?", deviceID, credentialHash, now.UTC()).First(&discovery).Error; err != nil {
		return DeviceDiscoveryRecord{}, databaseNotFound(err)
	}
	fingerprint := []byte{}
	if discovery.SIMFingerprint != nil {
		fingerprint, _ = hex.DecodeString(*discovery.SIMFingerprint)
	}
	record := discoveryRecordFromModel(discovery, fingerprint)
	if discovery.PairedDeviceID != nil {
		var slot deviceSlotModel
		if err := store.database(ctx).Where("device_id = ? AND removed_at IS NULL", *discovery.PairedDeviceID).Order("slot_index").First(&slot).Error; err == nil {
			record.SlotID = &slot.ID
			if slot.CurrentSIMID != nil {
				record.SIMID = slot.CurrentSIMID
				var sim simCardModel
				if err := store.database(ctx).Where("id = ? AND retired_at IS NULL", *slot.CurrentSIMID).First(&sim).Error; err == nil {
					record.NumericID = sim.NumericID
				}
			}
		}
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
