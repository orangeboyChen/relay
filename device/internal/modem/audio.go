package modem

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"sim-deck-device/internal/at"
)

const (
	usbCompositionQuery = `AT+QCFG="usbcfg"`
	uacEnableCommand    = "AT+QPCMV=1,2"
	uacStatusQuery      = "AT+QPCMV?"
)

// ConfigureUAC verifies the persistent USB composition and enables volatile UAC voice routing.
func ConfigureUAC(ctx context.Context, client *at.Client, timeout time.Duration) error {
	if client == nil || timeout <= 0 {
		return errors.New("AT client and positive timeout are required for UAC configuration")
	}

	response, err := sendAudioCommand(ctx, client, timeout, usbCompositionQuery)
	if err != nil {
		return fmt.Errorf("query USB composition: %w", err)
	}
	if !response.OK() {
		return fmt.Errorf("modem rejected USB composition query: %s", response.Terminal)
	}
	enabled, ok := parseUACComposition(response.Lines)
	if !ok {
		return errors.New("modem USB composition response is missing the UAC function flag")
	}
	if !enabled {
		return errors.New("modem USB audio interface is disabled in AT+QCFG=\"usbcfg\"; preserve the current composition, set only the final UAC flag to 1, and restart the modem")
	}

	response, err = sendAudioCommand(ctx, client, timeout, uacEnableCommand)
	if err != nil {
		return fmt.Errorf("enable UAC voice routing: %w", err)
	}
	if !response.OK() {
		return fmt.Errorf("modem rejected UAC voice routing: %s", response.Terminal)
	}

	response, err = sendAudioCommand(ctx, client, timeout, uacStatusQuery)
	if err != nil {
		return fmt.Errorf("verify UAC voice routing: %w", err)
	}
	if !response.OK() {
		return fmt.Errorf("modem rejected UAC voice routing query: %s", response.Terminal)
	}
	if !hasUACVoiceStatus(response.Lines) {
		return errors.New("modem did not confirm UAC voice routing mode 1,2")
	}
	return nil
}

func sendAudioCommand(ctx context.Context, client *at.Client, timeout time.Duration, command string) (at.Response, error) {
	commandContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return client.Send(commandContext, command)
}

func parseUACComposition(lines []string) (bool, bool) {
	for _, line := range lines {
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), `+qcfg: "usbcfg",`) {
			continue
		}
		fields := strings.Split(line, ",")
		if len(fields) != 10 {
			return false, false
		}
		value := strings.TrimSpace(fields[len(fields)-1])
		switch value {
		case "0":
			return false, true
		case "1":
			return true, true
		default:
			return false, false
		}
	}
	return false, false
}

func hasUACVoiceStatus(lines []string) bool {
	for _, line := range lines {
		if strings.TrimSpace(line) == "+QPCMV: 1,2" {
			return true
		}
	}
	return false
}
