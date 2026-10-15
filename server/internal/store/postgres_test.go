package store

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"sim-deck-server/internal/auth"
	"sim-deck-server/internal/domain"
)

const (
	firstDeviceID  = "01900000-0000-7000-8000-000000000001"
	secondDeviceID = "01900000-0000-7000-8000-000000000002"
)

var _ = Describe("Embedded migrations", func() {
	It("contains the encrypted application tables", func() {
		schema, err := InitialMigration()
		Expect(err).NotTo(HaveOccurred())
		for _, table := range [][]byte{
			[]byte("owner_accounts"),
			[]byte("app_installations"),
			[]byte("devices"),
			[]byte("events"),
			[]byte("messages"),
			[]byte("attachments"),
			[]byte("calls"),
			[]byte("outbox_jobs"),
		} {
			Expect(schema).To(ContainSubstring(string(table)))
		}
	})

	DescribeTable("contains each incremental schema contract",
		func(path string, required string) {
			schema, err := migrations.ReadFile(path)
			Expect(err).NotTo(HaveOccurred())
			Expect(bytes.Contains(schema, []byte(required))).To(BeTrue())
		},
		Entry("single owner", "migrations/002_single_owner.sql", "owner_accounts_singleton_idx"),
		Entry("sessions", "migrations/003_auth_sessions.sql", "auth_sessions"),
		Entry("device credentials", "migrations/004_device_pairing.sql", "device_credentials"),
		Entry("pairing retries", "migrations/005_pairing_retry.sql", "paired_device_id"),
		Entry("installation agreement keys", "migrations/007_installation_key_agreement.sql", "key_agreement_public_key"),
		Entry("incoming call notifications", "migrations/008_incoming_call_notifications.sql", "incoming_call_recipients"),
		Entry("incoming call claims", "migrations/009_incoming_call_claims.sql", "incoming_call_claims"),
		Entry("outbox expiry", "migrations/010_outbox_expiry.sql", "expires_at"),
	)
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
		Expect(database.Migrate(context.Background())).To(Succeed())
		Expect(database.Migrate(context.Background())).To(Succeed())
	})

	AfterAll(func() {
		if database != nil {
			database.Close()
		}
	})

	BeforeEach(func() {
		now = time.Date(2026, 10, 16, 1, 0, 0, 0, time.UTC)
		_, err := database.pool.Exec(context.Background(), `
			TRUNCATE TABLE owner_accounts, devices, pairing_tokens, events,
			       auth_sessions, device_credentials
			RESTART IDENTITY CASCADE`)
		Expect(err).NotTo(HaveOccurred())
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

		retried, created, err := database.RegisterAppInstallation(context.Background(), owner.ID, first, now.Add(time.Minute))
		Expect(err).NotTo(HaveOccurred())
		Expect(created).To(BeFalse())
		Expect(retried).To(Equal(firstRecord))

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
			KeyAgreementPublicKey: first.KeyAgreementPublicKey,
		}}))

		first.IdentityPublicKey = bytes.Repeat([]byte{11}, 32)
		_, _, err = database.RegisterAppInstallation(context.Background(), owner.ID, first, now)
		Expect(err).To(MatchError(domain.ErrInstallationConflict))
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
		Expect(records[1].NumericID).To(Equal(5))
	})

	It("returns no numeric ID after all visible identifiers are occupied", func() {
		for numericID := domain.MinNumericID; numericID <= domain.MaxNumericID; numericID++ {
			_, err := database.pool.Exec(context.Background(), `
				INSERT INTO devices (id, numeric_id, profile_ciphertext, identity_public_key)
				VALUES (gen_random_uuid(), $1, ''::bytea, ''::bytea)`, numericID)
			Expect(err).NotTo(HaveOccurred())
		}

		_, err := database.CreateDevice(context.Background(), firstDeviceID, nil, nil, nil)

		Expect(errors.Is(err, domain.ErrNoNumericID)).To(BeTrue())
	})

	It("cannot pair when all visible identifiers are occupied", func() {
		owner := createOwner()
		for numericID := domain.MinNumericID; numericID <= domain.MaxNumericID; numericID++ {
			_, err := database.pool.Exec(context.Background(), `
				INSERT INTO devices (id, numeric_id, profile_ciphertext, identity_public_key)
				VALUES (gen_random_uuid(), $1, ''::bytea, ''::bytea)`, numericID)
			Expect(err).NotTo(HaveOccurred())
		}
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

		var kind string
		var routingKey string
		var payload []byte
		Expect(database.pool.QueryRow(context.Background(), `
			SELECT kind, routing_key, payload
			FROM outbox_jobs
			WHERE kind = 'voip.incoming_call'`).Scan(&kind, &routingKey, &payload)).To(Succeed())
		Expect(kind).To(Equal("voip.incoming_call"))
		Expect(routingKey).To(Equal(installation.ID))
		var delivery domain.IncomingCallDelivery
		Expect(json.Unmarshal(payload, &delivery)).To(Succeed())
		Expect(delivery.NotificationID).To(Equal(notification.ID))
		Expect(delivery.Recipient).To(Equal(notification.Recipients[0]))
		var jobs int
		Expect(database.pool.QueryRow(context.Background(), `
			SELECT count(*) FROM outbox_jobs WHERE kind = 'voip.incoming_call'`).Scan(&jobs)).To(Succeed())
		Expect(jobs).To(Equal(1))

		notification.Recipients[0].Ciphertext = []byte("different ciphertext")
		_, err = database.InsertIncomingCall(context.Background(), notification, expiresAt)
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
		var jobID int64
		Expect(database.pool.QueryRow(context.Background(), `
			INSERT INTO outbox_jobs (kind, routing_key, payload, available_at, expires_at)
			VALUES ('voip.incoming_call', $1, $2, $3, $4)
			RETURNING id`, installation.ID, []byte(`{"ciphertext":"opaque"}`), now, now.Add(time.Minute)).Scan(&jobID)).To(Succeed())

		job, err := database.ClaimVoIPPushJob(context.Background(), now, 20*time.Second)
		Expect(err).NotTo(HaveOccurred())
		Expect(job.ID).To(Equal(jobID))
		Expect(job.Attempt).To(Equal(1))
		Expect(job.Environment).To(Equal("sandbox"))
		Expect(job.DeviceToken).To(Equal(token))
		Expect(job.LeaseExpiresAt).To(Equal(now.Add(20 * time.Second)))

		Expect(database.RetryPushJob(
			context.Background(), job.ID, job.Attempt, now.Add(5*time.Second), "temporary failure",
		)).To(Succeed())
		job, err = database.ClaimVoIPPushJob(context.Background(), now.Add(5*time.Second), 20*time.Second)
		Expect(err).NotTo(HaveOccurred())
		Expect(job.Attempt).To(Equal(2))
		Expect(database.CompletePushJob(context.Background(), job.ID, job.Attempt, now.Add(6*time.Second))).To(Succeed())
		Expect(database.CompletePushJob(context.Background(), job.ID, job.Attempt, now.Add(7*time.Second))).To(MatchError(pgx.ErrNoRows))

		Expect(database.DeleteVoIPPushToken(context.Background(), installation.ID, "sandbox", token)).To(Succeed())
		var tokens int
		Expect(database.pool.QueryRow(context.Background(), `
			SELECT count(*) FROM push_tokens WHERE installation_id = $1`, installation.ID).Scan(&tokens)).To(Succeed())
		Expect(tokens).To(BeZero())
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
		var count int
		Expect(database.pool.QueryRow(context.Background(), `
			SELECT count(*) FROM incoming_call_notifications WHERE id = $1`, notification.ID).Scan(&count)).To(Succeed())
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
		_, _, err := database.RegisterAppInstallation(context.Background(), owner.ID, first, now)
		Expect(err).NotTo(HaveOccurred())
		_, _, err = database.RegisterAppInstallation(context.Background(), owner.ID, second, now.Add(time.Second))
		Expect(err).NotTo(HaveOccurred())
		_, err = database.pool.Exec(context.Background(), `
			UPDATE app_installations SET approved_at = $2 WHERE id = $1`, second.ID, now)
		Expect(err).NotTo(HaveOccurred())
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

		var claimedJobs int
		var routingKey string
		var payload []byte
		Expect(database.pool.QueryRow(context.Background(), `
			SELECT count(*) FROM outbox_jobs WHERE kind = 'voip.incoming_call_claimed'`).Scan(&claimedJobs)).To(Succeed())
		Expect(claimedJobs).To(Equal(1))
		Expect(database.pool.QueryRow(context.Background(), `
			SELECT routing_key, payload
			FROM outbox_jobs
			WHERE kind = 'voip.incoming_call_claimed'`).Scan(&routingKey, &payload)).To(Succeed())
		Expect(routingKey).To(Equal(second.ID))
		var delivery domain.IncomingCallClaimDelivery
		Expect(json.Unmarshal(payload, &delivery)).To(Succeed())
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
		Expect(err).To(MatchError(ContainSubstring("insert event")))
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
		Expect(err).To(MatchError(ContainSubstring("begin owner bootstrap")))
		_, err = database.OwnerCredentialByUsername(ctx, "admin")
		Expect(err).To(HaveOccurred())
		Expect(database.CreateSession(ctx, owner.ID, pair)).To(MatchError(ContainSubstring("create auth session")))
		Expect(database.RotateSession(ctx, pair.RefreshToken, newPair(), now)).To(MatchError(ContainSubstring("begin session rotation")))
		Expect(database.RevokeSession(ctx, pair.AccessToken, now)).To(MatchError(ContainSubstring("revoke auth session")))
		_, err = database.AuthenticateOwnerSession(ctx, pair.AccessToken, now)
		Expect(err).To(HaveOccurred())
		Expect(database.CreatePairingToken(ctx, owner.ID, bytes.Repeat([]byte{1}, 32), now)).To(MatchError(ContainSubstring("create pairing token")))
		_, err = database.ConsumePairingToken(ctx, bytes.Repeat([]byte{1}, 32), now, DevicePairing{})
		Expect(err).To(MatchError(ContainSubstring("begin device pairing")))
		_, err = database.AuthenticateDevice(ctx, firstDeviceID, newToken(), now)
		Expect(err).To(HaveOccurred())
		_, err = database.CreateDevice(ctx, firstDeviceID, []byte{}, []byte{}, nil)
		Expect(err).To(MatchError(ContainSubstring("begin device transaction")))
		Expect(database.UpdateNumericID(ctx, firstDeviceID, 1)).To(MatchError(ContainSubstring("update numeric id")))
		_, err = database.ListDevices(ctx)
		Expect(err).To(MatchError(ContainSubstring("list devices")))
		_, _, err = database.InsertEvent(ctx, domain.EventEnvelope{}, now)
		Expect(err).To(MatchError(ContainSubstring("insert event")))
	})

	It("reports ping and migration failures", func() {
		_, err := Open(context.Background(), "postgres://simdeck:simdeck-test-only@127.0.0.1:1/simdeck_test?sslmode=disable&connect_timeout=1")
		Expect(err).To(MatchError(ContainSubstring("ping postgres")))

		closed, err := Open(context.Background(), os.Getenv("TEST_DATABASE_URL"))
		Expect(err).NotTo(HaveOccurred())
		closed.Close()
		Expect(closed.Migrate(context.Background())).To(MatchError(ContainSubstring("apply migration")))
	})
})
