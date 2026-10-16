//go:build !linux

package serialport

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Non-Linux serial port", func() {
	It("reports the supported production platform", func() {
		file, err := Open("/dev/ttyUSB2", 115200, "", "", "")

		Expect(file).To(BeNil())
		Expect(err).To(MatchError("serial modem access is supported only on Linux"))
	})
})
