package modem

import (
	"context"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
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
var ErrEUICCUnsupported = errors.New("modem does not expose an eUICC EID")

const (
	esimTransactionChunkSize = 1400
	esimTransactionTimeout   = 10 * time.Second
	esimHTTPResponseLimit    = 2 << 20
)

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

// ModemIdentity contains the manufacturer strings exposed by the modem.
// The model is used for capability diagnostics only; it is never trusted as a
// SIM or device identity.
type ModemIdentity struct {
	Manufacturer string
	Model        string
	Revision     string
}

// ESIMProfile describes one profile returned by Quectel's local QESIM LPA.
type ESIMProfile struct {
	ICCID    string
	Enabled  bool
	Nickname string
	Provider string
}

// ReadEUICCIdentifier performs a read-only generic SIM APDU EID probe.
// Profile download, enable, disable, and delete require a separate validated
// LPA flow and are deliberately not inferred from a modem model string.
func (session *Session) ReadEUICCIdentifier(ctx context.Context) (string, error) {
	if session == nil || session.client == nil {
		return "", errors.New("modem session is required")
	}
	for _, apdu := range []string{
		"00A40400023F00", // Select MF.
		"00A40400022FE2", // Select EF.EID.
		"00B0000010",     // Read 16-byte EID.
	} {
		response, err := session.send(ctx, fmt.Sprintf(`AT+CSIM=%d,"%s"`, len(apdu), apdu))
		if err != nil {
			return "", err
		}
		if !response.OK() {
			return "", ErrEUICCUnsupported
		}
		data, status, ok := parseCSIMResponse(response.Lines)
		if !ok || status != "9000" {
			return "", ErrEUICCUnsupported
		}
		if apdu == "00B0000010" {
			if len(data) != 32 || !allHexDigits(data) {
				return "", ErrEUICCUnsupported
			}
			return data, nil
		}
	}
	return "", ErrEUICCUnsupported
}

// ReadEUICCIdentifierWithQESIM prefers Quectel's documented QESIM command and
// falls back to the generic APDU path for older firmware.
func (session *Session) ReadEUICCIdentifierWithQESIM(ctx context.Context) (string, error) {
	if session == nil || session.client == nil {
		return "", errors.New("modem session is required")
	}
	if eid, err := session.readQESIMEID(ctx); err == nil {
		return eid, nil
	}
	return session.ReadEUICCIdentifier(ctx)
}

func (session *Session) readQESIMEID(ctx context.Context) (string, error) {
	response, err := session.send(ctx, `AT+QESIM="eid"`)
	if err != nil {
		return "", err
	}
	if !response.OK() {
		return "", ErrEUICCUnsupported
	}
	for _, rawLine := range response.Lines {
		line := strings.TrimSpace(rawLine)
		if !strings.HasPrefix(line, "+QESIM:") {
			continue
		}
		fields := parseATCSV(line, `+QESIM:`)
		if len(fields) != 3 || !strings.EqualFold(strings.TrimSpace(fields[0]), "eid") || strings.TrimSpace(fields[1]) != "0" {
			continue
		}
		eid := strings.Trim(strings.TrimSpace(fields[2]), `"`)
		if len(eid) == 32 && allHexDigits(eid) {
			return strings.ToUpper(eid), nil
		}
	}
	return "", ErrEUICCUnsupported
}

// ListESIMProfiles returns the installed profiles without changing modem state.
func (session *Session) ListESIMProfiles(ctx context.Context) ([]ESIMProfile, error) {
	response, err := session.send(ctx, `AT+QESIM="list"`)
	if err != nil {
		return nil, err
	}
	if !response.OK() {
		return nil, fmt.Errorf("QESIM list rejected: %s", response.Terminal)
	}
	profiles, result, ok := parseQESIMList(response.Lines)
	if !ok {
		return nil, ErrEUICCUnsupported
	}
	if result != 0 {
		return nil, fmt.Errorf("QESIM list failed with result %d", result)
	}
	return profiles, nil
}

// EnableESIMProfile enables one installed profile by ICCID.
func (session *Session) EnableESIMProfile(ctx context.Context, iccid string) error {
	return session.mutateESIMProfile(ctx, "enable", iccid)
}

// DisableESIMProfile disables one installed profile by ICCID.
func (session *Session) DisableESIMProfile(ctx context.Context, iccid string) error {
	return session.mutateESIMProfile(ctx, "disable", iccid)
}

// DeleteESIMProfile removes one installed profile by ICCID.
func (session *Session) DeleteESIMProfile(ctx context.Context, iccid string) error {
	return session.mutateESIMProfile(ctx, "delete", iccid)
}

// RenameESIMProfile changes the local nickname of one installed profile.
func (session *Session) RenameESIMProfile(ctx context.Context, iccid, nickname string) error {
	if !validESIMICCID(iccid) || len([]rune(nickname)) > 32 {
		return errors.New("invalid eSIM profile nickname or ICCID")
	}
	response, err := session.send(ctx, fmt.Sprintf(`AT+QESIM="nickname","%s","%s"`, escapeATString(nickname), iccid))
	if err != nil {
		return err
	}
	return parseQESIMMutation(response, "nickname")
}

// DownloadESIMProfile provisions a profile through Quectel's external-network
// QESIM flow. The modem owns all eUICC state; this method only forwards the
// modem's GSMA RSP messages to the advertised SMDP+ endpoint over HTTPS.
func (session *Session) DownloadESIMProfile(ctx context.Context, activationCode string) error {
	activationCode = strings.TrimSpace(activationCode)
	if !strings.HasPrefix(activationCode, "LPA:1$") || strings.ContainsAny(activationCode, "\r\n\"") || len(activationCode) > 512 {
		return errors.New("invalid eSIM activation code")
	}
	response, err := session.sendWithTimeout(ctx, fmt.Sprintf(`AT+QESIM="download","%s"`, escapeATString(activationCode)), esimTransactionTimeout)
	if err != nil {
		return fmt.Errorf("start eSIM download: %w", err)
	}
	if !response.OK() {
		return fmt.Errorf("QESIM download rejected: %s", response.Terminal)
	}
	transaction, result, err := parseQESIMTransaction(response.Lines)
	if err != nil {
		return err
	}
	if result != 0 {
		return fmt.Errorf("QESIM download failed with result %d", result)
	}
	for {
		httpResponse, err := postESIMTransaction(ctx, transaction.URL, transaction.Payload)
		if err != nil {
			return fmt.Errorf("post eSIM RSP message: %w", err)
		}
		next, downloadResult, err := session.forwardESIMHTTPResponse(ctx, httpResponse)
		if err != nil {
			return err
		}
		if downloadResult != nil {
			if *downloadResult != 0 {
				return fmt.Errorf("QESIM download failed with result %d", *downloadResult)
			}
			return nil
		}
		if next == nil {
			return errors.New("QESIM modem did not request the next RSP message")
		}
		transaction = next
	}
}

type esimTransaction struct {
	URL     string
	Payload string
}

// parseQESIMTransaction deliberately does not use CSV: EC20 firmware places
// unescaped JSON quotes inside the final quoted payload field.
func parseQESIMTransaction(lines []string) (*esimTransaction, int, error) {
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if !strings.HasPrefix(line, "+QESIM:") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, "+QESIM:"))
		if !strings.HasPrefix(value, `"trans"`) {
			continue
		}
		value = strings.TrimSpace(strings.TrimPrefix(value, `"trans"`))
		if !strings.HasPrefix(value, ",") {
			return nil, 0, errors.New("malformed QESIM transaction result")
		}
		value = strings.TrimSpace(strings.TrimPrefix(value, ","))
		comma := strings.IndexByte(value, ',')
		if comma < 0 {
			result, parseErr := strconv.Atoi(value)
			if parseErr != nil {
				return nil, 0, errors.New("malformed QESIM transaction result")
			}
			return nil, result, nil
		}
		result, parseErr := strconv.Atoi(strings.TrimSpace(value[:comma]))
		if parseErr != nil {
			return nil, 0, errors.New("malformed QESIM transaction result")
		}
		if result != 0 {
			return nil, result, nil
		}
		value = strings.TrimSpace(value[comma+1:])
		if len(value) < 2 || value[0] != '"' {
			return nil, 0, errors.New("malformed QESIM transaction URL")
		}
		urlEnd := strings.IndexByte(value[1:], '"')
		if urlEnd < 0 {
			return nil, 0, errors.New("malformed QESIM transaction URL")
		}
		endpoint := strings.TrimSpace(value[1 : urlEnd+1])
		value = strings.TrimSpace(value[urlEnd+2:])
		if !strings.HasPrefix(value, ",") {
			return nil, 0, errors.New("malformed QESIM transaction payload length")
		}
		value = strings.TrimSpace(value[1:])
		comma = strings.IndexByte(value, ',')
		if comma < 0 {
			return nil, 0, errors.New("malformed QESIM transaction payload")
		}
		payloadLength, parseErr := strconv.Atoi(strings.TrimSpace(value[:comma]))
		if parseErr != nil || payloadLength < 0 {
			return nil, 0, errors.New("malformed QESIM transaction payload length")
		}
		payload := strings.TrimSpace(value[comma+1:])
		if len(payload) < 2 || payload[0] != '"' || payload[len(payload)-1] != '"' {
			return nil, 0, errors.New("malformed QESIM transaction payload")
		}
		payload = payload[1 : len(payload)-1]
		if len(payload) != payloadLength {
			return nil, 0, fmt.Errorf("QESIM transaction payload length mismatch: got %d, want %d", len(payload), payloadLength)
		}
		return &esimTransaction{URL: endpoint, Payload: payload}, 0, nil
	}
	return nil, 0, errors.New("QESIM download did not return a transaction")
}

func postESIMTransaction(ctx context.Context, endpoint, payload string) ([]byte, error) {
	if strings.HasPrefix(endpoint, "/") {
		var requestPayload struct {
			SMDPAddress string `json:"smdpAddress"`
		}
		if err := json.Unmarshal([]byte(payload), &requestPayload); err != nil || strings.TrimSpace(requestPayload.SMDPAddress) == "" {
			return nil, errors.New("QESIM transaction endpoint is missing its SMDP+ host")
		}
		endpoint = "https://" + strings.TrimSpace(requestPayload.SMDPAddress) + endpoint
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Path == "" {
		return nil, errors.New("QESIM transaction endpoint must be an HTTPS URL")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, parsed.String(), strings.NewReader(payload))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	client := &http.Client{}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.ContentLength > esimHTTPResponseLimit {
		return nil, errors.New("QESIM SMDP+ response is too large")
	}
	dump, err := httputil.DumpResponse(response, true)
	if err != nil {
		return nil, err
	}
	if len(dump) > esimHTTPResponseLimit {
		return nil, errors.New("QESIM SMDP+ response is too large")
	}
	return dump, nil
}

func (session *Session) forwardESIMHTTPResponse(ctx context.Context, response []byte) (*esimTransaction, *int, error) {
	hexResponse := strings.ToUpper(hex.EncodeToString(response))
	if len(hexResponse) == 0 || len(hexResponse) > 65535 {
		return nil, nil, errors.New("QESIM SMDP+ response cannot be forwarded")
	}
	for offset, sequence := 0, 0; offset < len(hexResponse); sequence++ {
		end := offset + esimTransactionChunkSize
		if end > len(hexResponse) {
			end = len(hexResponse)
		}
		chunk := hexResponse[offset:end]
		state := 1
		if end == len(hexResponse) {
			state = 0
		}
		command := fmt.Sprintf(`AT+QESIM="trans",%d,%d,%d,%d,"%s"`, len(hexResponse), state, sequence, len(chunk), chunk)
		atResponse, err := session.sendWithTimeout(ctx, command, esimTransactionTimeout)
		if err != nil {
			return nil, nil, fmt.Errorf("forward QESIM transaction block %d: %w", sequence, err)
		}
		if !atResponse.OK() {
			return nil, nil, fmt.Errorf("QESIM transaction block %d rejected: %s", sequence, atResponse.Terminal)
		}
		if end == len(hexResponse) {
			next, result, found, parseErr := session.readQESIMResult(ctx, atResponse.Lines)
			if parseErr != nil {
				return nil, nil, parseErr
			}
			if found {
				if result != nil {
					return nil, result, nil
				}
				return next, nil, nil
			}
		}
		offset = end
	}
	return nil, nil, errors.New("QESIM transaction completed without a modem result")
}

func (session *Session) readQESIMResult(ctx context.Context, lines []string) (*esimTransaction, *int, bool, error) {
	if next, result, found, err := parseQESIMResultLines(lines); found || err != nil {
		return next, result, found, err
	}
	// The macOS USB bridge reads the bulk IN endpoint only while servicing a
	// command. Probe once so a QESIM URC queued immediately after the final
	// block is flushed through that bridge as part of the probe response.
	if session.client == nil {
		return nil, nil, false, errors.New("modem session is required")
	}
	probe, probeErr := session.sendWithTimeout(ctx, "AT", esimTransactionTimeout)
	if probeErr == nil {
		if next, result, found, err := parseQESIMResultLines(probe.Lines); found || err != nil {
			return next, result, found, err
		}
	}
	timer := time.NewTimer(esimTransactionTimeout)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, nil, false, ctx.Err()
		case <-timer.C:
			return nil, nil, false, errors.New("timed out waiting for QESIM transaction result")
		case urc := <-session.client.URCs():
			if !strings.HasPrefix(strings.TrimSpace(urc), "+QESIM:") {
				continue
			}
			return parseQESIMResultLines([]string{urc})
		}
	}
}

func parseQESIMResultLines(lines []string) (*esimTransaction, *int, bool, error) {
	if next, result, err := parseQESIMTransaction(lines); err == nil {
		if result != 0 {
			return nil, &result, true, nil
		}
		return next, nil, true, nil
	} else if result != 0 {
		return nil, &result, true, nil
	}
	for _, line := range lines {
		if !strings.HasPrefix(strings.TrimSpace(line), "+QESIM: \"download\", ") {
			continue
		}
		fields := parseATCSV(line, "+QESIM:")
		if len(fields) != 2 {
			return nil, nil, false, errors.New("malformed QESIM download result")
		}
		result, parseErr := strconv.Atoi(strings.TrimSpace(fields[1]))
		if parseErr != nil {
			return nil, nil, false, errors.New("malformed QESIM download result")
		}
		return nil, &result, true, nil
	}
	return nil, nil, false, nil
}

func (session *Session) mutateESIMProfile(ctx context.Context, operation, iccid string) error {
	if !validESIMICCID(iccid) {
		return errors.New("invalid eSIM profile ICCID")
	}
	response, err := session.send(ctx, fmt.Sprintf(`AT+QESIM="%s","%s"`, operation, iccid))
	if err != nil {
		return err
	}
	return parseQESIMMutation(response, operation)
}

func parseQESIMMutation(response at.Response, operation string) error {
	if !response.OK() {
		return fmt.Errorf("QESIM %s rejected: %s", operation, response.Terminal)
	}
	for _, line := range response.Lines {
		fields := parseATCSV(line, "+QESIM:")
		if len(fields) != 2 || !strings.EqualFold(strings.TrimSpace(fields[0]), operation) {
			continue
		}
		result, err := strconv.Atoi(strings.TrimSpace(fields[1]))
		if err != nil || result != 0 {
			return fmt.Errorf("QESIM %s failed with result %s", operation, strings.TrimSpace(fields[1]))
		}
		return nil
	}
	return ErrEUICCUnsupported
}

func parseQESIMList(lines []string) ([]ESIMProfile, int, bool) {
	var profiles []ESIMProfile
	result := 0
	foundHeader := false
	for _, line := range lines {
		fields := parseATCSV(line, "+QESIM:")
		// EC20CE firmware returns a bare "+QESIM:" line when the eUICC is
		// present but has no installed profiles. Treat that as a valid empty
		// list instead of reporting that eSIM management is unsupported.
		if strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "+QESIM:")) == "" {
			foundHeader = true
			continue
		}
		if len(fields) >= 2 && strings.EqualFold(strings.TrimSpace(fields[0]), "list") {
			parsed, err := strconv.Atoi(strings.TrimSpace(fields[1]))
			if err != nil {
				return nil, 0, false
			}
			result = parsed
			foundHeader = true
			continue
		}
		if !foundHeader {
			continue
		}
		fields = parseATCSV(line, "")
		if len(fields) != 4 || !validESIMICCID(fields[0]) {
			continue
		}
		status, err := strconv.Atoi(strings.TrimSpace(fields[1]))
		if err != nil || (status != 0 && status != 1) {
			return nil, 0, false
		}
		profiles = append(profiles, ESIMProfile{ICCID: fields[0], Enabled: status == 1, Nickname: fields[2], Provider: fields[3]})
	}
	return profiles, result, foundHeader
}

func validESIMICCID(value string) bool {
	if len(value) < 18 || len(value) > 22 {
		return false
	}
	for _, character := range value {
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f') || (character >= 'A' && character <= 'F')) {
			return false
		}
	}
	return true
}

func escapeATString(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `"`, `\"`)
}

func parseCSIMResponse(lines []string) (data string, status string, ok bool) {
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if !strings.HasPrefix(line, "+CSIM:") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, "+CSIM:"))
		_, payload, found := strings.Cut(value, ",")
		if !found {
			continue
		}
		payload = strings.Trim(strings.TrimSpace(payload), `"`)
		if len(payload) < 4 {
			continue
		}
		return strings.ToUpper(payload[:len(payload)-4]), strings.ToUpper(payload[len(payload)-4:]), true
	}
	return "", "", false
}

func allHexDigits(value string) bool {
	for _, character := range value {
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f') || (character >= 'A' && character <= 'F')) {
			return false
		}
	}
	return true
}

// ReadModemIdentity reads the standard, read-only modem identification
// commands. Quectel EC20CE exposes the model through ATI/CGMM even when its
// USB descriptors are identical to other EC20 variants.
func ReadModemIdentity(ctx context.Context, client *at.Client, timeout time.Duration) (ModemIdentity, error) {
	if client == nil || timeout <= 0 {
		return ModemIdentity{}, errors.New("AT client and positive timeout are required")
	}
	identity := ModemIdentity{}
	var failures []error
	for _, command := range []string{"ATI", "AT+CGMM", "AT+CGMR"} {
		commandContext, cancel := context.WithTimeout(ctx, timeout)
		response, err := client.Send(commandContext, command)
		cancel()
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", command, err))
			continue
		}
		if !response.OK() {
			failures = append(failures, fmt.Errorf("%s: modem rejected command: %s", command, response.Terminal))
			continue
		}
		identity = mergeModemIdentity(identity, command, response.Lines)
	}
	if identity.Model == "" && identity.Manufacturer == "" && identity.Revision == "" {
		return ModemIdentity{}, errors.Join(failures...)
	}
	return identity, nil
}

func mergeModemIdentity(identity ModemIdentity, command string, lines []string) ModemIdentity {
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "+") || strings.EqualFold(line, "OK") {
			continue
		}
		switch command {
		case "AT+CGMM":
			if identity.Model == "" {
				identity.Model = line
			}
		case "AT+CGMR":
			if identity.Revision == "" {
				identity.Revision = line
			}
		case "ATI":
			if strings.Contains(strings.ToLower(line), "quectel") && identity.Manufacturer == "" {
				identity.Manufacturer = line
				continue
			}
			if identity.Model == "" && (strings.Contains(strings.ToLower(line), "ec20") || strings.Contains(strings.ToLower(line), "ec25")) {
				identity.Model = line
			}
			if identity.Revision == "" && (strings.Contains(strings.ToLower(line), "revision") || strings.Contains(strings.ToLower(line), "rev")) {
				identity.Revision = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(line, "Revision:"), "revision:"))
			}
		}
	}
	return identity
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

// ClearOrphanedCalls ends carrier calls that survived a previous agent process.
// A new process cannot safely recover their encrypted media or application lease.
func (session *Session) ClearOrphanedCalls(ctx context.Context) error {
	response, err := session.send(ctx, "AT+CLCC")
	if err != nil {
		return fmt.Errorf("query orphaned modem calls: %w", err)
	}
	if !response.OK() {
		return fmt.Errorf("modem rejected orphaned call query: %s", response.Terminal)
	}
	if err := session.calls.ApplyCLCC(response.Lines); err != nil {
		return fmt.Errorf("parse orphaned modem calls: %w", err)
	}
	snapshot := session.calls.Snapshot()
	if snapshot.State == call.StateIdle || snapshot.State == call.StateEnded {
		return nil
	}
	session.logger.Warn(
		"ending orphaned cellular call during startup",
		"state", snapshot.State,
		"active", snapshot.Active,
		"held", snapshot.Held,
		"waiting", snapshot.Waiting,
	)
	if err := session.calls.Hangup(ctx); err != nil {
		return fmt.Errorf("hang up orphaned modem call: %w", err)
	}
	return nil
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
