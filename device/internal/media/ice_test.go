package media

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ICE settings", func() {
	It("binds configured UDP and TCP listeners and releases them", func() {
		settings, closeICE, err := NewICESettings(ICEConfig{UDPPort: 0, TCPPort: 0})
		Expect(err).NotTo(HaveOccurred())
		Expect(settings).NotTo(BeZero())
		closeICE()
	})

	DescribeTable("rejects invalid public media configuration",
		func(config ICEConfig, message string) {
			_, _, err := NewICESettings(config)
			Expect(err).To(MatchError(message))
		},
		Entry("invalid UDP port", ICEConfig{UDPPort: 65536}, "ICE media ports must be between 0 and 65535"),
		Entry("invalid TCP port", ICEConfig{TCPPort: -1}, "ICE media ports must be between 0 and 65535"),
		Entry("non IPv4 public address", ICEConfig{PublicIP: "2001:db8::1"}, "ICE public IP must be an IPv4 address"),
	)
})
