package agent

import (
	"fmt"
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
	defaultDeviceRoot         = "/dev"
	defaultSysfsRoot          = "/sys"
	defaultBaud               = 115200
	defaultStateDir           = "/var/lib/sim-deck"
)

// AudioMode selects the modem audio transport exposed to the media peer.
type AudioMode string

// DiscoveryMode selects whether one explicitly configured modem or all visible
// modem topologies are managed by the node agent.
type DiscoveryMode string

const (
	DiscoveryModeSingle DiscoveryMode = "single"
	DiscoveryModeAuto   DiscoveryMode = "auto"
)

const (
	AudioModeALSA      AudioMode = "alsa"
	AudioModeRawPCM    AudioMode = "raw-pcm"
	AudioModeRemotePCM AudioMode = "remote-pcm"
)

// Config describes the host devices and local health endpoint used by one agent instance.
type Config struct {
	DiscoveryMode         DiscoveryMode
	DeviceRoot            string
	SysfsRoot             string
	DeviceUUID            string
	SlotKey               string
	SlotIndex             int
	ATDevice              string
	ATTLSCAFile           string
	ATTLSCertificateFile  string
	ATTLSKeyFile          string
	AudioMode             AudioMode
	ALSACaptureDevice     string
	ALSAPlaybackDevice    string
	PCMDevice             string
	PCMEndpoint           string
	PCMTLSCAFile          string
	PCMTLSCertificateFile string
	PCMTLSKeyFile         string
	HealthAddress         string
	Baud                  int
	CommandTimeout        time.Duration
	CommandPoll           time.Duration
	DiscoveryPoll         time.Duration
	RadioStatusPoll       time.Duration
	ServerURL             string
	StateDirectory        string
	AllowInsecureHTTP     bool
}

// LoadConfig reads agent configuration from an environment lookup function.
func LoadConfig(lookup func(string) (string, bool)) (Config, error) {
	discoveryMode := DiscoveryMode(strings.ToLower(strings.TrimSpace(valueOrDefault(lookup, "SIM_DECK_DISCOVERY_MODE", string(DiscoveryModeSingle)))))
	config := Config{
		DiscoveryMode:         discoveryMode,
		DeviceRoot:            valueOrDefault(lookup, "SIM_DECK_DEVICE_ROOT", defaultDeviceRoot),
		SysfsRoot:             valueOrDefault(lookup, "SIM_DECK_SYSFS_ROOT", defaultSysfsRoot),
		DeviceUUID:            strings.ToLower(strings.TrimSpace(valueOrDefault(lookup, "SIM_DECK_DEVICE_UUID", ""))),
		SlotKey:               strings.TrimSpace(valueOrDefault(lookup, "SIM_DECK_SLOT_KEY", "1")),
		SlotIndex:             1,
		ATDevice:              valueOrDefault(lookup, "SIM_DECK_AT_DEVICE", defaultATDevice),
		ATTLSCAFile:           strings.TrimSpace(valueOrDefault(lookup, "SIM_DECK_AT_TLS_CA_FILE", "")),
		ATTLSCertificateFile:  strings.TrimSpace(valueOrDefault(lookup, "SIM_DECK_AT_TLS_CERT_FILE", "")),
		ATTLSKeyFile:          strings.TrimSpace(valueOrDefault(lookup, "SIM_DECK_AT_TLS_KEY_FILE", "")),
		AudioMode:             AudioMode(strings.ToLower(strings.TrimSpace(valueOrDefault(lookup, "SIM_DECK_AUDIO_MODE", string(AudioModeALSA))))),
		ALSACaptureDevice:     valueOrDefault(lookup, "SIM_DECK_ALSA_CAPTURE_DEVICE", defaultALSACaptureDevice),
		ALSAPlaybackDevice:    valueOrDefault(lookup, "SIM_DECK_ALSA_PLAYBACK_DEVICE", defaultALSAPlaybackDevice),
		PCMDevice:             valueOrDefault(lookup, "SIM_DECK_PCM_DEVICE", defaultPCMDevice),
		PCMEndpoint:           strings.TrimSpace(valueOrDefault(lookup, "SIM_DECK_PCM_ENDPOINT", "")),
		PCMTLSCAFile:          strings.TrimSpace(valueOrDefault(lookup, "SIM_DECK_PCM_TLS_CA_FILE", "")),
		PCMTLSCertificateFile: strings.TrimSpace(valueOrDefault(lookup, "SIM_DECK_PCM_TLS_CERT_FILE", "")),
		PCMTLSKeyFile:         strings.TrimSpace(valueOrDefault(lookup, "SIM_DECK_PCM_TLS_KEY_FILE", "")),
		HealthAddress:         valueOrDefault(lookup, "SIM_DECK_HEALTH_ADDRESS", defaultHealthAddr),
		Baud:                  defaultBaud,
		CommandTimeout:        5 * time.Second,
		CommandPoll:           time.Second,
		DiscoveryPoll:         2 * time.Second,
		RadioStatusPoll:       15 * time.Second,
		ServerURL:             strings.TrimRight(strings.TrimSpace(valueOrDefault(lookup, "SIM_DECK_SERVER_URL", "")), "/"),
		StateDirectory:        valueOrDefault(lookup, "SIM_DECK_STATE_DIRECTORY", defaultStateDir),
	}
	if config.DiscoveryMode != DiscoveryModeSingle && config.DiscoveryMode != DiscoveryModeAuto {
		return Config{}, fmt.Errorf("SIM_DECK_DISCOVERY_MODE must be single or auto")
	}
	if config.DiscoveryMode == DiscoveryModeAuto && !hasValue(lookup, "SIM_DECK_AT_DEVICE") {
		config.ATDevice = ""
	}
	if config.DeviceUUID != "" && !identity.ValidDeviceUUID(config.DeviceUUID) {
		return Config{}, fmt.Errorf("SIM_DECK_DEVICE_UUID must be empty or a canonical UUID")
	}
	if config.SlotKey == "" || len(config.SlotKey) > 64 {
		return Config{}, fmt.Errorf("SIM_DECK_SLOT_KEY must be between 1 and 64 characters")
	}
	if value, ok := lookup("SIM_DECK_SLOT_INDEX"); ok && strings.TrimSpace(value) != "" {
		index, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || index < 1 || index > 32 {
			return Config{}, fmt.Errorf("SIM_DECK_SLOT_INDEX must be between 1 and 32")
		}
		config.SlotIndex = index
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
	if config.DiscoveryMode == DiscoveryModeSingle && strings.TrimSpace(config.ATDevice) == "" {
		return Config{}, fmt.Errorf("SIM_DECK_AT_DEVICE must not be empty")
	}
	if config.DiscoveryMode == DiscoveryModeAuto && strings.TrimSpace(config.ATDevice) != "" {
		return Config{}, fmt.Errorf("SIM_DECK_AT_DEVICE must be omitted in automatic discovery mode")
	}
	if config.DiscoveryMode == DiscoveryModeSingle && strings.HasPrefix(config.ATDevice, "tls://") {
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
	} else if config.DiscoveryMode == DiscoveryModeSingle && strings.Contains(config.ATDevice, "://") {
		return Config{}, fmt.Errorf("SIM_DECK_AT_DEVICE supports only a local TTY or TLS endpoint")
	} else if config.DiscoveryMode == DiscoveryModeSingle && (config.ATTLSCAFile != "" || config.ATTLSCertificateFile != "" || config.ATTLSKeyFile != "") {
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
	case AudioModeRemotePCM:
		endpoint, err := url.Parse(config.PCMEndpoint)
		if err != nil || endpoint.Scheme != "tls" || endpoint.Host == "" || endpoint.Port() == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || (endpoint.Path != "" && endpoint.Path != "/") {
			return Config{}, fmt.Errorf("SIM_DECK_PCM_ENDPOINT must be tls://host:port")
		}
		if !filepath.IsAbs(config.PCMTLSCAFile) {
			return Config{}, fmt.Errorf("SIM_DECK_PCM_TLS_CA_FILE must be an absolute path for remote-pcm mode")
		}
		if !filepath.IsAbs(config.PCMTLSCertificateFile) || !filepath.IsAbs(config.PCMTLSKeyFile) {
			return Config{}, fmt.Errorf("SIM_DECK_PCM_TLS_CERT_FILE and SIM_DECK_PCM_TLS_KEY_FILE must be absolute paths for remote-pcm mode")
		}
	default:
		return Config{}, fmt.Errorf("SIM_DECK_AUDIO_MODE must be alsa, raw-pcm, or remote-pcm")
	}
	if config.AudioMode != AudioModeRemotePCM && (config.PCMEndpoint != "" || config.PCMTLSCAFile != "" || config.PCMTLSCertificateFile != "" || config.PCMTLSKeyFile != "") {
		return Config{}, fmt.Errorf("remote PCM endpoint and TLS credentials require remote-pcm mode")
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
	if config.DiscoveryMode == DiscoveryModeAuto {
		if !filepath.IsAbs(config.DeviceRoot) || !filepath.IsAbs(config.SysfsRoot) {
			return Config{}, fmt.Errorf("SIM_DECK_DEVICE_ROOT and SIM_DECK_SYSFS_ROOT must be absolute paths")
		}
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

func hasValue(lookup func(string) (string, bool), key string) bool {
	value, ok := lookup(key)
	return ok && strings.TrimSpace(value) != ""
}
