package store

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"

	webauthnlib "github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"gorm.io/gorm"
	"sim-deck-server/internal/auth"
	"sim-deck-server/internal/domain"
)

const (
	firstDeviceID  = "01900000-0000-7000-8000-000000000001"
	secondDeviceID = "01900000-0000-7000-8000-000000000002"
)

var _ = Describe("Embedded schema", func() {
	It("contains the complete application schema", func() {
		schema, err := Schema()
		Expect(err).NotTo(HaveOccurred())
		for _, table := range [][]byte{
			[]byte("owner_accounts"),
			[]byte("app_installations"),
			[]byte("devices"),
			[]byte("device_credentials"),
			[]byte("sim_cards"),
			[]byte("device_slots"),
			[]byte("slot_sim_assignments"),
			[]byte("events"),
			[]byte("messages"),
			[]byte("attachments"),
			[]byte("calls"),
			[]byte("outbox_jobs"),
			[]byte("encrypted_device_commands"),
			[]byte("incoming_call_notifications"),
			[]byte("device_discoveries"),
			[]byte("esim_profiles"),
		} {
			Expect(schema).To(ContainSubstring(string(table)))
		}
		Expect(schema).To(ContainSubstring("fingerprint_scope"))
		Expect(schema).To(ContainSubstring("sim_fingerprint"))
		Expect(schema).NotTo(ContainSubstring("ALTER TABLE"))
		Expect(schema).NotTo(ContainSubstring("UPDATE outbox_jobs"))
	})

	It("rejects migration initialization without a database", func() {
		Expect((&Postgres{}).InitializeSchema(context.Background())).To(MatchError(ContainSubstring("database is not initialized")))
	})

	It("keeps GORM table names aligned with the schema", func() {
		Expect(deviceCertificateModel{}.TableName()).To(Equal("device_certificates"))
		Expect(messageModel{}.TableName()).To(Equal("messages"))
		Expect(attachmentModel{}.TableName()).To(Equal("attachments"))
		Expect(callModel{}.TableName()).To(Equal("calls"))
		Expect(auditEventModel{}.TableName()).To(Equal("audit_events"))
	})
})

var _ = Describe("PostgreSQL integration", Ordered, func() {
	var (
		database *Postgres
		now      time.Time
	)

	BeforeAll(func() {
		databaseURL := os.Getenv("TEST_DATABASE_URL")
		if databaseURL == "" {
			Skip("TEST_DATABASE_URL is not configured")
		}
		var err error
		database, err = Open(context.Background(), databaseURL)
		Expect(err).NotTo(HaveOccurred())
		Expect(database.InitializeSchema(context.Background())).To(Succeed())
		Expect(database.InitializeSchema(context.Background())).To(Succeed())
	})

	AfterAll(func() {
		if database != nil {
			database.Close()
		}
	})

	BeforeEach(func() {
		now = time.Date(2026, 10, 16, 1, 0, 0, 0, time.UTC)
		for _, model := range []any{
			&incomingCallClaimModel{}, &incomingCallRecipientModel{}, &incomingCallNotificationModel{},
			&deviceDiscoveryModel{}, &deviceDiscoverySessionModel{}, &encryptedDeviceCommandModel{},
			&outboxJobModel{}, &callModel{}, &attachmentModel{}, &messageModel{}, &eventModel{},
			&encryptedHistorySnapshotModel{}, &slotSIMAssignmentModel{}, &deviceSlotModel{},
			&esimProfileModel{}, &simCardModel{}, &deviceCredentialModel{}, &deviceCertificateModel{},
			&pairingTokenModel{}, &pushTokenModel{}, &authSessionModel{}, &webauthnCredentialModel{},
			&appInstallationModel{}, &deviceModel{}, &auditEventModel{}, &ownerAccountModel{},
		} {
			Expect(database.db.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(model).Error).To(Succeed())
		}
	})

	createOwner := func() OwnerCredential {
		created, err := database.CreateOwner(context.Background(), "password-hash", bytes.Repeat([]byte{3}, ed25519.PublicKeySize))
		Expect(err).NotTo(HaveOccurred())
		Expect(created).To(BeTrue())
		credential, err := database.OwnerCredentialByUsername(context.Background(), "admin")
		Expect(err).NotTo(HaveOccurred())
		return credential
	}

	newToken := func() string {
		token, err := auth.NewOpaqueToken()
		Expect(err).NotTo(HaveOccurred())
		return token
	}

	newPair := func() auth.TokenPair {
		pair, err := auth.NewTokenPair(now)
		Expect(err).NotTo(HaveOccurred())
		return pair
	}

	createDevice := func(deviceID string) DeviceRecord {
		record, err := database.CreateDevice(
			context.Background(),
			deviceID,
			[]byte("encrypted profile"),
			bytes.Repeat([]byte{4}, ed25519.PublicKeySize),
			[]byte("wrapped key"),
		)
		Expect(err).NotTo(HaveOccurred())
		return record
	}

	createSIMCards := func(firstNumericID, lastNumericID int) {
		cards := make([]simCardModel, 0, lastNumericID-firstNumericID+1)
		for numericID := firstNumericID; numericID <= lastNumericID; numericID++ {
			cards = append(cards, simCardModel{
				ID: uuid.NewString(), NumericID: numericID, ProfileCiphertext: []byte{},
				FirstSeenAt: now, LastSeenAt: now, CreatedAt: now,
			})
		}
		Expect(database.db.CreateInBatches(&cards, 100).Error).To(Succeed())
	}

	It("creates exactly one owner and loads only login state", func() {
		exists, err := database.OwnerExists(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(exists).To(BeFalse())

		credential := createOwner()
		Expect(credential.ID).NotTo(BeEmpty())
		Expect(credential.PasswordHash).To(Equal("password-hash"))
		Expect(credential.PasswordDisabledAt).To(BeNil())

		created, err := database.CreateOwner(context.Background(), "second", []byte("second key"))
		Expect(err).NotTo(HaveOccurred())
		Expect(created).To(BeFalse())
		exists, err = database.OwnerExists(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(exists).To(BeTrue())
	})

	It("persists passkeys and disables the bootstrap password", func() {
		owner := createOwner()
		ownerID, err := database.OwnerID(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(ownerID).To(Equal(owner.ID))

		credential := webauthnlib.Credential{
			ID:        []byte("credential-id"),
			PublicKey: bytes.Repeat([]byte{7}, 32),
			Authenticator: webauthnlib.Authenticator{
				SignCount: 3,
			},
		}
		Expect(database.SaveWebAuthnCredential(context.Background(), owner.ID, credential)).To(Succeed())
		credentials, err := database.WebAuthnCredentials(context.Background(), owner.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(credentials).To(Equal([]webauthnlib.Credential{credential}))

		credential.Authenticator.SignCount = 4
		Expect(database.SaveWebAuthnCredential(context.Background(), owner.ID, credential)).To(Succeed())
		credentials, err = database.WebAuthnCredentials(context.Background(), owner.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(credentials).To(Equal([]webauthnlib.Credential{credential}))

		disabledAt := now.Add(time.Minute)
		Expect(database.DisableOwnerPassword(context.Background(), owner.ID, disabledAt)).To(Succeed())
		owner, err = database.OwnerCredentialByUsername(context.Background(), "admin")
		Expect(err).NotTo(HaveOccurred())
		Expect(owner.PasswordHash).To(BeEmpty())
		Expect(owner.PasswordDisabledAt).To(HaveValue(BeTemporally("==", disabledAt)))
	})

	It("rejects malformed persisted passkey data", func() {
		owner := createOwner()
		err := database.db.Create(&webauthnCredentialModel{
			ID:        []byte("invalid-credential"),
			OwnerID:   owner.ID,
			PublicKey: []byte("not-json"),
			CreatedAt: now,
		}).Error
		Expect(err).NotTo(HaveOccurred())

		_, err = database.WebAuthnCredentials(context.Background(), owner.ID)
		Expect(err).To(MatchError(ContainSubstring("decode passkey credential")))
	})

	It("creates, authenticates, rotates, and revokes owner sessions", func() {
		owner := createOwner()
		first := newPair()
		Expect(database.CreateSession(context.Background(), owner.ID, first)).To(Succeed())

		ownerID, err := database.AuthenticateOwnerSession(context.Background(), first.AccessToken, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(ownerID).To(Equal(owner.ID))

		second := newPair()
		Expect(database.RotateSession(context.Background(), first.RefreshToken, second, now)).To(Succeed())
		_, err = database.AuthenticateOwnerSession(context.Background(), first.AccessToken, now)
		Expect(errors.Is(err, pgx.ErrNoRows)).To(BeTrue())
		ownerID, err = database.AuthenticateOwnerSession(context.Background(), second.AccessToken, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(ownerID).To(Equal(owner.ID))

		Expect(database.RevokeSession(context.Background(), second.AccessToken, now)).To(Succeed())
		Expect(database.RevokeSession(context.Background(), second.AccessToken, now)).To(MatchError(pgx.ErrNoRows))
		Expect(database.RotateSession(context.Background(), first.RefreshToken, newPair(), now)).To(MatchError(pgx.ErrNoRows))
	})

	It("approves only the first app installation and makes retries idempotent", func() {
		owner := createOwner()
		first := AppInstallationRegistration{
			ID:                    "01900000-0000-7000-8000-000000000101",
			IdentityPublicKey:     bytes.Repeat([]byte{9}, 32),
			KeyAgreementPublicKey: bytes.Repeat([]byte{12}, 32),
			WrappedVaultKey:       []byte("first wrapped key"),
		}
		firstRecord, created, err := database.RegisterAppInstallation(context.Background(), owner.ID, first, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(created).To(BeTrue())
		Expect(firstRecord.ApprovedAt).NotTo(BeNil())
		Expect(firstRecord.ApprovedAt.Location()).To(Equal(time.UTC))
		Expect(firstRecord.CreatedAt.Location()).To(Equal(time.UTC))

		retried, created, err := database.RegisterAppInstallation(context.Background(), owner.ID, first, now.Add(time.Minute))
		Expect(err).NotTo(HaveOccurred())
		Expect(created).To(BeFalse())
		Expect(retried.ID).To(Equal(firstRecord.ID))
		Expect(retried.IdentityPublicKey).To(Equal(firstRecord.IdentityPublicKey))
		Expect(retried.KeyAgreementPublicKey).To(Equal(firstRecord.KeyAgreementPublicKey))
		Expect(retried.WrappedVaultKey).To(Equal(firstRecord.WrappedVaultKey))
		Expect(retried.ApprovedAt).NotTo(BeNil())
		Expect(*retried.ApprovedAt).To(BeTemporally("==", *firstRecord.ApprovedAt))
		Expect(retried.RevokedAt).To(BeNil())
		Expect(retried.CreatedAt).To(BeTemporally("==", firstRecord.CreatedAt))

		second := AppInstallationRegistration{
			ID:                    "01900000-0000-7000-8000-000000000102",
			IdentityPublicKey:     bytes.Repeat([]byte{10}, 32),
			KeyAgreementPublicKey: bytes.Repeat([]byte{13}, 32),
		}
		secondRecord, created, err := database.RegisterAppInstallation(context.Background(), owner.ID, second, now.Add(2*time.Minute))
		Expect(err).NotTo(HaveOccurred())
		Expect(created).To(BeTrue())
		Expect(secondRecord.ApprovedAt).To(BeNil())
		recipients, err := database.ListApprovedAppInstallationRecipients(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(recipients).To(Equal([]AppInstallationRecipient{{
			InstallationID:        first.ID,
			IdentityPublicKey:     first.IdentityPublicKey,
			KeyAgreementPublicKey: first.KeyAgreementPublicKey,
		}}))

		first.IdentityPublicKey = bytes.Repeat([]byte{11}, 32)
		_, _, err = database.RegisterAppInstallation(context.Background(), owner.ID, first, now)
		Expect(err).To(MatchError(domain.ErrInstallationConflict))
	})

	It("upgrades a legacy installation with a key agreement identity", func() {
		owner := createOwner()
		registration := AppInstallationRegistration{
			ID:                    "01900000-0000-7000-8000-000000000101",
			IdentityPublicKey:     bytes.Repeat([]byte{41}, 32),
			KeyAgreementPublicKey: bytes.Repeat([]byte{42}, 32),
			WrappedVaultKey:       []byte("legacy wrapped vault key"),
		}
		Expect(database.db.Create(&appInstallationModel{
			ID:                registration.ID,
			OwnerID:           owner.ID,
			IdentityPublicKey: registration.IdentityPublicKey,
			WrappedVaultKey:   registration.WrappedVaultKey,
			ApprovedAt:        ptrTime(now),
			CreatedAt:         now,
		}).Error).To(Succeed())

		record, created, err := database.RegisterAppInstallation(
			context.Background(), owner.ID, registration, now.Add(time.Minute),
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(created).To(BeFalse())
		Expect(record.KeyAgreementPublicKey).To(Equal(registration.KeyAgreementPublicKey))

		var persisted appInstallationModel
		Expect(database.db.First(&persisted, "id = ?", registration.ID).Error).To(Succeed())
		Expect(persisted.KeyAgreementPublicKey).To(Equal(registration.KeyAgreementPublicKey))
	})

	It("rejects malformed bearer credentials before querying PostgreSQL", func() {
		owner := createOwner()
		invalidPair := auth.TokenPair{AccessToken: "bad", RefreshToken: "bad"}
		Expect(database.CreateSession(context.Background(), owner.ID, invalidPair)).To(MatchError(auth.ErrInvalidToken))
		validPair := newPair()
		validPair.RefreshToken = "bad"
		Expect(database.CreateSession(context.Background(), owner.ID, validPair)).To(MatchError(auth.ErrInvalidToken))
		Expect(database.RotateSession(context.Background(), "bad", invalidPair, now)).To(MatchError(auth.ErrInvalidToken))
		validRefresh := newToken()
		Expect(database.RotateSession(context.Background(), validRefresh, invalidPair, now)).To(MatchError(auth.ErrInvalidToken))
		invalidReplacementRefresh := newPair()
		invalidReplacementRefresh.RefreshToken = "bad"
		Expect(database.RotateSession(context.Background(), validRefresh, invalidReplacementRefresh, now)).To(MatchError(auth.ErrInvalidToken))
		Expect(database.RevokeSession(context.Background(), "bad", now)).To(MatchError(auth.ErrInvalidToken))
		_, err := database.AuthenticateOwnerSession(context.Background(), "bad", now)
		Expect(err).To(MatchError(auth.ErrInvalidToken))
		_, err = database.AuthenticateDevice(context.Background(), firstDeviceID, "bad", now)
		Expect(err).To(MatchError(auth.ErrInvalidToken))
	})

	It("pairs a device once and safely returns the same binding on retry", func() {
		owner := createOwner()
		pairingToken := newToken()
		pairingHash, err := auth.TokenHash(pairingToken)
		Expect(err).NotTo(HaveOccurred())
		Expect(database.CreatePairingToken(context.Background(), owner.ID, pairingHash[:], now.Add(10*time.Minute))).To(Succeed())
		deviceCredential := newToken()
		credentialHash, err := auth.TokenHash(deviceCredential)
		Expect(err).NotTo(HaveOccurred())
		pairing := DevicePairing{
			DeviceUUID:        firstDeviceID,
			ProfileCiphertext: []byte("encrypted profile"),
			IdentityPublicKey: bytes.Repeat([]byte{5}, ed25519.PublicKeySize),
			CommandPublicKey:  bytes.Repeat([]byte{6}, 32),
			WrappedVaultKey:   []byte("wrapped key"),
			CredentialHash:    credentialHash[:],
		}

		created, err := database.ConsumePairingToken(context.Background(), pairingHash[:], now, pairing)
		Expect(err).NotTo(HaveOccurred())
		Expect(created.ID).To(Equal(firstDeviceID))
		Expect(created.NumericID).To(Equal(1))
		retried, err := database.ConsumePairingToken(context.Background(), pairingHash[:], now.Add(time.Minute), pairing)
		Expect(err).NotTo(HaveOccurred())
		Expect(retried.ID).To(Equal(created.ID))
		Expect(retried.NumericID).To(Equal(created.NumericID))

		publicKey, err := database.AuthenticateDevice(context.Background(), firstDeviceID, deviceCredential, now.Add(2*time.Minute))
		Expect(err).NotTo(HaveOccurred())
		Expect(publicKey).To(Equal(pairing.IdentityPublicKey))
		_, err = database.AuthenticateDevice(context.Background(), firstDeviceID, newToken(), now)
		Expect(errors.Is(err, pgx.ErrNoRows)).To(BeTrue())

		wrongPairing := pairing
		wrongPairing.CredentialHash = bytes.Repeat([]byte{8}, len(pairing.CredentialHash))
		_, err = database.ConsumePairingToken(context.Background(), pairingHash[:], now, wrongPairing)
		Expect(errors.Is(err, domain.ErrInvalidPairingToken)).To(BeTrue())
	})

	It("opens, closes, and replaces the owner device discovery window", func() {
		owner := createOwner()

		session, err := database.OpenDeviceDiscovery(context.Background(), owner.ID, now, now.Add(10*time.Minute))
		Expect(err).NotTo(HaveOccurred())
		Expect(session.OwnerID).To(Equal(owner.ID))
		Expect(session.Active(now.Add(9 * time.Minute))).To(BeTrue())
		Expect(session.Active(now.Add(10 * time.Minute))).To(BeFalse())

		loaded, err := database.GetDeviceDiscoverySession(context.Background(), owner.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(loaded.OwnerID).To(Equal(session.OwnerID))
		Expect(loaded.OpenedAt).To(BeTemporally("==", session.OpenedAt))
		Expect(loaded.ExpiresAt).To(BeTemporally("==", session.ExpiresAt))
		Expect(loaded.ClosedAt).To(BeNil())
		Expect(database.CloseDeviceDiscovery(context.Background(), owner.ID, now.Add(time.Minute))).To(Succeed())
		closed, err := database.GetDeviceDiscoverySession(context.Background(), owner.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(closed.Active(now.Add(2 * time.Minute))).To(BeFalse())

		reopened, err := database.OpenDeviceDiscovery(context.Background(), owner.ID, now.Add(3*time.Minute), now.Add(13*time.Minute))
		Expect(err).NotTo(HaveOccurred())
		Expect(reopened.ClosedAt).To(BeNil())
		Expect(reopened.Active(now.Add(4 * time.Minute))).To(BeTrue())
		_, err = database.OpenDeviceDiscovery(context.Background(), owner.ID, now, now)
		Expect(err).To(MatchError("device discovery expiration must be after its opening time"))
	})

	It("records idempotent candidates only while discovery is active", func() {
		owner := createOwner()
		candidate := DeviceDiscoveryRequest{
			DeviceUUID: firstDeviceID, IdentityPublicKey: bytes.Repeat([]byte{1}, ed25519.PublicKeySize),
			CommandPublicKey: bytes.Repeat([]byte{2}, 32), CredentialHash: bytes.Repeat([]byte{3}, 32),
		}

		_, err := database.SubmitDeviceDiscovery(context.Background(), now, candidate)
		Expect(errors.Is(err, domain.ErrDeviceDiscoveryClosed)).To(BeTrue())
		_, err = database.OpenDeviceDiscovery(context.Background(), owner.ID, now, now.Add(10*time.Minute))
		Expect(err).NotTo(HaveOccurred())
		created, err := database.SubmitDeviceDiscovery(context.Background(), now.Add(time.Minute), candidate)
		Expect(err).NotTo(HaveOccurred())
		Expect(created.DeviceUUID).To(Equal(firstDeviceID))
		Expect(created.Status).To(Equal(DeviceDiscoveryPending))
		Expect(created.ExpiresAt).To(BeTemporally("==", now.Add(10*time.Minute)))

		retried, err := database.SubmitDeviceDiscovery(context.Background(), now.Add(2*time.Minute), candidate)
		Expect(err).NotTo(HaveOccurred())
		Expect(retried.FirstSeenAt).To(BeTemporally("==", created.FirstSeenAt))
		Expect(retried.LastSeenAt).To(BeTemporally("==", now.Add(2*time.Minute)))
		candidate.CommandPublicKey = bytes.Repeat([]byte{9}, 32)
		_, err = database.SubmitDeviceDiscovery(context.Background(), now.Add(3*time.Minute), candidate)
		Expect(errors.Is(err, domain.ErrDeviceDiscoveryConflict)).To(BeTrue())
		candidate.DeviceUUID = secondDeviceID
		_, err = database.SubmitDeviceDiscovery(context.Background(), now.Add(3*time.Minute), candidate)
		Expect(errors.Is(err, domain.ErrDeviceDiscoveryConflict)).To(BeTrue())

		pending, err := database.ListPendingDeviceDiscoveries(context.Background(), owner.ID, now.Add(3*time.Minute))
		Expect(err).NotTo(HaveOccurred())
		Expect(pending).To(HaveLen(1))
		Expect(pending[0].DeviceUUID).To(Equal(firstDeviceID))
		Expect(pending[0].IdentityPublicKey).To(Equal(bytes.Repeat([]byte{1}, ed25519.PublicKeySize)))
	})

	It("approves one discovery atomically and retains its result after closing discovery", func() {
		owner := createOwner()
		credentialHash := bytes.Repeat([]byte{3}, 32)
		_, err := database.OpenDeviceDiscovery(context.Background(), owner.ID, now, now.Add(10*time.Minute))
		Expect(err).NotTo(HaveOccurred())
		_, err = database.SubmitDeviceDiscovery(context.Background(), now, DeviceDiscoveryRequest{
			DeviceUUID: firstDeviceID, IdentityPublicKey: bytes.Repeat([]byte{1}, ed25519.PublicKeySize),
			CommandPublicKey: bytes.Repeat([]byte{2}, 32), CredentialHash: credentialHash,
		})
		Expect(err).NotTo(HaveOccurred())

		approved, err := database.ApproveDeviceDiscovery(context.Background(), owner.ID, firstDeviceID, now.Add(time.Minute))
		Expect(err).NotTo(HaveOccurred())
		Expect(approved.NumericID).To(Equal(1))
		Expect(approved.IdentityPublicKey).To(Equal(bytes.Repeat([]byte{1}, ed25519.PublicKeySize)))
		Expect(approved.CommandPublicKey).To(Equal(bytes.Repeat([]byte{2}, 32)))
		retried, err := database.ApproveDeviceDiscovery(context.Background(), owner.ID, firstDeviceID, now.Add(2*time.Minute))
		Expect(err).NotTo(HaveOccurred())
		Expect(retried.NumericID).To(Equal(approved.NumericID))

		Expect(database.CloseDeviceDiscovery(context.Background(), owner.ID, now.Add(3*time.Minute))).To(Succeed())
		status, err := database.DeviceDiscoveryStatus(context.Background(), firstDeviceID, credentialHash, now.Add(4*time.Minute))
		Expect(err).NotTo(HaveOccurred())
		Expect(status.Status).To(Equal(DeviceDiscoveryApproved))
		Expect(status.NumericID).To(Equal(1))
		_, err = database.DeviceDiscoveryStatus(context.Background(), firstDeviceID, bytes.Repeat([]byte{8}, 32), now.Add(4*time.Minute))
		Expect(errors.Is(err, pgx.ErrNoRows)).To(BeTrue())
	})

	It("binds the discovered physical slot and SIM fingerprint during approval", func() {
		owner := createOwner()
		fingerprint := bytes.Repeat([]byte{7}, 32)
		_, err := database.OpenDeviceDiscovery(context.Background(), owner.ID, now, now.Add(10*time.Minute))
		Expect(err).NotTo(HaveOccurred())
		_, err = database.SubmitDeviceDiscovery(context.Background(), now, DeviceDiscoveryRequest{
			DeviceUUID: firstDeviceID, SlotKey: "usb-primary", SlotIndex: 2, SIMFingerprint: fingerprint,
			IdentityPublicKey: bytes.Repeat([]byte{1}, ed25519.PublicKeySize),
			CommandPublicKey:  bytes.Repeat([]byte{2}, 32), CredentialHash: bytes.Repeat([]byte{3}, 32),
		})
		Expect(err).NotTo(HaveOccurred())

		approved, err := database.ApproveDeviceDiscovery(context.Background(), owner.ID, firstDeviceID, now.Add(time.Minute))

		Expect(err).NotTo(HaveOccurred())
		Expect(approved.Slots).To(HaveLen(1))
		Expect(approved.Slots[0].SlotKey).To(Equal("usb-primary"))
		Expect(approved.Slots[0].SlotIndex).To(Equal(2))
		Expect(approved.Slots[0].SIMID).To(HaveValue(Equal(firstDeviceID)))
		var storedSIM simCardModel
		Expect(database.db.First(&storedSIM, "id = ?", firstDeviceID).Error).To(Succeed())
		Expect(storedSIM.ICCIDFingerprint).To(Equal(fingerprint))
		Expect(storedSIM.FingerprintScope).To(HaveValue(Equal(firstDeviceID)))
	})

	It("approves a replacement device when its SIM is already known", func() {
		firstDevice := createDevice(firstDeviceID)
		fingerprint := bytes.Repeat([]byte{67}, 32)
		firstSlot := firstDevice.Slots[0]
		firstAssignment, err := database.UpdateDeviceSIMIdentity(
			context.Background(), firstDevice.ID, firstSlot.SlotKey, firstSlot.SlotIndex, fingerprint, now,
		)
		Expect(err).NotTo(HaveOccurred())
		owner := createOwner()
		_, err = database.OpenDeviceDiscovery(context.Background(), owner.ID, now, now.Add(10*time.Minute))
		Expect(err).NotTo(HaveOccurred())
		_, err = database.SubmitDeviceDiscovery(context.Background(), now, DeviceDiscoveryRequest{
			DeviceUUID: secondDeviceID, SlotKey: "1", SlotIndex: 1, SIMFingerprint: fingerprint,
			IdentityPublicKey: bytes.Repeat([]byte{2}, ed25519.PublicKeySize),
			CommandPublicKey:  bytes.Repeat([]byte{3}, 32), CredentialHash: bytes.Repeat([]byte{4}, 32),
		})
		Expect(err).NotTo(HaveOccurred())

		approved, err := database.ApproveDeviceDiscovery(context.Background(), owner.ID, secondDeviceID, now.Add(time.Minute))

		Expect(err).NotTo(HaveOccurred())
		Expect(approved.Slots).To(HaveLen(1))
		movedAssignment, err := database.UpdateDeviceSIMIdentity(
			context.Background(), secondDeviceID, approved.Slots[0].SlotKey, approved.Slots[0].SlotIndex,
			fingerprint, now.Add(2*time.Minute),
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(movedAssignment.SIMID).To(Equal(firstAssignment.SIMID))
		Expect(movedAssignment.NumericID).To(Equal(firstAssignment.NumericID))
	})

	It("rejects approval after the candidate discovery expires", func() {
		owner := createOwner()
		_, err := database.OpenDeviceDiscovery(context.Background(), owner.ID, now, now.Add(time.Minute))
		Expect(err).NotTo(HaveOccurred())
		_, err = database.SubmitDeviceDiscovery(context.Background(), now, DeviceDiscoveryRequest{
			DeviceUUID: firstDeviceID, IdentityPublicKey: bytes.Repeat([]byte{1}, ed25519.PublicKeySize),
			CommandPublicKey: bytes.Repeat([]byte{2}, 32), CredentialHash: bytes.Repeat([]byte{3}, 32),
		})
		Expect(err).NotTo(HaveOccurred())

		_, err = database.ApproveDeviceDiscovery(context.Background(), owner.ID, firstDeviceID, now.Add(2*time.Minute))

		Expect(errors.Is(err, domain.ErrDeviceDiscoveryExpired)).To(BeTrue())
	})

	It("rejects approval when the candidate UUID was paired concurrently", func() {
		owner := createOwner()
		_, err := database.OpenDeviceDiscovery(context.Background(), owner.ID, now, now.Add(10*time.Minute))
		Expect(err).NotTo(HaveOccurred())
		_, err = database.SubmitDeviceDiscovery(context.Background(), now, DeviceDiscoveryRequest{
			DeviceUUID: firstDeviceID, IdentityPublicKey: bytes.Repeat([]byte{1}, ed25519.PublicKeySize),
			CommandPublicKey: bytes.Repeat([]byte{2}, 32), CredentialHash: bytes.Repeat([]byte{3}, 32),
		})
		Expect(err).NotTo(HaveOccurred())
		createDevice(firstDeviceID)

		_, err = database.ApproveDeviceDiscovery(context.Background(), owner.ID, firstDeviceID, now.Add(time.Minute))

		Expect(errors.Is(err, domain.ErrDeviceExists)).To(BeTrue())
	})

	It("reports exhausted SIM identifiers during discovery approval", func() {
		owner := createOwner()
		_, err := database.OpenDeviceDiscovery(context.Background(), owner.ID, now, now.Add(10*time.Minute))
		Expect(err).NotTo(HaveOccurred())
		_, err = database.SubmitDeviceDiscovery(context.Background(), now, DeviceDiscoveryRequest{
			DeviceUUID: firstDeviceID, IdentityPublicKey: bytes.Repeat([]byte{1}, ed25519.PublicKeySize),
			CommandPublicKey: bytes.Repeat([]byte{2}, 32), CredentialHash: bytes.Repeat([]byte{3}, 32),
		})
		Expect(err).NotTo(HaveOccurred())
		cards := make([]simCardModel, 0, domain.MaxNumericID-domain.MinNumericID+1)
		for numericID := domain.MinNumericID; numericID <= domain.MaxNumericID; numericID++ {
			cards = append(cards, simCardModel{
				ID: uuid.NewString(), NumericID: numericID, ProfileCiphertext: []byte{},
				FirstSeenAt: now, LastSeenAt: now, CreatedAt: now,
			})
		}
		Expect(database.db.CreateInBatches(&cards, 100).Error).To(Succeed())

		_, err = database.ApproveDeviceDiscovery(context.Background(), owner.ID, firstDeviceID, now.Add(time.Minute))

		Expect(errors.Is(err, domain.ErrNoNumericID)).To(BeTrue())
	})

	It("replaces expired discovery evidence but rejects an already paired device", func() {
		owner := createOwner()
		firstCandidate := DeviceDiscoveryRequest{
			DeviceUUID: firstDeviceID, IdentityPublicKey: bytes.Repeat([]byte{1}, ed25519.PublicKeySize),
			CommandPublicKey: bytes.Repeat([]byte{2}, 32), CredentialHash: bytes.Repeat([]byte{3}, 32),
		}
		_, err := database.OpenDeviceDiscovery(context.Background(), owner.ID, now, now.Add(time.Minute))
		Expect(err).NotTo(HaveOccurred())
		_, err = database.SubmitDeviceDiscovery(context.Background(), now, firstCandidate)
		Expect(err).NotTo(HaveOccurred())
		_, err = database.OpenDeviceDiscovery(context.Background(), owner.ID, now.Add(2*time.Minute), now.Add(12*time.Minute))
		Expect(err).NotTo(HaveOccurred())
		replacement := firstCandidate
		replacement.IdentityPublicKey = bytes.Repeat([]byte{4}, ed25519.PublicKeySize)
		replaced, err := database.SubmitDeviceDiscovery(context.Background(), now.Add(2*time.Minute), replacement)
		Expect(err).NotTo(HaveOccurred())
		Expect(replaced.IdentityPublicKey).To(Equal(replacement.IdentityPublicKey))
		Expect(replaced.FirstSeenAt).To(BeTemporally("==", now.Add(2*time.Minute)))

		createDevice(secondDeviceID)
		replacement.DeviceUUID = secondDeviceID
		_, err = database.SubmitDeviceDiscovery(context.Background(), now.Add(3*time.Minute), replacement)
		Expect(errors.Is(err, domain.ErrDeviceExists)).To(BeTrue())
	})

	It("rejects a pending discovery without creating a device", func() {
		owner := createOwner()
		credentialHash := bytes.Repeat([]byte{4}, 32)
		_, err := database.OpenDeviceDiscovery(context.Background(), owner.ID, now, now.Add(10*time.Minute))
		Expect(err).NotTo(HaveOccurred())
		_, err = database.SubmitDeviceDiscovery(context.Background(), now, DeviceDiscoveryRequest{
			DeviceUUID: secondDeviceID, IdentityPublicKey: bytes.Repeat([]byte{5}, ed25519.PublicKeySize),
			CommandPublicKey: bytes.Repeat([]byte{6}, 32), CredentialHash: credentialHash,
		})
		Expect(err).NotTo(HaveOccurred())

		Expect(database.RejectDeviceDiscovery(context.Background(), owner.ID, secondDeviceID, now.Add(time.Minute))).To(Succeed())
		Expect(database.RejectDeviceDiscovery(context.Background(), owner.ID, secondDeviceID, now.Add(2*time.Minute))).To(Succeed())
		status, err := database.DeviceDiscoveryStatus(context.Background(), secondDeviceID, credentialHash, now.Add(3*time.Minute))
		Expect(err).NotTo(HaveOccurred())
		Expect(status.Status).To(Equal(DeviceDiscoveryRejected))
		devices, err := database.ListDevices(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(devices).To(BeEmpty())
		_, err = database.ApproveDeviceDiscovery(context.Background(), owner.ID, secondDeviceID, now.Add(3*time.Minute))
		Expect(errors.Is(err, domain.ErrDeviceDiscoveryRejected)).To(BeTrue())
	})

	It("distinguishes missing, expired, and already approved discovery rejections", func() {
		owner := createOwner()
		Expect(database.RejectDeviceDiscovery(context.Background(), owner.ID, firstDeviceID, now)).To(MatchError(pgx.ErrNoRows))
		_, err := database.OpenDeviceDiscovery(context.Background(), owner.ID, now, now.Add(10*time.Minute))
		Expect(err).NotTo(HaveOccurred())

		for index, deviceID := range []string{firstDeviceID, secondDeviceID} {
			_, err = database.SubmitDeviceDiscovery(context.Background(), now, DeviceDiscoveryRequest{
				DeviceUUID: deviceID, IdentityPublicKey: bytes.Repeat([]byte{byte(index + 1)}, ed25519.PublicKeySize),
				CommandPublicKey: bytes.Repeat([]byte{byte(index + 3)}, 32), CredentialHash: bytes.Repeat([]byte{byte(index + 5)}, 32),
			})
			Expect(err).NotTo(HaveOccurred())
		}

		_, err = database.ApproveDeviceDiscovery(context.Background(), owner.ID, firstDeviceID, now.Add(time.Minute))
		Expect(err).NotTo(HaveOccurred())
		Expect(database.RejectDeviceDiscovery(context.Background(), owner.ID, firstDeviceID, now.Add(2*time.Minute))).To(
			MatchError(domain.ErrDeviceExists),
		)
		Expect(database.RejectDeviceDiscovery(context.Background(), owner.ID, secondDeviceID, now.Add(11*time.Minute))).To(
			MatchError(domain.ErrDeviceDiscoveryExpired),
		)
	})

	It("rejects expired, unknown, and conflicting pairing tokens", func() {
		owner := createOwner()
		expired := newToken()
		expiredHash, err := auth.TokenHash(expired)
		Expect(err).NotTo(HaveOccurred())
		Expect(database.CreatePairingToken(context.Background(), owner.ID, expiredHash[:], now.Add(-time.Minute))).To(Succeed())
		pairing := DevicePairing{
			DeviceUUID:        firstDeviceID,
			ProfileCiphertext: []byte{},
			IdentityPublicKey: bytes.Repeat([]byte{1}, ed25519.PublicKeySize),
			CredentialHash:    bytes.Repeat([]byte{1}, 32),
		}
		_, err = database.ConsumePairingToken(context.Background(), expiredHash[:], now, pairing)
		Expect(errors.Is(err, domain.ErrInvalidPairingToken)).To(BeTrue())

		unknownHash := bytes.Repeat([]byte{9}, 32)
		_, err = database.ConsumePairingToken(context.Background(), unknownHash, now, pairing)
		Expect(errors.Is(err, domain.ErrInvalidPairingToken)).To(BeTrue())

		valid := newToken()
		validHash, err := auth.TokenHash(valid)
		Expect(err).NotTo(HaveOccurred())
		Expect(database.CreatePairingToken(context.Background(), owner.ID, validHash[:], now.Add(time.Minute))).To(Succeed())
		_, err = database.ConsumePairingToken(context.Background(), validHash[:], now, pairing)
		Expect(err).NotTo(HaveOccurred())
		pairing.DeviceUUID = secondDeviceID
		_, err = database.ConsumePairingToken(context.Background(), validHash[:], now, pairing)
		Expect(errors.Is(err, domain.ErrInvalidPairingToken)).To(BeTrue())
	})

	It("rejects a new pairing attempt for an existing device UUID", func() {
		owner := createOwner()
		createDevice(firstDeviceID)
		token := newToken()
		tokenHash, err := auth.TokenHash(token)
		Expect(err).NotTo(HaveOccurred())
		Expect(database.CreatePairingToken(context.Background(), owner.ID, tokenHash[:], now.Add(time.Minute))).To(Succeed())
		credentialHash, err := auth.TokenHash(newToken())
		Expect(err).NotTo(HaveOccurred())

		_, err = database.ConsumePairingToken(context.Background(), tokenHash[:], now, DevicePairing{
			DeviceUUID:        firstDeviceID,
			ProfileCiphertext: []byte{},
			IdentityPublicKey: bytes.Repeat([]byte{1}, ed25519.PublicKeySize),
			CredentialHash:    credentialHash[:],
		})

		Expect(errors.Is(err, domain.ErrDeviceExists)).To(BeTrue())
	})

	It("allocates, renumbers, and lists active devices", func() {
		second := createDevice(secondDeviceID)
		first := createDevice(firstDeviceID)
		Expect(second.NumericID).To(Equal(1))
		Expect(first.NumericID).To(Equal(2))

		Expect(database.UpdateNumericID(context.Background(), firstDeviceID, 5)).To(Succeed())
		Expect(database.UpdateNumericID(context.Background(), secondDeviceID, 5)).To(MatchError(domain.ErrDuplicateNumericID))
		Expect(database.UpdateNumericID(context.Background(), firstDeviceID, 0)).To(MatchError(domain.ErrInvalidNumericID))
		Expect(database.UpdateNumericID(context.Background(), "01900000-0000-7000-8000-000000000099", 6)).To(MatchError(pgx.ErrNoRows))

		records, err := database.ListDevices(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(records).To(HaveLen(2))
		Expect(records[0].NumericID).To(Equal(1))
		Expect(records[0].RadioAccessTechnology).To(BeEmpty())
		Expect(records[0].SignalBars).To(BeNil())
		Expect(records[0].RadioStatusUpdatedAt).To(BeNil())
		Expect(records[1].NumericID).To(Equal(5))
	})

	It("updates encrypted profiles and immediately revokes device credentials", func() {
		owner := createOwner()
		pairingToken := newToken()
		pairingHash, err := auth.TokenHash(pairingToken)
		Expect(err).NotTo(HaveOccurred())
		Expect(database.CreatePairingToken(context.Background(), owner.ID, pairingHash[:], now.Add(time.Minute))).To(Succeed())
		deviceCredential := newToken()
		credentialHash, err := auth.TokenHash(deviceCredential)
		Expect(err).NotTo(HaveOccurred())
		_, err = database.ConsumePairingToken(context.Background(), pairingHash[:], now, DevicePairing{
			DeviceUUID:        firstDeviceID,
			ProfileCiphertext: []byte("initial encrypted profile"),
			IdentityPublicKey: bytes.Repeat([]byte{4}, ed25519.PublicKeySize),
			CredentialHash:    credentialHash[:],
		})
		Expect(err).NotTo(HaveOccurred())

		Expect(database.UpdateDeviceProfile(context.Background(), firstDeviceID, []byte("updated encrypted profile"))).To(Succeed())
		records, err := database.ListDevices(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(records).To(HaveLen(1))
		Expect(records[0].ProfileCiphertext).To(Equal([]byte("updated encrypted profile")))

		Expect(database.RevokeDevice(context.Background(), firstDeviceID, now.Add(time.Minute))).To(Succeed())
		records, err = database.ListDevices(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(records).To(BeEmpty())
		var slotCount int64
		Expect(database.db.Model(&deviceSlotModel{}).Where("device_id = ?", firstDeviceID).Count(&slotCount).Error).To(Succeed())
		Expect(slotCount).To(BeZero())
		cards, err := database.ListSIMCards(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(cards).To(HaveLen(1))
		Expect(cards[0].AssignedSlotID).To(BeNil())
		Expect(database.RetireSIMCard(context.Background(), cards[0].ID, now.Add(2*time.Minute))).To(Succeed())
		cards, err = database.ListSIMCards(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(cards).To(BeEmpty())
		_, err = database.AuthenticateDevice(context.Background(), firstDeviceID, deviceCredential, now.Add(2*time.Minute))
		Expect(errors.Is(err, pgx.ErrNoRows)).To(BeTrue())
		Expect(database.UpdateDeviceProfile(context.Background(), firstDeviceID, []byte("late update"))).To(MatchError(pgx.ErrNoRows))
		Expect(database.RevokeDevice(context.Background(), firstDeviceID, now.Add(3*time.Minute))).To(MatchError(pgx.ErrNoRows))
	})

	It("tracks radio state and routes encrypted commands through their full lifecycle", func() {
		owner := createOwner()
		installation := AppInstallationRegistration{
			ID:                    "01900000-0000-7000-8000-000000000101",
			IdentityPublicKey:     bytes.Repeat([]byte{31}, 32),
			KeyAgreementPublicKey: bytes.Repeat([]byte{32}, 32),
		}
		_, created, err := database.RegisterAppInstallation(context.Background(), owner.ID, installation, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(created).To(BeTrue())
		device := createDevice(firstDeviceID)
		commandKey := bytes.Repeat([]byte{21}, 32)
		signalBars := 3
		Expect(database.UpdateDeviceCommandPublicKey(context.Background(), device.ID, commandKey)).To(Succeed())
		Expect(database.UpdateDeviceRadioStatus(context.Background(), device.ID, "1", 1, "5G", &signalBars, now)).To(Succeed())

		records, err := database.ListDevices(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(records).To(HaveLen(1))
		Expect(records[0].CommandPublicKey).To(Equal(commandKey))
		Expect(records[0].RadioAccessTechnology).To(Equal("5G"))
		Expect(records[0].SignalBars).NotTo(BeNil())
		Expect(*records[0].SignalBars).To(Equal(3))
		Expect(records[0].RadioStatusUpdatedAt).NotTo(BeNil())
		Expect(*records[0].RadioStatusUpdatedAt).To(BeTemporally("==", now))
		Expect(records[0].Slots).To(HaveLen(1))
		Expect(records[0].Slots[0].RadioAccessTechnology).To(Equal("5G"))
		Expect(records[0].Slots[0].SignalBars).NotTo(BeNil())
		Expect(*records[0].Slots[0].SignalBars).To(Equal(3))
		Expect(records[0].Slots[0].RadioStatusUpdatedAt).NotTo(BeNil())
		Expect(*records[0].Slots[0].RadioStatusUpdatedAt).To(BeTemporally("==", now))

		encrypted := domain.EncryptedCommand{
			ID:                   "01900000-0000-7000-8000-000000000050",
			DeviceUUID:           device.ID,
			SenderInstallationID: installation.ID,
			SenderPublicKey:      bytes.Repeat([]byte{22}, 32),
			Nonce:                bytes.Repeat([]byte{23}, 12),
			Ciphertext:           []byte("opaque dial command"),
			Signature:            bytes.Repeat([]byte{24}, ed25519.SignatureSize),
			CreatedAt:            now,
			ExpiresAt:            now.Add(time.Hour),
		}
		inserted, err := database.EnqueueCommand(context.Background(), encrypted)
		Expect(err).NotTo(HaveOccurred())
		Expect(inserted).To(BeTrue())

		inserted, err = database.EnqueueCommand(context.Background(), encrypted)
		Expect(err).NotTo(HaveOccurred())
		Expect(inserted).To(BeFalse())

		conflicting := encrypted
		conflicting.Ciphertext = []byte("different ciphertext")
		_, err = database.EnqueueCommand(context.Background(), conflicting)
		Expect(errors.Is(err, domain.ErrIdempotencyConflict)).To(BeTrue())

		status, err := database.CommandStatus(context.Background(), device.ID, encrypted.ID, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(status.State).To(Equal("pending"))
		Expect(status.Result).To(BeNil())

		leaseUntil := now.Add(30 * time.Second)
		claimed, err := database.ClaimCommand(context.Background(), device.ID, now, leaseUntil)
		Expect(err).NotTo(HaveOccurred())
		Expect(claimed.ID).To(Equal(encrypted.ID))
		Expect(claimed.DeviceUUID).To(Equal(encrypted.DeviceUUID))
		Expect(claimed.SenderInstallationID).To(Equal(encrypted.SenderInstallationID))
		Expect(claimed.SenderPublicKey).To(Equal(encrypted.SenderPublicKey))
		Expect(claimed.Nonce).To(Equal(encrypted.Nonce))
		Expect(claimed.Ciphertext).To(Equal(encrypted.Ciphertext))
		Expect(claimed.Signature).To(Equal(encrypted.Signature))
		Expect(claimed.CreatedAt).To(BeTemporally("==", encrypted.CreatedAt))
		Expect(claimed.ExpiresAt).To(BeTemporally("==", encrypted.ExpiresAt))
		_, err = database.ClaimCommand(context.Background(), device.ID, now.Add(time.Second), leaseUntil)
		Expect(errors.Is(err, pgx.ErrNoRows)).To(BeTrue())

		status, err = database.CommandStatus(context.Background(), device.ID, encrypted.ID, now.Add(time.Second))
		Expect(err).NotTo(HaveOccurred())
		Expect(status.State).To(Equal("processing"))

		result := domain.EncryptedCommandResult{
			Nonce:      bytes.Repeat([]byte{24}, 12),
			Ciphertext: []byte("opaque modem result"),
		}
		completedAt := now.Add(2 * time.Second)
		Expect(database.CompleteCommand(context.Background(), device.ID, encrypted.ID, result, completedAt)).To(Succeed())
		Expect(database.CompleteCommand(context.Background(), device.ID, encrypted.ID, result, completedAt)).To(MatchError(pgx.ErrNoRows))

		status, err = database.CommandStatus(context.Background(), device.ID, encrypted.ID, now.Add(3*time.Second))
		Expect(err).NotTo(HaveOccurred())
		Expect(status.State).To(Equal("completed"))
		Expect(status.Result).To(Equal(&result))
		Expect(status.CompletedAt).NotTo(BeNil())
		Expect(*status.CompletedAt).To(BeTemporally("==", completedAt))

		_, err = database.ClaimCommand(context.Background(), device.ID, now.Add(3*time.Second), now.Add(time.Minute))
		Expect(errors.Is(err, pgx.ErrNoRows)).To(BeTrue())
		Expect(database.UpdateDeviceCommandPublicKey(context.Background(), secondDeviceID, commandKey)).To(MatchError(pgx.ErrNoRows))
		Expect(database.UpdateDeviceRadioStatus(context.Background(), secondDeviceID, "1", 1, "LTE", nil, now)).To(MatchError(pgx.ErrNoRows))

		unknownDeviceCommand := encrypted
		unknownDeviceCommand.ID = "01900000-0000-7000-8000-000000000051"
		unknownDeviceCommand.DeviceUUID = secondDeviceID
		_, err = database.EnqueueCommand(context.Background(), unknownDeviceCommand)
		Expect(errors.Is(err, pgx.ErrNoRows)).To(BeTrue())

		unapproved := AppInstallationRegistration{
			ID:                    "01900000-0000-7000-8000-000000000102",
			IdentityPublicKey:     bytes.Repeat([]byte{41}, 32),
			KeyAgreementPublicKey: bytes.Repeat([]byte{42}, 32),
		}
		_, created, err = database.RegisterAppInstallation(context.Background(), owner.ID, unapproved, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(created).To(BeTrue())
		untrustedCommand := encrypted
		untrustedCommand.ID = "01900000-0000-7000-8000-000000000052"
		untrustedCommand.SenderInstallationID = unapproved.ID
		_, err = database.EnqueueCommand(context.Background(), untrustedCommand)
		Expect(errors.Is(err, pgx.ErrNoRows)).To(BeTrue())
	})

	It("keeps physical slot identity separate from replaceable SIM identity", func() {
		device := createDevice(firstDeviceID)
		firstFingerprint := bytes.Repeat([]byte{61}, 32)
		secondFingerprint := bytes.Repeat([]byte{62}, 32)
		slots, err := database.ListDevices(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(slots[0].Slots).To(HaveLen(1))
		slotKey := slots[0].Slots[0].SlotKey
		slotIndex := slots[0].Slots[0].SlotIndex
		firstAssignment, err := database.UpdateDeviceSIMIdentity(context.Background(), device.ID, slotKey, slotIndex, firstFingerprint, now)
		Expect(err).NotTo(HaveOccurred())
		firstSIM, firstNumeric := firstAssignment.SIMID, firstAssignment.NumericID
		Expect(firstAssignment.SlotID).To(Equal(slots[0].Slots[0].ID))
		Expect(database.UpdateSIMNumericID(context.Background(), firstSIM, 7)).To(Succeed())
		firstNumeric = 7
		Expect(database.UpdateSIMProfile(context.Background(), firstSIM, []byte("encrypted line profile"))).To(Succeed())
		listed, err := database.ListDevices(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(listed[0].Slots[0].SIMNumericID).To(HaveValue(Equal(7)))
		Expect(listed[0].Slots[0].ProfileCiphertext).To(Equal([]byte("encrypted line profile")))
		Expect(database.db.Create(&simCardModel{
			ID: uuid.NewString(), NumericID: 8, ProfileCiphertext: []byte{},
			FirstSeenAt: now, LastSeenAt: now, CreatedAt: now,
		}).Error).To(Succeed())
		Expect(database.UpdateSIMNumericID(context.Background(), firstSIM, 8)).To(MatchError(domain.ErrDuplicateNumericID))
		secondAssignment, err := database.UpdateDeviceSIMIdentity(context.Background(), device.ID, slotKey, slotIndex, secondFingerprint, now.Add(time.Minute))
		Expect(err).NotTo(HaveOccurred())
		secondSIM, secondNumeric := secondAssignment.SIMID, secondAssignment.NumericID
		Expect(secondSIM).NotTo(Equal(firstSIM))
		Expect(secondNumeric).NotTo(Equal(firstNumeric))
		Expect(database.UpdateSIMNumericID(context.Background(), firstSIM, 9)).To(Succeed())
		firstNumeric = 9
		Expect(database.UpdateSIMProfile(context.Background(), firstSIM, []byte("offline"))).To(Succeed())
		cards, err := database.ListSIMCards(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(cards).To(ContainElement(And(
			HaveField("ID", firstSIM),
			HaveField("AssignedSlotID", BeNil()),
		)))
		Expect(database.RetireSIMCard(context.Background(), secondSIM, now.Add(90*time.Second))).To(MatchError(domain.ErrSIMAssigned))
		missingSIM := "01900000-0000-7000-8000-000000000199"
		Expect(database.UpdateSIMNumericID(context.Background(), missingSIM, 9)).To(MatchError(ContainSubstring(pgx.ErrNoRows.Error())))
		Expect(database.UpdateSIMProfile(context.Background(), missingSIM, []byte("missing"))).To(MatchError(ContainSubstring(pgx.ErrNoRows.Error())))
		reusedAssignment, err := database.UpdateDeviceSIMIdentity(context.Background(), device.ID, slotKey, slotIndex, firstFingerprint, now.Add(2*time.Minute))
		Expect(err).NotTo(HaveOccurred())
		Expect(reusedAssignment.SIMID).To(Equal(firstSIM))
		Expect(reusedAssignment.NumericID).To(Equal(firstNumeric))
		listed, err = database.ListDevices(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(listed[0].Slots[0].SIMID).To(HaveValue(Equal(firstSIM)))
		var assignmentCount int64
		Expect(database.db.Model(&slotSIMAssignmentModel{}).Where("slot_id = ?", listed[0].Slots[0].ID).Count(&assignmentCount).Error).To(Succeed())
		Expect(assignmentCount).To(Equal(int64(3)))
	})

	It("manages eSIM profiles for one SIM", func() {
		createDevice(firstDeviceID)
		devices, err := database.ListDevices(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(devices[0].Slots[0].SIMID).NotTo(BeNil())
		simID := *devices[0].Slots[0].SIMID
		_, err = database.ListESIMProfiles(context.Background(), simID)
		Expect(err).NotTo(HaveOccurred())

		_, err = database.UpsertESIMProfile(context.Background(), ESIMProfileRecord{})
		Expect(errors.Is(err, domain.ErrInvalidESIMProfile)).To(BeTrue())
		for _, invalid := range []ESIMProfileRecord{
			{SIMID: "01900000-0000-7000-8000-000000000199", ProfileID: "missing-sim", Name: "Test"},
			{SIMID: simID, ProfileID: strings.Repeat("x", 257)},
			{SIMID: simID, ProfileID: "too-long-name", Name: strings.Repeat("x", 129)},
			{SIMID: simID, ProfileID: "too-long-iccid", ICCID: strings.Repeat("1", 33)},
			{SIMID: simID, ProfileID: "too-long-operator", OperatorName: strings.Repeat("x", 129)},
		} {
			_, err = database.UpsertESIMProfile(context.Background(), invalid)
			if invalid.ProfileID == "missing-sim" {
				Expect(errors.Is(err, pgx.ErrNoRows)).To(BeTrue())
			} else {
				Expect(errors.Is(err, domain.ErrInvalidESIMProfile)).To(BeTrue())
			}
		}
		_, err = database.ListESIMProfiles(context.Background(), "01900000-0000-7000-8000-000000000199")
		Expect(errors.Is(err, pgx.ErrNoRows)).To(BeTrue())
		profileOne, err := database.UpsertESIMProfile(context.Background(), ESIMProfileRecord{
			SIMID: simID, ProfileID: "profile-one", Name: "Primary", ICCID: "890100000000000001", OperatorName: "Test Carrier", IsEnabled: true, CreatedAt: now,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(profileOne.Name).To(Equal("Primary"))
		profileTwo, err := database.UpsertESIMProfile(context.Background(), ESIMProfileRecord{
			SIMID: simID, ProfileID: "profile-two", Name: "Backup", ICCID: "890100000000000002", OperatorName: "Test Carrier", CreatedAt: now,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(profileTwo.IsEnabled).To(BeFalse())
		updated, err := database.UpsertESIMProfile(context.Background(), ESIMProfileRecord{
			SIMID: simID, ProfileID: "profile-one", Name: "Renamed by modem", ICCID: "890100000000000003", OperatorName: "Updated Carrier", CreatedAt: now,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(updated.Name).To(Equal("Renamed by modem"))

		profiles, err := database.ListESIMProfiles(context.Background(), simID)
		Expect(err).NotTo(HaveOccurred())
		Expect(profiles).To(HaveLen(2))
		selected, err := database.SetESIMProfileEnabled(context.Background(), simID, "profile-two", true)
		Expect(err).NotTo(HaveOccurred())
		Expect(selected.IsEnabled).To(BeTrue())
		disabled, err := database.SetESIMProfileEnabled(context.Background(), simID, "profile-two", false)
		Expect(err).NotTo(HaveOccurred())
		Expect(disabled.IsEnabled).To(BeFalse())
		selected, err = database.SetESIMProfileEnabled(context.Background(), simID, "profile-two", true)
		Expect(err).NotTo(HaveOccurred())
		profiles, err = database.ListESIMProfiles(context.Background(), simID)
		Expect(err).NotTo(HaveOccurred())
		for _, profile := range profiles {
			if profile.ProfileID == "profile-one" {
				Expect(profile.IsEnabled).To(BeFalse())
			}
		}
		renamed, err := database.RenameESIMProfile(context.Background(), simID, "profile-two", "  Travel  ")
		Expect(err).NotTo(HaveOccurred())
		Expect(renamed.Name).To(Equal("Travel"))
		deleted := database.DeleteESIMProfile(context.Background(), simID, "profile-one")
		Expect(deleted).NotTo(HaveOccurred())
		Expect(database.DeleteESIMProfile(context.Background(), simID, "profile-one")).To(MatchError(pgx.ErrNoRows))
		_, err = database.RenameESIMProfile(context.Background(), simID, "missing", "name")
		Expect(errors.Is(err, pgx.ErrNoRows)).To(BeTrue())
		_, err = database.SetESIMProfileEnabled(context.Background(), simID, "missing", true)
		Expect(errors.Is(err, pgx.ErrNoRows)).To(BeTrue())
		remaining, err := database.ListESIMProfiles(context.Background(), simID)
		Expect(err).NotTo(HaveOccurred())
		Expect(remaining).To(HaveLen(1))
	})

	It("propagates canceled requests from database operations", func() {
		createDevice(firstDeviceID)
		devices, err := database.ListDevices(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(devices[0].Slots[0].SIMID).NotTo(BeNil())
		simID := *devices[0].Slots[0].SIMID
		owner := createOwner()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err = database.ListESIMProfiles(ctx, simID)
		Expect(errors.Is(err, context.Canceled)).To(BeTrue())
		_, err = database.UpsertESIMProfile(ctx, ESIMProfileRecord{SIMID: simID, ProfileID: "profile"})
		Expect(errors.Is(err, context.Canceled)).To(BeTrue())
		_, err = database.RenameESIMProfile(ctx, simID, "profile", "name")
		Expect(errors.Is(err, context.Canceled)).To(BeTrue())
		_, err = database.SetESIMProfileEnabled(ctx, simID, "profile", true)
		Expect(errors.Is(err, context.Canceled)).To(BeTrue())
		Expect(errors.Is(database.DeleteESIMProfile(ctx, simID, "profile"), context.Canceled)).To(BeTrue())
		_, err = database.GetHistorySnapshot(ctx, owner.ID, 1)
		Expect(errors.Is(err, context.Canceled)).To(BeTrue())
	})

	It("rejects invalid identifiers before touching persistence", func() {
		ctx := context.Background()
		Expect(database.UpdateNumericID(ctx, firstDeviceID, 0)).To(MatchError(domain.ErrInvalidNumericID))
		Expect(database.UpdateSIMNumericID(ctx, firstDeviceID, 100)).To(MatchError(domain.ErrInvalidNumericID))
		_, err := database.UpdateDeviceSIMIdentity(ctx, firstDeviceID, "", 0, nil, now)
		Expect(err).To(HaveOccurred())
		Expect(database.UpdateDeviceRadioStatus(ctx, firstDeviceID, "", 0, "", nil, now)).To(HaveOccurred())
		Expect(database.RetireSIMCard(ctx, "", now)).To(HaveOccurred())
		_, err = database.ListSIMCards(ctx)
		Expect(err).NotTo(HaveOccurred())
		_, err = database.ListDevices(ctx)
		Expect(err).NotTo(HaveOccurred())
		_, err = database.GetHistorySnapshot(ctx, "", 0)
		Expect(err).To(HaveOccurred())
	})

	It("returns not-found for valid operations on missing records", func() {
		ctx := context.Background()
		missingID := "01900000-0000-7000-8000-000000000199"
		Expect(database.UpdateNumericID(ctx, missingID, 2)).To(HaveOccurred())
		Expect(database.UpdateDeviceProfile(ctx, missingID, []byte("profile"))).To(HaveOccurred())
		Expect(database.UpdateSIMNumericID(ctx, missingID, 2)).To(HaveOccurred())
		Expect(database.UpdateSIMProfile(ctx, missingID, []byte("profile"))).To(HaveOccurred())
		Expect(database.UpdateDeviceCommandPublicKey(ctx, missingID, bytes.Repeat([]byte{1}, 32))).To(HaveOccurred())
		Expect(database.UpdateDeviceRadioStatus(ctx, missingID, "slot", 1, "LTE", nil, now)).To(HaveOccurred())
		_, err := database.UpdateDeviceSIMIdentity(ctx, missingID, "slot", 1, bytes.Repeat([]byte{1}, 32), now)
		Expect(err).To(HaveOccurred())
		Expect(database.RevokeDevice(ctx, missingID, now)).To(HaveOccurred())
		_, err = database.AuthenticateDevice(ctx, missingID, "credential", now)
		Expect(err).To(HaveOccurred())
		_, err = database.ClaimCommand(ctx, missingID, now, now.Add(time.Minute))
		Expect(err).To(HaveOccurred())
		Expect(database.CompleteCommand(ctx, missingID, "command", domain.EncryptedCommandResult{}, now)).To(HaveOccurred())
		_, err = database.CommandStatus(ctx, missingID, "command", now)
		Expect(err).To(HaveOccurred())
	})

	It("stores encrypted history snapshots with optimistic revisions", func() {
		owner := createOwner()
		_, err := database.GetHistorySnapshot(context.Background(), owner.ID, 1)
		Expect(errors.Is(err, pgx.ErrNoRows)).To(BeTrue())
		first, err := database.PutHistorySnapshot(context.Background(), owner.ID, 1, 0, []byte("first archive"), now)
		Expect(err).NotTo(HaveOccurred())
		Expect(first.Revision).To(Equal(int64(1)))
		loaded, err := database.GetHistorySnapshot(context.Background(), owner.ID, 1)
		Expect(err).NotTo(HaveOccurred())
		Expect(loaded.Ciphertext).To(Equal([]byte("first archive")))
		second, err := database.PutHistorySnapshot(context.Background(), owner.ID, 1, 1, []byte("second archive"), now.Add(time.Minute))
		Expect(err).NotTo(HaveOccurred())
		Expect(second.Revision).To(Equal(int64(2)))
		_, err = database.PutHistorySnapshot(context.Background(), owner.ID, 1, 1, []byte("stale archive"), now.Add(2*time.Minute))
		Expect(errors.Is(err, domain.ErrHistoryRevisionConflict)).To(BeTrue())
		_, err = database.PutHistorySnapshot(context.Background(), owner.ID, 2, 1, []byte("wrong initial revision"), now)
		Expect(errors.Is(err, domain.ErrHistoryRevisionConflict)).To(BeTrue())
		_, err = database.PutHistorySnapshot(context.Background(), owner.ID, 2, 0, []byte{}, now)
		Expect(err).To(MatchError(ContainSubstring("write encrypted history snapshot")))
	})

	It("reuses one SIM identity when a card moves between devices", func() {
		firstDevice := createDevice(firstDeviceID)
		secondDevice := createDevice(secondDeviceID)
		fingerprint := bytes.Repeat([]byte{66}, 32)
		firstSlot := firstDevice.Slots[0]
		secondSlot := secondDevice.Slots[0]

		assignment, err := database.UpdateDeviceSIMIdentity(
			context.Background(), firstDevice.ID, firstSlot.SlotKey, firstSlot.SlotIndex, fingerprint, now,
		)
		Expect(err).NotTo(HaveOccurred())
		movedAssignment, err := database.UpdateDeviceSIMIdentity(
			context.Background(), secondDevice.ID, secondSlot.SlotKey, secondSlot.SlotIndex, fingerprint, now.Add(time.Minute),
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(movedAssignment.SIMID).To(Equal(assignment.SIMID))
		Expect(movedAssignment.NumericID).To(Equal(assignment.NumericID))

		listed, err := database.ListDevices(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(listed).To(HaveLen(2))
		byID := make(map[string]DeviceRecord, len(listed))
		for _, record := range listed {
			byID[record.ID] = record
		}
		Expect(byID[firstDevice.ID].Slots[0].SIMID).To(BeNil())
		Expect(byID[secondDevice.ID].Slots[0].SIMID).To(HaveValue(Equal(assignment.SIMID)))
	})

	It("moves one identified SIM between physical slots without cloning it", func() {
		device := createDevice(firstDeviceID)
		fingerprint := bytes.Repeat([]byte{63}, 32)
		firstSlot := device.Slots[0]
		assignment, err := database.UpdateDeviceSIMIdentity(
			context.Background(), device.ID, firstSlot.SlotKey, firstSlot.SlotIndex, fingerprint, now,
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(database.db.Create(&deviceSlotModel{
			ID: uuid.NewString(), DeviceID: device.ID, SlotKey: "secondary", SlotIndex: 2,
			CreatedAt: now, LastSeenAt: ptrTime(now),
		}).Error).To(Succeed())

		movedAssignment, err := database.UpdateDeviceSIMIdentity(
			context.Background(), device.ID, "secondary", 2, fingerprint, now.Add(time.Minute),
		)

		Expect(err).NotTo(HaveOccurred())
		Expect(movedAssignment.SIMID).To(Equal(assignment.SIMID))
		Expect(movedAssignment.NumericID).To(Equal(assignment.NumericID))
		listed, err := database.ListDevices(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(listed).To(HaveLen(1))
		Expect(listed[0].Slots).To(HaveLen(2))
		Expect(listed[0].Slots[0].SIMID).To(BeNil())
		Expect(listed[0].Slots[1].SIMID).To(HaveValue(Equal(assignment.SIMID)))
		var currentAssignments int64
		Expect(database.db.Model(&slotSIMAssignmentModel{}).Where("sim_id = ? AND removed_at IS NULL", assignment.SIMID).Count(&currentAssignments).Error).To(Succeed())
		Expect(currentAssignments).To(Equal(int64(1)))
	})

	It("validates SIM synchronization and reports exhausted numeric IDs", func() {
		device := createDevice(firstDeviceID)
		fingerprint := bytes.Repeat([]byte{64}, 32)
		_, err := database.UpdateDeviceSIMIdentity(context.Background(), device.ID, "", 1, fingerprint, now)
		Expect(err).To(MatchError("invalid slot or SIM fingerprint"))
		_, err = database.UpdateDeviceSIMIdentity(context.Background(), device.ID, "primary", 0, fingerprint, now)
		Expect(err).To(MatchError("invalid slot or SIM fingerprint"))
		_, err = database.UpdateDeviceSIMIdentity(context.Background(), device.ID, "primary", 1, []byte{1}, now)
		Expect(err).To(MatchError("invalid slot or SIM fingerprint"))
		_, err = database.UpdateDeviceSIMIdentity(context.Background(), device.ID, "missing", 2, fingerprint, now)
		Expect(errors.Is(err, pgx.ErrNoRows)).To(BeTrue())

		firstSlot := device.Slots[0]
		_, err = database.UpdateDeviceSIMIdentity(
			context.Background(), device.ID, firstSlot.SlotKey, firstSlot.SlotIndex, fingerprint, now,
		)
		Expect(err).NotTo(HaveOccurred())
		createSIMCards(domain.MinNumericID+1, domain.MaxNumericID)
		_, err = database.UpdateDeviceSIMIdentity(
			context.Background(), device.ID, firstSlot.SlotKey, firstSlot.SlotIndex,
			bytes.Repeat([]byte{65}, 32), now.Add(time.Minute),
		)
		Expect(errors.Is(err, domain.ErrNoNumericID)).To(BeTrue())
	})

	It("returns no numeric ID after all visible identifiers are occupied", func() {
		createSIMCards(domain.MinNumericID, domain.MaxNumericID)

		_, err := database.CreateDevice(context.Background(), firstDeviceID, nil, nil, nil)

		Expect(errors.Is(err, domain.ErrNoNumericID)).To(BeTrue())
	})

	It("cannot pair when all visible identifiers are occupied", func() {
		owner := createOwner()
		createSIMCards(domain.MinNumericID, domain.MaxNumericID)
		tokenHash, err := auth.TokenHash(newToken())
		Expect(err).NotTo(HaveOccurred())
		Expect(database.CreatePairingToken(context.Background(), owner.ID, tokenHash[:], now.Add(time.Minute))).To(Succeed())

		_, err = database.ConsumePairingToken(context.Background(), tokenHash[:], now, DevicePairing{
			DeviceUUID:        firstDeviceID,
			ProfileCiphertext: []byte{},
			IdentityPublicKey: bytes.Repeat([]byte{1}, ed25519.PublicKeySize),
			CredentialHash:    bytes.Repeat([]byte{2}, 32),
		})

		Expect(errors.Is(err, domain.ErrNoNumericID)).To(BeTrue())
	})

	It("does not leave a partial initial slot when SIM identity insertion conflicts", func() {
		createSIMCards(domain.MinNumericID, domain.MinNumericID)
		record := DeviceRecord{}
		err := database.db.Transaction(func(tx *gorm.DB) error {
			return createInitialSIMSlotDB(tx, firstDeviceID, domain.MinNumericID, nil, now, &record)
		})
		Expect(err).To(MatchError(ContainSubstring("insert initial SIM identity")))
		Expect(record.Slots).To(BeEmpty())
		var count int64
		Expect(database.db.Model(&simCardModel{}).Count(&count).Error).To(Succeed())
		Expect(count).To(Equal(int64(1)))
	})

	It("rejects a duplicate SIM identity when adding a discovered slot", func() {
		createDevice(firstDeviceID)
		record := DeviceRecord{}
		err := database.db.Transaction(func(tx *gorm.DB) error {
			return createSIMSlotDB(tx, firstDeviceID, "secondary", 2, firstDeviceID, 2, nil, now, &record)
		})
		Expect(err).To(MatchError(ContainSubstring("insert initial SIM identity")))
		Expect(record.Slots).To(BeEmpty())
	})

	It("stores encrypted events idempotently and rejects conflicting reuse", func() {
		device := createDevice(firstDeviceID)
		envelope := domain.EventEnvelope{
			ID:         "01900000-0000-7000-8000-000000000010",
			Type:       "message.received",
			DeviceUUID: device.ID,
			OccurredAt: now,
			KeyEpoch:   1,
			Nonce:      bytes.Repeat([]byte{1}, 24),
			Ciphertext: []byte("ciphertext"),
			Signature:  bytes.Repeat([]byte{2}, ed25519.SignatureSize),
		}
		expiresAt := now.Add(365 * 24 * time.Hour)

		sequence, created, err := database.InsertEvent(context.Background(), envelope, expiresAt)
		Expect(err).NotTo(HaveOccurred())
		Expect(created).To(BeTrue())
		Expect(sequence).To(BeNumerically(">", 0))
		retriedSequence, created, err := database.InsertEvent(context.Background(), envelope, expiresAt)
		Expect(err).NotTo(HaveOccurred())
		Expect(created).To(BeFalse())
		Expect(retriedSequence).To(Equal(sequence))

		envelope.Ciphertext = []byte("different")
		_, _, err = database.InsertEvent(context.Background(), envelope, expiresAt)
		Expect(errors.Is(err, domain.ErrIdempotencyConflict)).To(BeTrue())
	})

	It("queues targeted encrypted SMS alerts without exposing profile events", func() {
		deliveryNow := time.Now().UTC().Truncate(time.Microsecond)
		owner := createOwner()
		installation := AppInstallationRegistration{
			ID:                    "01900000-0000-7000-8000-000000000101",
			IdentityPublicKey:     bytes.Repeat([]byte{9}, ed25519.PublicKeySize),
			KeyAgreementPublicKey: bytes.Repeat([]byte{10}, 32),
		}
		_, created, err := database.RegisterAppInstallation(context.Background(), owner.ID, installation, deliveryNow)
		Expect(err).NotTo(HaveOccurred())
		Expect(created).To(BeTrue())
		alertToken := []byte("encrypted-alert-token")
		Expect(database.UpsertPushToken(
			context.Background(), owner.ID, installation.ID, "sandbox", "alert", alertToken, deliveryNow,
		)).To(Succeed())
		device := createDevice(firstDeviceID)
		event := domain.EventEnvelope{
			ID: "01900000-0000-7000-8000-000000000011", Type: "message.received",
			DeviceUUID: device.ID, RecipientInstallationID: installation.ID,
			OccurredAt: deliveryNow, KeyEpoch: 1, Nonce: bytes.Repeat([]byte{1}, 12),
			Ciphertext: []byte("opaque SMS"), Signature: bytes.Repeat([]byte{2}, ed25519.SignatureSize),
		}
		_, inserted, err := database.InsertEvent(context.Background(), event, deliveryNow.Add(time.Hour))
		Expect(err).NotTo(HaveOccurred())
		Expect(inserted).To(BeTrue())

		job, err := database.ClaimPushJob(context.Background(), deliveryNow.Add(time.Second), time.Minute)
		Expect(err).NotTo(HaveOccurred())
		Expect(job.Kind).To(Equal("alert.incoming_message"))
		Expect(job.RoutingKey).To(Equal(installation.ID))
		Expect(job.Environment).To(Equal("sandbox"))
		Expect(job.DeviceToken).To(Equal(alertToken))
		var delivered domain.EventEnvelope
		Expect(json.Unmarshal(job.Payload, &delivered)).To(Succeed())
		Expect(delivered).To(Equal(event))
		Expect(database.CompletePushJob(context.Background(), job.ID, job.Attempt, deliveryNow.Add(2*time.Second))).To(Succeed())

		profileEvent := event
		profileEvent.ID = "01900000-0000-7000-8000-000000000012"
		profileEvent.Type = "sim.profile"
		_, inserted, err = database.InsertEvent(context.Background(), profileEvent, deliveryNow.Add(time.Hour))
		Expect(err).NotTo(HaveOccurred())
		Expect(inserted).To(BeTrue())
		_, err = database.ClaimPushJob(context.Background(), deliveryNow.Add(3*time.Second), time.Minute)
		Expect(errors.Is(err, pgx.ErrNoRows)).To(BeTrue())
	})

	It("atomically stores incoming call ciphertexts and one VoIP outbox job per approved recipient", func() {
		owner := createOwner()
		installation := AppInstallationRegistration{
			ID:                    "01900000-0000-7000-8000-000000000101",
			IdentityPublicKey:     bytes.Repeat([]byte{9}, 32),
			KeyAgreementPublicKey: bytes.Repeat([]byte{10}, 32),
		}
		_, created, err := database.RegisterAppInstallation(context.Background(), owner.ID, installation, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(created).To(BeTrue())
		device := createDevice(firstDeviceID)
		notification := domain.IncomingCallNotification{
			ID:         "01900000-0000-7000-8000-000000000040",
			Type:       "call.incoming",
			DeviceUUID: device.ID,
			OccurredAt: now,
			KeyEpoch:   1,
			Recipients: []domain.IncomingCallRecipient{{
				InstallationID:  installation.ID,
				SenderPublicKey: bytes.Repeat([]byte{11}, 32),
				Nonce:           bytes.Repeat([]byte{12}, 12),
				Ciphertext:      []byte("opaque incoming call"),
				Signature:       bytes.Repeat([]byte{13}, ed25519.SignatureSize),
			}},
		}
		expiresAt := now.Add(365 * 24 * time.Hour)

		inserted, err := database.InsertIncomingCall(context.Background(), notification, expiresAt)
		Expect(err).NotTo(HaveOccurred())
		Expect(inserted).To(BeTrue())
		inserted, err = database.InsertIncomingCall(context.Background(), notification, expiresAt)
		Expect(err).NotTo(HaveOccurred())
		Expect(inserted).To(BeFalse())
		conflicts := []domain.IncomingCallNotification{}
		changedType := notification
		changedType.Type = "call.updated"
		conflicts = append(conflicts, changedType)
		changedDevice := notification
		changedDevice.DeviceUUID = secondDeviceID
		conflicts = append(conflicts, changedDevice)
		changedTime := notification
		changedTime.OccurredAt = now.Add(time.Second)
		conflicts = append(conflicts, changedTime)
		changedEpoch := notification
		changedEpoch.KeyEpoch++
		conflicts = append(conflicts, changedEpoch)
		changedRecipient := notification
		changedRecipient.Recipients = nil
		conflicts = append(conflicts, changedRecipient)
		changedKey := notification
		changedKey.Recipients = append([]domain.IncomingCallRecipient(nil), notification.Recipients...)
		changedKey.Recipients[0].SenderPublicKey = bytes.Repeat([]byte{14}, 32)
		conflicts = append(conflicts, changedKey)
		changedNonce := notification
		changedNonce.Recipients = append([]domain.IncomingCallRecipient(nil), notification.Recipients...)
		changedNonce.Recipients[0].Nonce = bytes.Repeat([]byte{15}, 12)
		conflicts = append(conflicts, changedNonce)
		changedSignature := notification
		changedSignature.Recipients = append([]domain.IncomingCallRecipient(nil), notification.Recipients...)
		changedSignature.Recipients[0].Signature = bytes.Repeat([]byte{16}, ed25519.SignatureSize)
		conflicts = append(conflicts, changedSignature)
		changedRecipientID := notification
		changedRecipientID.Recipients = append([]domain.IncomingCallRecipient(nil), notification.Recipients...)
		changedRecipientID.Recipients[0].InstallationID = secondDeviceID
		conflicts = append(conflicts, changedRecipientID)
		for _, conflict := range conflicts {
			_, err := database.InsertIncomingCall(context.Background(), conflict, expiresAt)
			Expect(errors.Is(err, domain.ErrIdempotencyConflict)).To(BeTrue())
		}
		_, err = database.InsertIncomingCall(context.Background(), notification, expiresAt.Add(time.Second))
		Expect(errors.Is(err, domain.ErrIdempotencyConflict)).To(BeTrue())

		var job outboxJobModel
		Expect(database.db.Where("kind = ?", "voip.incoming_call").First(&job).Error).To(Succeed())
		Expect(job.Kind).To(Equal("voip.incoming_call"))
		Expect(job.RoutingKey).To(Equal(installation.ID))
		var delivery domain.IncomingCallDelivery
		Expect(json.Unmarshal(job.Payload, &delivery)).To(Succeed())
		Expect(delivery.NotificationID).To(Equal(notification.ID))
		Expect(delivery.Recipient).To(Equal(notification.Recipients[0]))
		var jobs int64
		Expect(database.db.Model(&outboxJobModel{}).Where("kind = ?", "voip.incoming_call").Count(&jobs).Error).To(Succeed())
		Expect(jobs).To(Equal(int64(1)))

		changedCiphertext := notification
		changedCiphertext.Recipients = append([]domain.IncomingCallRecipient(nil), notification.Recipients...)
		changedCiphertext.Recipients[0].Ciphertext = []byte("different ciphertext")
		_, err = database.InsertIncomingCall(context.Background(), changedCiphertext, expiresAt)
		Expect(errors.Is(err, domain.ErrIdempotencyConflict)).To(BeTrue())
	})

	It("leases, retries, completes, and invalidates routed VoIP outbox jobs", func() {
		owner := createOwner()
		installation := AppInstallationRegistration{
			ID:                    "01900000-0000-7000-8000-000000000101",
			IdentityPublicKey:     bytes.Repeat([]byte{9}, 32),
			KeyAgreementPublicKey: bytes.Repeat([]byte{10}, 32),
		}
		_, created, err := database.RegisterAppInstallation(context.Background(), owner.ID, installation, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(created).To(BeTrue())
		token := []byte{1, 2, 3, 4}
		Expect(database.UpsertPushToken(
			context.Background(), owner.ID, installation.ID, "sandbox", "voip", token, now,
		)).To(Succeed())
		expiresAt := now.Add(time.Minute)
		fixtureJob := outboxJobModel{Kind: "voip.incoming_call", RoutingKey: installation.ID, Payload: []byte(`{"ciphertext":"opaque"}`), AvailableAt: now, ExpiresAt: &expiresAt}
		Expect(database.db.Create(&fixtureJob).Error).To(Succeed())
		jobID := fixtureJob.ID

		job, err := database.ClaimVoIPPushJob(context.Background(), now, 20*time.Second)
		Expect(err).NotTo(HaveOccurred())
		Expect(job.ID).To(Equal(jobID))
		Expect(job.Attempt).To(Equal(1))
		Expect(job.Environment).To(Equal("sandbox"))
		Expect(job.DeviceToken).To(Equal(token))
		Expect(job.LeaseExpiresAt).To(BeTemporally("==", now.Add(20*time.Second)))

		Expect(database.RetryPushJob(
			context.Background(), job.ID, job.Attempt, now.Add(5*time.Second), "temporary failure",
		)).To(Succeed())
		job, err = database.ClaimVoIPPushJob(context.Background(), now.Add(5*time.Second), 20*time.Second)
		Expect(err).NotTo(HaveOccurred())
		Expect(job.Attempt).To(Equal(2))
		Expect(database.CompletePushJob(context.Background(), job.ID, job.Attempt, now.Add(6*time.Second))).To(Succeed())
		Expect(database.CompletePushJob(context.Background(), job.ID, job.Attempt, now.Add(7*time.Second))).To(MatchError(pgx.ErrNoRows))

		Expect(database.DeleteVoIPPushToken(context.Background(), installation.ID, "sandbox", token)).To(Succeed())
		var tokens int64
		Expect(database.db.Model(&pushTokenModel{}).Where("installation_id = ?", installation.ID).Count(&tokens).Error).To(Succeed())
		Expect(tokens).To(BeZero())
	})

	It("updates owner-scoped push tokens and records discarded deliveries", func() {
		owner := createOwner()
		installation := AppInstallationRegistration{
			ID:                    "01900000-0000-7000-8000-000000000101",
			IdentityPublicKey:     bytes.Repeat([]byte{31}, 32),
			KeyAgreementPublicKey: bytes.Repeat([]byte{32}, 32),
		}
		_, _, err := database.RegisterAppInstallation(context.Background(), owner.ID, installation, now)
		Expect(err).NotTo(HaveOccurred())

		firstToken := []byte("first encrypted alert token")
		secondToken := []byte("replacement encrypted alert token")
		Expect(database.UpsertPushToken(
			context.Background(), owner.ID, installation.ID, "sandbox", "alert", firstToken, now,
		)).To(Succeed())
		Expect(database.UpsertPushToken(
			context.Background(), owner.ID, installation.ID, "sandbox", "alert", secondToken, now.Add(time.Second),
		)).To(Succeed())
		Expect(database.UpsertPushToken(
			context.Background(), "01900000-0000-7000-8000-000000000099", installation.ID,
			"sandbox", "alert", firstToken, now,
		)).To(MatchError(pgx.ErrNoRows))

		var storedToken pushTokenModel
		Expect(database.db.Where("installation_id = ? AND environment = ? AND kind = ?", installation.ID, "sandbox", "alert").First(&storedToken).Error).To(Succeed())
		Expect(storedToken.TokenCiphertext).To(Equal(secondToken))

		Expect(database.DeletePushToken(
			context.Background(), owner.ID, installation.ID, "production", "alert",
		)).To(MatchError(pgx.ErrNoRows))
		Expect(database.DeletePushToken(
			context.Background(), owner.ID, installation.ID, "sandbox", "alert",
		)).To(Succeed())
		Expect(database.DeletePushToken(
			context.Background(), owner.ID, installation.ID, "sandbox", "alert",
		)).To(MatchError(pgx.ErrNoRows))

		expiresAt := now.Add(time.Minute)
		fixtureJob := outboxJobModel{Kind: "voip.incoming_call", RoutingKey: installation.ID, Payload: []byte("opaque push"), AvailableAt: now, ExpiresAt: &expiresAt}
		Expect(database.db.Create(&fixtureJob).Error).To(Succeed())
		jobID := fixtureJob.ID
		job, err := database.ClaimVoIPPushJob(context.Background(), now, 20*time.Second)
		Expect(err).NotTo(HaveOccurred())
		Expect(job.ID).To(Equal(jobID))
		Expect(database.DiscardPushJob(
			context.Background(), job.ID, job.Attempt, now.Add(time.Second), "invalid device token",
		)).To(Succeed())
		Expect(database.DiscardPushJob(
			context.Background(), job.ID, job.Attempt, now.Add(2*time.Second), "duplicate discard",
		)).To(MatchError(pgx.ErrNoRows))

		var completedJob outboxJobModel
		Expect(database.db.First(&completedJob, job.ID).Error).To(Succeed())
		Expect(completedJob.CompletedAt).NotTo(BeNil())
		Expect(*completedJob.CompletedAt).To(BeTemporally("==", now.Add(time.Second)))
		Expect(completedJob.LastError).NotTo(BeNil())
		Expect(*completedJob.LastError).To(Equal("invalid device token"))
	})

	It("rolls back an incoming call batch containing an unapproved recipient", func() {
		owner := createOwner()
		pending := AppInstallationRegistration{
			ID:                    "01900000-0000-7000-8000-000000000102",
			IdentityPublicKey:     bytes.Repeat([]byte{9}, 32),
			KeyAgreementPublicKey: bytes.Repeat([]byte{10}, 32),
		}
		approved := pending
		approved.ID = "01900000-0000-7000-8000-000000000101"
		_, _, err := database.RegisterAppInstallation(context.Background(), owner.ID, approved, now)
		Expect(err).NotTo(HaveOccurred())
		_, _, err = database.RegisterAppInstallation(context.Background(), owner.ID, pending, now.Add(time.Second))
		Expect(err).NotTo(HaveOccurred())
		device := createDevice(firstDeviceID)
		notification := domain.IncomingCallNotification{
			ID:         "01900000-0000-7000-8000-000000000041",
			Type:       "call.incoming",
			DeviceUUID: device.ID,
			OccurredAt: now,
			KeyEpoch:   1,
			Recipients: []domain.IncomingCallRecipient{{
				InstallationID:  pending.ID,
				SenderPublicKey: bytes.Repeat([]byte{11}, 32),
				Nonce:           bytes.Repeat([]byte{12}, 12),
				Ciphertext:      []byte("opaque incoming call"),
				Signature:       bytes.Repeat([]byte{13}, ed25519.SignatureSize),
			}},
		}

		_, err = database.InsertIncomingCall(context.Background(), notification, now.Add(time.Hour))
		Expect(errors.Is(err, domain.ErrUntrustedInstallationRecipient)).To(BeTrue())
		var count int64
		Expect(database.db.Model(&incomingCallNotificationModel{}).Where("id = ?", notification.ID).Count(&count).Error).To(Succeed())
		Expect(count).To(BeZero())
	})

	It("allows only the first approved installation to claim an incoming call", func() {
		owner := createOwner()
		first := AppInstallationRegistration{
			ID:                    "01900000-0000-7000-8000-000000000101",
			IdentityPublicKey:     bytes.Repeat([]byte{9}, 32),
			KeyAgreementPublicKey: bytes.Repeat([]byte{10}, 32),
		}
		second := first
		second.ID = "01900000-0000-7000-8000-000000000102"
		third := first
		third.ID = "01900000-0000-7000-8000-000000000103"
		_, _, err := database.RegisterAppInstallation(context.Background(), owner.ID, first, now)
		Expect(err).NotTo(HaveOccurred())
		_, _, err = database.RegisterAppInstallation(context.Background(), owner.ID, second, now.Add(time.Second))
		Expect(err).NotTo(HaveOccurred())
		_, _, err = database.RegisterAppInstallation(context.Background(), owner.ID, third, now.Add(2*time.Second))
		Expect(err).NotTo(HaveOccurred())
		Expect(database.db.Model(&appInstallationModel{}).Where("id = ?", second.ID).Update("approved_at", now).Error).To(Succeed())
		Expect(database.db.Model(&appInstallationModel{}).Where("id = ?", third.ID).Update("approved_at", now).Error).To(Succeed())
		device := createDevice(firstDeviceID)
		notification := domain.IncomingCallNotification{
			ID:         "01900000-0000-7000-8000-000000000042",
			Type:       "call.incoming",
			DeviceUUID: device.ID,
			OccurredAt: now,
			KeyEpoch:   1,
			Recipients: []domain.IncomingCallRecipient{
				{
					InstallationID:  first.ID,
					SenderPublicKey: bytes.Repeat([]byte{11}, 32),
					Nonce:           bytes.Repeat([]byte{12}, 12),
					Ciphertext:      []byte("opaque first call"),
					Signature:       bytes.Repeat([]byte{13}, ed25519.SignatureSize),
				},
				{
					InstallationID:  second.ID,
					SenderPublicKey: bytes.Repeat([]byte{14}, 32),
					Nonce:           bytes.Repeat([]byte{15}, 12),
					Ciphertext:      []byte("opaque second call"),
					Signature:       bytes.Repeat([]byte{16}, ed25519.SignatureSize),
				},
			},
		}
		_, err = database.InsertIncomingCall(context.Background(), notification, now.Add(time.Hour))
		Expect(err).NotTo(HaveOccurred())
		_, err = database.ClaimIncomingCall(context.Background(), owner.ID, notification.ID, "01900000-0000-7000-8000-000000000199", now)
		Expect(errors.Is(err, pgx.ErrNoRows)).To(BeTrue())
		_, err = database.ClaimIncomingCall(context.Background(), owner.ID, notification.ID, third.ID, now)
		Expect(errors.Is(err, pgx.ErrNoRows)).To(BeTrue())

		winner, err := database.ClaimIncomingCall(context.Background(), owner.ID, notification.ID, first.ID, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(winner.InstallationID).To(Equal(first.ID))
		Expect(winner.Won).To(BeTrue())
		loser, err := database.ClaimIncomingCall(context.Background(), owner.ID, notification.ID, second.ID, now.Add(time.Second))
		Expect(err).NotTo(HaveOccurred())
		Expect(loser.InstallationID).To(Equal(first.ID))
		Expect(loser.Won).To(BeFalse())
		retry, err := database.ClaimIncomingCall(context.Background(), owner.ID, notification.ID, first.ID, now.Add(2*time.Second))
		Expect(err).NotTo(HaveOccurred())
		Expect(retry.Won).To(BeTrue())

		var claimedJobs int64
		Expect(database.db.Model(&outboxJobModel{}).Where("kind = ?", "voip.incoming_call_claimed").Count(&claimedJobs).Error).To(Succeed())
		Expect(claimedJobs).To(Equal(int64(1)))
		var claimedJob outboxJobModel
		Expect(database.db.Where("kind = ?", "voip.incoming_call_claimed").First(&claimedJob).Error).To(Succeed())
		Expect(claimedJob.RoutingKey).To(Equal(second.ID))
		var delivery domain.IncomingCallClaimDelivery
		Expect(json.Unmarshal(claimedJob.Payload, &delivery)).To(Succeed())
		Expect(delivery.NotificationID).To(Equal(notification.ID))
		Expect(delivery.Reason).To(Equal("answered_elsewhere"))

		_, err = database.ClaimIncomingCall(
			context.Background(), owner.ID, "01900000-0000-7000-8000-000000000099", first.ID, now,
		)
		Expect(errors.Is(err, pgx.ErrNoRows)).To(BeTrue())
	})

	It("lists only unexpired encrypted events after a sequence cursor", func() {
		device := createDevice(firstDeviceID)
		first := domain.EventEnvelope{
			ID: "01900000-0000-7000-8000-000000000030", Type: "call.state", DeviceUUID: device.ID,
			OccurredAt: now, KeyEpoch: 1, Nonce: bytes.Repeat([]byte{1}, 12), Ciphertext: []byte("first"), Signature: bytes.Repeat([]byte{2}, ed25519.SignatureSize),
		}
		second := first
		second.ID = "01900000-0000-7000-8000-000000000031"
		second.Ciphertext = []byte("second")
		third := first
		third.ID = "01900000-0000-7000-8000-000000000032"
		third.Ciphertext = []byte("expired")

		firstSequence, _, err := database.InsertEvent(context.Background(), first, now.Add(time.Hour))
		Expect(err).NotTo(HaveOccurred())
		secondSequence, _, err := database.InsertEvent(context.Background(), second, now.Add(time.Hour))
		Expect(err).NotTo(HaveOccurred())
		_, _, err = database.InsertEvent(context.Background(), third, now.Add(-time.Second))
		Expect(err).NotTo(HaveOccurred())

		events, err := database.ListEvents(context.Background(), firstSequence, 1, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(events).To(HaveLen(1))
		Expect(events[0].Sequence).To(Equal(secondSequence))
		Expect(events[0].Ciphertext).To(Equal([]byte("second")))
		Expect(events[0].RecipientInstallationID).To(BeEmpty())
	})

	It("wraps PostgreSQL connection and foreign-key failures", func() {
		_, err := Open(context.Background(), "://invalid")
		Expect(err).To(MatchError(ContainSubstring("configure postgres")))
		_, _, err = database.InsertEvent(context.Background(), domain.EventEnvelope{
			ID:         "01900000-0000-7000-8000-000000000020",
			Type:       "message.received",
			DeviceUUID: firstDeviceID,
			OccurredAt: now,
			KeyEpoch:   1,
		}, now.Add(time.Hour))
		Expect(err).To(MatchError(ContainSubstring("insert encrypted event")))
	})

	It("reports duplicate constraints for sessions, pairing tokens, and devices", func() {
		owner := createOwner()
		pair := newPair()
		Expect(database.CreateSession(context.Background(), owner.ID, pair)).To(Succeed())
		Expect(database.CreateSession(context.Background(), owner.ID, pair)).To(MatchError(ContainSubstring("create auth session")))

		tokenHash, err := auth.TokenHash(newToken())
		Expect(err).NotTo(HaveOccurred())
		Expect(database.CreatePairingToken(context.Background(), owner.ID, tokenHash[:], now.Add(time.Minute))).To(Succeed())
		Expect(database.CreatePairingToken(context.Background(), owner.ID, tokenHash[:], now.Add(time.Minute))).To(MatchError(ContainSubstring("create pairing token")))

		createDevice(firstDeviceID)
		_, err = database.CreateDevice(context.Background(), firstDeviceID, []byte{}, []byte{}, nil)
		Expect(err).To(MatchError(ContainSubstring("insert device")))
	})

	It("rolls back pairing and session rotation when dependent inserts fail", func() {
		owner := createOwner()
		first := newPair()
		second := newPair()
		Expect(database.CreateSession(context.Background(), owner.ID, first)).To(Succeed())
		Expect(database.CreateSession(context.Background(), owner.ID, second)).To(Succeed())
		Expect(database.RotateSession(context.Background(), first.RefreshToken, second, now)).To(MatchError(ContainSubstring("insert rotated session")))
		ownerID, err := database.AuthenticateOwnerSession(context.Background(), first.AccessToken, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(ownerID).To(Equal(owner.ID))

		tokenHash, err := auth.TokenHash(newToken())
		Expect(err).NotTo(HaveOccurred())
		Expect(database.CreatePairingToken(context.Background(), owner.ID, tokenHash[:], now.Add(time.Minute))).To(Succeed())
		_, err = database.ConsumePairingToken(context.Background(), tokenHash[:], now, DevicePairing{
			DeviceUUID:        firstDeviceID,
			ProfileCiphertext: []byte{},
			IdentityPublicKey: bytes.Repeat([]byte{1}, ed25519.PublicKeySize),
			CredentialHash:    nil,
		})
		Expect(err).To(MatchError(ContainSubstring("insert device credential")))
		records, listErr := database.ListDevices(context.Background())
		Expect(listErr).NotTo(HaveOccurred())
		Expect(records).To(BeEmpty())
	})

	It("reports an invalid owner recovery key insert", func() {
		created, err := database.CreateOwner(context.Background(), "hash", nil)

		Expect(created).To(BeFalse())
		Expect(err).To(MatchError(ContainSubstring("create owner account")))
	})

	It("returns contextual errors when database work is canceled", func() {
		owner := createOwner()
		pair := newPair()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := database.OwnerExists(ctx)
		Expect(err).To(MatchError(ContainSubstring("check owner account")))
		_, err = database.CreateOwner(ctx, "hash", []byte("key"))
		Expect(err).To(MatchError(ContainSubstring("create owner account")))
		_, err = database.OwnerCredentialByUsername(ctx, "admin")
		Expect(err).To(HaveOccurred())
		_, err = database.OwnerID(ctx)
		Expect(err).To(HaveOccurred())
		_, err = database.WebAuthnCredentials(ctx, owner.ID)
		Expect(err).To(MatchError(ContainSubstring("list passkey credentials")))
		Expect(database.SaveWebAuthnCredential(ctx, owner.ID, webauthnlib.Credential{})).To(HaveOccurred())
		Expect(database.DisableOwnerPassword(ctx, owner.ID, now)).To(HaveOccurred())
		Expect(database.CreateSession(ctx, owner.ID, pair)).To(MatchError(ContainSubstring("create auth session")))
		Expect(database.RotateSession(ctx, pair.RefreshToken, newPair(), now)).To(MatchError(ContainSubstring("rotate session")))
		Expect(database.RevokeSession(ctx, pair.AccessToken, now)).To(MatchError(ContainSubstring("revoke auth session")))
		_, err = database.AuthenticateOwnerSession(ctx, pair.AccessToken, now)
		Expect(err).To(HaveOccurred())
		Expect(database.CreatePairingToken(ctx, owner.ID, bytes.Repeat([]byte{1}, 32), now)).To(MatchError(ContainSubstring("create pairing token")))
		_, err = database.ConsumePairingToken(ctx, bytes.Repeat([]byte{1}, 32), now, DevicePairing{})
		Expect(err).To(MatchError(ContainSubstring("device pairing")))
		_, err = database.AuthenticateDevice(ctx, firstDeviceID, newToken(), now)
		Expect(err).To(HaveOccurred())
		_, err = database.CreateDevice(ctx, firstDeviceID, []byte{}, []byte{}, nil)
		Expect(err).To(MatchError(ContainSubstring("create device")))
		Expect(database.UpdateNumericID(ctx, firstDeviceID, 1)).To(HaveOccurred())
		Expect(database.UpdateDeviceProfile(ctx, firstDeviceID, []byte("profile"))).To(HaveOccurred())
		Expect(database.UpdateSIMNumericID(ctx, firstDeviceID, 1)).To(HaveOccurred())
		Expect(database.UpdateSIMProfile(ctx, firstDeviceID, []byte("profile"))).To(HaveOccurred())
		_, err = database.UpdateDeviceSIMIdentity(ctx, firstDeviceID, "slot", 1, bytes.Repeat([]byte{1}, 32), now)
		Expect(err).To(HaveOccurred())
		Expect(database.UpdateDeviceRadioStatus(ctx, firstDeviceID, "slot", 1, "LTE", nil, now)).To(HaveOccurred())
		Expect(database.RevokeDevice(ctx, firstDeviceID, now)).To(HaveOccurred())
		_, err = database.ListSIMCards(ctx)
		Expect(err).To(HaveOccurred())
		Expect(database.RetireSIMCard(ctx, firstDeviceID, now)).To(HaveOccurred())
		_, err = database.ListDevices(ctx)
		Expect(err).To(MatchError(ContainSubstring("list devices")))
		_, _, err = database.InsertEvent(ctx, domain.EventEnvelope{}, now)
		Expect(err).To(HaveOccurred())
		_, _, err = database.RegisterAppInstallation(ctx, owner.ID, AppInstallationRegistration{}, now)
		Expect(err).To(HaveOccurred())
		_, err = database.ListApprovedAppInstallationRecipients(ctx)
		Expect(err).To(HaveOccurred())
		Expect(database.UpsertPushToken(ctx, owner.ID, "installation", "sandbox", "voip", nil, now)).To(
			HaveOccurred(),
		)
		Expect(database.DeletePushToken(ctx, owner.ID, "installation", "sandbox", "voip")).To(
			HaveOccurred(),
		)
		_, err = database.InsertIncomingCall(ctx, domain.IncomingCallNotification{}, now)
		Expect(err).To(HaveOccurred())
		_, err = database.ClaimIncomingCall(ctx, owner.ID, "notification", "installation", now)
		Expect(err).To(HaveOccurred())
		_, err = database.ClaimVoIPPushJob(ctx, now, time.Minute)
		Expect(err).To(HaveOccurred())
		Expect(database.CompletePushJob(ctx, 1, 1, now)).To(HaveOccurred())
		Expect(database.DiscardPushJob(ctx, 1, 1, now, "discarded")).To(HaveOccurred())
		Expect(database.RetryPushJob(ctx, 1, 1, now, "retry")).To(HaveOccurred())
		Expect(database.DeleteVoIPPushToken(ctx, "installation", "sandbox", nil)).To(
			HaveOccurred(),
		)
		_, err = database.OpenDeviceDiscovery(ctx, owner.ID, now, now.Add(time.Minute))
		Expect(err).To(HaveOccurred())
		Expect(database.CloseDeviceDiscovery(ctx, owner.ID, now)).To(HaveOccurred())
		_, err = database.GetDeviceDiscoverySession(ctx, owner.ID)
		Expect(err).To(HaveOccurred())
		_, err = database.SubmitDeviceDiscovery(ctx, now, DeviceDiscoveryRequest{})
		Expect(err).To(HaveOccurred())
		_, err = database.ListPendingDeviceDiscoveries(ctx, owner.ID, now)
		Expect(err).To(HaveOccurred())
		_, err = database.ApproveDeviceDiscovery(ctx, owner.ID, firstDeviceID, now)
		Expect(err).To(HaveOccurred())
		Expect(database.RejectDeviceDiscovery(ctx, owner.ID, firstDeviceID, now)).To(
			HaveOccurred(),
		)
		_, err = database.DeviceDiscoveryStatus(ctx, firstDeviceID, nil, now)
		Expect(err).To(HaveOccurred())
		Expect(database.UpdateDeviceRadioStatus(ctx, firstDeviceID, "1", 1, "LTE", nil, now)).To(HaveOccurred())
		_, err = database.ListEvents(ctx, 0, 100, now)
		Expect(err).To(HaveOccurred())
		_, err = database.EnqueueCommand(ctx, domain.EncryptedCommand{})
		Expect(err).To(HaveOccurred())
		_, err = database.ClaimCommand(ctx, firstDeviceID, now, now.Add(time.Minute))
		Expect(err).To(HaveOccurred())
		Expect(database.CompleteCommand(ctx, firstDeviceID, "command", domain.EncryptedCommandResult{}, now)).To(HaveOccurred())
		_, err = database.CommandStatus(ctx, firstDeviceID, "command", now)
		Expect(err).To(HaveOccurred())
		Expect(database.UpdateDeviceCommandPublicKey(ctx, firstDeviceID, nil)).To(HaveOccurred())
	})

	It("reports ping and schema initialization failures", func() {
		_, err := Open(context.Background(), "not a postgres connection string")
		Expect(err).To(MatchError(ContainSubstring("configure postgres")))

		_, err = Open(context.Background(), "postgres://simdeck:simdeck-test-only@127.0.0.1:1/simdeck_test?sslmode=disable&connect_timeout=1")
		Expect(err).To(MatchError(ContainSubstring("ping postgres")))

		closed, err := Open(context.Background(), os.Getenv("TEST_DATABASE_URL"))
		Expect(err).NotTo(HaveOccurred())
		closed.Close()
		Expect(closed.InitializeSchema(context.Background())).To(MatchError(ContainSubstring("create schema migration table")))
	})
})
