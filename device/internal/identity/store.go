package identity

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	stateVersion = 3
	stateFile    = "identity.json"
	tokenBytes   = 32
)

var ErrInvalidState = errors.New("device identity state is invalid")

var ErrDeviceUUIDMismatch = errors.New("configured device UUID does not match persisted identity")
var ErrSlotIdentityMismatch = errors.New("configured modem slot does not match persisted identity")

// State is the durable identity and server binding for one modem container.
type State struct {
	Version            int                `json:"version"`
	DeviceUUID         string             `json:"device_uuid"`
	IdentityPrivateKey ed25519.PrivateKey `json:"identity_private_key"`
	CommandPrivateKey  []byte             `json:"command_private_key"`
	ServerURL          string             `json:"server_url,omitempty"`
	PendingCredential  string             `json:"pending_credential,omitempty"`
	Credential         string             `json:"credential,omitempty"`
	NumericID          int                `json:"numeric_id,omitempty"`
	SlotID             string             `json:"slot_id,omitempty"`
	SIMID              string             `json:"sim_id,omitempty"`
	SlotKey            string             `json:"slot_key,omitempty"`
	SlotIndex          int                `json:"slot_index,omitempty"`
	SIMFingerprint     string             `json:"sim_fingerprint,omitempty"`
}

// FingerprintSIM returns a stable opaque fingerprint without persisting the ICCID.
func (state State) FingerprintSIM(iccid string) (string, error) {
	iccid = strings.TrimSpace(iccid)
	if len(iccid) < 18 || len(iccid) > 22 {
		return "", fmt.Errorf("SIM ICCID has an invalid length")
	}
	for _, digit := range iccid {
		if digit < '0' || digit > '9' {
			return "", fmt.Errorf("SIM ICCID must contain only digits")
		}
	}
	digest := sha256.Sum256([]byte("sim-deck-sim-iccid-v1\x00" + iccid))
	return hex.EncodeToString(digest[:]), nil
}

// CommandPublicKey derives the X25519 key advertised for encrypted device commands.
func (state State) CommandPublicKey() []byte {
	privateKey, err := ecdh.X25519().NewPrivateKey(state.CommandPrivateKey)
	if err != nil {
		return nil
	}
	return append([]byte(nil), privateKey.PublicKey().Bytes()...)
}

// PublicKey derives the identity public key without persisting a second copy.
func (state State) PublicKey() ed25519.PublicKey {
	if len(state.IdentityPrivateKey) != ed25519.PrivateKeySize {
		return nil
	}
	publicKey := state.IdentityPrivateKey.Public().(ed25519.PublicKey)
	return append(ed25519.PublicKey(nil), publicKey...)
}

// IdentityFingerprint returns the short SHA-256 fingerprint shown during owner approval.
func (state State) IdentityFingerprint() string {
	publicKey := state.PublicKey()
	if len(publicKey) != ed25519.PublicKeySize {
		return ""
	}
	digest := sha256.Sum256(publicKey)
	parts := make([]string, 6)
	for index, value := range digest[:6] {
		parts[index] = fmt.Sprintf("%02X", value)
	}
	return strings.Join(parts, " ")
}

// Store persists the identity in a container-specific state directory.
type Store struct {
	directory  string
	deviceUUID string
	random     io.Reader
}

// NewStore creates an identity store rooted at a private persistent directory.
func NewStore(directory string, deviceUUID string) Store {
	return Store{directory: directory, deviceUUID: deviceUUID, random: rand.Reader}
}

// LoadOrCreate returns the existing identity or atomically creates a new one.
func (store Store) LoadOrCreate() (State, error) {
	if strings.TrimSpace(store.directory) == "" {
		return State{}, fmt.Errorf("%w: state directory is required", ErrInvalidState)
	}
	if !ValidDeviceUUID(store.deviceUUID) {
		return State{}, fmt.Errorf("%w: configured device UUID is malformed", ErrInvalidState)
	}
	if err := os.MkdirAll(store.directory, 0o700); err != nil {
		return State{}, fmt.Errorf("create state directory: %w", err)
	}
	path := filepath.Join(store.directory, stateFile)
	state, err := load(path)
	if err == nil {
		if state.DeviceUUID != store.deviceUUID {
			return State{}, fmt.Errorf("%w: configured %s, persisted %s", ErrDeviceUUIDMismatch, store.deviceUUID, state.DeviceUUID)
		}
		if state.Version == 1 {
			commandKey, keyError := ecdh.X25519().GenerateKey(store.randomSource())
			if keyError != nil {
				return State{}, fmt.Errorf("migrate device command key: %w", keyError)
			}
			state.Version = 2
			state.CommandPrivateKey = commandKey.Bytes()
			if saveError := store.save(state); saveError != nil {
				return State{}, fmt.Errorf("persist device identity migration: %w", saveError)
			}
		}
		if state.Version == 2 {
			state.Version = stateVersion
			if state.SlotKey == "" {
				state.SlotKey = "1"
			}
			if saveError := store.save(state); saveError != nil {
				return State{}, fmt.Errorf("persist slot identity migration: %w", saveError)
			}
		}
		return state, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return State{}, err
	}

	_, privateKey, err := ed25519.GenerateKey(store.randomSource())
	if err != nil {
		return State{}, fmt.Errorf("generate device identity key: %w", err)
	}
	commandKey, err := ecdh.X25519().GenerateKey(store.randomSource())
	if err != nil {
		return State{}, fmt.Errorf("generate device command key: %w", err)
	}
	state = State{
		Version:            stateVersion,
		DeviceUUID:         store.deviceUUID,
		IdentityPrivateKey: privateKey,
		CommandPrivateKey:  commandKey.Bytes(),
		SlotKey:            "1",
	}
	if err := store.save(state); err != nil {
		return State{}, err
	}
	return state, nil
}

// PrepareDiscovery creates and durably stores the credential before requesting app approval.
func (store Store) PrepareDiscovery(state State) (State, error) {
	if state.Credential != "" {
		return state, nil
	}
	if state.PendingCredential == "" {
		credential := make([]byte, tokenBytes)
		if _, err := io.ReadFull(store.randomSource(), credential); err != nil {
			return State{}, fmt.Errorf("generate pending device credential: %w", err)
		}
		state.PendingCredential = base64.RawURLEncoding.EncodeToString(credential)
	}
	if err := store.save(state); err != nil {
		return State{}, err
	}
	return state, nil
}

// ConfigureSlot binds this agent to one stable physical slot on its modem.
func (store Store) ConfigureSlot(state State, slotKey string, slotIndex int) (State, error) {
	if strings.TrimSpace(slotKey) == "" || len(slotKey) > 64 || slotIndex < 1 || slotIndex > 32 {
		return State{}, fmt.Errorf("%w: invalid slot key or index", ErrInvalidState)
	}
	if state.Credential != "" && state.SlotKey != "" && (state.SlotKey != slotKey || state.SlotIndex != 0 && state.SlotIndex != slotIndex) {
		return State{}, fmt.Errorf("%w: configured %s/%d, persisted %s/%d", ErrSlotIdentityMismatch, slotKey, slotIndex, state.SlotKey, state.SlotIndex)
	}
	state.SlotKey = slotKey
	state.SlotIndex = slotIndex
	if err := store.save(state); err != nil {
		return State{}, err
	}
	return state, nil
}

// SetSIMFingerprint persists the current card fingerprint in the device identity state.
func (store Store) SetSIMFingerprint(state State, fingerprint string) (State, error) {
	if fingerprint != "" && (len(fingerprint) != 64 || strings.Trim(fingerprint, "0123456789abcdef") != "") {
		return State{}, fmt.Errorf("%w: invalid SIM fingerprint", ErrInvalidState)
	}
	state.SIMFingerprint = fingerprint
	if err := store.save(state); err != nil {
		return State{}, err
	}
	return state, nil
}

// SetSIMAssignment persists server-owned identifiers for the card currently in this slot.
func (store Store) SetSIMAssignment(state State, simID string, numericID int) (State, error) {
	return store.SetSIMIdentity(state, state.SIMFingerprint, simID, numericID)
}

// SetServerSIMAssignment refreshes the server-owned slot and SIM identifiers without changing hardware identity.
func (store Store) SetServerSIMAssignment(state State, slotID string, fingerprint string, simID string, numericID int) (State, error) {
	if !ValidDeviceUUID(slotID) {
		return State{}, fmt.Errorf("%w: invalid slot assignment", ErrInvalidState)
	}
	state.SlotID = slotID
	return store.SetSIMIdentity(state, fingerprint, simID, numericID)
}

// SetSIMIdentity atomically persists the current card fingerprint and its server assignment.
func (store Store) SetSIMIdentity(state State, fingerprint string, simID string, numericID int) (State, error) {
	if fingerprint == "" || len(fingerprint) != 64 || strings.Trim(fingerprint, "0123456789abcdef") != "" ||
		!ValidDeviceUUID(simID) || numericID < 1 || numericID > 99 || !ValidDeviceUUID(state.SlotID) {
		return State{}, fmt.Errorf("%w: invalid SIM assignment", ErrInvalidState)
	}
	state.SIMFingerprint = fingerprint
	state.SIMID = simID
	state.NumericID = numericID
	if err := store.save(state); err != nil {
		return State{}, err
	}
	return state, nil
}

// Bind promotes the pending credential and stores the visible SIM ID after app approval.
func (store Store) Bind(state State, serverURL string, numericID int) (State, error) {
	return store.BindAssignment(state, serverURL, numericID, "", "", "1", 1)
}

// BindAssignment persists the server-assigned physical slot and SIM identities.
func (store Store) BindAssignment(state State, serverURL string, numericID int, slotID string, simID string, slotKey string, slotIndex int) (State, error) {
	state.ServerURL = strings.TrimRight(strings.TrimSpace(serverURL), "/")
	state.Credential = state.PendingCredential
	state.PendingCredential = ""
	state.NumericID = numericID
	state.SlotID = slotID
	state.SIMID = simID
	state.SlotKey = slotKey
	state.SlotIndex = slotIndex
	if err := validate(state); err != nil {
		return State{}, err
	}
	if err := store.save(state); err != nil {
		return State{}, err
	}
	return state, nil
}

func load(path string) (State, error) {
	file, err := os.Open(path)
	if err != nil {
		return State{}, err
	}
	defer file.Close()

	var state State
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return State{}, fmt.Errorf("%w: decode state: %v", ErrInvalidState, err)
	}
	if err := validate(state); err != nil {
		return State{}, err
	}
	return state, nil
}

func (store Store) save(state State) error {
	if state.DeviceUUID != store.deviceUUID {
		return fmt.Errorf("%w: configured %s, state %s", ErrDeviceUUIDMismatch, store.deviceUUID, state.DeviceUUID)
	}
	if err := validate(state); err != nil {
		return err
	}
	if err := os.MkdirAll(store.directory, 0o700); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	temporary, err := os.CreateTemp(store.directory, ".identity-*")
	if err != nil {
		return fmt.Errorf("create temporary identity: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()

	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("protect temporary identity: %w", err)
	}
	if err := json.NewEncoder(temporary).Encode(state); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("encode identity: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync identity: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close identity: %w", err)
	}
	path := filepath.Join(store.directory, stateFile)
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace identity: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("protect identity: %w", err)
	}
	return nil
}

func validate(state State) error {
	if state.Version != 1 && state.Version != 2 && state.Version != stateVersion {
		return fmt.Errorf("%w: unsupported version", ErrInvalidState)
	}
	if !ValidDeviceUUID(state.DeviceUUID) {
		return fmt.Errorf("%w: malformed device UUID", ErrInvalidState)
	}
	if len(state.IdentityPrivateKey) != ed25519.PrivateKeySize || len(state.PublicKey()) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: malformed private key", ErrInvalidState)
	}
	if state.Version == 1 {
		if len(state.CommandPrivateKey) != 0 {
			return fmt.Errorf("%w: legacy state contains a command key", ErrInvalidState)
		}
	} else if len(state.CommandPrivateKey) != 32 || len(state.CommandPublicKey()) != 32 {
		return fmt.Errorf("%w: malformed command private key", ErrInvalidState)
	}
	if state.PendingCredential != "" {
		if state.Credential != "" || state.ServerURL != "" || state.NumericID != 0 || !validCredential(state.PendingCredential) {
			return fmt.Errorf("%w: pending credential state is inconsistent", ErrInvalidState)
		}
	} else if state.Credential != "" {
		if state.ServerURL == "" {
			return fmt.Errorf("%w: server URL is required for a bound identity", ErrInvalidState)
		}
		if !validCredential(state.Credential) {
			return fmt.Errorf("%w: malformed device credential", ErrInvalidState)
		}
		if state.NumericID < 1 || state.NumericID > 99 {
			return fmt.Errorf("%w: numeric id must be between 1 and 99", ErrInvalidState)
		}
	} else if state.NumericID != 0 || state.ServerURL != "" {
		return fmt.Errorf("%w: binding metadata requires a device credential", ErrInvalidState)
	}
	if state.SlotID != "" && !ValidDeviceUUID(state.SlotID) {
		return fmt.Errorf("%w: malformed slot id", ErrInvalidState)
	}
	if state.SIMID != "" && !ValidDeviceUUID(state.SIMID) {
		return fmt.Errorf("%w: malformed SIM id", ErrInvalidState)
	}
	if state.SlotKey != "" && (len(state.SlotKey) > 64 || strings.TrimSpace(state.SlotKey) == "") {
		return fmt.Errorf("%w: malformed slot key", ErrInvalidState)
	}
	if state.SlotIndex < 0 || state.SlotIndex > 32 {
		return fmt.Errorf("%w: malformed slot index", ErrInvalidState)
	}
	if state.SIMFingerprint != "" && (len(state.SIMFingerprint) != 64 || strings.Trim(state.SIMFingerprint, "0123456789abcdef") != "") {
		return fmt.Errorf("%w: malformed SIM fingerprint", ErrInvalidState)
	}
	return nil
}

func validCredential(credential string) bool {
	decoded, err := base64.RawURLEncoding.DecodeString(credential)
	return err == nil && len(decoded) == tokenBytes
}

func (store Store) randomSource() io.Reader {
	if store.random == nil {
		return rand.Reader
	}
	return store.random
}

// ValidDeviceUUID reports whether value is a canonical lowercase UUID.
func ValidDeviceUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	for index, character := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			continue
		}
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f')) {
			return false
		}
	}
	return true
}
