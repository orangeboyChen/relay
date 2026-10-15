package at

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
)

var ErrClosed = errors.New("AT client is closed")

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
	lines  []string
	result chan commandResult
}

// Client serializes AT commands while delivering unsolicited result codes separately.
type Client struct {
	transport io.ReadWriteCloser
	commands  sync.Mutex
	state     sync.Mutex
	pending   *pendingCommand
	urcs      chan string
	done      chan struct{}
	closeOnce sync.Once
}

// NewClient starts reading responses from an already configured serial transport.
func NewClient(transport io.ReadWriteCloser) *Client {
	client := &Client{
		transport: transport,
		urcs:      make(chan string, 128),
		done:      make(chan struct{}),
	}
	go client.readLoop()
	return client
}

// URCs returns unsolicited modem notifications such as incoming calls and messages.
func (client *Client) URCs() <-chan string {
	return client.urcs
}

// Send writes one command and waits for its terminal result.
func (client *Client) Send(ctx context.Context, command string) (Response, error) {
	if strings.TrimSpace(command) == "" || strings.ContainsAny(command, "\r\n") {
		return Response{}, errors.New("AT command must be one non-empty line")
	}
	client.commands.Lock()
	defer client.commands.Unlock()

	select {
	case <-ctx.Done():
		return Response{}, ctx.Err()
	case <-client.done:
		return Response{}, ErrClosed
	default:
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
		client.clearPending(pending)
		return Response{}, ctx.Err()
	case <-client.done:
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
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		client.routeLine(line)
	}
	if err := scanner.Err(); err != nil {
		client.failPending(fmt.Errorf("read AT transport: %w", err))
	} else {
		client.failPending(io.EOF)
	}
}

func (client *Client) routeLine(line string) {
	if isURC(line) {
		select {
		case client.urcs <- line:
		default:
		}
		return
	}

	client.state.Lock()
	defer client.state.Unlock()
	if client.pending == nil {
		select {
		case client.urcs <- line:
		default:
		}
		return
	}
	if isTerminal(line) {
		pending := client.pending
		client.pending = nil
		pending.result <- commandResult{response: Response{Lines: append([]string(nil), pending.lines...), Terminal: line}}
		return
	}
	client.pending.lines = append(client.pending.lines, line)
}

func (client *Client) clearPending(expected *pendingCommand) {
	client.state.Lock()
	defer client.state.Unlock()
	if client.pending == expected {
		client.pending = nil
	}
}

func (client *Client) failPending(err error) {
	client.state.Lock()
	defer client.state.Unlock()
	if client.pending != nil {
		pending := client.pending
		client.pending = nil
		pending.result <- commandResult{err: err}
	}
}

func isTerminal(line string) bool {
	return line == "OK" || line == "ERROR" || strings.HasPrefix(line, "+CME ERROR:") || strings.HasPrefix(line, "+CMS ERROR:")
}

func isURC(line string) bool {
	for _, prefix := range []string{"RING", "+CMTI:", "+CMT:", "+CLIP:", "+CCWA:", "NO CARRIER", "NO ANSWER", "BUSY"} {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}
