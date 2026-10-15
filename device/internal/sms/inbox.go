package sms

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"time"
)

var ErrInvalidConcatenation = errors.New("invalid SMS concatenation metadata")

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
}

// NewInbox creates an inbox that remembers delivered fingerprints for the retention period.
func NewInbox(retention time.Duration) *Inbox {
	return newInbox(retention, time.Now)
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

// ImportPDUs decodes modem history and returns only newly completed messages.
func (inbox *Inbox) ImportPDUs(pdus []string) ([]ReceivedMessage, error) {
	result := make([]ReceivedMessage, 0, len(pdus))
	for index, pdu := range pdus {
		message, err := DecodeDeliver(pdu)
		if err != nil {
			return nil, fmt.Errorf("decode history PDU %d: %w", index, err)
		}
		complete, created, err := inbox.AddSegment(message)
		if err != nil {
			return nil, fmt.Errorf("import history PDU %d: %w", index, err)
		}
		if created {
			result = append(result, complete)
		}
	}
	return result, nil
}

// AddSegment accepts a decoded message or multipart segment.
func (inbox *Inbox) AddSegment(message Message) (ReceivedMessage, bool, error) {
	inbox.mu.Lock()
	defer inbox.mu.Unlock()

	now := inbox.clock()
	inbox.prune(now)
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
	return ReceivedMessage{
		ID:        fmt.Sprintf("%x", fingerprint),
		Sender:    sender,
		Timestamp: timestamp,
		Text:      text,
		Segments:  segments,
	}, true, nil
}

func (inbox *Inbox) prune(now time.Time) {
	cutoff := now.Add(-inbox.retention)
	for fingerprint, deliveredAt := range inbox.delivered {
		if deliveredAt.Before(cutoff) {
			delete(inbox.delivered, fingerprint)
		}
	}
	for key, message := range inbox.multipart {
		if message.updatedAt.Before(cutoff) {
			delete(inbox.multipart, key)
		}
	}
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
