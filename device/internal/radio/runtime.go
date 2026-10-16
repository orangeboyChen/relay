package radio

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"sim-deck-device/internal/at"
	"sim-deck-device/internal/identity"
)

// Status is the current non-sensitive cellular registration summary.
type Status struct {
	RadioAccessTechnology string `json:"radio_access_technology"`
	SignalBars            *int   `json:"signal_bars"`
}

type commander interface {
	Send(context.Context, string) (at.Response, error)
}

// Remote accepts authenticated radio status updates.
type Remote interface {
	UpdateRadioStatus(context.Context, identity.State, Status) error
}

// Runtime polls one modem and publishes its current cellular status.
type Runtime struct {
	commander commander
	remote    Remote
	identity  identity.State
	interval  time.Duration
	timeout   time.Duration
}

// NewRuntime creates a radio poller for one bound modem identity.
func NewRuntime(commander commander, remote Remote, state identity.State, interval time.Duration, timeout time.Duration) (*Runtime, error) {
	if commander == nil || remote == nil || state.DeviceUUID == "" || state.Credential == "" || interval <= 0 || timeout <= 0 {
		return nil, errors.New("radio runtime requires modem, remote, bound identity, and positive intervals")
	}
	return &Runtime{commander: commander, remote: remote, identity: state, interval: interval, timeout: timeout}, nil
}

// Run publishes immediately and then at the configured interval until cancellation.
func (runtime *Runtime) Run(ctx context.Context) error {
	if err := runtime.publish(ctx); err != nil {
		return err
	}
	ticker := time.NewTicker(runtime.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := runtime.publish(ctx); err != nil {
				return err
			}
		}
	}
}

func (runtime *Runtime) publish(ctx context.Context) error {
	status, err := runtime.read(ctx)
	if err != nil {
		return err
	}
	if err := runtime.remote.UpdateRadioStatus(ctx, runtime.identity, status); err != nil {
		return fmt.Errorf("publish radio status: %w", err)
	}
	return nil
}

func (runtime *Runtime) read(ctx context.Context) (Status, error) {
	network, err := runtime.send(ctx, "AT+QNWINFO")
	if err != nil {
		return Status{}, fmt.Errorf("query radio access technology: %w", err)
	}
	if !network.OK() {
		return Status{}, fmt.Errorf("modem rejected radio access query: %s", network.Terminal)
	}
	signal, err := runtime.send(ctx, "AT+CSQ")
	if err != nil {
		return Status{}, fmt.Errorf("query signal strength: %w", err)
	}
	if !signal.OK() {
		return Status{}, fmt.Errorf("modem rejected signal query: %s", signal.Terminal)
	}
	return Status{
		RadioAccessTechnology: parseTechnology(network.Lines),
		SignalBars:            parseSignalBars(signal.Lines),
	}, nil
}

func (runtime *Runtime) send(ctx context.Context, command string) (at.Response, error) {
	commandContext, cancel := context.WithTimeout(ctx, runtime.timeout)
	defer cancel()
	return runtime.commander.Send(commandContext, command)
}

func parseTechnology(lines []string) string {
	for _, line := range lines {
		if !strings.HasPrefix(line, "+QNWINFO:") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, "+QNWINFO:"))
		technology := strings.ToUpper(strings.Trim(strings.Split(value, ",")[0], " \""))
		switch {
		case strings.Contains(technology, "NR5G-SA"), technology == "NR SA":
			return "nr_sa"
		case strings.Contains(technology, "NR5G-NSA"), technology == "NR NSA":
			return "nr_nsa"
		case strings.Contains(technology, "LTE"):
			return "lte"
		case strings.Contains(technology, "HSPA"), strings.Contains(technology, "HSDPA"), strings.Contains(technology, "HSUPA"):
			return "hspa"
		case strings.Contains(technology, "WCDMA"), strings.Contains(technology, "UMTS"):
			return "umts"
		case strings.Contains(technology, "EDGE"):
			return "edge"
		case strings.Contains(technology, "GSM"), strings.Contains(technology, "GPRS"):
			return "gsm"
		default:
			return "unknown"
		}
	}
	return "unknown"
}

func parseSignalBars(lines []string) *int {
	for _, line := range lines {
		if !strings.HasPrefix(line, "+CSQ:") {
			continue
		}
		fields := strings.Split(strings.TrimSpace(strings.TrimPrefix(line, "+CSQ:")), ",")
		if len(fields) < 1 {
			return nil
		}
		rssi, err := strconv.Atoi(strings.TrimSpace(fields[0]))
		if err != nil || rssi < 0 || rssi > 31 {
			return nil
		}
		bars := 0
		switch {
		case rssi >= 20:
			bars = 4
		case rssi >= 15:
			bars = 3
		case rssi >= 10:
			bars = 2
		case rssi >= 2:
			bars = 1
		}
		return &bars
	}
	return nil
}
