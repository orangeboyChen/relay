package sms

import (
	"encoding/hex"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type parsedSubmit struct {
	firstOctet byte
	dcs        byte
	udl        int
	payload    []byte
}

func parseSubmit(pdu SubmitPDU) parsedSubmit {
	data, err := hex.DecodeString(pdu.Hex)
	Expect(err).NotTo(HaveOccurred())
	Expect(len(data)).To(BeNumerically(">", 8))
	destinationDigits := int(data[3])
	position := 5 + (destinationDigits+1)/2
	Expect(position + 4).To(BeNumerically("<=", len(data)))
	return parsedSubmit{
		firstOctet: data[1],
		dcs:        data[position+1],
		udl:        int(data[position+3]),
		payload:    data[position+4:],
	}
}

func deliverPDU(sender string, firstOctet byte, dcs byte, userDataLength byte, payload []byte) string {
	digits := strings.TrimPrefix(sender, "+")
	typeOfAddress := byte(0x81)
	if strings.HasPrefix(sender, "+") {
		typeOfAddress = 0x91
	}
	data := []byte{0x00, firstOctet, byte(len(digits)), typeOfAddress}
	data = append(data, encodeSemiOctets(digits)...)
	data = append(data, 0x00, dcs)
	data = append(data, []byte{0x62, 0x01, 0x51, 0x21, 0x43, 0x65, 0x00}...)
	data = append(data, userDataLength)
	data = append(data, payload...)
	return strings.ToUpper(hex.EncodeToString(data))
}

var _ = Describe("SMS PDU codec", func() {
	It("decodes a UCS-2 SMS-DELIVER message", func() {
		message, err := DecodeDeliver("00040B913105000000F0000862015121436500044F60597D")

		Expect(err).NotTo(HaveOccurred())
		Expect(message.Sender).To(Equal("+8613500000000"))
		Expect(message.Text).To(Equal("你好"))
		Expect(message.Timestamp.Year()).To(Equal(2026))
		Expect(message.Timestamp.Month()).To(Equal(time.October))
		Expect(message.Timestamp.Day()).To(Equal(15))
	})

	It("encodes a single GSM-7 message", func() {
		pdus, err := EncodeSubmit("+8613500000000", "hello", 7)

		Expect(err).NotTo(HaveOccurred())
		Expect(pdus).To(HaveLen(1))
		Expect(pdus[0].Total).To(Equal(1))
		Expect(pdus[0].TPDULength).To(BeNumerically(">", 0))
		Expect(pdus[0].Hex).To(HavePrefix("0011000B913105000000F0"))
	})

	It("round-trips every GSM-7 extension-table character", func() {
		text := "^{}\\[~]|€\f"
		septets, ok := encodeGSM7(text)
		Expect(ok).To(BeTrue())
		Expect(septets).To(HaveLen(2 * len([]rune(text))))

		decoded, err := decodeGSM7(packSeptets(septets, nil), len(septets), 0)

		Expect(err).NotTo(HaveOccurred())
		Expect(decoded).To(Equal(text))
	})

	It("decodes a GSM-7 SMS-DELIVER message with extension characters", func() {
		text := "hello ^[]€"
		septets, ok := encodeGSM7(text)
		Expect(ok).To(BeTrue())
		pdu := deliverPDU("+8613500000000", 0x04, 0x00, byte(len(septets)), packSeptets(septets, nil))

		message, err := DecodeDeliver(pdu)

		Expect(err).NotTo(HaveOccurred())
		Expect(message.Sender).To(Equal("+8613500000000"))
		Expect(message.Text).To(Equal(text))
		Expect(message.Concatenation).To(BeNil())
	})

	It("decodes multipart GSM-7 metadata using the aligned UDH offset", func() {
		septets, ok := encodeGSM7("segment one")
		Expect(ok).To(BeTrue())
		header := []byte{0x05, 0x00, 0x03, 0x2a, 0x02, 0x01}
		headerSeptets := (len(header)*8 + 6) / 7
		pdu := deliverPDU("10086", 0x44, 0x00, byte(headerSeptets+len(septets)), packSeptets(septets, header))

		message, err := DecodeDeliver(pdu)

		Expect(err).NotTo(HaveOccurred())
		Expect(message.Text).To(Equal("segment one"))
		Expect(message.Concatenation).To(Equal(&Concatenation{Reference: 0x2a, Total: 2, Sequence: 1}))
	})

	It("decodes both concatenation reference formats and ignores unrelated UDH elements", func() {
		Expect(decodeConcatenation([]byte{0x70, 0x01, 0xff, 0x00, 0x03, 0x08, 0x02, 0x01})).To(Equal(&Concatenation{Reference: 8, Total: 2, Sequence: 1}))
		Expect(decodeConcatenation([]byte{0x08, 0x04, 0x12, 0x34, 0x03, 0x02})).To(Equal(&Concatenation{Reference: 0x1234, Total: 3, Sequence: 2}))
		Expect(decodeConcatenation([]byte{0x00, 0x04, 0x01})).To(BeNil())
		Expect(decodeConcatenation([]byte{0x70, 0x00})).To(BeNil())
	})

	It("uses GSM-7 concatenation with the required UDH fill bit", func() {
		text := strings.Repeat("a", 161)
		pdus, err := EncodeSubmit("13500000000", text, 9)

		Expect(err).NotTo(HaveOccurred())
		Expect(pdus).To(HaveLen(2))
		decoded := ""
		for index, pdu := range pdus {
			parsed := parseSubmit(pdu)
			Expect(parsed.firstOctet & 0x40).NotTo(BeZero())
			Expect(parsed.dcs).To(Equal(byte(0x00)))
			Expect(parsed.payload[:6]).To(Equal([]byte{0x05, 0x00, 0x03, 9, 2, byte(index + 1)}))
			headerSeptets := (6*8 + 6) / 7
			segment, decodeErr := decodeGSM7(parsed.payload, parsed.udl-headerSeptets, headerSeptets*7)
			Expect(decodeErr).NotTo(HaveOccurred())
			decoded += segment
		}
		Expect(decoded).To(Equal(text))
	})

	It("does not split a GSM-7 escape sequence between segments", func() {
		text := strings.Repeat("^", 81)
		pdus, err := EncodeSubmit("13500000000", text, 4)

		Expect(err).NotTo(HaveOccurred())
		Expect(pdus).To(HaveLen(2))
		for _, pdu := range pdus {
			parsed := parseSubmit(pdu)
			headerSeptets := (6*8 + 6) / 7
			segmentSeptets := parsed.udl - headerSeptets
			segment, decodeErr := decodeGSM7(parsed.payload, segmentSeptets, headerSeptets*7)
			Expect(decodeErr).NotTo(HaveOccurred())
			Expect(segment).NotTo(BeEmpty())
		}
	})

	It("uses UTF-16 code units without splitting an emoji surrogate pair", func() {
		single, err := EncodeSubmit("13500000000", strings.Repeat("😀", 35), 3)
		Expect(err).NotTo(HaveOccurred())
		Expect(single).To(HaveLen(1))
		Expect(parseSubmit(single[0]).payload).To(HaveLen(140))

		multipart, err := EncodeSubmit("13500000000", strings.Repeat("😀", 36), 3)
		Expect(err).NotTo(HaveOccurred())
		Expect(multipart).To(HaveLen(2))
		for _, pdu := range multipart {
			parsed := parseSubmit(pdu)
			Expect(parsed.dcs).To(Equal(byte(0x08)))
			Expect(len(parsed.payload)).To(BeNumerically("<=", 140))
			Expect((len(parsed.payload) - 6) % 4).To(Equal(0))
		}
	})

	It("splits long UCS-2 text with concatenation metadata", func() {
		pdus, err := EncodeSubmit("13500000000", strings.Repeat("你", 140), 9)

		Expect(err).NotTo(HaveOccurred())
		Expect(pdus).To(HaveLen(3))
		for index, pdu := range pdus {
			Expect(pdu.Segment).To(Equal(index + 1))
			Expect(pdu.Total).To(Equal(3))
			Expect(pdu.Hex).To(ContainSubstring("0500030903"))
		}
	})

	It("decodes a negative timezone without treating the sign bit as a digit", func() {
		timestamp, err := decodeTimestamp([]byte{0x62, 0x01, 0x51, 0x21, 0x43, 0x65, 0x2b})

		Expect(err).NotTo(HaveOccurred())
		_, offset := timestamp.Zone()
		Expect(offset).To(Equal(-8 * 60 * 60))
	})

	It("rejects invalid timestamps and odd UCS-2 payloads", func() {
		_, err := decodeTimestamp([]byte{0x62})
		Expect(err).To(MatchError(ErrUnsupportedPDU))
		_, err = decodeTimestamp([]byte{0x62, 0x01, 0x51, 0x21, 0x99, 0x65, 0x00})
		Expect(err).To(MatchError(ErrUnsupportedPDU))
		_, err = decodeUCS2([]byte{0x00})
		Expect(err).To(MatchError(ErrUnsupportedPDU))
	})

	DescribeTable("rejects invalid outbound messages",
		func(destination string, text string, expected string) {
			_, err := EncodeSubmit(destination, text, 1)
			Expect(err).To(MatchError(ContainSubstring(expected)))
		},
		Entry("empty destination", "", "hello", "destination"),
		Entry("long destination", strings.Repeat("1", 21), "hello", "destination"),
		Entry("non-digit destination", "12A", "hello", "non-digit"),
		Entry("empty message", "10086", "", "must not be empty"),
		Entry("too many GSM segments", "10086", strings.Repeat("a", 153*255+1), "more than 255"),
		Entry("too many UCS-2 segments", "10086", strings.Repeat("你", 67*255+1), "more than 255"),
	)

	DescribeTable("rejects malformed inbound data",
		func(pdu string) {
			_, err := DecodeDeliver(pdu)
			Expect(err).To(HaveOccurred())
		},
		Entry("invalid hex", "not-hex"),
		Entry("truncated PDU", "00"),
		Entry("unsupported message type", "000100"),
	)

	It("rejects truncated and invalid GSM-7 escape data", func() {
		_, err := decodeGSM7(nil, 1, 0)
		Expect(err).To(MatchError(ErrUnsupportedPDU))

		_, err = decodeGSM7(packSeptets([]byte{0x1b}, nil), 1, 0)
		Expect(err).To(MatchError(ErrUnsupportedPDU))

		_, err = decodeGSM7(packSeptets([]byte{0x1b, 0x7f}, nil), 2, 0)
		Expect(err).To(MatchError(ErrUnsupportedPDU))
	})
})
