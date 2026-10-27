package domain

import (
	"errors"
	"testing"
)

func TestDisplayName(t *testing.T) {
	tests := []struct {
		name     string
		identity DeviceLineIdentity
		want     string
	}{
		{name: "phone number", identity: DeviceLineIdentity{NumericID: 2, LineName: "Work", PhoneNumber: "13500000000"}, want: "13500000000"},
		{name: "line name", identity: DeviceLineIdentity{NumericID: 2, LineName: "Work"}, want: "Work"},
		{name: "numeric fallback", identity: DeviceLineIdentity{NumericID: 2}, want: "SIM 2"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.identity.DisplayName(); got != test.want {
				t.Fatalf("DisplayName() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestNextNumericID(t *testing.T) {
	occupied := map[int]struct{}{1: {}, 2: {}, 4: {}}
	got, err := NextNumericID(occupied)
	if err != nil {
		t.Fatalf("NextNumericID() error = %v", err)
	}
	if got != 3 {
		t.Fatalf("NextNumericID() = %d, want 3", got)
	}
	allOccupied := make(map[int]struct{}, MaxNumericID-MinNumericID+1)
	for numericID := MinNumericID; numericID <= MaxNumericID; numericID++ {
		allOccupied[numericID] = struct{}{}
	}
	if _, err := NextNumericID(allOccupied); !errors.Is(err, ErrNoNumericID) {
		t.Fatalf("NextNumericID() error = %v, want ErrNoNumericID", err)
	}
}

func TestValidateNumericID(t *testing.T) {
	occupied := map[int]struct{}{2: {}}
	if err := ValidateNumericID(3, occupied); err != nil {
		t.Fatalf("ValidateNumericID() unexpected error = %v", err)
	}
	if err := ValidateNumericID(0, occupied); !errors.Is(err, ErrInvalidNumericID) {
		t.Fatalf("ValidateNumericID() error = %v, want ErrInvalidNumericID", err)
	}
	if err := ValidateNumericID(2, occupied); !errors.Is(err, ErrDuplicateNumericID) {
		t.Fatalf("ValidateNumericID() error = %v, want ErrDuplicateNumericID", err)
	}
}
