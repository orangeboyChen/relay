package store

import (
	"bytes"
	"testing"
)

func TestInitialMigrationContainsRequiredTables(t *testing.T) {
	schema, err := InitialMigration()
	if err != nil {
		t.Fatalf("InitialMigration() error = %v", err)
	}
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
		if !bytes.Contains(schema, table) {
			t.Fatalf("initial migration does not contain %q", table)
		}
	}
}

func TestAuthSessionMigrationContainsSessionTable(t *testing.T) {
	schema, err := migrations.ReadFile("migrations/003_auth_sessions.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	if !bytes.Contains(schema, []byte("auth_sessions")) {
		t.Fatal("auth migration does not contain the session table")
	}
}

func TestMigrationsEnforceSingleOwner(t *testing.T) {
	schema, err := migrations.ReadFile("migrations/002_single_owner.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	if !bytes.Contains(schema, []byte("owner_accounts_singleton_idx")) {
		t.Fatal("single-owner migration does not contain its unique index")
	}
}

func TestDevicePairingMigrationContainsCredentialStorage(t *testing.T) {
	schema, err := migrations.ReadFile("migrations/004_device_pairing.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	for _, required := range [][]byte{
		[]byte("owner_id"),
		[]byte("device_credentials"),
		[]byte("credential_hash"),
	} {
		if !bytes.Contains(schema, required) {
			t.Fatalf("device pairing migration does not contain %q", required)
		}
	}
}

func TestPairingRetryMigrationLinksConsumedTokenToDevice(t *testing.T) {
	schema, err := migrations.ReadFile("migrations/005_pairing_retry.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	if !bytes.Contains(schema, []byte("paired_device_id")) {
		t.Fatal("pairing retry migration does not link a token to its paired device")
	}
}
