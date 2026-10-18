package identity

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strconv"
	"strings"
)

const hardwareIdentityDomain = "sim-deck-device-hardware-v1"

// HardwareSlotKey returns an opaque stable key for one modem-owned physical SIM slot.
func HardwareSlotKey(equipmentIdentity string, slotIndex int) (string, error) {
	equipmentIdentity = strings.TrimSpace(equipmentIdentity)
	if len(equipmentIdentity) < 14 || len(equipmentIdentity) > 16 || !digitsOnly(equipmentIdentity) {
		return "", errors.New("modem equipment identity must contain 14 to 16 digits")
	}
	if slotIndex < 1 || slotIndex > 32 {
		return "", errors.New("SIM slot index must be between 1 and 32")
	}
	digest := sha256.Sum256([]byte(hardwareIdentityDomain + "\x00" + equipmentIdentity + "\x00" + strconv.Itoa(slotIndex)))
	return hex.EncodeToString(digest[:]), nil
}

// DeriveDeviceUUIDFromEquipmentIdentity identifies one modem independently of its active slot.
func DeriveDeviceUUIDFromEquipmentIdentity(equipmentIdentity string) (string, error) {
	equipmentIdentity = strings.TrimSpace(equipmentIdentity)
	if len(equipmentIdentity) < 14 || len(equipmentIdentity) > 16 || !digitsOnly(equipmentIdentity) {
		return "", errors.New("modem equipment identity must contain 14 to 16 digits")
	}
	digest := sha256.Sum256([]byte(hardwareIdentityDomain + "\x00device\x00" + equipmentIdentity))
	return uuidFromDigest(digest), nil
}

// DeriveDeviceUUID deterministically identifies a device from all of its physical slot keys.
func DeriveDeviceUUID(slotKeys []string) (string, error) {
	if len(slotKeys) == 0 {
		return "", errors.New("at least one physical SIM slot key is required")
	}
	normalized := make([]string, len(slotKeys))
	seen := make(map[string]struct{}, len(slotKeys))
	for index, key := range slotKeys {
		key = strings.ToLower(strings.TrimSpace(key))
		if len(key) != sha256.Size*2 {
			return "", errors.New("physical SIM slot key is invalid")
		}
		if _, err := hex.DecodeString(key); err != nil {
			return "", errors.New("physical SIM slot key is invalid")
		}
		if _, exists := seen[key]; exists {
			return "", errors.New("physical SIM slot keys must be unique")
		}
		seen[key] = struct{}{}
		normalized[index] = key
	}
	sort.Strings(normalized)
	digest := sha256.Sum256([]byte(hardwareIdentityDomain + "\x00slots\x00" + strings.Join(normalized, "\x00")))
	return uuidFromDigest(digest), nil
}

func uuidFromDigest(digest [sha256.Size]byte) string {
	uuid := append([]byte(nil), digest[:16]...)
	uuid[6] = uuid[6]&0x0f | 0x50
	uuid[8] = uuid[8]&0x3f | 0x80
	encoded := hex.EncodeToString(uuid)
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32]
}

func digitsOnly(value string) bool {
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}
