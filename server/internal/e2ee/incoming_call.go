package e2ee

import (
	"crypto/ed25519"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"

	"sim-deck-server/internal/domain"
)

var ErrInvalidIncomingCall = errors.New("incoming call notification is invalid")

const (
	incomingCallType          = "call.incoming"
	maxIncomingCallRecipients = 64
	keyAgreementPublicBytes   = 32
	maxIncomingCiphertext     = 64 << 10
)

// VerifyIncomingCallNotification validates canonical recipient routing and the device signature.
func VerifyIncomingCallNotification(verificationKey ed25519.PublicKey, notification domain.IncomingCallNotification) error {
	if len(verificationKey) != ed25519.PublicKeySize {
		return ErrInvalidSigningKey
	}
	if err := validateIncomingCallNotification(notification); err != nil {
		return err
	}
	if !ed25519.Verify(verificationKey, incomingCallSignedData(notification), notification.Signature) {
		return ErrInvalidSignature
	}
	return nil
}

func validateIncomingCallNotification(notification domain.IncomingCallNotification) error {
	if !validUUID(notification.ID) || notification.Type != incomingCallType || !validUUID(notification.DeviceUUID) {
		return fmt.Errorf("%w: routing identity is invalid", ErrInvalidIncomingCall)
	}
	if notification.OccurredAt.IsZero() || notification.KeyEpoch < 1 {
		return fmt.Errorf("%w: timestamp and key epoch are required", ErrInvalidIncomingCall)
	}
	if len(notification.Recipients) < 1 || len(notification.Recipients) > maxIncomingCallRecipients {
		return fmt.Errorf("%w: recipient count must be between 1 and %d", ErrInvalidIncomingCall, maxIncomingCallRecipients)
	}
	previousID := ""
	for _, recipient := range notification.Recipients {
		if !validUUID(recipient.InstallationID) ||
			len(recipient.SenderPublicKey) != keyAgreementPublicBytes ||
			len(recipient.Nonce) != gcmNonceBytes ||
			len(recipient.Ciphertext) < gcmAuthenticationBytes ||
			len(recipient.Ciphertext) > maxIncomingCiphertext {
			return fmt.Errorf("%w: recipient envelope is invalid", ErrInvalidIncomingCall)
		}
		if previousID != "" && strings.Compare(previousID, recipient.InstallationID) >= 0 {
			return fmt.Errorf("%w: recipients must be unique and sorted", ErrInvalidIncomingCall)
		}
		previousID = recipient.InstallationID
	}
	if len(notification.Signature) != ed25519.SignatureSize {
		return fmt.Errorf("%w: signature must contain exactly %d bytes", ErrInvalidIncomingCall, ed25519.SignatureSize)
	}
	return nil
}

func incomingCallSignedData(notification domain.IncomingCallNotification) []byte {
	buffer := make([]byte, 0, 512)
	buffer = appendBytes(buffer, []byte(notification.ID))
	buffer = appendBytes(buffer, []byte(notification.Type))
	buffer = appendBytes(buffer, []byte(notification.DeviceUUID))
	buffer = binary.BigEndian.AppendUint64(buffer, uint64(notification.OccurredAt.UTC().UnixNano()))
	buffer = binary.BigEndian.AppendUint64(buffer, uint64(notification.KeyEpoch))
	buffer = binary.BigEndian.AppendUint32(buffer, uint32(len(notification.Recipients)))
	for _, recipient := range notification.Recipients {
		buffer = appendBytes(buffer, []byte(recipient.InstallationID))
		buffer = appendBytes(buffer, recipient.SenderPublicKey)
		buffer = appendBytes(buffer, recipient.Nonce)
		buffer = appendBytes(buffer, recipient.Ciphertext)
	}
	return buffer
}

func validUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for index, character := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			if character != '-' {
				return false
			}
			continue
		}
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f') || (character >= 'A' && character <= 'F')) {
			return false
		}
	}
	return true
}
