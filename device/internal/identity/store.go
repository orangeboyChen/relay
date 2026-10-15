package identity

import (
	"crypto/ed25519"
	"crypto/rand"
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
	stateVersion = 1
	stateFile    = "identity.json"
	tokenBytes   = 32
)

var ErrInvalidState = errors.New("device identity state is invalid")

// State is the durable identity and server binding for one modem container.
type State struct {
	Version            int                `json:"version"`
	DeviceUUID         string             `json:"device_uuid"`
	IdentityPrivateKey ed25519.PrivateKey `json:"identity_private_key"`
	ServerURL          string             `json:"server_url,omitempty"`
	PendingCredential  string             `json:"pending_credential,omitempty"`
	Credential         string             `json:"credential,omitempty"`
	NumericID          int                `json:"numeric_id,omitempty"`
}

// PublicKey derives the identity public key without persisting a second copy.
func (state State) PublicKey() ed25519.PublicKey {
	if len(state.IdentityPrivateKey) != ed25519.PrivateKeySize {
		return nil
	}
	publicKey := state.IdentityPrivateKey.Public().(ed25519.PublicKey)
	return append(ed25519.PublicKey(nil), publicKey...)
}

// Store persists the identity in a container-specific state directory.
type Store struct {
	directory string
	random    io.Reader
}

// NewStore creates an identity store rooted at a private persistent directory.
func NewStore(directory string) Store {
	return Store{directory: directory, random: rand.Reader}
}

// LoadOrCreate returns the existing identity or atomically creates a new one.
func (store Store) LoadOrCreate() (State, error) {
	if strings.TrimSpace(store.directory) == "" {
		return State{}, fmt.Errorf("%w: state directory is required", ErrInvalidState)
	}
	if err := os.MkdirAll(store.directory, 0o700); err != nil {
		return State{}, fmt.Errorf("create state directory: %w", err)
	}
	path := filepath.Join(store.directory, stateFile)
	state, err := load(path)
	if err == nil {
		return state, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return State{}, err
	}

	_, privateKey, err := ed25519.GenerateKey(store.randomSource())
	if err != nil {
		return State{}, fmt.Errorf("generate device identity key: %w", err)
	}
	deviceUUID, err := randomUUID(store.randomSource())
	if err != nil {
		return State{}, err
	}
	state = State{
		Version:            stateVersion,
		DeviceUUID:         deviceUUID,
		IdentityPrivateKey: privateKey,
	}
	if err := store.save(state); err != nil {
		return State{}, err
	}
	return state, nil
}

// PreparePairing creates and durably stores the credential before a one-time pairing request is sent.
func (store Store) PreparePairing(state State) (State, error) {
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

// Bind promotes the pending credential and stores the visible SIM ID after successful pairing.
func (store Store) Bind(state State, serverURL string, numericID int) (State, error) {
	state.ServerURL = strings.TrimRight(strings.TrimSpace(serverURL), "/")
	state.Credential = state.PendingCredential
	state.PendingCredential = ""
	state.NumericID = numericID
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
	if state.Version != stateVersion {
		return fmt.Errorf("%w: unsupported version", ErrInvalidState)
	}
	if !validUUID(state.DeviceUUID) {
		return fmt.Errorf("%w: malformed device UUID", ErrInvalidState)
	}
	if len(state.IdentityPrivateKey) != ed25519.PrivateKeySize || len(state.PublicKey()) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: malformed private key", ErrInvalidState)
	}
	if state.PendingCredential != "" {
		if state.Credential != "" || state.ServerURL != "" || state.NumericID != 0 || !validCredential(state.PendingCredential) {
			return fmt.Errorf("%w: pending credential state is inconsistent", ErrInvalidState)
		}
		return nil
	}
	if state.Credential != "" {
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

func randomUUID(random io.Reader) (string, error) {
	value := make([]byte, 16)
	if _, err := io.ReadFull(random, value); err != nil {
		return "", fmt.Errorf("generate device UUID: %w", err)
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value)
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32], nil
}

func validUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	decoded := make([]byte, 16)
	_, err := hex.Decode(decoded, []byte(strings.ReplaceAll(value, "-", "")))
	return err == nil
}
