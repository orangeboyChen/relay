package agent

import (
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func configLookup(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}

var _ = Describe("Agent configuration", func() {
	It("uses stable device and persistence defaults", func() {
		config, err := LoadConfig(configLookup(map[string]string{
			"SIM_DECK_SERVER_URL": "https://deck.example",
		}))

		Expect(err).NotTo(HaveOccurred())
		Expect(config.ATDevice).To(Equal("/dev/sim-deck/at"))
		Expect(config.AudioMode).To(Equal(AudioModeALSA))
		Expect(config.ALSACaptureDevice).To(Equal("hw:0,0"))
		Expect(config.ALSAPlaybackDevice).To(Equal("hw:0,0"))
		Expect(config.PCMDevice).To(Equal("/dev/sim-deck/pcm"))
		Expect(config.HealthAddress).To(Equal(":8080"))
		Expect(config.Baud).To(Equal(115200))
		Expect(config.CommandTimeout).To(Equal(5 * time.Second))
		Expect(config.CommandPoll).To(Equal(time.Second))
		Expect(config.RadioStatusPoll).To(Equal(15 * time.Second))
		Expect(config.StateDirectory).To(Equal("/var/lib/sim-deck"))
		Expect(config.PairingTokenFile).To(Equal("/run/secrets/sim_deck_pairing_token"))
		Expect(config.AllowInsecureHTTP).To(BeFalse())
	})

	It("reads explicit development and device overrides", func() {
		config, err := LoadConfig(configLookup(map[string]string{
			"SIM_DECK_AT_DEVICE":             "/dev/ttyUSB2",
			"SIM_DECK_AUDIO_MODE":            "raw-pcm",
			"SIM_DECK_ALSA_CAPTURE_DEVICE":   "plughw:2,0",
			"SIM_DECK_ALSA_PLAYBACK_DEVICE":  "plughw:2,1",
			"SIM_DECK_PCM_DEVICE":            "/dev/ttyUSB4",
			"SIM_DECK_HEALTH_ADDRESS":        "127.0.0.1:9090",
			"SIM_DECK_BAUD":                  "9600",
			"SIM_DECK_COMMAND_TIMEOUT":       "9s",
			"SIM_DECK_COMMAND_POLL_INTERVAL": "250ms",
			"SIM_DECK_RADIO_STATUS_INTERVAL": "30s",
			"SIM_DECK_SERVER_URL":            "http://server:8080/",
			"SIM_DECK_STATE_DIRECTORY":       "/tmp/state",
			"SIM_DECK_PAIRING_TOKEN_FILE":    "/tmp/pairing-token",
			"SIM_DECK_ALLOW_INSECURE_HTTP":   "true",
		}))

		Expect(err).NotTo(HaveOccurred())
		Expect(config.ATDevice).To(Equal("/dev/ttyUSB2"))
		Expect(config.AudioMode).To(Equal(AudioModeRawPCM))
		Expect(config.ALSACaptureDevice).To(Equal("plughw:2,0"))
		Expect(config.ALSAPlaybackDevice).To(Equal("plughw:2,1"))
		Expect(config.PCMDevice).To(Equal("/dev/ttyUSB4"))
		Expect(config.HealthAddress).To(Equal("127.0.0.1:9090"))
		Expect(config.Baud).To(Equal(9600))
		Expect(config.CommandTimeout).To(Equal(9 * time.Second))
		Expect(config.CommandPoll).To(Equal(250 * time.Millisecond))
		Expect(config.RadioStatusPoll).To(Equal(30 * time.Second))
		Expect(config.ServerURL).To(Equal("http://server:8080"))
		Expect(config.StateDirectory).To(Equal("/tmp/state"))
		Expect(config.PairingTokenFile).To(Equal("/tmp/pairing-token"))
		Expect(config.AllowInsecureHTTP).To(BeTrue())
	})

	DescribeTable("rejects invalid environment values",
		func(overrides map[string]string, expectedMessage string) {
			values := map[string]string{"SIM_DECK_SERVER_URL": "https://deck.example"}
			for key, value := range overrides {
				values[key] = value
			}

			_, err := LoadConfig(configLookup(values))

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(expectedMessage))
		},
		Entry("invalid insecure HTTP flag", map[string]string{"SIM_DECK_ALLOW_INSECURE_HTTP": "perhaps"}, "ALLOW_INSECURE_HTTP"),
		Entry("invalid baud", map[string]string{"SIM_DECK_BAUD": "fast"}, "BAUD"),
		Entry("zero baud", map[string]string{"SIM_DECK_BAUD": "0"}, "BAUD"),
		Entry("invalid command timeout", map[string]string{"SIM_DECK_COMMAND_TIMEOUT": "later"}, "COMMAND_TIMEOUT"),
		Entry("zero command timeout", map[string]string{"SIM_DECK_COMMAND_TIMEOUT": "0s"}, "COMMAND_TIMEOUT"),
		Entry("invalid command poll", map[string]string{"SIM_DECK_COMMAND_POLL_INTERVAL": "later"}, "COMMAND_POLL_INTERVAL"),
		Entry("zero command poll", map[string]string{"SIM_DECK_COMMAND_POLL_INTERVAL": "0s"}, "COMMAND_POLL_INTERVAL"),
		Entry("invalid radio status interval", map[string]string{"SIM_DECK_RADIO_STATUS_INTERVAL": "later"}, "RADIO_STATUS_INTERVAL"),
		Entry("zero radio status interval", map[string]string{"SIM_DECK_RADIO_STATUS_INTERVAL": "0s"}, "RADIO_STATUS_INTERVAL"),
		Entry("empty AT device", map[string]string{"SIM_DECK_AT_DEVICE": " "}, "AT_DEVICE"),
		Entry("invalid audio mode", map[string]string{"SIM_DECK_AUDIO_MODE": "automatic"}, "AUDIO_MODE"),
		Entry("missing ALSA capture", map[string]string{"SIM_DECK_ALSA_CAPTURE_DEVICE": " "}, "ALSA_CAPTURE_DEVICE"),
		Entry("missing ALSA playback", map[string]string{"SIM_DECK_ALSA_PLAYBACK_DEVICE": " "}, "ALSA_PLAYBACK_DEVICE"),
		Entry("missing raw PCM path", map[string]string{"SIM_DECK_AUDIO_MODE": "raw-pcm", "SIM_DECK_PCM_DEVICE": " "}, "PCM_DEVICE"),
		Entry("missing server URL", map[string]string{"SIM_DECK_SERVER_URL": ""}, "SERVER_URL"),
		Entry("relative server URL", map[string]string{"SIM_DECK_SERVER_URL": "server:8080"}, "SERVER_URL"),
		Entry("HTTP without opt in", map[string]string{"SIM_DECK_SERVER_URL": "http://server:8080"}, "must use HTTPS"),
		Entry("server URL credentials", map[string]string{"SIM_DECK_SERVER_URL": "https://user:pass@server"}, "SERVER_URL"),
		Entry("server URL query", map[string]string{"SIM_DECK_SERVER_URL": "https://server?token=value"}, "SERVER_URL"),
		Entry("server URL fragment", map[string]string{"SIM_DECK_SERVER_URL": "https://server/#fragment"}, "SERVER_URL"),
		Entry("relative state directory", map[string]string{"SIM_DECK_STATE_DIRECTORY": "state"}, "STATE_DIRECTORY"),
		Entry("relative pairing token", map[string]string{"SIM_DECK_PAIRING_TOKEN_FILE": "pairing-token"}, "PAIRING_TOKEN_FILE"),
	)

	It("does not treat whitespace-only overrides as values", func() {
		config, err := LoadConfig(configLookup(map[string]string{
			"SIM_DECK_SERVER_URL":          "https://deck.example/",
			"SIM_DECK_BAUD":                " ",
			"SIM_DECK_COMMAND_TIMEOUT":     " ",
			"SIM_DECK_ALLOW_INSECURE_HTTP": " ",
		}))

		Expect(err).NotTo(HaveOccurred())
		Expect(config.ServerURL).To(Equal("https://deck.example"))
		Expect(config.Baud).To(Equal(115200))
		Expect(config.CommandTimeout).To(Equal(5 * time.Second))
		Expect(strings.TrimSpace(config.ATDevice)).NotTo(BeEmpty())
	})
})
