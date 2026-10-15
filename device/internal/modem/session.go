package modem

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"sim-deck-device/internal/at"
	"sim-deck-device/internal/call"
	"sim-deck-device/internal/sms"
)

var initializationCommands = []string{
	"ATE0",
	"AT+CMEE=2",
	"AT+CMGF=0",
	"AT+CNMI=2,2,0,1,0",
	"AT+CCWA=1,1",
}

const defaultCallPollInterval = time.Second

// MessageSink receives one complete, durably deduplicated SMS.
type MessageSink interface {
	ReceiveSMS(context.Context, sms.ReceivedMessage) error
}

// MessageSinkFunc adapts a function to MessageSink.
type MessageSinkFunc func(context.Context, sms.ReceivedMessage) error

// ReceiveSMS invokes the adapted function.
func (function MessageSinkFunc) ReceiveSMS(ctx context.Context, message sms.ReceivedMessage) error {
	return function(ctx, message)
}

// Session owns modem initialization and routes unsolicited SMS and call events.
type Session struct {
	client           *at.Client
	inbox            *sms.Inbox
	calls            *call.Controller
	sink             MessageSink
	logger           *slog.Logger
	timeout          time.Duration
	callPollInterval time.Duration
}

// NewSession creates a modem session from endpoint-local dependencies.
func NewSession(client *at.Client, inbox *sms.Inbox, sink MessageSink, logger *slog.Logger, timeout time.Duration) (*Session, error) {
	if client == nil || inbox == nil || sink == nil || logger == nil || timeout <= 0 {
		return nil, errors.New("modem session dependencies and positive timeout are required")
	}
	return &Session{
		client:           client,
		inbox:            inbox,
		calls:            call.NewController(client),
		sink:             sink,
		logger:           logger,
		timeout:          timeout,
		callPollInterval: defaultCallPollInterval,
	}, nil
}

// Calls returns the call controller updated by the session's URC loop.
func (session *Session) Calls() *call.Controller {
	return session.calls
}

// Initialize configures modem notifications and imports stored messages before readiness.
func (session *Session) Initialize(ctx context.Context) error {
	for _, command := range initializationCommands {
		response, err := session.send(ctx, command)
		if err != nil {
			return err
		}
		if !response.OK() {
			return fmt.Errorf("modem rejected initialization command %s: %s", command, response.Terminal)
		}
	}

	response, err := session.send(ctx, "AT+CMGL=4")
	if err != nil {
		return err
	}
	if !response.OK() {
		return fmt.Errorf("modem rejected SMS history import: %s", response.Terminal)
	}
	pdus, extractionError := historyPDUs(response.Lines)
	messages, importError := session.inbox.ImportPDUs(pdus)
	if extractionError != nil || importError != nil {
		session.logger.Warn("some modem SMS history entries were skipped", "error", errors.Join(extractionError, importError))
	}
	for _, message := range messages {
		if err := session.sink.ReceiveSMS(ctx, message); err != nil {
			return fmt.Errorf("deliver imported SMS: %w", err)
		}
	}
	return nil
}

// Run routes modem notifications until cancellation or a terminal serial error.
func (session *Session) Run(ctx context.Context) error {
	callPoll := time.NewTicker(session.callPollInterval)
	defer callPoll.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-session.client.Errors():
			return err
		case urc := <-session.client.URCs():
			if err := session.handleURC(ctx, urc); err != nil {
				return err
			}
		case <-callPoll.C:
			if err := session.refreshCallState(ctx); err != nil {
				return err
			}
		}
	}
}

func (session *Session) refreshCallState(ctx context.Context) error {
	state := session.calls.State()
	if state == call.StateIdle || state == call.StateEnded {
		return nil
	}
	response, err := session.send(ctx, "AT+CLCC")
	if err != nil {
		return fmt.Errorf("query modem call state: %w", err)
	}
	if !response.OK() {
		return fmt.Errorf("modem rejected call state query: %s", response.Terminal)
	}
	if err := session.calls.ApplyCLCC(response.Lines); err != nil {
		return fmt.Errorf("parse modem call state: %w", err)
	}
	return nil
}

func (session *Session) send(ctx context.Context, command string) (at.Response, error) {
	commandContext, cancel := context.WithTimeout(ctx, session.timeout)
	defer cancel()
	return session.client.Send(commandContext, command)
}

func (session *Session) handleURC(ctx context.Context, urc string) error {
	switch {
	case strings.HasPrefix(urc, "+CMT:"):
		_, encoded, ok := strings.Cut(urc, "\n")
		if !ok || strings.TrimSpace(encoded) == "" {
			session.logger.Warn("discarded malformed direct SMS notification")
			return nil
		}
		message, err := sms.DecodeDeliver(encoded)
		if err != nil {
			session.logger.Warn("discarded malformed direct SMS PDU", "error", err)
			return nil
		}
		received, created, err := session.inbox.AddSegment(message)
		if err != nil {
			return fmt.Errorf("store direct SMS: %w", err)
		}
		if !created {
			return nil
		}
		if err := session.sink.ReceiveSMS(ctx, received); err != nil {
			return fmt.Errorf("deliver direct SMS: %w", err)
		}
	case strings.HasPrefix(urc, "+CDS:"):
		session.logger.Info("SMS delivery status report received")
	case isCallURC(urc):
		session.calls.ApplyURC(urc)
		snapshot := session.calls.Snapshot()
		session.logger.Info("cellular call state changed", "state", snapshot.State, "active", snapshot.Active, "held", snapshot.Held, "waiting", snapshot.Waiting)
	default:
		session.logger.Debug("unhandled modem notification", "type", notificationType(urc))
	}
	return nil
}

func historyPDUs(lines []string) ([]string, error) {
	result := make([]string, 0)
	var failures []error
	for index := 0; index < len(lines); index++ {
		if !strings.HasPrefix(lines[index], "+CMGL:") {
			continue
		}
		if index+1 >= len(lines) || strings.HasPrefix(lines[index+1], "+CMGL:") {
			failures = append(failures, fmt.Errorf("history entry at response line %d has no PDU", index))
			continue
		}
		result = append(result, lines[index+1])
		index++
	}
	return result, errors.Join(failures...)
}

func isCallURC(urc string) bool {
	return urc == "RING" || strings.HasPrefix(urc, "+CCWA:") || urc == "NO CARRIER" || urc == "NO ANSWER" || urc == "BUSY"
}

func notificationType(urc string) string {
	if prefix, _, ok := strings.Cut(urc, ":"); ok {
		return prefix
	}
	if _, _, ok := strings.Cut(urc, "\n"); ok {
		return "multiline"
	}
	return urc
}
