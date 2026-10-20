package media

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

const quecTelUSBVendorComponent = "usb2c7c:"

type alsaInfoRunner func(context.Context, string, ...string) ([]byte, error)

// ValidateALSADevices rejects stale container mappings that point at another USB sound card.
func ValidateALSADevices(ctx context.Context, captureDevice string, playbackDevice string) error {
	return validateALSADevices(ctx, captureDevice, playbackDevice, func(ctx context.Context, executable string, arguments ...string) ([]byte, error) {
		return exec.CommandContext(ctx, executable, arguments...).CombinedOutput()
	})
}

func validateALSADevices(ctx context.Context, captureDevice string, playbackDevice string, run alsaInfoRunner) error {
	if run == nil {
		return errors.New("ALSA device inspector is required")
	}
	devices := []string{captureDevice, playbackDevice}
	inspected := make(map[string]struct{}, len(devices))
	for _, device := range devices {
		controlDevice, err := alsaControlDevice(device)
		if err != nil {
			return err
		}
		if _, exists := inspected[controlDevice]; exists {
			continue
		}
		inspected[controlDevice] = struct{}{}
		output, runError := run(ctx, "amixer", "--device", controlDevice, "info")
		detail := strings.TrimSpace(string(output))
		if runError != nil {
			if detail == "" {
				detail = runError.Error()
			}
			return fmt.Errorf("inspect ALSA device %q: %s", controlDevice, detail)
		}
		if !strings.Contains(strings.ToLower(detail), quecTelUSBVendorComponent) {
			return fmt.Errorf(
				"ALSA device %q is not the Quectel modem USB audio interface (expected USB vendor 2c7c): %s",
				controlDevice,
				firstLine(detail),
			)
		}
	}
	return nil
}

func alsaControlDevice(pcmDevice string) (string, error) {
	device := strings.TrimSpace(pcmDevice)
	if device == "" {
		return "", errors.New("ALSA capture and playback devices are required")
	}
	if strings.HasPrefix(device, "plughw:") {
		device = "hw:" + strings.TrimPrefix(device, "plughw:")
	}
	control, _, _ := strings.Cut(device, ",")
	return control, nil
}

func firstLine(value string) string {
	line, _, _ := strings.Cut(value, "\n")
	if strings.TrimSpace(line) == "" {
		return "no ALSA card identity returned"
	}
	return strings.TrimSpace(line)
}
