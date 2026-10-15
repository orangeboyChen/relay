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
