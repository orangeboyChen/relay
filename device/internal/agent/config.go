package agent

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	defaultATDevice           = "/dev/sim-deck/at"
	defaultPCMDevice          = "/dev/sim-deck/pcm"
	defaultALSACaptureDevice  = "hw:0,0"
	defaultALSAPlaybackDevice = "hw:0,0"
	defaultHealthAddr         = ":8080"
	defaultBaud               = 115200
	defaultStateDir           = "/var/lib/sim-deck"
	defaultPairingFile        = "/run/secrets/sim_deck_pairing_token"
)

// AudioMode selects the modem audio transport exposed to the media peer.
type AudioMode string

const (
	AudioModeALSA   AudioMode = "alsa"
	AudioModeRawPCM AudioMode = "raw-pcm"
)

// Config describes the host devices and local health endpoint used by one agent instance.
type Config struct {
	ATDevice           string
	AudioMode          AudioMode
	ALSACaptureDevice  string
	ALSAPlaybackDevice string
	PCMDevice          string
	HealthAddress      string
	Baud               int
	CommandTimeout     time.Duration
	CommandPoll        time.Duration
	RadioStatusPoll    time.Duration
	ServerURL          string
	StateDirectory     string
	PairingTokenFile   string
	AllowInsecureHTTP  bool
}

// LoadConfig reads agent configuration from an environment lookup function.
func LoadConfig(lookup func(string) (string, bool)) (Config, error) {
	config := Config{
		ATDevice:           valueOrDefault(lookup, "SIM_DECK_AT_DEVICE", defaultATDevice),
		AudioMode:          AudioMode(strings.ToLower(strings.TrimSpace(valueOrDefault(lookup, "SIM_DECK_AUDIO_MODE", string(AudioModeALSA))))),
		ALSACaptureDevice:  valueOrDefault(lookup, "SIM_DECK_ALSA_CAPTURE_DEVICE", defaultALSACaptureDevice),
		ALSAPlaybackDevice: valueOrDefault(lookup, "SIM_DECK_ALSA_PLAYBACK_DEVICE", defaultALSAPlaybackDevice),
		PCMDevice:          valueOrDefault(lookup, "SIM_DECK_PCM_DEVICE", defaultPCMDevice),
		HealthAddress:      valueOrDefault(lookup, "SIM_DECK_HEALTH_ADDRESS", defaultHealthAddr),
		Baud:               defaultBaud,
		CommandTimeout:     5 * time.Second,
		CommandPoll:        time.Second,
		RadioStatusPoll:    15 * time.Second,
		ServerURL:          strings.TrimRight(strings.TrimSpace(valueOrDefault(lookup, "SIM_DECK_SERVER_URL", "")), "/"),
		StateDirectory:     valueOrDefault(lookup, "SIM_DECK_STATE_DIRECTORY", defaultStateDir),
		PairingTokenFile:   valueOrDefault(lookup, "SIM_DECK_PAIRING_TOKEN_FILE", defaultPairingFile),
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
	switch config.AudioMode {
	case AudioModeALSA:
		if strings.TrimSpace(config.ALSACaptureDevice) == "" || strings.TrimSpace(config.ALSAPlaybackDevice) == "" {
			return Config{}, fmt.Errorf("SIM_DECK_ALSA_CAPTURE_DEVICE and SIM_DECK_ALSA_PLAYBACK_DEVICE must not be empty in ALSA mode")
		}
	case AudioModeRawPCM:
		if strings.TrimSpace(config.PCMDevice) == "" {
			return Config{}, fmt.Errorf("SIM_DECK_PCM_DEVICE must not be empty in raw-pcm mode")
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
	if !filepath.IsAbs(config.PairingTokenFile) {
		return Config{}, fmt.Errorf("SIM_DECK_PAIRING_TOKEN_FILE must be an absolute path")
	}
	return config, nil
}

func valueOrDefault(lookup func(string) (string, bool), key string, fallback string) string {
	if value, ok := lookup(key); ok {
		return value
	}
	return fallback
}
