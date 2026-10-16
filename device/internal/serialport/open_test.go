package serialport

import (
	"bufio"
	"crypto/tls"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("AT endpoint selection", func() {
	It("opens a TLS 1.3 bridge authenticated by the configured CA", func() {
		server := httptest.NewUnstartedServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			_, _ = response.Write([]byte("ready"))
		}))
		server.TLS = &tls.Config{MinVersion: tls.VersionTLS13}
		server.StartTLS()
		defer server.Close()

		certificate := pem.EncodeToMemory(&pem.Block{
			Type:  "CERTIFICATE",
			Bytes: server.Certificate().Raw,
		})
		caFile := filepath.Join(GinkgoT().TempDir(), "bridge-ca.pem")
		Expect(os.WriteFile(caFile, certificate, 0o600)).To(Succeed())

		connection, err := Open(strings.Replace(server.URL, "https://", "tls://", 1), 115200, caFile)
		Expect(err).NotTo(HaveOccurred())
		defer connection.Close()

		_, err = fmt.Fprintf(connection, "GET / HTTP/1.1\r\nHost: bridge\r\nConnection: close\r\n\r\n")
		Expect(err).NotTo(HaveOccurred())
		response, err := http.ReadResponse(bufio.NewReader(connection), nil)
		Expect(err).NotTo(HaveOccurred())
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(body)).To(Equal("ready"))
	})

	DescribeTable("rejects unsafe or malformed bridge configuration",
		func(endpoint string, caFile string, expected string) {
			connection, err := Open(endpoint, 115200, caFile)
			Expect(connection).To(BeNil())
			Expect(err).To(MatchError(ContainSubstring(expected)))
		},
		Entry("plaintext network", "tcp://bridge:9000", "", "unsupported AT endpoint scheme"),
		Entry("missing port", "tls://bridge", "", "include a port"),
		Entry("credentials", "tls://user:secret@bridge:9000", "", "/host:port"),
		Entry("missing CA", "tls://bridge:9000", "", "CA file is required"),
		Entry("missing CA file", "tls://bridge:9000", "/missing/ca.pem", "read AT TLS CA file"),
		Entry("CA on local TTY", "/dev/ttyUSB2", "/tmp/ca.pem", "requires a tls:// endpoint"),
	)
})
