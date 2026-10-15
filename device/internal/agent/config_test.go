package agent

import (
	"testing"
	"time"
)

func TestLoadConfigUsesStableDeviceDefaults(t *testing.T) {
	config, err := LoadConfig(func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if config.ATDevice != "/dev/sim-deck/at" || config.PCMDevice != "/dev/sim-deck/pcm" {
		t.Fatalf("device paths = %q, %q", config.ATDevice, config.PCMDevice)
	}
	if config.Baud != 115200 || config.CommandTimeout != 5*time.Second {
		t.Fatalf("serial defaults = %d, %s", config.Baud, config.CommandTimeout)
	}
}

func TestLoadConfigReadsOverrides(t *testing.T) {
	values := map[string]string{
		"SIM_DECK_AT_DEVICE":       "/dev/ttyUSB2",
		"SIM_DECK_BAUD":            "9600",
		"SIM_DECK_COMMAND_TIMEOUT": "9s",
	}
	config, err := LoadConfig(func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	})
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if config.ATDevice != "/dev/ttyUSB2" || config.Baud != 9600 || config.CommandTimeout != 9*time.Second {
		t.Fatalf("config = %#v", config)
	}
}

func TestLoadConfigRejectsInvalidBaud(t *testing.T) {
	_, err := LoadConfig(func(key string) (string, bool) {
		if key == "SIM_DECK_BAUD" {
			return "fast", true
		}
		return "", false
	})
	if err == nil {
		t.Fatal("LoadConfig() error = nil, want validation error")
	}
}
