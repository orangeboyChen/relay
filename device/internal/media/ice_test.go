package media

import (
	"net"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ICE settings", func() {
	It("binds configured UDP and TCP listeners and releases them", func() {
		udpPort := reserveUDPPort()
		tcpPort := reserveTCPPort()
		settings, closeICE, err := NewICESettings(ICEConfig{UDPPort: udpPort, TCPPort: tcpPort, PublicIP: "192.0.2.10"})
		Expect(err).NotTo(HaveOccurred())
		Expect(settings).NotTo(BeZero())
		closeICE()
	})

	It("closes a UDP listener when the TCP listener cannot be created", func() {
		udpPort := reserveUDPPort()
		tcpListener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4zero, Port: 0})
		Expect(err).NotTo(HaveOccurred())
		defer tcpListener.Close()

		_, _, err = NewICESettings(ICEConfig{UDPPort: udpPort, TCPPort: tcpListener.Addr().(*net.TCPAddr).Port})
		Expect(err).To(MatchError(ContainSubstring("listen for WebRTC TCP media")))
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

func reserveUDPPort() int {
	listener, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		panic(err)
	}
	port := listener.LocalAddr().(*net.UDPAddr).Port
	_ = listener.Close()
	return port
}

func reserveTCPPort() int {
	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		panic(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return port
}
