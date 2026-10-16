//go:build linux

package serialport

import (
	"os"
	"syscall"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Linux serial port", func() {
	DescribeTable("maps supported baud rates",
		func(baud int, expected uint32) {
			speed, err := baudConstant(baud)
			Expect(err).NotTo(HaveOccurred())
			Expect(speed).To(Equal(expected))
		},
		Entry("9600", 9600, uint32(syscall.B9600)),
		Entry("19200", 19200, uint32(syscall.B19200)),
		Entry("38400", 38400, uint32(syscall.B38400)),
		Entry("57600", 57600, uint32(syscall.B57600)),
		Entry("115200", 115200, uint32(syscall.B115200)),
	)

	It("rejects an unsupported baud rate before opening the path", func() {
		file, err := Open("/does/not/exist", 12345, "", "", "")

		Expect(file).To(BeNil())
		Expect(err).To(MatchError("unsupported baud rate 12345"))
	})

	It("reports failure to open a missing serial device", func() {
		file, err := Open("/does/not/exist", 115200, "", "", "")

		Expect(file).To(BeNil())
		Expect(err).To(MatchError(ContainSubstring("open serial port")))
	})

	It("rejects a regular file that cannot provide terminal settings", func() {
		path := GinkgoT().TempDir() + "/not-a-terminal"
		Expect(os.WriteFile(path, nil, 0o600)).To(Succeed())

		file, err := Open(path, 115200, "", "", "")

		Expect(file).To(BeNil())
		Expect(err).To(MatchError(ContainSubstring("read serial settings")))
	})

	It("configures the Linux pseudo-terminal multiplexer", func() {
		file, err := Open("/dev/ptmx", 115200, "", "", "")

		Expect(err).NotTo(HaveOccurred())
		Expect(file).NotTo(BeNil())
		Expect(file.Close()).To(Succeed())
	})
})
