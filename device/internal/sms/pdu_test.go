package sms

import (
	"strings"
	"testing"
)

func TestDecodeDeliverUCS2(t *testing.T) {
	message, err := DecodeDeliver("00040B913105000000F0000862015121436500044F60597D")
	if err != nil {
		t.Fatalf("DecodeDeliver() error = %v", err)
	}
	if message.Sender != "+8613500000000" {
		t.Fatalf("Sender = %q", message.Sender)
	}
	if message.Text != "你好" {
		t.Fatalf("Text = %q", message.Text)
	}
	if message.Timestamp.Year() != 2026 || int(message.Timestamp.Month()) != 10 || message.Timestamp.Day() != 15 {
		t.Fatalf("Timestamp = %v", message.Timestamp)
	}
}

func TestEncodeSubmitGSM7(t *testing.T) {
	pdus, err := EncodeSubmit("+8613500000000", "hello", 7)
	if err != nil {
		t.Fatalf("EncodeSubmit() error = %v", err)
	}
	if len(pdus) != 1 || pdus[0].Total != 1 || pdus[0].TPDULength == 0 {
		t.Fatalf("PDUs = %#v", pdus)
	}
	if !strings.HasPrefix(pdus[0].Hex, "0011000B913105000000F0") {
		t.Fatalf("PDU = %s", pdus[0].Hex)
	}
}

func TestEncodeSubmitSplitsLongUCS2(t *testing.T) {
	pdus, err := EncodeSubmit("13500000000", strings.Repeat("你", 140), 9)
	if err != nil {
		t.Fatalf("EncodeSubmit() error = %v", err)
	}
	if len(pdus) != 3 {
		t.Fatalf("segment count = %d, want 3", len(pdus))
	}
	for index, pdu := range pdus {
		if pdu.Segment != index+1 || pdu.Total != 3 {
			t.Fatalf("segment = %#v", pdu)
		}
		if !strings.Contains(pdu.Hex, "0500030903") {
			t.Fatalf("PDU does not contain concatenation header: %s", pdu.Hex)
		}
	}
}
