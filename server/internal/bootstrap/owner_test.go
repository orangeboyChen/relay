package bootstrap

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"sim-deck-server/internal/auth"
)

type memoryOwnerStore struct {
	existsError       error
	exists            bool
	createError       error
	createResult      bool
	createCalls       int
	passwordHash      string
	recoveryPublicKey []byte
}

func (store *memoryOwnerStore) OwnerExists(context.Context) (bool, error) {
	return store.exists, store.existsError
}

func (store *memoryOwnerStore) CreateOwner(_ context.Context, passwordHash string, recoveryPublicKey []byte) (bool, error) {
	store.createCalls++
	store.passwordHash = passwordHash
	store.recoveryPublicKey = append([]byte(nil), recoveryPublicKey...)
	return store.createResult, store.createError
}

var _ = Describe("Owner bootstrap", func() {
	var (
		directory    string
		passwordPath string
		recoveryPath string
		publicKey    []byte
		repository   *memoryOwnerStore
	)

	BeforeEach(func() {
		directory = GinkgoT().TempDir()
		passwordPath = filepath.Join(directory, "password")
		recoveryPath = filepath.Join(directory, "recovery-public-key")
		publicKey = bytes.Repeat([]byte{3}, ed25519.PublicKeySize)
		Expect(os.WriteFile(recoveryPath, []byte(base64.RawStdEncoding.EncodeToString(publicKey)), 0o600)).To(Succeed())
		repository = &memoryOwnerStore{createResult: true}
	})

	It("reads Docker secrets and stores only derived owner state", func() {
		Expect(os.WriteFile(passwordPath, []byte("a strong bootstrap password\n"), 0o600)).To(Succeed())

		result, err := Owner(context.Background(), repository, Config{
			PasswordFile:          passwordPath,
			RecoveryPublicKeyFile: recoveryPath,
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(Result{Created: true}))
		Expect(repository.createCalls).To(Equal(1))
		valid, err := auth.VerifyPassword("a strong bootstrap password", repository.passwordHash)
		Expect(err).NotTo(HaveOccurred())
		Expect(valid).To(BeTrue())
		Expect(repository.recoveryPublicKey).To(Equal(publicKey))
	})

	It("generates a one-time password only for a newly created owner", func() {
		result, err := Owner(context.Background(), repository, Config{
			PasswordFile:          filepath.Join(directory, "missing-password"),
			RecoveryPublicKeyFile: recoveryPath,
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(result.Created).To(BeTrue())
		Expect(result.GeneratedPassword).NotTo(BeEmpty())
		valid, err := auth.VerifyPassword(result.GeneratedPassword, repository.passwordHash)
		Expect(err).NotTo(HaveOccurred())
		Expect(valid).To(BeTrue())
	})

	It("returns immediately when the owner already exists", func() {
		repository.exists = true

		result, err := Owner(context.Background(), repository, Config{})

		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(BeZero())
		Expect(repository.createCalls).To(BeZero())
	})

	It("does not reveal a generated password when another bootstrap wins", func() {
		repository.createResult = false

		result, err := Owner(context.Background(), repository, Config{
			RecoveryPublicKeyFile: recoveryPath,
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(BeZero())
		Expect(repository.createCalls).To(Equal(1))
	})

	It("propagates owner lookup and creation failures", func() {
		repository.existsError = errors.New("lookup failed")
		_, err := Owner(context.Background(), repository, Config{})
		Expect(err).To(MatchError("lookup failed"))

		repository.existsError = nil
		repository.createError = errors.New("create failed")
		_, err = Owner(context.Background(), repository, Config{RecoveryPublicKeyFile: recoveryPath})
		Expect(err).To(MatchError("create failed"))
	})

	DescribeTable("rejects an invalid recovery key secret",
		func(path func() string, expected string) {
			_, err := Owner(context.Background(), repository, Config{RecoveryPublicKeyFile: path()})
			Expect(err).To(MatchError(ContainSubstring(expected)))
			Expect(repository.createCalls).To(BeZero())
		},
		Entry("missing configuration", func() string { return "" }, "RECOVERY_PUBLIC_KEY_FILE is required"),
		Entry("missing file", func() string { return filepath.Join(directory, "missing-recovery") }, "read recovery public key"),
		Entry("unreadable path", func() string { return directory }, "read recovery public key"),
		Entry("invalid base64", func() string {
			path := filepath.Join(directory, "invalid-base64")
			Expect(os.WriteFile(path, []byte("!"), 0o600)).To(Succeed())
			return path
		}, "decode recovery public key"),
		Entry("wrong key size", func() string {
			path := filepath.Join(directory, "short-key")
			Expect(os.WriteFile(path, []byte(base64.RawStdEncoding.EncodeToString([]byte("short"))), 0o600)).To(Succeed())
			return path
		}, "recovery public key must contain"),
	)

	It("rejects an empty configured password", func() {
		Expect(os.WriteFile(passwordPath, []byte(" \n"), 0o600)).To(Succeed())

		_, err := Owner(context.Background(), repository, Config{
			PasswordFile:          passwordPath,
			RecoveryPublicKeyFile: recoveryPath,
		})

		Expect(err).To(MatchError("admin password secret is empty"))
		Expect(repository.createCalls).To(BeZero())
	})

	It("reports configured password read failures", func() {
		_, err := Owner(context.Background(), repository, Config{
			PasswordFile:          directory,
			RecoveryPublicKeyFile: recoveryPath,
		})

		Expect(err).To(MatchError(ContainSubstring("read admin password secret")))
		Expect(repository.createCalls).To(BeZero())
	})
})
