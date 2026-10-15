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

func TestMigrationsEnforceSingleOwner(t *testing.T) {
	schema, err := migrations.ReadFile("migrations/002_single_owner.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	if !bytes.Contains(schema, []byte("owner_accounts_singleton_idx")) {
		t.Fatal("single-owner migration does not contain its unique index")
	}
}
