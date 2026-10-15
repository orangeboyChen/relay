package sms

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("SMS inbox", func() {
	var now time.Time

	BeforeEach(func() {
		now = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	})

	It("reassembles multipart messages received out of order", func() {
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

		_, created, err := inbox.AddSegment(second)
		Expect(err).NotTo(HaveOccurred())
		Expect(created).To(BeFalse())
		complete, created, err := inbox.AddSegment(first)

		Expect(err).NotTo(HaveOccurred())
		Expect(created).To(BeTrue())
		Expect(complete.Text).To(Equal("hello world"))
		Expect(complete.Segments).To(Equal(2))
		Expect(complete.Timestamp).To(Equal(now))
	})

	It("ignores duplicate segments and complete messages", func() {
		inbox := newInbox(24*time.Hour, func() time.Time { return now })
		segment := Message{
			Sender:        "+8613500000000",
			Timestamp:     now,
			Text:          "one",
			Concatenation: &Concatenation{Reference: 5, Total: 2, Sequence: 1},
		}

		_, created, err := inbox.AddSegment(segment)
		Expect(err).NotTo(HaveOccurred())
		Expect(created).To(BeFalse())
		_, created, err = inbox.AddSegment(segment)
		Expect(err).NotTo(HaveOccurred())
		Expect(created).To(BeFalse())

		single := Message{Sender: "10086", Timestamp: now, Text: "balance"}
		_, created, err = inbox.AddSegment(single)
		Expect(err).NotTo(HaveOccurred())
		Expect(created).To(BeTrue())
		_, created, err = inbox.AddSegment(single)
		Expect(err).NotTo(HaveOccurred())
		Expect(created).To(BeFalse())
	})

	It("imports valid history entries while isolating malformed PDUs", func() {
		inbox := NewInbox(24 * time.Hour)
		pdu := "00040B913105000000F0000862015121436500044F60597D"

		messages, err := inbox.ImportPDUs([]string{"not-a-pdu", pdu, pdu})

		Expect(err).To(MatchError(ContainSubstring("decode history PDU 0")))
		Expect(messages).To(HaveLen(1))
		Expect(messages[0].Text).To(Equal("你好"))
		second, secondErr := inbox.ImportPDUs([]string{pdu})
		Expect(secondErr).NotTo(HaveOccurred())
		Expect(second).To(BeEmpty())
	})

	It("rejects invalid and conflicting concatenation metadata", func() {
		inbox := NewInbox(time.Hour)
		invalid := Message{Sender: "10086", Timestamp: now, Text: "bad", Concatenation: &Concatenation{Reference: 1, Total: 2, Sequence: 3}}
		_, _, err := inbox.AddSegment(invalid)
		Expect(errors.Is(err, ErrInvalidConcatenation)).To(BeTrue())

		base := Message{Sender: "10086", Timestamp: now, Text: "first", Concatenation: &Concatenation{Reference: 1, Total: 2, Sequence: 1}}
		_, _, err = inbox.AddSegment(base)
		Expect(err).NotTo(HaveOccurred())
		base.Text = "changed"
		_, _, err = inbox.AddSegment(base)
		Expect(errors.Is(err, ErrInvalidConcatenation)).To(BeTrue())
	})

	It("isolates reuse of the same multipart reference in a later time window", func() {
		inbox := newInbox(24*time.Hour, func() time.Time { return now })
		firstTime := now
		secondTime := now.Add(20 * time.Minute)
		firstA := Message{Sender: "10086", Timestamp: firstTime, Text: "A1", Concatenation: &Concatenation{Reference: 7, Total: 2, Sequence: 1}}
		firstB := Message{Sender: "10086", Timestamp: secondTime, Text: "B1", Concatenation: &Concatenation{Reference: 7, Total: 2, Sequence: 1}}
		secondA := Message{Sender: "10086", Timestamp: firstTime.Add(time.Second), Text: "A2", Concatenation: &Concatenation{Reference: 7, Total: 2, Sequence: 2}}
		secondB := Message{Sender: "10086", Timestamp: secondTime.Add(time.Second), Text: "B2", Concatenation: &Concatenation{Reference: 7, Total: 2, Sequence: 2}}

		for _, message := range []Message{firstA, firstB} {
			_, created, err := inbox.AddSegment(message)
			Expect(err).NotTo(HaveOccurred())
			Expect(created).To(BeFalse())
		}
		completeA, created, err := inbox.AddSegment(secondA)
		Expect(err).NotTo(HaveOccurred())
		Expect(created).To(BeTrue())
		Expect(completeA.Text).To(Equal("A1A2"))
		completeB, created, err := inbox.AddSegment(secondB)
		Expect(err).NotTo(HaveOccurred())
		Expect(created).To(BeTrue())
		Expect(completeB.Text).To(Equal("B1B2"))
	})

	It("persists only fingerprints and suppresses duplicates after restart", func() {
		statePath := filepath.Join(GinkgoT().TempDir(), "sms-inbox.json")
		inbox, err := newPersistentInbox(statePath, 24*time.Hour, func() time.Time { return now })
		Expect(err).NotTo(HaveOccurred())
		message := Message{Sender: "+8613500000000", Timestamp: now, Text: "private message"}

		received, created, err := inbox.AddSegment(message)

		Expect(err).NotTo(HaveOccurred())
		Expect(created).To(BeTrue())
		Expect(received.ID).To(HaveLen(64))
		content, err := os.ReadFile(statePath)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(content)).NotTo(ContainSubstring(message.Sender))
		Expect(string(content)).NotTo(ContainSubstring(message.Text))
		info, err := os.Stat(statePath)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o600)))

		restarted, err := newPersistentInbox(statePath, 24*time.Hour, func() time.Time { return now })
		Expect(err).NotTo(HaveOccurred())
		_, created, err = restarted.AddSegment(message)
		Expect(err).NotTo(HaveOccurred())
		Expect(created).To(BeFalse())
	})

	It("expires persistent fingerprints after the retention period", func() {
		statePath := filepath.Join(GinkgoT().TempDir(), "sms-inbox.json")
		clock := now
		inbox, err := newPersistentInbox(statePath, time.Hour, func() time.Time { return clock })
		Expect(err).NotTo(HaveOccurred())
		message := Message{Sender: "10086", Timestamp: now, Text: "balance"}
		_, created, err := inbox.AddSegment(message)
		Expect(err).NotTo(HaveOccurred())
		Expect(created).To(BeTrue())

		clock = clock.Add(2 * time.Hour)
		restarted, err := newPersistentInbox(statePath, time.Hour, func() time.Time { return clock })
		Expect(err).NotTo(HaveOccurred())
		_, created, err = restarted.AddSegment(message)
		Expect(err).NotTo(HaveOccurred())
		Expect(created).To(BeTrue())
	})

	It("rolls back an in-memory fingerprint when durable persistence fails", func() {
		directory := GinkgoT().TempDir()
		statePath := filepath.Join(directory, "sms-inbox.json")
		inbox, err := newPersistentInbox(statePath, time.Hour, func() time.Time { return now })
		Expect(err).NotTo(HaveOccurred())
		Expect(os.Mkdir(statePath, 0o700)).To(Succeed())
		message := Message{Sender: "10086", Timestamp: now, Text: "retry me"}

		_, created, err := inbox.AddSegment(message)

		Expect(err).To(MatchError(ContainSubstring("replace SMS inbox state")))
		Expect(created).To(BeFalse())
		Expect(os.Remove(statePath)).To(Succeed())
		_, created, err = inbox.AddSegment(message)
		Expect(err).NotTo(HaveOccurred())
		Expect(created).To(BeTrue())
	})

	DescribeTable("rejects invalid persistent state",
		func(content string) {
			statePath := filepath.Join(GinkgoT().TempDir(), "sms-inbox.json")
			Expect(os.WriteFile(statePath, []byte(content), 0o600)).To(Succeed())

			_, err := NewPersistentInbox(statePath, time.Hour)

			Expect(err).To(MatchError("SMS inbox state is invalid"))
		},
		Entry("invalid JSON", "{"),
		Entry("unsupported version", `{"version":2,"delivered":[]}`),
		Entry("malformed fingerprint", `{"version":1,"delivered":[{"fingerprint":"short","delivered_at":"2026-10-15T12:00:00Z"}]}`),
		Entry("zero timestamp", `{"version":1,"delivered":[{"fingerprint":"`+strings.Repeat("00", 32)+`","delivered_at":"0001-01-01T00:00:00Z"}]}`),
	)

	It("requires an absolute persistent state path", func() {
		_, err := NewPersistentInbox("relative.json", time.Hour)
		Expect(err).To(MatchError("SMS inbox state path must be absolute"))
	})
})
