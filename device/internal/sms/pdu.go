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
		text, err = decodeGSM7(userData, septets, headerSeptets*7)
		if err != nil {
			return Message{}, err
		}
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

	if septets, ok := encodeGSM7(text); ok {
		if len(septets) <= 160 {
			packed := packSeptets(septets, nil)
			return []SubmitPDU{buildSubmitPDU(destination, 0x11, 0x00, byte(len(septets)), packed, 1, 1)}, nil
		}
		segments := splitGSM7(septets, 153)
		if len(segments) > 255 {
			return nil, errors.New("message requires more than 255 segments")
		}
		result := make([]SubmitPDU, 0, len(segments))
		for index, segment := range segments {
			header := []byte{0x05, 0x00, 0x03, reference, byte(len(segments)), byte(index + 1)}
			payload := packSeptets(segment, header)
			headerSeptets := (len(header)*8 + 6) / 7
			result = append(result, buildSubmitPDU(destination, 0x51, 0x00, byte(headerSeptets+len(segment)), payload, index+1, len(segments)))
		}
		return result, nil
	}

	runes := []rune(text)
	encodedText := encodeUCS2(runes)
	if len(encodedText) <= 140 {
		return []SubmitPDU{buildSubmitPDU(destination, 0x11, 0x08, byte(len(encodedText)), encodedText, 1, 1)}, nil
	}
	segments := splitUCS2(runes, 67)
	if len(segments) > 255 {
		return nil, errors.New("message requires more than 255 segments")
	}
	result := make([]SubmitPDU, 0, len(segments))
	for index, segment := range segments {
		header := []byte{0x05, 0x00, 0x03, reference, byte(len(segments)), byte(index + 1)}
		payload := append(header, encodeUCS2(segment)...)
		result = append(result, buildSubmitPDU(destination, 0x51, 0x08, byte(len(payload)), payload, index+1, len(segments)))
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
	parts := make([]int, 6)
	for index, octet := range value[:6] {
		parts[index] = int(octet&0x0f)*10 + int(octet>>4&0x0f)
	}
	year := 2000 + parts[0]
	offsetQuarters := int(value[6]&0x07)*10 + int(value[6]>>4&0x0f)
	if value[6]&0x08 != 0 {
		offsetQuarters = -offsetQuarters
	}
	zone := time.FixedZone("SMS", offsetQuarters*15*60)
	timestamp := time.Date(year, time.Month(parts[1]), parts[2], parts[3], parts[4], parts[5], 0, zone)
	if timestamp.Year() != year || timestamp.Month() != time.Month(parts[1]) || timestamp.Day() != parts[2] || timestamp.Hour() != parts[3] || timestamp.Minute() != parts[4] || timestamp.Second() != parts[5] {
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

func decodeGSM7(data []byte, septetCount int, bitOffset int) (string, error) {
	if septetCount < 0 || bitOffset < 0 || (bitOffset+septetCount*7+7)/8 > len(data) {
		return "", ErrUnsupportedPDU
	}
	septets := make([]byte, 0, septetCount)
	for index := 0; index < septetCount; index++ {
		position := bitOffset + index*7
		byteIndex := position / 8
		shift := position % 8
		value := data[byteIndex] >> shift
		if shift > 1 && byteIndex+1 < len(data) {
			value |= data[byteIndex+1] << (8 - shift)
		}
		septets = append(septets, value&0x7f)
	}
	result := make([]rune, 0, septetCount)
	for position := 0; position < len(septets); position++ {
		value := septets[position]
		if value != 0x1b {
			result = append(result, gsm7Alphabet[value])
			continue
		}
		position++
		if position >= len(septets) {
			return "", ErrUnsupportedPDU
		}
		character, ok := gsm7ExtensionAlphabet[septets[position]]
		if !ok {
			return "", ErrUnsupportedPDU
		}
		result = append(result, character)
	}
	return string(result), nil
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
			extension, ok := gsm7ExtensionEncoding[character]
			if !ok {
				return nil, false
			}
			result = append(result, 0x1b, extension)
			continue
		}
		result = append(result, byte(index))
	}
	return result, true
}

func splitGSM7(septets []byte, limit int) [][]byte {
	result := make([][]byte, 0, (len(septets)+limit-1)/limit)
	for position := 0; position < len(septets); {
		end := min(position+limit, len(septets))
		if end < len(septets) && septets[end-1] == 0x1b {
			end--
		}
		result = append(result, append([]byte(nil), septets[position:end]...))
		position = end
	}
	return result
}

func splitUCS2(runes []rune, limit int) [][]rune {
	result := make([][]rune, 0)
	start := 0
	units := 0
	for index, character := range runes {
		characterUnits := utf16.RuneLen(character)
		if units+characterUnits > limit {
			result = append(result, append([]rune(nil), runes[start:index]...))
			start = index
			units = 0
		}
		units += characterUnits
	}
	if start < len(runes) {
		result = append(result, append([]rune(nil), runes[start:]...))
	}
	return result
}

func packSeptets(septets []byte, prefix []byte) []byte {
	bitOffset := ((len(prefix)*8 + 6) / 7) * 7
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

var gsm7ExtensionAlphabet = map[byte]rune{
	0x0a: '\f',
	0x14: '^',
	0x28: '{',
	0x29: '}',
	0x2f: '\\',
	0x3c: '[',
	0x3d: '~',
	0x3e: ']',
	0x40: '|',
	0x65: '€',
}

var gsm7ExtensionEncoding = map[rune]byte{
	'\f': 0x0a,
	'^':  0x14,
	'{':  0x28,
	'}':  0x29,
	'\\': 0x2f,
	'[':  0x3c,
	'~':  0x3d,
	']':  0x3e,
	'|':  0x40,
	'€':  0x65,
}
