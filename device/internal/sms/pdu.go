package sms

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf16"
)

var ErrUnsupportedPDU = errors.New("unsupported SMS PDU")

// Concatenation identifies one segment of a multipart SMS.
type Concatenation struct {
	Reference uint16
	Total     int
	Sequence  int
}

// Message is a decoded SMS-DELIVER payload.
type Message struct {
	Sender        string
	Timestamp     time.Time
	Text          string
	Concatenation *Concatenation
}

// SubmitPDU contains a complete PDU and the TPDU length required by AT+CMGS.
type SubmitPDU struct {
	Hex        string
	TPDULength int
	Segment    int
	Total      int
}

// DecodeDeliver decodes a GSM SMS-DELIVER PDU.
func DecodeDeliver(encoded string) (Message, error) {
	data, err := hex.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return Message{}, fmt.Errorf("decode PDU hex: %w", err)
	}
	if len(data) < 2 {
		return Message{}, ErrUnsupportedPDU
	}
	position := 1 + int(data[0])
	if position+2 > len(data) {
		return Message{}, ErrUnsupportedPDU
	}
	firstOctet := data[position]
	position++
	if firstOctet&0x03 != 0 {
		return Message{}, ErrUnsupportedPDU
	}
	addressDigits := int(data[position])
	position++
	if position >= len(data) {
		return Message{}, ErrUnsupportedPDU
	}
	typeOfAddress := data[position]
	position++
	addressBytes := (addressDigits + 1) / 2
	if position+addressBytes+10 > len(data) {
		return Message{}, ErrUnsupportedPDU
	}
	sender := decodeSemiOctets(data[position:position+addressBytes], addressDigits)
	if typeOfAddress&0x90 == 0x90 {
		sender = "+" + sender
	}
	position += addressBytes
	position++
	dcs := data[position]
	position++
	timestamp, err := decodeTimestamp(data[position : position+7])
	if err != nil {
		return Message{}, err
	}
	position += 7
	userDataLength := int(data[position])
	position++
	if position > len(data) {
		return Message{}, ErrUnsupportedPDU
	}
	userData := data[position:]

	var concatenation *Concatenation
	headerBytes := 0
	if firstOctet&0x40 != 0 {
		if len(userData) == 0 || int(userData[0])+1 > len(userData) {
			return Message{}, ErrUnsupportedPDU
		}
		headerBytes = int(userData[0]) + 1
		concatenation = decodeConcatenation(userData[1:headerBytes])
	}

	var text string
	switch dcs & 0x0c {
	case 0x00:
		headerSeptets := (headerBytes*8 + 6) / 7
		septets := userDataLength - headerSeptets
		if septets < 0 {
			return Message{}, ErrUnsupportedPDU
		}
		text = decodeGSM7(userData, septets, headerBytes*8)
	case 0x08:
		if userDataLength > len(userData) {
			return Message{}, ErrUnsupportedPDU
		}
		text, err = decodeUCS2(userData[headerBytes:userDataLength])
		if err != nil {
			return Message{}, err
		}
	default:
		return Message{}, ErrUnsupportedPDU
	}

	return Message{Sender: sender, Timestamp: timestamp, Text: text, Concatenation: concatenation}, nil
}

// EncodeSubmit creates one or more SMS-SUBMIT PDUs using GSM-7 when possible and UCS-2 otherwise.
func EncodeSubmit(destination string, text string, reference byte) ([]SubmitPDU, error) {
	digits := strings.TrimPrefix(strings.TrimSpace(destination), "+")
	if digits == "" || len(digits) > 20 {
		return nil, errors.New("destination must contain between 1 and 20 digits")
	}
	for _, digit := range digits {
		if digit < '0' || digit > '9' {
			return nil, errors.New("destination contains a non-digit")
		}
	}
	if text == "" {
		return nil, errors.New("message must not be empty")
	}

	if septets, ok := encodeGSM7(text); ok && len(septets) <= 160 {
		packed := packSeptets(septets, nil)
		return []SubmitPDU{buildSubmitPDU(destination, 0x11, 0x00, byte(len(septets)), packed, 1, 1)}, nil
	}

	runes := []rune(text)
	const segmentRunes = 67
	total := (len(runes) + segmentRunes - 1) / segmentRunes
	if total > 255 {
		return nil, errors.New("message requires more than 255 segments")
	}
	result := make([]SubmitPDU, 0, total)
	for index := 0; index < total; index++ {
		start := index * segmentRunes
		end := min(start+segmentRunes, len(runes))
		encodedText := encodeUCS2(runes[start:end])
		payload := encodedText
		firstOctet := byte(0x11)
		if total > 1 {
			firstOctet = 0x51
			header := []byte{0x05, 0x00, 0x03, reference, byte(total), byte(index + 1)}
			payload = append(header, encodedText...)
		}
		result = append(result, buildSubmitPDU(destination, firstOctet, 0x08, byte(len(payload)), payload, index+1, total))
	}
	return result, nil
}

func buildSubmitPDU(destination string, firstOctet byte, dcs byte, userDataLength byte, userData []byte, segment int, total int) SubmitPDU {
	digits := strings.TrimPrefix(destination, "+")
	typeOfAddress := byte(0x81)
	if strings.HasPrefix(destination, "+") {
		typeOfAddress = 0x91
	}
	pdu := []byte{0x00, firstOctet, 0x00, byte(len(digits)), typeOfAddress}
	pdu = append(pdu, encodeSemiOctets(digits)...)
	pdu = append(pdu, 0x00, dcs, 0xaa, userDataLength)
	pdu = append(pdu, userData...)
	return SubmitPDU{Hex: strings.ToUpper(hex.EncodeToString(pdu)), TPDULength: len(pdu) - 1, Segment: segment, Total: total}
}

func decodeConcatenation(header []byte) *Concatenation {
	for position := 0; position+2 <= len(header); {
		identifier := header[position]
		length := int(header[position+1])
		position += 2
		if position+length > len(header) {
			return nil
		}
		value := header[position : position+length]
		position += length
		switch {
		case identifier == 0x00 && length == 3:
			return &Concatenation{Reference: uint16(value[0]), Total: int(value[1]), Sequence: int(value[2])}
		case identifier == 0x08 && length == 4:
			return &Concatenation{Reference: uint16(value[0])<<8 | uint16(value[1]), Total: int(value[2]), Sequence: int(value[3])}
		}
	}
	return nil
}

func decodeTimestamp(value []byte) (time.Time, error) {
	if len(value) != 7 {
		return time.Time{}, ErrUnsupportedPDU
	}
	parts := make([]int, 7)
	for index, octet := range value {
		parts[index] = int(octet&0x0f)*10 + int(octet>>4&0x0f)
	}
	year := 2000 + parts[0]
	offsetQuarters := parts[6]
	if value[6]&0x08 != 0 {
		offsetQuarters = -offsetQuarters
	}
	zone := time.FixedZone("SMS", offsetQuarters*15*60)
	timestamp := time.Date(year, time.Month(parts[1]), parts[2], parts[3], parts[4], parts[5], 0, zone)
	if timestamp.Month() != time.Month(parts[1]) || timestamp.Day() != parts[2] {
		return time.Time{}, ErrUnsupportedPDU
	}
	return timestamp, nil
}

func decodeSemiOctets(value []byte, digits int) string {
	result := make([]byte, 0, digits)
	for _, octet := range value {
		result = append(result, '0'+(octet&0x0f))
		if len(result) < digits {
			result = append(result, '0'+((octet>>4)&0x0f))
		}
	}
	return string(result)
}

func encodeSemiOctets(digits string) []byte {
	result := make([]byte, 0, (len(digits)+1)/2)
	for index := 0; index < len(digits); index += 2 {
		low := digits[index] - '0'
		high := byte(0x0f)
		if index+1 < len(digits) {
			high = digits[index+1] - '0'
		}
		result = append(result, low|high<<4)
	}
	return result
}

func decodeUCS2(value []byte) (string, error) {
	if len(value)%2 != 0 {
		return "", ErrUnsupportedPDU
	}
	units := make([]uint16, 0, len(value)/2)
	for position := 0; position < len(value); position += 2 {
		units = append(units, uint16(value[position])<<8|uint16(value[position+1]))
	}
	return string(utf16.Decode(units)), nil
}

func encodeUCS2(value []rune) []byte {
	units := utf16.Encode(value)
	result := make([]byte, 0, len(units)*2)
	for _, unit := range units {
		result = append(result, byte(unit>>8), byte(unit))
	}
	return result
}

func decodeGSM7(data []byte, septetCount int, bitOffset int) string {
	result := make([]rune, 0, septetCount)
	for index := 0; index < septetCount; index++ {
		position := bitOffset + index*7
		byteIndex := position / 8
		shift := position % 8
		if byteIndex >= len(data) {
			break
		}
		value := data[byteIndex] >> shift
		if shift > 1 && byteIndex+1 < len(data) {
			value |= data[byteIndex+1] << (8 - shift)
		}
		result = append(result, gsm7Alphabet[value&0x7f])
	}
	return string(result)
}

func encodeGSM7(text string) ([]byte, bool) {
	result := make([]byte, 0, len(text))
	for _, character := range text {
		index := -1
		for candidate, alphabetCharacter := range gsm7Alphabet {
			if alphabetCharacter == character {
				index = candidate
				break
			}
		}
		if index < 0 {
			return nil, false
		}
		result = append(result, byte(index))
	}
	return result, true
}

func packSeptets(septets []byte, prefix []byte) []byte {
	bitOffset := len(prefix) * 8
	length := (bitOffset + len(septets)*7 + 7) / 8
	result := make([]byte, length)
	copy(result, prefix)
	for index, septet := range septets {
		position := bitOffset + index*7
		byteIndex := position / 8
		shift := position % 8
		result[byteIndex] |= septet << shift
		if shift > 1 && byteIndex+1 < len(result) {
			result[byteIndex+1] |= septet >> (8 - shift)
		}
	}
	return result
}

var gsm7Alphabet = []rune("@£$¥èéùìòÇ\nØø\rÅåΔ_ΦΓΛΩΠΨΣΘΞ\x1bÆæßÉ !\"#¤%&'()*+,-./0123456789:;<=>?¡ABCDEFGHIJKLMNOPQRSTUVWXYZÄÖÑÜ§¿abcdefghijklmnopqrstuvwxyzäöñüà")
