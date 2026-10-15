package sms

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

var ErrInvalidConcatenation = errors.New("invalid SMS concatenation metadata")

const (
	inboxStateVersion = 1
	multipartWindow   = 10 * time.Minute
)

// ReceivedMessage is one complete, deduplicated SMS ready for synchronization.
type ReceivedMessage struct {
	ID        string
	Sender    string
	Timestamp time.Time
	Text      string
	Segments  int
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
	mu        sync.Mutex
	multipart map[multipartKey]*multipartMessage
	delivered map[[sha256.Size]byte]time.Time
	retention time.Duration
	clock     func() time.Time
	statePath string
}

type persistedInbox struct {
	Version   int               `json:"version"`
	Delivered []persistedRecord `json:"delivered"`
}

type persistedRecord struct {
	Fingerprint string    `json:"fingerprint"`
	DeliveredAt time.Time `json:"delivered_at"`
}

// NewInbox creates an inbox that remembers delivered fingerprints for the retention period.
func NewInbox(retention time.Duration) *Inbox {
	return newInbox(retention, time.Now)
}

// NewPersistentInbox creates an inbox whose delivered-message fingerprints survive restarts.
func NewPersistentInbox(statePath string, retention time.Duration) (*Inbox, error) {
	return newPersistentInbox(statePath, retention, time.Now)
}

func newInbox(retention time.Duration, clock func() time.Time) *Inbox {
	if retention <= 0 {
		retention = 7 * 24 * time.Hour
	}
	return &Inbox{
		multipart: make(map[multipartKey]*multipartMessage),
		delivered: make(map[[sha256.Size]byte]time.Time),
		retention: retention,
		clock:     clock,
	}
}

func newPersistentInbox(statePath string, retention time.Duration, clock func() time.Time) (*Inbox, error) {
	if statePath == "" || !filepath.IsAbs(statePath) {
		return nil, errors.New("SMS inbox state path must be absolute")
	}
	inbox := newInbox(retention, clock)
	inbox.statePath = statePath
	delivered, err := loadDelivered(statePath)
	if err != nil {
		return nil, err
	}
	inbox.delivered = delivered
	if inbox.prune(clock()) {
		if err := inbox.persistDelivered(); err != nil {
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
		if err := inbox.persistDelivered(); err != nil {
			return ReceivedMessage{}, false, err
		}
	}
	if message.Concatenation == nil {
		return inbox.deliver(message.Sender, message.Timestamp, message.Text, 1, now)
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
	return inbox.deliver(message.Sender, timestamp, text, concatenation.Total, now)
}

func (inbox *Inbox) deliver(sender string, timestamp time.Time, text string, segments int, now time.Time) (ReceivedMessage, bool, error) {
	fingerprint := messageFingerprint(sender, timestamp, text)
	if _, exists := inbox.delivered[fingerprint]; exists {
		return ReceivedMessage{}, false, nil
	}
	inbox.delivered[fingerprint] = now
	received := ReceivedMessage{
		ID:        fmt.Sprintf("%x", fingerprint),
		Sender:    sender,
		Timestamp: timestamp,
		Text:      text,
		Segments:  segments,
	}
	if err := inbox.persistDelivered(); err != nil {
		delete(inbox.delivered, fingerprint)
		return ReceivedMessage{}, false, err
	}
	return received, true, nil
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

func loadDelivered(path string) (map[[sha256.Size]byte]time.Time, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return make(map[[sha256.Size]byte]time.Time), nil
	}
	if err != nil {
		return nil, fmt.Errorf("open SMS inbox state: %w", err)
	}
	defer file.Close()

	var state persistedInbox
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil || state.Version != inboxStateVersion {
		return nil, errors.New("SMS inbox state is invalid")
	}
	delivered := make(map[[sha256.Size]byte]time.Time, len(state.Delivered))
	for _, record := range state.Delivered {
		decoded, err := hex.DecodeString(record.Fingerprint)
		if err != nil || len(decoded) != sha256.Size || record.DeliveredAt.IsZero() {
			return nil, errors.New("SMS inbox state is invalid")
		}
		var fingerprint [sha256.Size]byte
		copy(fingerprint[:], decoded)
		delivered[fingerprint] = record.DeliveredAt
	}
	return delivered, nil
}

func (inbox *Inbox) persistDelivered() error {
	if inbox.statePath == "" {
		return nil
	}
	records := make([]persistedRecord, 0, len(inbox.delivered))
	for fingerprint, deliveredAt := range inbox.delivered {
		records = append(records, persistedRecord{Fingerprint: hex.EncodeToString(fingerprint[:]), DeliveredAt: deliveredAt})
	}
	sort.Slice(records, func(left int, right int) bool { return records[left].Fingerprint < records[right].Fingerprint })
	content, err := json.Marshal(persistedInbox{Version: inboxStateVersion, Delivered: records})
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

func messageFingerprint(sender string, timestamp time.Time, text string) [sha256.Size]byte {
	buffer := make([]byte, 0, len(sender)+len(text)+16)
	buffer = binary.BigEndian.AppendUint32(buffer, uint32(len(sender)))
	buffer = append(buffer, sender...)
	buffer = binary.BigEndian.AppendUint64(buffer, uint64(timestamp.UTC().UnixMilli()))
	buffer = binary.BigEndian.AppendUint32(buffer, uint32(len(text)))
	buffer = append(buffer, text...)
	return sha256.Sum256(buffer)
}
