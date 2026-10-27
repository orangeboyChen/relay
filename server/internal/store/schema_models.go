package store

import "time"

// The database models deliberately mirror the existing PostgreSQL schema. Keeping
// the table names explicit makes the mapping stable when Go naming rules
// change and avoids coupling persisted data to exported Go field names.
type ownerAccountModel struct {
	ID                 string     `gorm:"column:id;type:uuid;primaryKey"`
	Username           string     `gorm:"column:username;uniqueIndex;not null"`
	PasswordHash       []byte     `gorm:"column:password_hash"`
	PasswordDisabledAt *time.Time `gorm:"column:password_disabled_at"`
	RecoveryPublicKey  []byte     `gorm:"column:recovery_public_key;not null"`
	CreatedAt          time.Time  `gorm:"column:created_at;not null"`
}

func (ownerAccountModel) TableName() string { return "owner_accounts" }

type webauthnCredentialModel struct {
	ID         []byte     `gorm:"column:id;type:bytea;primaryKey"`
	OwnerID    string     `gorm:"column:owner_id;type:uuid;not null"`
	PublicKey  []byte     `gorm:"column:public_key;not null"`
	SignCount  uint32     `gorm:"column:sign_count;not null"`
	CreatedAt  time.Time  `gorm:"column:created_at;not null"`
	LastUsedAt *time.Time `gorm:"column:last_used_at"`
}

func (webauthnCredentialModel) TableName() string { return "webauthn_credentials" }

type appInstallationModel struct {
	ID                    string     `gorm:"column:id;type:uuid;primaryKey"`
	OwnerID               string     `gorm:"column:owner_id;type:uuid;not null"`
	IdentityPublicKey     []byte     `gorm:"column:identity_public_key;not null"`
	KeyAgreementPublicKey []byte     `gorm:"column:key_agreement_public_key"`
	WrappedVaultKey       []byte     `gorm:"column:wrapped_vault_key"`
	ApprovedAt            *time.Time `gorm:"column:approved_at"`
	RevokedAt             *time.Time `gorm:"column:revoked_at"`
	CreatedAt             time.Time  `gorm:"column:created_at;not null"`
}

func (appInstallationModel) TableName() string { return "app_installations" }

type authSessionModel struct {
	ID               string     `gorm:"column:id;type:uuid;primaryKey"`
	OwnerID          string     `gorm:"column:owner_id;type:uuid;not null"`
	AccessTokenHash  []byte     `gorm:"column:access_token_hash;uniqueIndex;not null"`
	RefreshTokenHash []byte     `gorm:"column:refresh_token_hash;uniqueIndex;not null"`
	AccessExpiresAt  time.Time  `gorm:"column:access_expires_at;not null"`
	RefreshExpiresAt time.Time  `gorm:"column:refresh_expires_at;not null"`
	RevokedAt        *time.Time `gorm:"column:revoked_at"`
	CreatedAt        time.Time  `gorm:"column:created_at;not null"`
}

func (authSessionModel) TableName() string { return "auth_sessions" }

type pushTokenModel struct {
	InstallationID  string    `gorm:"column:installation_id;type:uuid;primaryKey"`
	Environment     string    `gorm:"column:environment;primaryKey;not null"`
	Kind            string    `gorm:"column:kind;primaryKey;not null"`
	TokenCiphertext []byte    `gorm:"column:token_ciphertext;not null"`
	UpdatedAt       time.Time `gorm:"column:updated_at;not null"`
}

func (pushTokenModel) TableName() string { return "push_tokens" }

type deviceModel struct {
	ID                    string     `gorm:"column:id;type:uuid;primaryKey"`
	NumericID             int        `gorm:"column:numeric_id;not null"`
	ProfileCiphertext     []byte     `gorm:"column:profile_ciphertext;not null"`
	IdentityPublicKey     []byte     `gorm:"column:identity_public_key;not null"`
	CommandPublicKey      []byte     `gorm:"column:command_public_key"`
	WrappedVaultKey       []byte     `gorm:"column:wrapped_vault_key"`
	PairedAt              time.Time  `gorm:"column:paired_at;not null"`
	LastSeenAt            *time.Time `gorm:"column:last_seen_at"`
	RadioAccessTechnology string     `gorm:"column:radio_access_technology"`
	SignalBars            *int       `gorm:"column:signal_bars"`
	RadioStatusUpdatedAt  *time.Time `gorm:"column:radio_status_updated_at"`
	RevokedAt             *time.Time `gorm:"column:revoked_at"`
}

func (deviceModel) TableName() string { return "devices" }

type deviceCertificateModel struct {
	Serial         string     `gorm:"column:serial;primaryKey"`
	DeviceID       string     `gorm:"column:device_id;type:uuid;not null"`
	CertificatePEM []byte     `gorm:"column:certificate_pem;not null"`
	ExpiresAt      time.Time  `gorm:"column:expires_at;not null"`
	RevokedAt      *time.Time `gorm:"column:revoked_at"`
}

func (deviceCertificateModel) TableName() string { return "device_certificates" }

type pairingTokenModel struct {
	TokenHash      []byte     `gorm:"column:token_hash;type:bytea;primaryKey"`
	OwnerID        *string    `gorm:"column:owner_id;type:uuid"`
	PairedDeviceID *string    `gorm:"column:paired_device_id;type:uuid"`
	ExpiresAt      time.Time  `gorm:"column:expires_at;not null"`
	ConsumedAt     *time.Time `gorm:"column:consumed_at"`
	CreatedAt      time.Time  `gorm:"column:created_at;not null"`
}

func (pairingTokenModel) TableName() string { return "pairing_tokens" }

type deviceCredentialModel struct {
	DeviceID       string     `gorm:"column:device_id;type:uuid;primaryKey"`
	CredentialHash []byte     `gorm:"column:credential_hash;uniqueIndex;not null"`
	CreatedAt      time.Time  `gorm:"column:created_at;not null"`
	RevokedAt      *time.Time `gorm:"column:revoked_at"`
}

func (deviceCredentialModel) TableName() string { return "device_credentials" }

type simCardModel struct {
	ID                string     `gorm:"column:id;type:uuid;primaryKey"`
	NumericID         int        `gorm:"column:numeric_id;not null;uniqueIndex:sim_cards_active_numeric_id_idx,where:retired_at IS NULL"`
	FingerprintScope  *string    `gorm:"column:fingerprint_scope;type:uuid"`
	ICCIDFingerprint  []byte     `gorm:"column:iccid_fingerprint"`
	ProfileCiphertext []byte     `gorm:"column:profile_ciphertext;not null"`
	FirstSeenAt       time.Time  `gorm:"column:first_seen_at;not null"`
	LastSeenAt        time.Time  `gorm:"column:last_seen_at;not null"`
	CreatedAt         time.Time  `gorm:"column:created_at;not null"`
	RetiredAt         *time.Time `gorm:"column:retired_at"`
}

func (simCardModel) TableName() string { return "sim_cards" }

type esimProfileModel struct {
	SIMID        string    `gorm:"column:sim_id;type:uuid;primaryKey"`
	ProfileID    string    `gorm:"column:profile_id;primaryKey"`
	Name         string    `gorm:"column:name;not null"`
	ICCID        string    `gorm:"column:iccid;not null"`
	OperatorName string    `gorm:"column:operator_name;not null"`
	IsEnabled    bool      `gorm:"column:is_enabled;not null"`
	CreatedAt    time.Time `gorm:"column:created_at;not null"`
	UpdatedAt    time.Time `gorm:"column:updated_at;not null"`
}

func (esimProfileModel) TableName() string { return "esim_profiles" }

type deviceSlotModel struct {
	ID                    string     `gorm:"column:id;type:uuid;primaryKey"`
	DeviceID              string     `gorm:"column:device_id;type:uuid;not null"`
	SlotKey               string     `gorm:"column:slot_key;not null"`
	SlotIndex             int        `gorm:"column:slot_index;not null"`
	CurrentSIMID          *string    `gorm:"column:current_sim_id;type:uuid"`
	RadioAccessTechnology string     `gorm:"column:radio_access_technology"`
	SignalBars            *int       `gorm:"column:signal_bars"`
	RadioStatusUpdatedAt  *time.Time `gorm:"column:radio_status_updated_at"`
	CreatedAt             time.Time  `gorm:"column:created_at;not null"`
	LastSeenAt            *time.Time `gorm:"column:last_seen_at"`
	RemovedAt             *time.Time `gorm:"column:removed_at"`
}

func (deviceSlotModel) TableName() string { return "device_slots" }

type slotSIMAssignmentModel struct {
	ID         int64      `gorm:"column:id;primaryKey;autoIncrement"`
	SlotID     string     `gorm:"column:slot_id;type:uuid;not null"`
	SIMID      string     `gorm:"column:sim_id;type:uuid;not null"`
	InsertedAt time.Time  `gorm:"column:inserted_at;not null"`
	RemovedAt  *time.Time `gorm:"column:removed_at"`
}

func (slotSIMAssignmentModel) TableName() string { return "slot_sim_assignments" }

type eventModel struct {
	Sequence                int64     `gorm:"column:sequence;primaryKey;autoIncrement"`
	ID                      string    `gorm:"column:id;type:uuid;uniqueIndex;not null"`
	Type                    string    `gorm:"column:type;not null"`
	DeviceID                string    `gorm:"column:device_id;type:uuid;not null"`
	RecipientInstallationID *string   `gorm:"column:recipient_installation_id;type:uuid"`
	OccurredAt              time.Time `gorm:"column:occurred_at;not null"`
	KeyEpoch                int       `gorm:"column:key_epoch;not null"`
	Nonce                   []byte    `gorm:"column:nonce;not null"`
	Ciphertext              []byte    `gorm:"column:ciphertext;not null"`
	Signature               []byte    `gorm:"column:signature;not null"`
	ExpiresAt               time.Time `gorm:"column:expires_at;not null"`
	CreatedAt               time.Time `gorm:"column:created_at;not null"`
}

func (eventModel) TableName() string { return "events" }

type encryptedHistorySnapshotModel struct {
	OwnerID    string    `gorm:"column:owner_id;type:uuid;primaryKey"`
	Kind       int       `gorm:"column:kind;primaryKey"`
	Revision   int64     `gorm:"column:revision;not null"`
	Ciphertext []byte    `gorm:"column:ciphertext;not null"`
	UpdatedAt  time.Time `gorm:"column:updated_at;not null"`
}

func (encryptedHistorySnapshotModel) TableName() string { return "encrypted_history_snapshots" }

type messageModel struct {
	EventID        string    `gorm:"column:event_id;type:uuid;primaryKey"`
	ConversationID string    `gorm:"column:conversation_id;type:uuid;not null"`
	Direction      string    `gorm:"column:direction;not null"`
	MediaKind      string    `gorm:"column:media_kind;not null"`
	DeliveryState  string    `gorm:"column:delivery_state;not null"`
	UpdatedAt      time.Time `gorm:"column:updated_at;not null"`
}

func (messageModel) TableName() string { return "messages" }

type attachmentModel struct {
	ID             string    `gorm:"column:id;type:uuid;primaryKey"`
	MessageEventID string    `gorm:"column:message_event_id;type:uuid;not null"`
	StorageKey     string    `gorm:"column:storage_key;uniqueIndex;not null"`
	EncryptedSize  int64     `gorm:"column:encrypted_size;not null"`
	ExpiresAt      time.Time `gorm:"column:expires_at;not null"`
	CreatedAt      time.Time `gorm:"column:created_at;not null"`
}

func (attachmentModel) TableName() string { return "attachments" }

type callModel struct {
	EventID   string     `gorm:"column:event_id;type:uuid;primaryKey"`
	CallID    string     `gorm:"column:call_id;type:uuid;uniqueIndex;not null"`
	Direction string     `gorm:"column:direction;not null"`
	State     string     `gorm:"column:state;not null"`
	ClaimedBy *string    `gorm:"column:claimed_by;type:uuid"`
	StartedAt *time.Time `gorm:"column:started_at"`
	EndedAt   *time.Time `gorm:"column:ended_at"`
}

func (callModel) TableName() string { return "calls" }

type outboxJobModel struct {
	ID          int64      `gorm:"column:id;primaryKey;autoIncrement"`
	Kind        string     `gorm:"column:kind;not null"`
	RoutingKey  string     `gorm:"column:routing_key;not null"`
	Payload     []byte     `gorm:"column:payload;not null"`
	Attempts    int        `gorm:"column:attempts;not null;default:0"`
	AvailableAt time.Time  `gorm:"column:available_at;not null"`
	ExpiresAt   *time.Time `gorm:"column:expires_at"`
	CompletedAt *time.Time `gorm:"column:completed_at"`
	LastError   *string    `gorm:"column:last_error"`
}

func (outboxJobModel) TableName() string { return "outbox_jobs" }

type auditEventModel struct {
	ID         int64     `gorm:"column:id;primaryKey;autoIncrement"`
	ActorType  string    `gorm:"column:actor_type;not null"`
	ActorID    string    `gorm:"column:actor_id;not null"`
	Action     string    `gorm:"column:action;not null"`
	TargetID   *string   `gorm:"column:target_id"`
	OccurredAt time.Time `gorm:"column:occurred_at;not null"`
}

func (auditEventModel) TableName() string { return "audit_events" }

type encryptedDeviceCommandModel struct {
	ID                   string     `gorm:"column:id;type:uuid;primaryKey"`
	DeviceID             string     `gorm:"column:device_id;type:uuid;not null"`
	SenderInstallationID string     `gorm:"column:sender_installation_id;type:uuid;not null"`
	SenderPublicKey      []byte     `gorm:"column:sender_public_key;not null"`
	Nonce                []byte     `gorm:"column:nonce;not null"`
	Ciphertext           []byte     `gorm:"column:ciphertext;not null"`
	Signature            []byte     `gorm:"column:signature;not null"`
	CreatedAt            time.Time  `gorm:"column:created_at;not null"`
	ExpiresAt            time.Time  `gorm:"column:expires_at;not null"`
	LeasedUntil          *time.Time `gorm:"column:leased_until"`
	DeliveryAttempts     int        `gorm:"column:delivery_attempts;not null"`
	ResultNonce          []byte     `gorm:"column:result_nonce"`
	ResultCiphertext     []byte     `gorm:"column:result_ciphertext"`
	CompletedAt          *time.Time `gorm:"column:completed_at"`
}

func (encryptedDeviceCommandModel) TableName() string { return "encrypted_device_commands" }

type incomingCallNotificationModel struct {
	ID         string    `gorm:"column:id;type:uuid;primaryKey"`
	Type       string    `gorm:"column:type;not null"`
	DeviceID   string    `gorm:"column:device_id;type:uuid;not null"`
	OccurredAt time.Time `gorm:"column:occurred_at;not null"`
	KeyEpoch   int       `gorm:"column:key_epoch;not null"`
	ExpiresAt  time.Time `gorm:"column:expires_at;not null"`
	CreatedAt  time.Time `gorm:"column:created_at;not null"`
}

func (incomingCallNotificationModel) TableName() string { return "incoming_call_notifications" }

type incomingCallRecipientModel struct {
	NotificationID  string `gorm:"column:notification_id;type:uuid;primaryKey"`
	InstallationID  string `gorm:"column:installation_id;type:uuid;primaryKey"`
	SenderPublicKey []byte `gorm:"column:sender_public_key;not null"`
	Nonce           []byte `gorm:"column:nonce;not null"`
	Ciphertext      []byte `gorm:"column:ciphertext;not null"`
	Signature       []byte `gorm:"column:signature;not null"`
}

func (incomingCallRecipientModel) TableName() string { return "incoming_call_recipients" }

type incomingCallClaimModel struct {
	NotificationID string    `gorm:"column:notification_id;type:uuid;primaryKey"`
	InstallationID string    `gorm:"column:installation_id;type:uuid;not null"`
	ClaimedAt      time.Time `gorm:"column:claimed_at;not null"`
}

func (incomingCallClaimModel) TableName() string { return "incoming_call_claims" }

type deviceDiscoverySessionModel struct {
	OwnerID   string     `gorm:"column:owner_id;type:uuid;primaryKey"`
	OpenedAt  time.Time  `gorm:"column:opened_at;not null"`
	ExpiresAt time.Time  `gorm:"column:expires_at;not null"`
	ClosedAt  *time.Time `gorm:"column:closed_at"`
}

func (deviceDiscoverySessionModel) TableName() string { return "device_discovery_sessions" }

type deviceDiscoveryModel struct {
	DeviceID          string     `gorm:"column:device_id;type:uuid;primaryKey"`
	OwnerID           string     `gorm:"column:owner_id;type:uuid;not null"`
	IdentityPublicKey []byte     `gorm:"column:identity_public_key;not null"`
	CommandPublicKey  []byte     `gorm:"column:command_public_key;not null"`
	CredentialHash    []byte     `gorm:"column:credential_hash;uniqueIndex;not null"`
	SlotKey           string     `gorm:"column:slot_key;not null"`
	SlotIndex         int        `gorm:"column:slot_index;not null"`
	SIMFingerprint    *string    `gorm:"column:sim_fingerprint"`
	Status            string     `gorm:"column:status;not null"`
	FirstSeenAt       time.Time  `gorm:"column:first_seen_at;not null"`
	LastSeenAt        time.Time  `gorm:"column:last_seen_at;not null"`
	ExpiresAt         time.Time  `gorm:"column:expires_at;not null"`
	DecidedAt         *time.Time `gorm:"column:decided_at"`
	PairedDeviceID    *string    `gorm:"column:paired_device_id;type:uuid"`
}

func (deviceDiscoveryModel) TableName() string { return "device_discoveries" }
