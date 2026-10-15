package agent

import (
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Pairing token secret", func() {
	It("trims surrounding whitespace", func() {
		path := filepath.Join(GinkgoT().TempDir(), "pairing-token")
		Expect(os.WriteFile(path, []byte("  token-value\n"), 0o600)).To(Succeed())

		secret, err := ReadSecret(path)

		Expect(err).NotTo(HaveOccurred())
		Expect(secret).To(Equal("token-value"))
	})

	DescribeTable("rejects secrets that cannot be used",
		func(prepare func(string) string, expectedMessage string) {
			directory := GinkgoT().TempDir()
			path := prepare(directory)

			_, err := ReadSecret(path)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(expectedMessage))
		},
		Entry("missing file", func(directory string) string {
			return filepath.Join(directory, "missing")
		}, "open pairing token secret"),
		Entry("directory instead of file", func(directory string) string {
			return directory
		}, "read pairing token secret"),
		Entry("empty file", func(directory string) string {
			path := filepath.Join(directory, "empty")
			Expect(os.WriteFile(path, []byte(" \n"), 0o600)).To(Succeed())
			return path
		}, "pairing token secret is empty"),
		Entry("oversized file", func(directory string) string {
			path := filepath.Join(directory, "oversized")
			Expect(os.WriteFile(path, []byte(strings.Repeat("a", maxSecretBytes+1)), 0o600)).To(Succeed())
			return path
		}, "exceeds 4096 bytes"),
	)
})
