package radio

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"sim-deck-device/internal/at"
	"sim-deck-device/internal/identity"
	"sim-deck-device/internal/slotpolicy"
)

// Status is the current non-sensitive cellular registration summary.
type Status struct {
	RadioAccessTechnology string              `json:"radio_access_technology"`
	SignalBars            *int                `json:"signal_bars"`
	SlotSettings          slotpolicy.Settings `json:"slot_settings"`
}

type commander interface {
	Send(context.Context, string) (at.Response, error)
}

// Remote accepts authenticated radio status updates.
type Remote interface {
	UpdateRadioStatus(context.Context, identity.State, Status) error
	PublishRadioStatus(context.Context, identity.State, Status, time.Time) error
}

// Runtime polls one modem and publishes its current cellular status.
type Runtime struct {
	commander commander
	remote    Remote
	identity  identity.State
	interval  time.Duration
	timeout   time.Duration
	logger    *slog.Logger
	policy    func() slotpolicy.Settings
}

// NewRuntime creates a radio poller for one bound modem identity.
func NewRuntime(commander commander, remote Remote, state identity.State, interval time.Duration, timeout time.Duration) (*Runtime, error) {
	return NewRuntimeWithLogger(commander, remote, state, interval, timeout, slog.Default())
}

// NewRuntimeWithLogger creates a radio poller that writes diagnostic status to the supplied logger.
func NewRuntimeWithLogger(commander commander, remote Remote, state identity.State, interval time.Duration, timeout time.Duration, logger *slog.Logger) (*Runtime, error) {
	return NewRuntimeWithLoggerAndPolicy(commander, remote, state, interval, timeout, logger, nil)
}

// NewRuntimeWithLoggerAndPolicy creates a radio poller that includes the current
// slot policy in each encrypted status heartbeat.
func NewRuntimeWithLoggerAndPolicy(commander commander, remote Remote, state identity.State, interval time.Duration, timeout time.Duration, logger *slog.Logger, policy func() slotpolicy.Settings) (*Runtime, error) {
	if commander == nil || remote == nil || state.DeviceUUID == "" || state.Credential == "" || interval <= 0 || timeout <= 0 {
		return nil, errors.New("radio runtime requires modem, remote, bound identity, and positive intervals")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Runtime{commander: commander, remote: remote, identity: state, interval: interval, timeout: timeout, logger: logger, policy: policy}, nil
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
			// A modem can temporarily delay radio queries while a call owns the
			// AT channel. Keep the ready device alive and retry on the next tick.
			if err := runtime.publish(ctx); err != nil {
				runtime.logger.Warn("radio status refresh failed", "device_uuid", runtime.identity.DeviceUUID, "error", err)
			}
		}
	}
}

// SetSlotSettingsProvider attaches the live policy snapshot to future heartbeats.
func (runtime *Runtime) SetSlotSettingsProvider(provider func() slotpolicy.Settings) {
	runtime.policy = provider
}

func (runtime *Runtime) publish(ctx context.Context) error {
	status, err := runtime.read(ctx)
	if err != nil {
		return err
	}
	if err := runtime.remote.UpdateRadioStatus(ctx, runtime.identity, status); err != nil {
		return fmt.Errorf("publish radio status: %w", err)
	}
	if err := runtime.remote.PublishRadioStatus(ctx, runtime.identity, status, time.Now().UTC()); err != nil {
		return fmt.Errorf("publish radio status event: %w", err)
	}
	runtime.logger.Info(
		"radio status published",
		"device_uuid", runtime.identity.DeviceUUID,
		"radio_access_technology", status.RadioAccessTechnology,
		"signal_bars", status.SignalBars,
	)
	return nil
}

func (runtime *Runtime) read(ctx context.Context) (Status, error) {
	_, err := runtime.readRegistration(ctx)
	if err != nil {
		return Status{}, err
	}

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
		SlotSettings:          runtime.currentSlotSettings(),
	}, nil
}

func (runtime *Runtime) currentSlotSettings() slotpolicy.Settings {
	if runtime.policy == nil {
		return slotpolicy.Settings{}
	}
	return runtime.policy()
}

func (runtime *Runtime) readRegistration(ctx context.Context) (bool, error) {
	eps, err := runtime.send(ctx, "AT+CEREG?")
	if err != nil {
		return false, fmt.Errorf("query EPS registration: %w", err)
	}
	epsStatus := -1
	if eps.OK() {
		if registered, found, status := parseRegistrationStatus(eps.Lines, "+CEREG:"); found {
			epsStatus = status
			if registered {
				return true, nil
			}
		}
	}

	circuit, err := runtime.send(ctx, "AT+CREG?")
	if err != nil {
		return false, fmt.Errorf("query circuit registration: %w", err)
	}
	circuitStatus := -1
	registrationFound := epsStatus >= 0
	if circuit.OK() {
		if registered, found, status := parseRegistrationStatus(circuit.Lines, "+CREG:"); found {
			registrationFound = true
			circuitStatus = status
			if registered {
				return true, nil
			}
		}
	}

	packet, err := runtime.send(ctx, "AT+CGREG?")
	if err != nil {
		return false, fmt.Errorf("query packet registration: %w", err)
	}
	packetStatus := -1
	if packet.OK() {
		if registered, found, status := parseRegistrationStatus(packet.Lines, "+CGREG:"); found {
			registrationFound = true
			packetStatus = status
			if registered {
				return true, nil
			}
		}
	}
	if !registrationFound {
		if !eps.OK() && !circuit.OK() && !packet.OK() {
			return false, fmt.Errorf("modem rejected registration queries: EPS %s, circuit %s, packet %s", eps.Terminal, circuit.Terminal, packet.Terminal)
		}
		return false, errors.New("modem registration response is missing +CEREG, +CREG, and +CGREG status")
	}
	runtime.logger.Warn(
		"modem is not registered with a cellular network",
		"device_uuid", runtime.identity.DeviceUUID,
		"eps_registration_status", epsStatus,
		"circuit_registration_status", circuitStatus,
		"packet_registration_status", packetStatus,
	)
	return false, nil
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

func parseRegistration(lines []string, prefix string) (bool, bool) {
	registered, found, _ := parseRegistrationStatus(lines, prefix)
	return registered, found
}

func parseRegistrationStatus(lines []string, prefix string) (bool, bool, int) {
	for _, line := range lines {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		fields := strings.Split(strings.TrimSpace(strings.TrimPrefix(line, prefix)), ",")
		statusIndex := 0
		if len(fields) > 1 {
			statusIndex = 1
		}
		status, err := strconv.Atoi(strings.Trim(strings.TrimSpace(fields[statusIndex]), "\""))
		if err != nil {
			return false, false, -1
		}
		return status == 1 || status == 5, true, status
	}
	return false, false, -1
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
