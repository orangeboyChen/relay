package media

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("G.711 mu-law", func() {
	DescribeTable("matches standard code points",
		func(sample int16, encoded byte, decoded int16) {
			Expect(encodeMuLaw(sample)).To(Equal(encoded))
			Expect(decodeMuLaw(encoded)).To(Equal(decoded))
		},
		Entry("silence", int16(0), byte(0xff), int16(0)),
		Entry("positive maximum", int16(32767), byte(0x80), int16(32124)),
		Entry("negative maximum", int16(-32768), byte(0x00), int16(-32124)),
		Entry("positive speech", int16(1000), byte(0xce), int16(988)),
		Entry("negative speech", int16(-1000), byte(0x4e), int16(-988)),
	)

	It("keeps representative speech samples within quantization error", func() {
		for sample := -30000; sample <= 30000; sample += 257 {
			decoded := int(decodeMuLaw(encodeMuLaw(int16(sample))))
			Expect(decoded).To(BeNumerically("~", sample, 700))
		}
	})
})
