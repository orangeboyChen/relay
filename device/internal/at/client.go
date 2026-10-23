package at

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
)

var (
	ErrClosed      = errors.New("AT client is closed")
	ErrURCOverflow = errors.New("AT unsolicited result queue is full")
)

// Response contains the non-URC lines and terminal result of one AT command.
type Response struct {
	Lines    []string
	Terminal string
}

// OK reports whether the modem returned a successful terminal result.
func (response Response) OK() bool {
	return response.Terminal == "OK"
}

type commandResult struct {
	response Response
	err      error
}

type pendingCommand struct {
	lines          []string
	result         chan commandResult
	prompt         chan struct{}
	expectsPrompt  bool
	expectsConnect bool
	promptSeen     bool
}

// Client serializes AT commands while delivering unsolicited result codes separately.
type Client struct {
	transport io.ReadWriteCloser
	commands  sync.Mutex
	state     sync.Mutex
	pending   *pendingCommand
	urcs      chan string
	errors    chan error
	idle      chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

// NewClient starts reading responses from an already configured serial transport.
func NewClient(transport io.ReadWriteCloser) *Client {
	client := &Client{
		transport: transport,
		urcs:      make(chan string, 128),
		errors:    make(chan error, 1),
		idle:      make(chan struct{}, 1),
		done:      make(chan struct{}),
	}
	client.idle <- struct{}{}
	go client.readLoop()
	return client
}

// URCs returns unsolicited modem notifications such as incoming calls and messages.
func (client *Client) URCs() <-chan string {
	return client.urcs
}

// Errors reports the terminal serial read failure that made the client unusable.
func (client *Client) Errors() <-chan error {
	return client.errors
}

// Send writes one command and waits for its terminal result.
func (client *Client) Send(ctx context.Context, command string) (Response, error) {
	if strings.TrimSpace(command) == "" || strings.ContainsAny(command, "\r\n") {
		return Response{}, errors.New("AT command must be one non-empty line")
	}
	client.commands.Lock()
	defer client.commands.Unlock()

	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	select {
	case <-client.done:
		return Response{}, ErrClosed
	default:
	}
	select {
	case <-ctx.Done():
		return Response{}, ctx.Err()
	case <-client.done:
		return Response{}, ErrClosed
	case <-client.idle:
	}

	pending := &pendingCommand{result: make(chan commandResult, 1)}
	client.state.Lock()
	client.pending = pending
	client.state.Unlock()

	if _, err := io.WriteString(client.transport, command+"\r"); err != nil {
		client.clearPending(pending)
		return Response{}, fmt.Errorf("write AT command: %w", err)
	}

	select {
	case result := <-pending.result:
		return result.response, result.err
	case <-ctx.Done():
		return Response{}, ctx.Err()
	case <-client.done:
		select {
		case result := <-pending.result:
			return result.response, result.err
		default:
		}
		return Response{}, ErrClosed
	}
}

// SendPrompt writes a command, waits for the modem prompt, then writes the payload and Ctrl-Z.
func (client *Client) SendPrompt(ctx context.Context, command string, payload []byte) (Response, error) {
	if strings.TrimSpace(command) == "" || strings.ContainsAny(command, "\r\n") {
		return Response{}, errors.New("AT command must be one non-empty line")
	}
	if len(payload) == 0 || bytes.Contains(payload, []byte{0x1a}) {
		return Response{}, errors.New("AT prompt payload must be non-empty and must not contain Ctrl-Z")
	}
	client.commands.Lock()
	defer client.commands.Unlock()

	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	select {
	case <-client.done:
		return Response{}, ErrClosed
	default:
	}
	select {
	case <-ctx.Done():
		return Response{}, ctx.Err()
	case <-client.done:
		return Response{}, ErrClosed
	case <-client.idle:
	}

	pending := &pendingCommand{
		result:        make(chan commandResult, 1),
		prompt:        make(chan struct{}, 1),
		expectsPrompt: true,
	}
	client.state.Lock()
	client.pending = pending
	client.state.Unlock()

	if _, err := io.WriteString(client.transport, command+"\r"); err != nil {
		client.clearPending(pending)
		return Response{}, fmt.Errorf("write AT command: %w", err)
	}

	select {
	case result := <-pending.result:
		return result.response, result.err
	case <-pending.prompt:
	case <-ctx.Done():
		return Response{}, ctx.Err()
	case <-client.done:
		return Response{}, ErrClosed
	}

	framedPayload := make([]byte, 0, len(payload)+1)
	framedPayload = append(framedPayload, payload...)
	framedPayload = append(framedPayload, 0x1a)
	if _, err := client.transport.Write(framedPayload); err != nil {
		client.clearPending(pending)
		return Response{}, fmt.Errorf("write AT prompt payload: %w", err)
	}

	select {
	case result := <-pending.result:
		return result.response, result.err
	case <-ctx.Done():
		return Response{}, ctx.Err()
	case <-client.done:
		select {
		case result := <-pending.result:
			return result.response, result.err
		default:
		}
		return Response{}, ErrClosed
	}
}

// SendUpload writes a fixed-length binary payload after a CONNECT prompt.
func (client *Client) SendUpload(ctx context.Context, command string, payload []byte) (Response, error) {
	if strings.TrimSpace(command) == "" || strings.ContainsAny(command, "\r\n") {
		return Response{}, errors.New("AT command must be one non-empty line")
	}
	if len(payload) == 0 {
		return Response{}, errors.New("AT upload payload must be non-empty")
	}
	client.commands.Lock()
	defer client.commands.Unlock()

	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	select {
	case <-client.done:
		return Response{}, ErrClosed
	default:
	}
	select {
	case <-ctx.Done():
		return Response{}, ctx.Err()
	case <-client.done:
		return Response{}, ErrClosed
	case <-client.idle:
	}

	pending := &pendingCommand{
		result:         make(chan commandResult, 1),
		prompt:         make(chan struct{}, 1),
		expectsPrompt:  true,
		expectsConnect: true,
	}
	client.state.Lock()
	client.pending = pending
	client.state.Unlock()

	if _, err := io.WriteString(client.transport, command+"\r"); err != nil {
		client.clearPending(pending)
		return Response{}, fmt.Errorf("write AT upload command: %w", err)
	}

	select {
	case result := <-pending.result:
		return result.response, result.err
	case <-pending.prompt:
	case <-ctx.Done():
		return Response{}, ctx.Err()
	case <-client.done:
		return Response{}, ErrClosed
	}

	if _, err := client.transport.Write(payload); err != nil {
		client.clearPending(pending)
		return Response{}, fmt.Errorf("write AT upload payload: %w", err)
	}

	select {
	case result := <-pending.result:
		return result.response, result.err
	case <-ctx.Done():
		return Response{}, ctx.Err()
	case <-client.done:
		select {
		case result := <-pending.result:
			return result.response, result.err
		default:
		}
		return Response{}, ErrClosed
	}
}

// Close stops the client and closes its transport.
func (client *Client) Close() error {
	var closeError error
	client.closeOnce.Do(func() {
		close(client.done)
		closeError = client.transport.Close()
	})
	return closeError
}

func (client *Client) readLoop() {
	scanner := bufio.NewScanner(client.transport)
	scanner.Split(splitATResponse)
	pendingDirectURC := ""
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if pendingDirectURC != "" {
			if err := client.deliverURC(pendingDirectURC + "\n" + line); err != nil {
				client.terminate(err)
				return
			}
			pendingDirectURC = ""
			continue
		}
		if isDirectDeliveryHeader(line) {
			pendingDirectURC = line
			continue
		}
		if err := client.routeLine(line); err != nil {
			client.terminate(err)
			return
		}
	}
	if pendingDirectURC != "" {
		client.terminate(fmt.Errorf("incomplete direct-delivery URC: %w", io.ErrUnexpectedEOF))
		return
	}
	if err := scanner.Err(); err != nil {
		client.terminate(fmt.Errorf("read AT transport: %w", err))
	} else {
		client.terminate(io.EOF)
	}
}

func (client *Client) routeLine(line string) error {
	if isURC(line) {
		return client.deliverURC(line)
	}

	client.state.Lock()
	if client.pending == nil {
		client.state.Unlock()
		return client.deliverURC(line)
	}
	if client.pending.expectsPrompt && !client.pending.promptSeen &&
		(line == ">" || (client.pending.expectsConnect && line == "CONNECT")) {
		client.pending.promptSeen = true
		prompt := client.pending.prompt
		client.state.Unlock()
		prompt <- struct{}{}
		return nil
	}
	if isTerminal(line) {
		pending := client.pending
		client.pending = nil
		client.state.Unlock()
		pending.result <- commandResult{response: Response{Lines: append([]string(nil), pending.lines...), Terminal: line}}
		client.releaseIdle()
		return nil
	}
	client.pending.lines = append(client.pending.lines, line)
	client.state.Unlock()
	return nil
}

func splitATResponse(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if index := bytes.IndexByte(data, '\n'); index >= 0 {
		return index + 1, data[:index], nil
	}
	trimmed := bytes.TrimLeft(data, "\r")
	if len(trimmed) >= 2 && trimmed[0] == '>' && trimmed[1] == ' ' {
		return len(data) - len(trimmed) + 2, trimmed[:1], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

func (client *Client) clearPending(expected *pendingCommand) {
	client.state.Lock()
	if client.pending == expected {
		client.pending = nil
		client.state.Unlock()
		client.releaseIdle()
		return
	}
	client.state.Unlock()
}

func (client *Client) failPending(err error) {
	client.state.Lock()
	if client.pending != nil {
		pending := client.pending
		client.pending = nil
		client.state.Unlock()
		pending.result <- commandResult{err: err}
		client.releaseIdle()
		return
	}
	client.state.Unlock()
}

func (client *Client) releaseIdle() {
	select {
	case client.idle <- struct{}{}:
	default:
	}
}

func (client *Client) deliverURC(urc string) error {
	select {
	case client.urcs <- urc:
		return nil
	default:
		return ErrURCOverflow
	}
}

func (client *Client) terminate(err error) {
	client.failPending(err)
	select {
	case client.errors <- err:
	default:
	}
	_ = client.Close()
}

func isTerminal(line string) bool {
	return line == "OK" || line == "ERROR" || strings.HasPrefix(line, "+CME ERROR:") || strings.HasPrefix(line, "+CMS ERROR:")
}

func isURC(line string) bool {
	for _, prefix := range []string{"RING", "+CMTI:", "+CLIP:", "+CCWA:", "+QMMSEND:", "NO CARRIER", "NO ANSWER", "BUSY"} {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

func isDirectDeliveryHeader(line string) bool {
	return strings.HasPrefix(line, "+CMT:") || strings.HasPrefix(line, "+CDS:")
}
