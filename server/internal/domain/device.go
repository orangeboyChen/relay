package domain

import (
	"errors"
	"fmt"
	"strings"
)

const (
	MinNumericID = 1
	MaxNumericID = 99
)

var (
	ErrInvalidNumericID   = errors.New("numeric id must be between 1 and 99")
	ErrDuplicateNumericID = errors.New("numeric id is already in use")
	ErrNoNumericID        = errors.New("no numeric id is available")
)

// DeviceLineIdentity is the public identity of one modem and SIM line.
type DeviceLineIdentity struct {
	DeviceUUID  string `json:"device_uuid"`
	NumericID   int    `json:"numeric_id"`
	DeviceName  string `json:"device_name"`
	LineName    string `json:"line_name"`
	PhoneNumber string `json:"phone_number"`
}

// DisplayName returns the preferred line label without exposing the management name.
func (identity DeviceLineIdentity) DisplayName() string {
	if phone := strings.TrimSpace(identity.PhoneNumber); phone != "" {
		return phone
	}
	if line := strings.TrimSpace(identity.LineName); line != "" {
		return line
	}
	return fmt.Sprintf("SIM %d", identity.NumericID)
}

// ValidateNumericID verifies the compact SIM identifier against the account allocation.
func ValidateNumericID(numericID int, occupied map[int]struct{}) error {
	if numericID < MinNumericID || numericID > MaxNumericID {
		return ErrInvalidNumericID
	}
	if _, exists := occupied[numericID]; exists {
		return ErrDuplicateNumericID
	}
	return nil
}

// NextNumericID returns the smallest available identifier in the supported range.
func NextNumericID(occupied map[int]struct{}) (int, error) {
	for candidate := MinNumericID; candidate <= MaxNumericID; candidate++ {
		if _, exists := occupied[candidate]; !exists {
			return candidate, nil
		}
	}
	return 0, ErrNoNumericID
}
