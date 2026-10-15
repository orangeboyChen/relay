package agent

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	defaultATDevice    = "/dev/sim-deck/at"
	defaultPCMDevice   = "/dev/sim-deck/pcm"
	defaultSoundDevice = "/dev/snd"
	defaultHealthAddr  = ":8080"
	defaultBaud        = 115200
)

// Config describes the host devices and local health endpoint used by one agent instance.
type Config struct {
	ATDevice       string
	PCMDevice      string
	SoundDevice    string
	HealthAddress  string
	Baud           int
	CommandTimeout time.Duration
}

// LoadConfig reads agent configuration from an environment lookup function.
func LoadConfig(lookup func(string) (string, bool)) (Config, error) {
	config := Config{
		ATDevice:       valueOrDefault(lookup, "SIM_DECK_AT_DEVICE", defaultATDevice),
		PCMDevice:      valueOrDefault(lookup, "SIM_DECK_PCM_DEVICE", defaultPCMDevice),
		SoundDevice:    valueOrDefault(lookup, "SIM_DECK_SOUND_DEVICE", defaultSoundDevice),
		HealthAddress:  valueOrDefault(lookup, "SIM_DECK_HEALTH_ADDRESS", defaultHealthAddr),
		Baud:           defaultBaud,
		CommandTimeout: 5 * time.Second,
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
	if strings.TrimSpace(config.ATDevice) == "" {
		return Config{}, fmt.Errorf("SIM_DECK_AT_DEVICE must not be empty")
	}
	return config, nil
}

func valueOrDefault(lookup func(string) (string, bool), key string, fallback string) string {
	if value, ok := lookup(key); ok && strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}
