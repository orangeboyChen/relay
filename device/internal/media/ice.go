package media

import (
	"errors"
	"fmt"
	"net"
	"net/netip"

	"github.com/pion/ice/v4"
	"github.com/pion/webrtc/v4"
)

// NewICESettings creates the shared Pion settings and listeners for one agent.
func NewICESettings(config ICEConfig) (webrtc.SettingEngine, func(), error) {
	if config.UDPPort < 0 || config.UDPPort > 65535 || config.TCPPort < 0 || config.TCPPort > 65535 {
		return webrtc.SettingEngine{}, func() {}, errors.New("ICE media ports must be between 0 and 65535")
	}
	settings := webrtc.SettingEngine{}
	settings.SetICEMulticastDNSMode(ice.MulticastDNSModeDisabled)
	if config.PublicIP != "" {
		address, err := netip.ParseAddr(config.PublicIP)
		if err != nil || !address.Is4() {
			return webrtc.SettingEngine{}, func() {}, errors.New("ICE public IP must be an IPv4 address")
		}
		settings.SetNAT1To1IPs([]string{config.PublicIP}, webrtc.ICECandidateTypeHost)
	}
	closers := make([]func() error, 0, 2)
	if config.UDPPort > 0 {
		connection, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: config.UDPPort})
		if err != nil {
			return webrtc.SettingEngine{}, func() {}, fmt.Errorf("listen for WebRTC UDP media: %w", err)
		}
		mux := webrtc.NewICEUDPMux(nil, connection)
		settings.SetICEUDPMux(mux)
		closers = append(closers, mux.Close)
	}
	if config.TCPPort > 0 {
		listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4zero, Port: config.TCPPort})
		if err != nil {
			for _, closeListener := range closers {
				_ = closeListener()
			}
			return webrtc.SettingEngine{}, func() {}, fmt.Errorf("listen for WebRTC TCP media: %w", err)
		}
		mux := webrtc.NewICETCPMux(nil, listener, 8)
		settings.SetICETCPMux(mux)
		closers = append(closers, mux.Close)
	}
	if config.UDPPort > 0 || config.TCPPort > 0 {
		networkTypes := make([]webrtc.NetworkType, 0, 2)
		if config.UDPPort > 0 {
			networkTypes = append(networkTypes, webrtc.NetworkTypeUDP4)
		}
		if config.TCPPort > 0 {
			networkTypes = append(networkTypes, webrtc.NetworkTypeTCP4)
		}
		settings.SetNetworkTypes(networkTypes)
	}
	return settings, func() {
		for _, closeListener := range closers {
			_ = closeListener()
		}
	}, nil
}
