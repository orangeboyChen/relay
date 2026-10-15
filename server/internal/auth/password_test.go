package auth

import (
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Password credentials", func() {
	It("hashes and verifies the correct password", func() {
		encoded, err := HashPassword("correct horse battery staple")
		Expect(err).NotTo(HaveOccurred())

		valid, err := VerifyPassword("correct horse battery staple", encoded)
		Expect(err).NotTo(HaveOccurred())
		Expect(valid).To(BeTrue())

		valid, err = VerifyPassword("wrong password", encoded)
		Expect(err).NotTo(HaveOccurred())
		Expect(valid).To(BeFalse())
	})

	It("uses the supplied entropy as the encoded salt", func() {
		salt := bytes.Repeat([]byte{7}, saltLength)
		encoded, err := hashPassword("password", bytes.NewReader(salt))

		Expect(err).NotTo(HaveOccurred())
		Expect(encoded).To(ContainSubstring(base64.RawStdEncoding.EncodeToString(salt)))
	})

	It("rejects an empty password before reading entropy", func() {
		encoded, err := hashPassword("", io.Reader(nil))

		Expect(err).To(MatchError("password must not be empty"))
		Expect(encoded).To(BeEmpty())
	})

	It("reports entropy failures while hashing", func() {
		encoded, err := hashPassword("password", strings.NewReader("short"))

		Expect(err).To(MatchError(ContainSubstring("generate password salt")))
		Expect(encoded).To(BeEmpty())
	})

	DescribeTable("rejects malformed password hashes",
		func(encoded string) {
			valid, err := VerifyPassword("password", encoded)
			Expect(errors.Is(err, ErrInvalidPasswordHash)).To(BeTrue())
			Expect(valid).To(BeFalse())
		},
		Entry("missing fields", "$argon2id$broken"),
		Entry("wrong algorithm", "$scrypt$v=19$m=8192,t=1,p=1$MTIzNDU2Nzg$MTIzNDU2Nzg5MDEyMzQ1Ng"),
		Entry("wrong version", "$argon2id$v=16$m=8192,t=1,p=1$MTIzNDU2Nzg$MTIzNDU2Nzg5MDEyMzQ1Ng"),
		Entry("unparseable parameters", "$argon2id$v=19$broken$MTIzNDU2Nzg$MTIzNDU2Nzg5MDEyMzQ1Ng"),
		Entry("memory too small", "$argon2id$v=19$m=1024,t=1,p=1$MTIzNDU2Nzg$MTIzNDU2Nzg5MDEyMzQ1Ng"),
		Entry("memory too large", "$argon2id$v=19$m=1048577,t=1,p=1$MTIzNDU2Nzg$MTIzNDU2Nzg5MDEyMzQ1Ng"),
		Entry("iterations too small", "$argon2id$v=19$m=8192,t=0,p=1$MTIzNDU2Nzg$MTIzNDU2Nzg5MDEyMzQ1Ng"),
		Entry("iterations too large", "$argon2id$v=19$m=8192,t=11,p=1$MTIzNDU2Nzg$MTIzNDU2Nzg5MDEyMzQ1Ng"),
		Entry("threads too small", "$argon2id$v=19$m=8192,t=1,p=0$MTIzNDU2Nzg$MTIzNDU2Nzg5MDEyMzQ1Ng"),
		Entry("threads too large", "$argon2id$v=19$m=8192,t=1,p=17$MTIzNDU2Nzg$MTIzNDU2Nzg5MDEyMzQ1Ng"),
		Entry("invalid salt encoding", "$argon2id$v=19$m=8192,t=1,p=1$!$MTIzNDU2Nzg5MDEyMzQ1Ng"),
		Entry("short salt", "$argon2id$v=19$m=8192,t=1,p=1$MTIz$MTIzNDU2Nzg5MDEyMzQ1Ng"),
		Entry("invalid hash encoding", "$argon2id$v=19$m=8192,t=1,p=1$MTIzNDU2Nzg$!"),
		Entry("short hash", "$argon2id$v=19$m=8192,t=1,p=1$MTIzNDU2Nzg$MTIz"),
		Entry("oversized hash", "$argon2id$v=19$m=8192,t=1,p=1$MTIzNDU2Nzg$"+base64.RawStdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 65))),
	)

	It("generates independent printable initial passwords", func() {
		first, err := GenerateInitialPassword()
		Expect(err).NotTo(HaveOccurred())
		second, err := GenerateInitialPassword()
		Expect(err).NotTo(HaveOccurred())

		Expect(first).To(HaveLen(32))
		Expect(second).To(HaveLen(32))
		Expect(first).NotTo(Equal(second))
		_, err = base64.RawURLEncoding.DecodeString(first)
		Expect(err).NotTo(HaveOccurred())
	})

	It("reports entropy failures while generating an initial password", func() {
		password, err := generateInitialPassword(strings.NewReader("short"))

		Expect(err).To(MatchError(ContainSubstring("generate initial password")))
		Expect(password).To(BeEmpty())
	})
})
