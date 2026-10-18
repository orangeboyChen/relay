package identity

import (
	"testing"
)

func TestHardwareSlotKeyIsStableAndSlotSpecific(t *testing.T) {
	first, err := HardwareSlotKey("867530900000001", 1)
	if err != nil {
		t.Fatal(err)
	}
	repeat, err := HardwareSlotKey("867530900000001", 1)
	if err != nil {
		t.Fatal(err)
	}
	if first != repeat {
		t.Fatalf("stable key changed: %q != %q", first, repeat)
	}
	second, err := HardwareSlotKey("867530900000001", 2)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("different slots share a hardware key")
	}
}

func TestDeriveDeviceUUIDIsOrderIndependent(t *testing.T) {
	first, err := HardwareSlotKey("867530900000001", 1)
	if err != nil {
		t.Fatal(err)
	}
	second, err := HardwareSlotKey("867530900000001", 2)
	if err != nil {
		t.Fatal(err)
	}
	left, err := DeriveDeviceUUID([]string{first, second})
	if err != nil {
		t.Fatal(err)
	}
	right, err := DeriveDeviceUUID([]string{second, first})
	if err != nil {
		t.Fatal(err)
	}
	if left != right || !ValidDeviceUUID(left) {
		t.Fatalf("device identity is not stable: %q != %q", left, right)
	}
}

func TestDeviceUUIDIsSharedByAllSlotsOnOneModem(t *testing.T) {
	first, err := DeriveDeviceUUIDFromEquipmentIdentity("867530900000001")
	if err != nil {
		t.Fatal(err)
	}
	second, err := DeriveDeviceUUIDFromEquipmentIdentity("867530900000001")
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("device identity changed between slots: %q != %q", first, second)
	}
}

func TestHardwareIdentityRejectsInvalidInputs(t *testing.T) {
	if _, err := HardwareSlotKey("not-an-imei", 1); err == nil {
		t.Fatal("expected invalid equipment identity error")
	}
	if _, err := HardwareSlotKey("867530900000001", 0); err == nil {
		t.Fatal("expected invalid slot error")
	}
	if _, err := DeriveDeviceUUID(nil); err == nil {
		t.Fatal("expected missing slot error")
	}
}
