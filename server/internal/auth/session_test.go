package auth

import (
	"bytes"
	"errors"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Session credentials", func() {
	var now time.Time

	BeforeEach(func() {
		now = time.Date(2026, 10, 15, 12, 0, 0, 123, time.FixedZone("test", 8*60*60))
	})

	It("creates independent access and refresh credentials", func() {
		randomBytes := append(bytes.Repeat([]byte{1}, tokenBytes), bytes.Repeat([]byte{2}, tokenBytes)...)

		pair, err := newTokenPair(now, bytes.NewReader(randomBytes))

		Expect(err).NotTo(HaveOccurred())
		Expect(pair.AccessToken).NotTo(Equal(pair.RefreshToken))
		Expect(pair.AccessExpiresAt).To(Equal(now.UTC().Add(15 * time.Minute)))
		Expect(pair.RefreshExpiresAt).To(Equal(now.UTC().Add(30 * 24 * time.Hour)))
		_, err = TokenHash(pair.AccessToken)
		Expect(err).NotTo(HaveOccurred())
	})

	It("creates a public token pair from system entropy", func() {
		pair, err := NewTokenPair(now)

		Expect(err).NotTo(HaveOccurred())
		Expect(pair.AccessToken).NotTo(BeEmpty())
		Expect(pair.RefreshToken).NotTo(BeEmpty())
	})

	It("does not return a partial pair when access entropy fails", func() {
		pair, err := newTokenPair(now, strings.NewReader("short"))

		Expect(err).To(MatchError(ContainSubstring("generate session token")))
		Expect(pair).To(BeZero())
	})

	It("does not return a partial pair when refresh entropy fails", func() {
		pair, err := newTokenPair(now, bytes.NewReader(bytes.Repeat([]byte{1}, tokenBytes)))

		Expect(err).To(MatchError(ContainSubstring("generate session token")))
		Expect(pair).To(BeZero())
	})

	It("hashes valid opaque credentials", func() {
		token, err := NewOpaqueToken()
		Expect(err).NotTo(HaveOccurred())

		first, err := TokenHash(token)
		Expect(err).NotTo(HaveOccurred())
		second, err := TokenHash(token)
		Expect(err).NotTo(HaveOccurred())
		Expect(first).To(Equal(second))
	})

	DescribeTable("rejects malformed opaque credentials",
		func(token string) {
			_, err := TokenHash(token)
			Expect(errors.Is(err, ErrInvalidToken)).To(BeTrue())
		},
		Entry("invalid encoding", "!"),
		Entry("too short", "c2hvcnQ"),
		Entry("too long", "YWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFh"),
	)

	It("reports entropy failures while creating an opaque token", func() {
		token, err := randomToken(strings.NewReader("short"))

		Expect(err).To(MatchError(ContainSubstring("generate session token")))
		Expect(token).To(BeEmpty())
	})
})
