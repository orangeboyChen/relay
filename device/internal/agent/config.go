package agent

import (
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"sim-deck-device/internal/identity"
)

const (
	defaultATDevice           = "/dev/sim-deck/at"
	defaultPCMDevice          = "/dev/sim-deck/pcm"
	defaultALSACaptureDevice  = "hw:0,0"
	defaultALSAPlaybackDevice = "hw:0,0"
	defaultHealthAddr         = ":8080"
	defaultBaud               = 115200
	defaultStateDir           = "/var/lib/sim-deck"
)

// AudioMode selects the modem audio transport exposed to the media peer.
type AudioMode string

const (
	AudioModeALSA   AudioMode = "alsa"
	AudioModeRawPCM AudioMode = "raw-pcm"
)

// Config describes the host devices and local health endpoint used by one agent instance.
type Config struct {
	DeviceUUID           string
	ATDevice             string
	ATTLSCAFile          string
	ATTLSCertificateFile string
	ATTLSKeyFile         string
	AudioMode            AudioMode
	ALSACaptureDevice    string
	ALSAPlaybackDevice   string
	PCMDevice            string
	MediaUDPPort         int
	MediaTCPPort         int
	MediaPublicIP        string
	HealthAddress        string
	Baud                 int
	CommandTimeout       time.Duration
	CommandPoll          time.Duration
	DiscoveryPoll        time.Duration
	RadioStatusPoll      time.Duration
	ServerURL            string
	StateDirectory       string
	AllowInsecureHTTP    bool
}

// LoadConfig reads agent configuration from an environment lookup function.
func LoadConfig(lookup func(string) (string, bool)) (Config, error) {
	config := Config{
		DeviceUUID:           strings.ToLower(strings.TrimSpace(valueOrDefault(lookup, "SIM_DECK_DEVICE_UUID", ""))),
		ATDevice:             valueOrDefault(lookup, "SIM_DECK_AT_DEVICE", defaultATDevice),
		ATTLSCAFile:          strings.TrimSpace(valueOrDefault(lookup, "SIM_DECK_AT_TLS_CA_FILE", "")),
		ATTLSCertificateFile: strings.TrimSpace(valueOrDefault(lookup, "SIM_DECK_AT_TLS_CERT_FILE", "")),
		ATTLSKeyFile:         strings.TrimSpace(valueOrDefault(lookup, "SIM_DECK_AT_TLS_KEY_FILE", "")),
		AudioMode:            AudioMode(strings.ToLower(strings.TrimSpace(valueOrDefault(lookup, "SIM_DECK_AUDIO_MODE", string(AudioModeALSA))))),
		ALSACaptureDevice:    valueOrDefault(lookup, "SIM_DECK_ALSA_CAPTURE_DEVICE", defaultALSACaptureDevice),
		ALSAPlaybackDevice:   valueOrDefault(lookup, "SIM_DECK_ALSA_PLAYBACK_DEVICE", defaultALSAPlaybackDevice),
		PCMDevice:            valueOrDefault(lookup, "SIM_DECK_PCM_DEVICE", defaultPCMDevice),
		MediaPublicIP:        strings.TrimSpace(valueOrDefault(lookup, "SIM_DECK_MEDIA_PUBLIC_IP", "")),
		HealthAddress:        valueOrDefault(lookup, "SIM_DECK_HEALTH_ADDRESS", defaultHealthAddr),
		Baud:                 defaultBaud,
		CommandTimeout:       5 * time.Second,
		CommandPoll:          time.Second,
		DiscoveryPoll:        2 * time.Second,
		RadioStatusPoll:      15 * time.Second,
		ServerURL:            strings.TrimRight(strings.TrimSpace(valueOrDefault(lookup, "SIM_DECK_SERVER_URL", "")), "/"),
		StateDirectory:       valueOrDefault(lookup, "SIM_DECK_STATE_DIRECTORY", defaultStateDir),
	}
	if !identity.ValidDeviceUUID(config.DeviceUUID) {
		return Config{}, fmt.Errorf("SIM_DECK_DEVICE_UUID must be a canonical UUID")
	}
	if value, ok := lookup("SIM_DECK_ALLOW_INSECURE_HTTP"); ok && strings.TrimSpace(value) != "" {
		allow, err := strconv.ParseBool(value)
		if err != nil {
			return Config{}, fmt.Errorf("SIM_DECK_ALLOW_INSECURE_HTTP must be a boolean")
		}
		config.AllowInsecureHTTP = allow
	}

	if value, ok := lookup("SIM_DECK_BAUD"); ok && strings.TrimSpace(value) != "" {
		baud, err := strconv.Atoi(value)
		if err != nil || baud <= 0 {
			return Config{}, fmt.Errorf("SIM_DECK_BAUD must be a positive integer")
		}
		config.Baud = baud
	}
	if value, ok := lookup("SIM_DECK_COMMAND_TIMEOUT"); ok && strings.TrimSpace(value) != "" {
		timeout, err := time.ParseDuration(value)
		if err != nil || timeout <= 0 {
			return Config{}, fmt.Errorf("SIM_DECK_COMMAND_TIMEOUT must be a positive duration")
		}
		config.CommandTimeout = timeout
	}
	if value, ok := lookup("SIM_DECK_COMMAND_POLL_INTERVAL"); ok && strings.TrimSpace(value) != "" {
		interval, err := time.ParseDuration(value)
		if err != nil || interval <= 0 {
			return Config{}, fmt.Errorf("SIM_DECK_COMMAND_POLL_INTERVAL must be a positive duration")
		}
		config.CommandPoll = interval
	}
	if value, ok := lookup("SIM_DECK_DISCOVERY_POLL_INTERVAL"); ok && strings.TrimSpace(value) != "" {
		interval, err := time.ParseDuration(value)
		if err != nil || interval <= 0 {
			return Config{}, fmt.Errorf("SIM_DECK_DISCOVERY_POLL_INTERVAL must be a positive duration")
		}
		config.DiscoveryPoll = interval
	}
	if value, ok := lookup("SIM_DECK_RADIO_STATUS_INTERVAL"); ok && strings.TrimSpace(value) != "" {
		interval, err := time.ParseDuration(value)
		if err != nil || interval <= 0 {
			return Config{}, fmt.Errorf("SIM_DECK_RADIO_STATUS_INTERVAL must be a positive duration")
		}
		config.RadioStatusPoll = interval
	}
	if strings.TrimSpace(config.ATDevice) == "" {
		return Config{}, fmt.Errorf("SIM_DECK_AT_DEVICE must not be empty")
	}
	for key, target := range map[string]*int{
		"SIM_DECK_MEDIA_UDP_PORT": &config.MediaUDPPort,
		"SIM_DECK_MEDIA_TCP_PORT": &config.MediaTCPPort,
	} {
		if value, ok := lookup(key); ok && strings.TrimSpace(value) != "" {
			port, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || port < 1 || port > 65535 {
				return Config{}, fmt.Errorf("%s must be an integer between 1 and 65535", key)
			}
			*target = port
		}
	}
	if config.MediaPublicIP != "" && net.ParseIP(config.MediaPublicIP) == nil {
		return Config{}, fmt.Errorf("SIM_DECK_MEDIA_PUBLIC_IP must be a valid IP address")
	}
	if strings.HasPrefix(config.ATDevice, "tls://") {
		endpoint, err := url.Parse(config.ATDevice)
		if err != nil || endpoint.Scheme != "tls" || endpoint.Host == "" || endpoint.Port() == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || (endpoint.Path != "" && endpoint.Path != "/") {
			return Config{}, fmt.Errorf("SIM_DECK_AT_DEVICE TLS endpoint must be tls://host:port")
		}
		if !filepath.IsAbs(config.ATTLSCAFile) {
			return Config{}, fmt.Errorf("SIM_DECK_AT_TLS_CA_FILE must be an absolute path for a TLS AT endpoint")
		}
		if !filepath.IsAbs(config.ATTLSCertificateFile) || !filepath.IsAbs(config.ATTLSKeyFile) {
			return Config{}, fmt.Errorf("SIM_DECK_AT_TLS_CERT_FILE and SIM_DECK_AT_TLS_KEY_FILE must be absolute paths for a TLS AT endpoint")
		}
	} else if strings.Contains(config.ATDevice, "://") {
		return Config{}, fmt.Errorf("SIM_DECK_AT_DEVICE supports only a local TTY or TLS endpoint")
	} else if config.ATTLSCAFile != "" || config.ATTLSCertificateFile != "" || config.ATTLSKeyFile != "" {
		return Config{}, fmt.Errorf("SIM_DECK_AT_TLS credentials require a TLS AT endpoint")
	}
	switch config.AudioMode {
	case AudioModeALSA:
		if strings.TrimSpace(config.ALSACaptureDevice) == "" || strings.TrimSpace(config.ALSAPlaybackDevice) == "" {
			return Config{}, fmt.Errorf("SIM_DECK_ALSA_CAPTURE_DEVICE and SIM_DECK_ALSA_PLAYBACK_DEVICE must not be empty in ALSA mode")
		}
	case AudioModeRawPCM:
		if strings.TrimSpace(config.PCMDevice) == "" {
			return Config{}, fmt.Errorf("SIM_DECK_PCM_DEVICE must not be empty in raw-pcm mode")
		}
		if isSyntheticPCMDevice(config.PCMDevice) {
			return Config{}, fmt.Errorf("SIM_DECK_PCM_DEVICE must reference a real full-duplex modem audio device")
		}
	default:
		return Config{}, fmt.Errorf("SIM_DECK_AUDIO_MODE must be alsa or raw-pcm")
	}
	serverURL, err := url.Parse(config.ServerURL)
	if err != nil || serverURL.Host == "" || serverURL.User != nil || serverURL.RawQuery != "" || serverURL.Fragment != "" {
		return Config{}, fmt.Errorf("SIM_DECK_SERVER_URL must be an absolute server URL without credentials, query, or fragment")
	}
	if serverURL.Scheme != "https" && !(serverURL.Scheme == "http" && config.AllowInsecureHTTP) {
		return Config{}, fmt.Errorf("SIM_DECK_SERVER_URL must use HTTPS unless SIM_DECK_ALLOW_INSECURE_HTTP is true")
	}
	if !filepath.IsAbs(config.StateDirectory) {
		return Config{}, fmt.Errorf("SIM_DECK_STATE_DIRECTORY must be an absolute path")
	}
	return config, nil
}

func isSyntheticPCMDevice(path string) bool {
	switch filepath.Clean(strings.TrimSpace(path)) {
	case "/dev/zero", "/dev/null", "/dev/full", "/dev/random", "/dev/urandom":
		return true
	default:
		return false
	}
}

func valueOrDefault(lookup func(string) (string, bool), key string, fallback string) string {
	if value, ok := lookup(key); ok {
		return value
	}
	return fallback
}
