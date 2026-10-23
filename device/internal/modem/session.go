package modem

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"sim-deck-device/internal/at"
	"sim-deck-device/internal/call"
	"sim-deck-device/internal/command"
	"sim-deck-device/internal/sms"
)

var initializationCommands = []string{
	"ATE0",
	"AT+CMEE=2",
	"AT+CFUN=1",
	"AT+CMGF=0",
	`AT+CPMS="MT","MT","MT"`,
	"AT+CNMI=2,1,0,1,0",
	"AT+CLIP=1",
	"AT+CCWA=1,1",
}

const simReadinessCommand = "AT+CPIN?"

const operatorSelectionQuery = "AT+COPS?"

const automaticOperatorSelectionTimeout = 3 * time.Minute

// ReadEquipmentIdentity returns the modem IMEI used only for local hardware identity derivation.
func ReadEquipmentIdentity(ctx context.Context, client *at.Client, timeout time.Duration) (string, error) {
	if client == nil || timeout <= 0 {
		return "", errors.New("AT client and positive timeout are required")
	}
	for _, command := range []string{"AT+CGSN", "AT+GSN"} {
		commandContext, cancel := context.WithTimeout(ctx, timeout)
		response, err := client.Send(commandContext, command)
		cancel()
		if err != nil {
			return "", err
		}
		if !response.OK() {
			continue
		}
		for _, line := range response.Lines {
			value := strings.Trim(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "+CGSN:")), "\" ")
			if len(value) >= 14 && len(value) <= 16 && allDigits(value) {
				return value, nil
			}
		}
	}
	return "", errors.New("modem did not return a valid equipment identity")
}

// ReadSIMSlotIndexes confirms the single physical slot exposed by EC20/EC25 modems.
func ReadSIMSlotIndexes(ctx context.Context, client *at.Client, timeout time.Duration) ([]int, error) {
	if client == nil || timeout <= 0 {
		return nil, errors.New("AT client and positive timeout are required")
	}
	commandContext, cancel := context.WithTimeout(ctx, timeout)
	response, err := client.Send(commandContext, "AT+QSIMSTAT?")
	cancel()
	if err != nil {
		return nil, err
	}
	if !response.OK() {
		return nil, ErrSIMSlotUnsupported
	}
	if _, ok := parseSIMInsertionStatus(response.Lines); !ok {
		return nil, ErrSIMSlotUnsupported
	}
	return []int{1}, nil
}

// ErrMessageDeliveryDeferred keeps a durable SMS pending without stopping unrelated modem services.
var ErrMessageDeliveryDeferred = errors.New("SMS delivery deferred")
var ErrSIMSlotUnsupported = errors.New("modem does not expose SIM slot status")

const defaultCallPollInterval = time.Second

const defaultMessageRetryInterval = time.Minute

const defaultMessagePollInterval = time.Minute

const simReadinessAttempts = 3

const simReadinessRetryDelay = 100 * time.Millisecond

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

// DeliveryReportSink receives authenticated modem delivery reports without SMS content.
type DeliveryReportSink func(context.Context, sms.DeliveryReport) error

// Session owns modem initialization and routes unsolicited SMS and call events.
type Session struct {
	client               *at.Client
	inbox                *sms.Inbox
	calls                *call.Controller
	sink                 MessageSink
	logger               *slog.Logger
	timeout              time.Duration
	callPollInterval     time.Duration
	messageRetryInterval time.Duration
	messagePollInterval  time.Duration
	references           map[byte]string
	pendingMessageID     string
	messageStatusMu      sync.Mutex
	deliveryReportSink   DeliveryReportSink
	mmsResults           chan error
}

// SIMSlotStatus describes one physical slot reported by a modem-specific command.
type SIMSlotStatus struct {
	Index   int
	Present bool
}

// SIMProfile contains subscriber data that a modem can read from the inserted card and network.
type SIMProfile struct {
	PhoneNumber string
	Operator    string
}

// NewSession creates a modem session from endpoint-local dependencies.
func NewSession(client *at.Client, inbox *sms.Inbox, sink MessageSink, logger *slog.Logger, timeout time.Duration) (*Session, error) {
	if client == nil || inbox == nil || sink == nil || logger == nil || timeout <= 0 {
		return nil, errors.New("modem session dependencies and positive timeout are required")
	}
	return &Session{
		client:               client,
		inbox:                inbox,
		calls:                call.NewController(client),
		sink:                 sink,
		logger:               logger,
		timeout:              timeout,
		callPollInterval:     defaultCallPollInterval,
		messageRetryInterval: defaultMessageRetryInterval,
		messagePollInterval:  defaultMessagePollInterval,
		references:           make(map[byte]string),
		mmsResults:           make(chan error, 1),
	}, nil
}

// SetDeliveryReportSink installs the encrypted status publisher after device binding.
func (session *Session) SetDeliveryReportSink(sink DeliveryReportSink) {
	session.deliveryReportSink = sink
}

// PrepareMessageStatus associates the next SMS submission with its application message ID.
func (session *Session) PrepareMessageStatus(messageID string) {
	session.messageStatusMu.Lock()
	defer session.messageStatusMu.Unlock()
	session.pendingMessageID = messageID
}

// Calls returns the call controller updated by the session's URC loop.
func (session *Session) Calls() *call.Controller {
	return session.calls
}

// ReadSIMICCID reads the modem-provided card identifier without storing it in the session.
func (session *Session) ReadSIMICCID(ctx context.Context) (string, error) {
	for _, command := range []string{"AT+QCCID", "AT+CCID"} {
		response, err := session.send(ctx, command)
		if err != nil {
			return "", err
		}
		if !response.OK() {
			continue
		}
		for _, line := range response.Lines {
			value := strings.TrimSpace(line)
			if strings.HasPrefix(value, "+QCCID:") {
				value = strings.TrimSpace(strings.TrimPrefix(value, "+QCCID:"))
			} else if strings.HasPrefix(value, "+CCID:") {
				value = strings.TrimSpace(strings.TrimPrefix(value, "+CCID:"))
			}
			value = strings.Trim(value, "\" ")
			if len(value) >= 18 && len(value) <= 22 && allDigits(value) {
				return value, nil
			}
		}
	}
	return "", errors.New("modem did not return a valid SIM ICCID")
}

// ReadSIMProfile reads the provisioned subscriber number and current operator when available.
// Many SIM cards do not provision their own phone number, so either field may be empty.
func (session *Session) ReadSIMProfile(ctx context.Context) (SIMProfile, error) {
	var profile SIMProfile
	var failures []error

	numberResponse, err := session.send(ctx, "AT+CNUM")
	if err != nil {
		failures = append(failures, fmt.Errorf("query subscriber number: %w", err))
	} else if numberResponse.OK() {
		profile.PhoneNumber = parseSubscriberNumber(numberResponse.Lines)
	} else {
		failures = append(failures, fmt.Errorf("modem rejected subscriber number query: %s", numberResponse.Terminal))
	}

	operatorResponse, err := session.send(ctx, "AT+COPS?")
	if err != nil {
		failures = append(failures, fmt.Errorf("query network operator: %w", err))
	} else if operatorResponse.OK() {
		profile.Operator = parseNetworkOperator(operatorResponse.Lines)
	} else {
		failures = append(failures, fmt.Errorf("modem rejected network operator query: %s", operatorResponse.Terminal))
	}

	if profile.PhoneNumber == "" && profile.Operator == "" && len(failures) > 0 {
		return SIMProfile{}, errors.Join(failures...)
	}
	return profile, nil
}

func parseSubscriberNumber(lines []string) string {
	for _, line := range lines {
		fields := parseATCSV(line, "+CNUM:")
		if len(fields) < 2 {
			continue
		}
		number := strings.TrimSpace(fields[1])
		if len(number) >= 3 && len(number) <= 20 && validDialNumber(number) {
			return number
		}
	}
	return ""
}

func parseNetworkOperator(lines []string) string {
	for _, line := range lines {
		fields := parseATCSV(line, "+COPS:")
		if len(fields) < 3 {
			continue
		}
		operator := strings.TrimSpace(fields[2])
		if operator != "" && len(operator) <= 64 {
			return operator
		}
	}
	return ""
}

func parseATCSV(line string, prefix string) []string {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, prefix) {
		return nil
	}
	reader := csv.NewReader(strings.NewReader(strings.TrimSpace(strings.TrimPrefix(line, prefix))))
	reader.TrimLeadingSpace = true
	fields, err := reader.Read()
	if err != nil {
		return nil
	}
	return fields
}

func validDialNumber(value string) bool {
	for index, character := range value {
		if character == '+' && index == 0 {
			continue
		}
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

// ReadSIMSlotStatus reads physical slot presence when the modem exposes QSIMSTAT.
// Unsupported modems return ErrSIMSlotUnsupported instead of guessing a slot.
func (session *Session) ReadSIMSlotStatus(ctx context.Context) ([]SIMSlotStatus, error) {
	response, err := session.send(ctx, "AT+QSIMSTAT?")
	if err != nil {
		return nil, err
	}
	if !response.OK() {
		return nil, ErrSIMSlotUnsupported
	}
	present, ok := parseSIMInsertionStatus(response.Lines)
	if !ok {
		return nil, ErrSIMSlotUnsupported
	}
	return []SIMSlotStatus{{Index: 1, Present: present}}, nil
}

func parseSIMInsertionStatus(lines []string) (bool, bool) {
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "+QSIMSTAT:") {
			continue
		}
		parts := strings.Split(strings.TrimSpace(strings.TrimPrefix(line, "+QSIMSTAT:")), ",")
		if len(parts) != 2 {
			continue
		}
		enabled, enabledErr := strconv.Atoi(strings.TrimSpace(parts[0]))
		inserted, insertedErr := strconv.Atoi(strings.TrimSpace(parts[1]))
		if enabledErr != nil || insertedErr != nil || (enabled != 0 && enabled != 1) || (inserted != 0 && inserted != 1) {
			continue
		}
		return inserted == 1, true
	}
	return false, false
}

func allDigits(value string) bool {
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

// SendSMS encodes and submits every segment of one SMS through the modem.
func (session *Session) SendSMS(ctx context.Context, destination string, text string, reference byte) error {
	messageID := session.takePendingMessageID()
	pdus, err := sms.EncodeSubmit(destination, text, reference)
	if err != nil {
		return fmt.Errorf("encode outbound SMS: %w", err)
	}
	for _, pdu := range pdus {
		response, err := session.sendPrompt(ctx, fmt.Sprintf("AT+CMGS=%d", pdu.TPDULength), []byte(pdu.Hex))
		if err != nil {
			failure := fmt.Errorf("submit SMS segment %d of %d: %w", pdu.Segment, pdu.Total, err)
			session.logger.Warn("SMS submission failed", "segment", pdu.Segment, "total", pdu.Total, "error", failure)
			return failure
		}
		if !response.OK() {
			failure := fmt.Errorf("modem rejected SMS segment %d of %d: %s", pdu.Segment, pdu.Total, response.Terminal)
			session.logger.Warn("SMS submission failed", "segment", pdu.Segment, "total", pdu.Total, "terminal", response.Terminal)
			return failure
		}
		if !hasMessageReference(response.Lines) {
			failure := fmt.Errorf("modem SMS segment %d of %d response is missing +CMGS", pdu.Segment, pdu.Total)
			session.logger.Warn("SMS submission failed", "segment", pdu.Segment, "total", pdu.Total, "error", failure)
			return failure
		}
		if messageID != "" {
			if reference, ok := parseMessageReference(response.Lines); ok {
				session.trackMessageReference(reference, messageID)
			}
		}
	}
	return nil
}

// SendMMS uploads an image to modem RAM and submits it through the configured MMSC.
func (session *Session) SendMMS(
	ctx context.Context,
	destination string,
	text string,
	attachment command.MessageAttachment,
) error {
	messageID := session.takePendingMessageID()
	if !validDialNumber(destination) || attachment.MIMEType != "image/jpeg" || len(attachment.Data) == 0 || len(attachment.Data) > 40_000 {
		return errors.New("invalid MMS destination or attachment")
	}
	for {
		select {
		case <-session.mmsResults:
		default:
			goto drained
		}
	}

drained:
	suffix := strings.ReplaceAll(messageID, "-", "")
	if len(suffix) > 12 {
		suffix = suffix[:12]
	}
	if suffix == "" {
		suffix = "outgoing"
	}
	imagePath := "RAM:sd_" + suffix + ".jpg"
	textPath := "RAM:sd_" + suffix + ".txt"
	paths := []string{imagePath}
	defer func() {
		_, _ = session.send(context.Background(), "AT+QMMSEDIT=0")
		for _, path := range paths {
			_, _ = session.send(context.Background(), fmt.Sprintf("AT+QFDEL=\"%s\"", path))
		}
	}()

	if err := session.requireOK(ctx, "AT+QMMSEDIT=0", "clear MMS editor"); err != nil {
		return err
	}
	if err := session.requireOK(
		ctx,
		fmt.Sprintf("AT+QMMSEDIT=1,1,\"%s\"", destination),
		"add MMS recipient",
	); err != nil {
		return err
	}
	if err := session.uploadMMSFile(ctx, imagePath, attachment.Data); err != nil {
		return err
	}
	if err := session.requireOK(
		ctx,
		fmt.Sprintf("AT+QMMSEDIT=5,1,\"%s\"", imagePath),
		"attach MMS image",
	); err != nil {
		return err
	}
	if body := strings.TrimSpace(text); body != "" {
		paths = append(paths, textPath)
		if err := session.requireOK(ctx, `AT+QMMSCFG="character","UTF8"`, "set MMS text encoding"); err != nil {
			return err
		}
		if err := session.uploadMMSFile(ctx, textPath, []byte(body)); err != nil {
			return err
		}
		if err := session.requireOK(
			ctx,
			fmt.Sprintf("AT+QMMSEDIT=5,1,\"%s\"", textPath),
			"attach MMS text",
		); err != nil {
			return err
		}
	}

	response, err := session.sendWithTimeout(ctx, "AT+QMMSEND=100", 110*time.Second)
	if err != nil {
		return fmt.Errorf("submit MMS: %w", err)
	}
	if !response.OK() {
		return fmt.Errorf("modem rejected MMS submission: %s", response.Terminal)
	}
	select {
	case result := <-session.mmsResults:
		return result
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(110 * time.Second):
		return errors.New("timed out waiting for MMS result")
	}
}

func (session *Session) uploadMMSFile(ctx context.Context, path string, data []byte) error {
	commandText := fmt.Sprintf("AT+QFUPL=\"%s\",%d,300,1", path, len(data))
	commandContext, cancel := context.WithTimeout(ctx, 310*time.Second)
	defer cancel()
	response, err := session.client.SendUpload(commandContext, commandText, data)
	if err != nil {
		return fmt.Errorf("upload MMS file %s: %w", path, err)
	}
	if !response.OK() {
		return fmt.Errorf("modem rejected MMS file %s: %s", path, response.Terminal)
	}
	return nil
}

func (session *Session) requireOK(ctx context.Context, commandText string, operation string) error {
	response, err := session.send(ctx, commandText)
	if err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	if !response.OK() {
		return fmt.Errorf("%s: %s", operation, response.Terminal)
	}
	return nil
}

func (session *Session) takePendingMessageID() string {
	session.messageStatusMu.Lock()
	defer session.messageStatusMu.Unlock()
	messageID := session.pendingMessageID
	session.pendingMessageID = ""
	return messageID
}

func (session *Session) trackMessageReference(reference byte, messageID string) {
	session.messageStatusMu.Lock()
	defer session.messageStatusMu.Unlock()
	session.references[reference] = messageID
}

func (session *Session) messageIDForReference(reference byte) string {
	session.messageStatusMu.Lock()
	defer session.messageStatusMu.Unlock()
	return session.references[reference]
}

func (session *Session) removeMessageReference(reference byte, messageID string) {
	session.messageStatusMu.Lock()
	defer session.messageStatusMu.Unlock()
	if session.references[reference] == messageID {
		delete(session.references, reference)
	}
}

// Initialize configures modem notifications and imports stored messages before readiness.
func (session *Session) Initialize(ctx context.Context) error {
	simStatus, err := session.querySIMStatus(ctx)
	if err != nil {
		return err
	}
	if simStatus != "READY" {
		return fmt.Errorf("modem SIM is not ready: %s", simStatus)
	}

	var response at.Response
	for _, command := range initializationCommands {
		response, err = session.send(ctx, command)
		if err != nil {
			return err
		}
		if !response.OK() {
			return fmt.Errorf("modem rejected initialization command %s: %s", command, response.Terminal)
		}
	}
	if err := session.ensureAutomaticOperatorSelection(ctx); err != nil {
		return err
	}

	if err := session.importStoredSMS(ctx); err != nil {
		return fmt.Errorf("import SMS history: %w", err)
	}
	return session.RetryPendingSMS(ctx)
}

func (session *Session) importStoredSMS(ctx context.Context) error {
	response, err := session.send(ctx, "AT+CMGL=4")
	if err != nil {
		return err
	}
	if !response.OK() {
		return fmt.Errorf("modem rejected stored SMS scan: %s", response.Terminal)
	}
	pdus, extractionError := historyPDUs(response.Lines)
	messages, importError := session.inbox.ImportPDUs(pdus)
	if len(pdus) > 0 || len(messages) > 0 {
		session.logger.Info(
			"stored SMS scan completed",
			"stored_entries", len(pdus),
			"new_complete_messages", len(messages),
		)
	}
	if extractionError != nil || importError != nil {
		session.logger.Warn("some stored SMS entries were skipped", "error", errors.Join(extractionError, importError))
	}
	return nil
}

func (session *Session) ensureAutomaticOperatorSelection(ctx context.Context) error {
	response, err := session.send(ctx, operatorSelectionQuery)
	if err != nil {
		return fmt.Errorf("query operator selection: %w", err)
	}
	if !response.OK() {
		return fmt.Errorf("modem rejected operator selection query: %s", response.Terminal)
	}
	mode, accessTechnology, found := parseOperatorSelection(response.Lines)
	if !found {
		session.logger.Warn("operator selection response is missing +COPS details")
		return nil
	}
	session.logger.Info(
		"operator selection inspected",
		"operator_selection_mode", mode,
		"operator_access_technology", accessTechnology,
	)
	if mode == 0 || mode == 4 {
		return nil
	}

	response, err = session.sendWithTimeout(ctx, "AT+COPS=0", automaticOperatorSelectionTimeout)
	if err != nil {
		return fmt.Errorf("request automatic operator selection: %w", err)
	}
	if !response.OK() {
		return fmt.Errorf("modem rejected automatic operator selection: %s", response.Terminal)
	}
	session.logger.Info("automatic operator selection requested", "previous_operator_selection_mode", mode)
	return nil
}

func parseOperatorSelection(lines []string) (int, int, bool) {
	for _, line := range lines {
		fields := parseATCSV(line, "+COPS:")
		if len(fields) == 0 {
			continue
		}
		mode, err := strconv.Atoi(strings.TrimSpace(fields[0]))
		if err != nil || mode < 0 || mode > 4 {
			return 0, -1, false
		}
		accessTechnology := -1
		if len(fields) >= 4 {
			value, parseError := strconv.Atoi(strings.TrimSpace(fields[3]))
			if parseError == nil && value >= 0 {
				accessTechnology = value
			}
		}
		return mode, accessTechnology, true
	}
	return 0, -1, false
}

func (session *Session) querySIMStatus(ctx context.Context) (string, error) {
	for attempt := 0; attempt < simReadinessAttempts; attempt++ {
		response, err := session.send(ctx, simReadinessCommand)
		if err != nil {
			return "", fmt.Errorf("query SIM readiness: %w", err)
		}
		if !response.OK() {
			return "", fmt.Errorf("modem rejected SIM readiness query: %s", response.Terminal)
		}
		if simStatus, ok := parseSIMStatus(response.Lines); ok {
			return simStatus, nil
		}
		if attempt+1 == simReadinessAttempts {
			break
		}
		timer := time.NewTimer(simReadinessRetryDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", ctx.Err()
		case <-timer.C:
		}
	}
	return "", errors.New("modem SIM readiness response is missing +CPIN")
}

// RetryPendingSMS delivers every durable incoming message not yet accepted downstream.
func (session *Session) RetryPendingSMS(ctx context.Context) error {
	for _, message := range session.inbox.PendingMessages() {
		if err := session.sink.ReceiveSMS(ctx, message); err != nil {
			if errors.Is(err, ErrMessageDeliveryDeferred) {
				session.logger.Warn("SMS delivery deferred", "message_id", message.ID)
				continue
			}
			return fmt.Errorf("deliver imported SMS: %w", err)
		}
		if err := session.inbox.Acknowledge(message.ID); err != nil {
			return fmt.Errorf("acknowledge imported SMS: %w", err)
		}
	}
	return nil
}

func parseSIMStatus(lines []string) (string, bool) {
	for _, line := range lines {
		if !strings.HasPrefix(line, "+CPIN:") {
			continue
		}
		status := strings.ToUpper(strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "+CPIN:")), "\""))
		if status == "" {
			return "", false
		}
		return status, true
	}
	return "", false
}

// Run routes modem notifications until cancellation or a terminal serial error.
func (session *Session) Run(ctx context.Context) error {
	callPoll := time.NewTicker(session.callPollInterval)
	defer callPoll.Stop()
	messageRetry := time.NewTicker(session.messageRetryInterval)
	defer messageRetry.Stop()
	messagePoll := time.NewTicker(session.messagePollInterval)
	defer messagePoll.Stop()
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
				session.logger.Warn("call state refresh deferred", "error", err)
			}
		case <-messageRetry.C:
			if err := session.RetryPendingSMS(ctx); err != nil {
				return err
			}
		case <-messagePoll.C:
			if err := session.importStoredSMS(ctx); err != nil {
				session.logger.Warn("stored SMS scan deferred", "error", err)
				continue
			}
			if err := session.RetryPendingSMS(ctx); err != nil {
				return err
			}
		}
	}
}

func (session *Session) refreshCallState(ctx context.Context) error {
	previous := session.calls.Snapshot()
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
	current := session.calls.Snapshot()
	if current != previous {
		session.logger.Info(
			"cellular call state changed",
			"source", "poll",
			"state", current.State,
			"active", current.Active,
			"held", current.Held,
			"waiting", current.Waiting,
			"incoming_number_available", current.IncomingNumber != "",
		)
	}
	return nil
}

func (session *Session) send(ctx context.Context, command string) (at.Response, error) {
	return session.sendWithTimeout(ctx, command, session.timeout)
}

func (session *Session) sendWithTimeout(ctx context.Context, command string, timeout time.Duration) (at.Response, error) {
	commandContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return session.client.Send(commandContext, command)
}

func (session *Session) sendPrompt(ctx context.Context, command string, payload []byte) (at.Response, error) {
	commandContext, cancel := context.WithTimeout(ctx, session.timeout)
	defer cancel()
	return session.client.SendPrompt(commandContext, command, payload)
}

func hasMessageReference(lines []string) bool {
	for _, line := range lines {
		if strings.HasPrefix(line, "+CMGS:") && strings.TrimSpace(strings.TrimPrefix(line, "+CMGS:")) != "" {
			return true
		}
	}
	return false
}

func parseMessageReference(lines []string) (byte, bool) {
	for _, line := range lines {
		if !strings.HasPrefix(line, "+CMGS:") {
			continue
		}
		value, err := strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(line, "+CMGS:")), 10, 8)
		if err == nil {
			return byte(value), true
		}
	}
	return 0, false
}

func (session *Session) handleURC(ctx context.Context, urc string) error {
	switch {
	case strings.HasPrefix(urc, "+QMMSEND:"):
		result := parseMMSResult(urc)
		select {
		case session.mmsResults <- result:
		default:
			session.logger.Warn("discarded MMS result without a pending submission", "result", urc)
		}
		return nil
	case strings.HasPrefix(urc, "+CMT:"):
		session.logger.Info("direct SMS notification received")
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
			session.logger.Info("direct SMS segment deduplicated")
			return nil
		}
		if err := session.sink.ReceiveSMS(ctx, received); err != nil {
			if errors.Is(err, ErrMessageDeliveryDeferred) {
				session.logger.Warn("SMS delivery deferred", "message_id", received.ID)
				return nil
			}
			return fmt.Errorf("deliver direct SMS: %w", err)
		}
		if err := session.inbox.Acknowledge(received.ID); err != nil {
			return fmt.Errorf("acknowledge direct SMS: %w", err)
		}
		session.logger.Info("incoming SMS delivered securely", "source", "direct")
	case strings.HasPrefix(urc, "+CMTI:"):
		// Some EC20 firmware stores the message and emits +CMTI even when
		// direct delivery was requested. Read the stored PDU before deleting it
		// so the same durable inbox path handles both notification modes.
		index, ok := storedMessageIndex(urc)
		if !ok {
			session.logger.Warn("discarded malformed stored SMS notification", "urc", urc)
			return nil
		}
		session.logger.Info("stored SMS notification received", "index", index)
		response, err := session.send(ctx, fmt.Sprintf("AT+CMGR=%d", index))
		if err != nil {
			return fmt.Errorf("read stored SMS %d: %w", index, err)
		}
		if !response.OK() {
			return fmt.Errorf("modem rejected stored SMS %d: %s", index, response.Terminal)
		}
		encoded, ok := storedMessagePDU(response.Lines)
		if !ok {
			session.logger.Warn("discarded stored SMS without a PDU", "index", index)
			return nil
		}
		message, err := sms.DecodeDeliver(encoded)
		if err != nil {
			session.logger.Warn("discarded malformed stored SMS PDU", "index", index, "error", err)
			return nil
		}
		received, created, err := session.inbox.AddSegment(message)
		if err != nil {
			return fmt.Errorf("store stored SMS %d: %w", index, err)
		}
		// AddSegment durably persists a complete message before returning. It is
		// therefore safe to remove the modem copy even when server delivery is
		// deferred; RetryPendingSMS will publish the encrypted pending record.
		shouldDelete := created
		if created {
			deliveryError := session.sink.ReceiveSMS(ctx, received)
			if deliveryError != nil && !errors.Is(deliveryError, ErrMessageDeliveryDeferred) {
				return fmt.Errorf("deliver stored SMS: %w", deliveryError)
			}
			if deliveryError == nil {
				if err := session.inbox.Acknowledge(received.ID); err != nil && !errors.Is(err, sms.ErrUnknownMessage) {
					return fmt.Errorf("acknowledge stored SMS: %w", err)
				}
			}
		}
		if !shouldDelete {
			session.logger.Info("stored SMS segment retained", "index", index, "complete", false)
			return nil
		}
		if response, err := session.send(ctx, fmt.Sprintf("AT+CMGD=%d", index)); err != nil || !response.OK() {
			if err != nil {
				session.logger.Warn("modem rejected stored SMS deletion", "index", index, "error", err)
			} else {
				session.logger.Warn("modem rejected stored SMS deletion", "index", index, "terminal", response.Terminal)
			}
		} else {
			session.logger.Info("stored SMS removed after durable import", "index", index)
		}
	case strings.HasPrefix(urc, "+CDS:"):
		_, encoded, ok := strings.Cut(urc, "\n")
		if !ok {
			session.logger.Warn("discarded malformed SMS delivery status report")
			return nil
		}
		report, err := sms.DecodeStatusReport(encoded)
		if err != nil {
			session.logger.Warn("discarded malformed SMS delivery status report", "error", err)
			return nil
		}
		if messageID := session.messageIDForReference(report.MessageReference); messageID != "" {
			if session.deliveryReportSink != nil {
				report.MessageID = messageID
				if err := session.deliveryReportSink(ctx, report); err != nil {
					return fmt.Errorf("publish SMS delivery report: %w", err)
				}
			}
			session.removeMessageReference(report.MessageReference, messageID)
		}
	case isCallURC(urc):
		session.calls.ApplyURC(urc)
		snapshot := session.calls.Snapshot()
		session.logger.Info("cellular call state changed", "state", snapshot.State, "active", snapshot.Active, "held", snapshot.Held, "waiting", snapshot.Waiting)
	default:
		session.logger.Debug("unhandled modem notification", "type", notificationType(urc))
	}
	return nil
}

func parseMMSResult(urc string) error {
	fields := strings.Split(strings.TrimSpace(strings.TrimPrefix(urc, "+QMMSEND:")), ",")
	if len(fields) < 2 || strings.TrimSpace(fields[0]) != "0" {
		return fmt.Errorf("MMS submission failed: %s", strings.TrimSpace(urc))
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

func storedMessageIndex(urc string) (int, bool) {
	_, payload, ok := strings.Cut(strings.TrimSpace(urc), ":")
	if !ok {
		return 0, false
	}
	parts := strings.Split(payload, ",")
	if len(parts) != 2 {
		return 0, false
	}
	index, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	return index, err == nil && index >= 0
}

func storedMessagePDU(lines []string) (string, bool) {
	for index, line := range lines {
		if !strings.HasPrefix(line, "+CMGR:") || index+1 >= len(lines) {
			continue
		}
		pdu := strings.TrimSpace(lines[index+1])
		if pdu != "" && !strings.HasPrefix(pdu, "+") {
			return pdu, true
		}
	}
	return "", false
}

func isCallURC(urc string) bool {
	return urc == "RING" || strings.HasPrefix(urc, "+CLIP:") || strings.HasPrefix(urc, "+CCWA:") || urc == "NO CARRIER" || urc == "NO ANSWER" || urc == "BUSY"
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
