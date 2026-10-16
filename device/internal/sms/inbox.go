package sms

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

var (
	ErrInvalidConcatenation = errors.New("invalid SMS concatenation metadata")
	ErrUnknownMessage       = errors.New("SMS message is not pending delivery")
)

const (
	inboxStateVersion = 1
	multipartWindow   = 10 * time.Minute
	storageKeyBytes   = 32
	storageNonceBytes = 12
	storageContext    = "sim-deck-sms-inbox-v1"
)

// ReceivedMessage is one complete, deduplicated SMS ready for synchronization.
type ReceivedMessage struct {
	ID        string    `json:"id"`
	Sender    string    `json:"sender"`
	Timestamp time.Time `json:"timestamp"`
	Text      string    `json:"text"`
	Segments  int       `json:"segments"`
}

type multipartKey struct {
	sender    string
	reference uint16
	total     int
	window    int64
}

type multipartMessage struct {
	parts     map[int]Message
	updatedAt time.Time
}

// Inbox reassembles multipart messages and suppresses repeated history or URC delivery.
type Inbox struct {
	mu         sync.Mutex
	multipart  map[multipartKey]*multipartMessage
	delivered  map[[sha256.Size]byte]time.Time
	pending    map[[sha256.Size]byte]ReceivedMessage
	retention  time.Duration
	clock      func() time.Time
	statePath  string
	storageKey []byte
	random     io.Reader
}

type persistedInbox struct {
	Version   int                `json:"version"`
	Delivered []persistedRecord  `json:"delivered"`
	Pending   []persistedPending `json:"pending,omitempty"`
}

type persistedRecord struct {
	Fingerprint string    `json:"fingerprint"`
	DeliveredAt time.Time `json:"delivered_at"`
}

type persistedPending struct {
	Fingerprint string `json:"fingerprint"`
	Nonce       string `json:"nonce"`
	Ciphertext  string `json:"ciphertext"`
}

// NewInbox creates an inbox that remembers delivered fingerprints for the retention period.
func NewInbox(retention time.Duration) *Inbox {
	return newInbox(retention, time.Now)
}

// NewPersistentInbox creates an inbox with encrypted pending messages and durable acknowledgements.
func NewPersistentInbox(statePath string, retention time.Duration, storageKey []byte) (*Inbox, error) {
	return newPersistentInbox(statePath, retention, time.Now, storageKey)
}

func newInbox(retention time.Duration, clock func() time.Time) *Inbox {
	if retention <= 0 {
		retention = 7 * 24 * time.Hour
	}
	return &Inbox{
		multipart: make(map[multipartKey]*multipartMessage),
		delivered: make(map[[sha256.Size]byte]time.Time),
		pending:   make(map[[sha256.Size]byte]ReceivedMessage),
		retention: retention,
		clock:     clock,
		random:    rand.Reader,
	}
}

func newPersistentInbox(statePath string, retention time.Duration, clock func() time.Time, storageKey []byte) (*Inbox, error) {
	if statePath == "" || !filepath.IsAbs(statePath) {
		return nil, errors.New("SMS inbox state path must be absolute")
	}
	if len(storageKey) != storageKeyBytes {
		return nil, errors.New("SMS inbox storage key must contain 32 bytes")
	}
	inbox := newInbox(retention, clock)
	inbox.statePath = statePath
	inbox.storageKey = append([]byte(nil), storageKey...)
	delivered, pending, err := loadState(statePath, storageKey)
	if err != nil {
		return nil, err
	}
	inbox.delivered = delivered
	inbox.pending = pending
	if inbox.prune(clock()) {
		if err := inbox.persistState(); err != nil {
			return nil, err
		}
	}
	return inbox, nil
}

// ImportPDUs decodes modem history and returns only newly completed messages.
func (inbox *Inbox) ImportPDUs(pdus []string) ([]ReceivedMessage, error) {
	result := make([]ReceivedMessage, 0, len(pdus))
	var failures []error
	for index, pdu := range pdus {
		message, err := DecodeDeliver(pdu)
		if err != nil {
			failures = append(failures, fmt.Errorf("decode history PDU %d: %w", index, err))
			continue
		}
		complete, created, err := inbox.AddSegment(message)
		if err != nil {
			failures = append(failures, fmt.Errorf("import history PDU %d: %w", index, err))
			continue
		}
		if created {
			result = append(result, complete)
		}
	}
	return result, errors.Join(failures...)
}

// AddSegment accepts a decoded message or multipart segment.
func (inbox *Inbox) AddSegment(message Message) (ReceivedMessage, bool, error) {
	inbox.mu.Lock()
	defer inbox.mu.Unlock()

	now := inbox.clock()
	if inbox.prune(now) {
		if err := inbox.persistState(); err != nil {
			return ReceivedMessage{}, false, err
		}
	}
	if message.Concatenation == nil {
		return inbox.deliver(message.Sender, message.Timestamp, message.Text, 1)
	}

	concatenation := message.Concatenation
	if concatenation.Total < 1 || concatenation.Sequence < 1 || concatenation.Sequence > concatenation.Total {
		return ReceivedMessage{}, false, ErrInvalidConcatenation
	}
	key := multipartKey{
		sender:    message.Sender,
		reference: concatenation.Reference,
		total:     concatenation.Total,
		window:    message.Timestamp.UTC().UnixNano() / int64(multipartWindow),
	}
	pending := inbox.multipart[key]
	if pending == nil {
		pending = &multipartMessage{parts: make(map[int]Message)}
		inbox.multipart[key] = pending
	}
	if existing, exists := pending.parts[concatenation.Sequence]; exists {
		if existing.Text != message.Text {
			return ReceivedMessage{}, false, ErrInvalidConcatenation
		}
		return ReceivedMessage{}, false, nil
	}
	pending.parts[concatenation.Sequence] = message
	pending.updatedAt = now
	if len(pending.parts) != concatenation.Total {
		return ReceivedMessage{}, false, nil
	}

	text := ""
	timestamp := message.Timestamp
	for sequence := 1; sequence <= concatenation.Total; sequence++ {
		part, exists := pending.parts[sequence]
		if !exists {
			return ReceivedMessage{}, false, nil
		}
		text += part.Text
		if part.Timestamp.Before(timestamp) {
			timestamp = part.Timestamp
		}
	}
	delete(inbox.multipart, key)
	return inbox.deliver(message.Sender, timestamp, text, concatenation.Total)
}

func (inbox *Inbox) deliver(sender string, timestamp time.Time, text string, segments int) (ReceivedMessage, bool, error) {
	fingerprint := messageFingerprint(sender, timestamp, text)
	if _, exists := inbox.delivered[fingerprint]; exists {
		return ReceivedMessage{}, false, nil
	}
	if _, exists := inbox.pending[fingerprint]; exists {
		return ReceivedMessage{}, false, nil
	}
	received := ReceivedMessage{
		ID:        fmt.Sprintf("%x", fingerprint),
		Sender:    sender,
		Timestamp: timestamp,
		Text:      text,
		Segments:  segments,
	}
	inbox.pending[fingerprint] = received
	if err := inbox.persistState(); err != nil {
		delete(inbox.pending, fingerprint)
		return ReceivedMessage{}, false, err
	}
	return received, true, nil
}

// PendingMessages returns complete messages that still require successful downstream delivery.
func (inbox *Inbox) PendingMessages() []ReceivedMessage {
	inbox.mu.Lock()
	defer inbox.mu.Unlock()
	messages := make([]ReceivedMessage, 0, len(inbox.pending))
	for _, message := range inbox.pending {
		messages = append(messages, message)
	}
	sort.Slice(messages, func(left int, right int) bool {
		if messages[left].Timestamp.Equal(messages[right].Timestamp) {
			return messages[left].ID < messages[right].ID
		}
		return messages[left].Timestamp.Before(messages[right].Timestamp)
	})
	return messages
}

// Acknowledge records a message only after the encrypted downstream sink accepted it.
func (inbox *Inbox) Acknowledge(messageID string) error {
	inbox.mu.Lock()
	defer inbox.mu.Unlock()
	fingerprint, err := decodeFingerprint(messageID)
	if err != nil {
		return ErrUnknownMessage
	}
	if _, exists := inbox.delivered[fingerprint]; exists {
		return nil
	}
	message, exists := inbox.pending[fingerprint]
	if !exists {
		return ErrUnknownMessage
	}
	delete(inbox.pending, fingerprint)
	inbox.delivered[fingerprint] = inbox.clock()
	if err := inbox.persistState(); err != nil {
		delete(inbox.delivered, fingerprint)
		inbox.pending[fingerprint] = message
		return err
	}
	return nil
}

func (inbox *Inbox) prune(now time.Time) bool {
	cutoff := now.Add(-inbox.retention)
	deliveredChanged := false
	for fingerprint, deliveredAt := range inbox.delivered {
		if deliveredAt.Before(cutoff) {
			delete(inbox.delivered, fingerprint)
			deliveredChanged = true
		}
	}
	for key, message := range inbox.multipart {
		if message.updatedAt.Before(cutoff) {
			delete(inbox.multipart, key)
		}
	}
	return deliveredChanged
}

func loadState(path string, storageKey []byte) (map[[sha256.Size]byte]time.Time, map[[sha256.Size]byte]ReceivedMessage, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return make(map[[sha256.Size]byte]time.Time), make(map[[sha256.Size]byte]ReceivedMessage), nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("open SMS inbox state: %w", err)
	}
	defer file.Close()

	var state persistedInbox
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil || state.Version != inboxStateVersion {
		return nil, nil, errors.New("SMS inbox state is invalid")
	}
	delivered := make(map[[sha256.Size]byte]time.Time, len(state.Delivered))
	for _, record := range state.Delivered {
		fingerprint, err := decodeFingerprint(record.Fingerprint)
		if err != nil || record.DeliveredAt.IsZero() {
			return nil, nil, errors.New("SMS inbox state is invalid")
		}
		delivered[fingerprint] = record.DeliveredAt
	}
	aead, err := storageAEAD(storageKey)
	if err != nil {
		return nil, nil, errors.New("SMS inbox state is invalid")
	}
	pending := make(map[[sha256.Size]byte]ReceivedMessage, len(state.Pending))
	for _, record := range state.Pending {
		fingerprint, err := decodeFingerprint(record.Fingerprint)
		nonce, nonceError := hex.DecodeString(record.Nonce)
		ciphertext, ciphertextError := hex.DecodeString(record.Ciphertext)
		if err != nil || nonceError != nil || ciphertextError != nil || len(nonce) != storageNonceBytes || len(ciphertext) == 0 {
			return nil, nil, errors.New("SMS inbox state is invalid")
		}
		plaintext, err := aead.Open(nil, nonce, ciphertext, fingerprint[:])
		if err != nil {
			return nil, nil, errors.New("SMS inbox state is invalid")
		}
		var message ReceivedMessage
		if err := json.Unmarshal(plaintext, &message); err != nil ||
			message.ID != record.Fingerprint || message.Timestamp.IsZero() || message.Segments < 1 ||
			messageFingerprint(message.Sender, message.Timestamp, message.Text) != fingerprint {
			return nil, nil, errors.New("SMS inbox state is invalid")
		}
		if _, exists := delivered[fingerprint]; exists {
			return nil, nil, errors.New("SMS inbox state is invalid")
		}
		pending[fingerprint] = message
	}
	return delivered, pending, nil
}

func (inbox *Inbox) persistState() error {
	if inbox.statePath == "" {
		return nil
	}
	records := make([]persistedRecord, 0, len(inbox.delivered))
	for fingerprint, deliveredAt := range inbox.delivered {
		records = append(records, persistedRecord{Fingerprint: hex.EncodeToString(fingerprint[:]), DeliveredAt: deliveredAt})
	}
	sort.Slice(records, func(left int, right int) bool { return records[left].Fingerprint < records[right].Fingerprint })
	aead, err := storageAEAD(inbox.storageKey)
	if err != nil {
		return fmt.Errorf("encrypt SMS inbox state: %w", err)
	}
	pending := make([]persistedPending, 0, len(inbox.pending))
	for fingerprint, message := range inbox.pending {
		plaintext, err := json.Marshal(message)
		if err != nil {
			return fmt.Errorf("encode pending SMS: %w", err)
		}
		nonce := make([]byte, storageNonceBytes)
		if _, err := io.ReadFull(inbox.random, nonce); err != nil {
			return fmt.Errorf("generate pending SMS nonce: %w", err)
		}
		pending = append(pending, persistedPending{
			Fingerprint: hex.EncodeToString(fingerprint[:]),
			Nonce:       hex.EncodeToString(nonce),
			Ciphertext:  hex.EncodeToString(aead.Seal(nil, nonce, plaintext, fingerprint[:])),
		})
	}
	sort.Slice(pending, func(left int, right int) bool { return pending[left].Fingerprint < pending[right].Fingerprint })
	content, err := json.Marshal(persistedInbox{Version: inboxStateVersion, Delivered: records, Pending: pending})
	if err != nil {
		return fmt.Errorf("encode SMS inbox state: %w", err)
	}
	directory := filepath.Dir(inbox.statePath)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create SMS inbox state directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".sms-inbox-*")
	if err != nil {
		return fmt.Errorf("create temporary SMS inbox state: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("protect temporary SMS inbox state: %w", err)
	}
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write SMS inbox state: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync SMS inbox state: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close SMS inbox state: %w", err)
	}
	if err := os.Rename(temporaryPath, inbox.statePath); err != nil {
		return fmt.Errorf("replace SMS inbox state: %w", err)
	}
	if err := os.Chmod(inbox.statePath, 0o600); err != nil {
		return fmt.Errorf("protect SMS inbox state: %w", err)
	}
	return nil
}

func storageAEAD(storageKey []byte) (cipher.AEAD, error) {
	key, err := hkdf.Key(sha256.New, storageKey, nil, storageContext, storageKeyBytes)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func decodeFingerprint(encoded string) ([sha256.Size]byte, error) {
	decoded, err := hex.DecodeString(encoded)
	if err != nil || len(decoded) != sha256.Size {
		return [sha256.Size]byte{}, errors.New("invalid SMS fingerprint")
	}
	var fingerprint [sha256.Size]byte
	copy(fingerprint[:], decoded)
	return fingerprint, nil
}

func messageFingerprint(sender string, timestamp time.Time, text string) [sha256.Size]byte {
	buffer := make([]byte, 0, len(sender)+len(text)+16)
	buffer = binary.BigEndian.AppendUint32(buffer, uint32(len(sender)))
	buffer = append(buffer, sender...)
	buffer = binary.BigEndian.AppendUint64(buffer, uint64(timestamp.UTC().UnixMilli()))
	buffer = binary.BigEndian.AppendUint32(buffer, uint32(len(text)))
	buffer = append(buffer, text...)
	return sha256.Sum256(buffer)
}
