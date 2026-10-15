package sms

import (
	"testing"
	"time"
)

func TestInboxReassemblesMultipartOutOfOrder(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	inbox := newInbox(24*time.Hour, func() time.Time { return now })
	second := Message{
		Sender:        "+8613500000000",
		Timestamp:     now.Add(time.Second),
		Text:          "world",
		Concatenation: &Concatenation{Reference: 9, Total: 2, Sequence: 2},
	}
	first := Message{
		Sender:        "+8613500000000",
		Timestamp:     now,
		Text:          "hello ",
		Concatenation: &Concatenation{Reference: 9, Total: 2, Sequence: 1},
	}

	if _, created, err := inbox.AddSegment(second); err != nil || created {
		t.Fatalf("second segment created=%v err=%v", created, err)
	}
	complete, created, err := inbox.AddSegment(first)
	if err != nil || !created {
		t.Fatalf("first segment created=%v err=%v", created, err)
	}
	if complete.Text != "hello world" || complete.Segments != 2 || complete.Timestamp != now {
		t.Fatalf("complete message = %#v", complete)
	}
}

func TestInboxIgnoresDuplicateSegmentsAndMessages(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	inbox := newInbox(24*time.Hour, func() time.Time { return now })
	segment := Message{
		Sender:        "+8613500000000",
		Timestamp:     now,
		Text:          "one",
		Concatenation: &Concatenation{Reference: 5, Total: 2, Sequence: 1},
	}
	if _, created, err := inbox.AddSegment(segment); err != nil || created {
		t.Fatalf("first segment created=%v err=%v", created, err)
	}
	if _, created, err := inbox.AddSegment(segment); err != nil || created {
		t.Fatalf("duplicate segment created=%v err=%v", created, err)
	}

	single := Message{Sender: "10086", Timestamp: now, Text: "balance"}
	if _, created, err := inbox.AddSegment(single); err != nil || !created {
		t.Fatalf("single message created=%v err=%v", created, err)
	}
	if _, created, err := inbox.AddSegment(single); err != nil || created {
		t.Fatalf("duplicate message created=%v err=%v", created, err)
	}
}

func TestInboxImportsHistoryWithoutDuplicatingExistingPDU(t *testing.T) {
	inbox := NewInbox(24 * time.Hour)
	pdu := "00040B913105000000F0000862015121436500044F60597D"
	first, err := inbox.ImportPDUs([]string{pdu, pdu})
	if err != nil {
		t.Fatalf("ImportPDUs() error = %v", err)
	}
	if len(first) != 1 || first[0].Text != "你好" {
		t.Fatalf("first import = %#v", first)
	}
	second, err := inbox.ImportPDUs([]string{pdu})
	if err != nil {
		t.Fatalf("second ImportPDUs() error = %v", err)
	}
	if len(second) != 0 {
		t.Fatalf("second import = %#v, want empty", second)
	}
}

func TestInboxRejectsConflictingDuplicateSegment(t *testing.T) {
	inbox := NewInbox(time.Hour)
	base := Message{
		Sender:        "10086",
		Timestamp:     time.Now(),
		Text:          "first",
		Concatenation: &Concatenation{Reference: 1, Total: 2, Sequence: 1},
	}
	if _, _, err := inbox.AddSegment(base); err != nil {
		t.Fatal(err)
	}
	base.Text = "changed"
	if _, _, err := inbox.AddSegment(base); err != ErrInvalidConcatenation {
		t.Fatalf("AddSegment() error = %v, want ErrInvalidConcatenation", err)
	}
}
