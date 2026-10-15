package at

import (
	"bufio"
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func TestClientSeparatesURCsFromCommandResponse(t *testing.T) {
	clientTransport, modemTransport := net.Pipe()
	client := NewClient(clientTransport)
	defer client.Close()
	defer modemTransport.Close()

	go func() {
		reader := bufio.NewReader(modemTransport)
		_, _ = reader.ReadString('\r')
		_, _ = modemTransport.Write([]byte("AT+CSQ\r\n+CMTI: \"ME\",4\r\n+CSQ: 22,99\r\nOK\r\n"))
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	response, err := client.Send(ctx, "AT+CSQ")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if !response.OK() || len(response.Lines) != 2 || response.Lines[1] != "+CSQ: 22,99" {
		t.Fatalf("Send() response = %#v", response)
	}

	select {
	case urc := <-client.URCs():
		if urc != "+CMTI: \"ME\",4" {
			t.Fatalf("URC = %q", urc)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for URC")
	}
}

func TestClientSerializesCommands(t *testing.T) {
	clientTransport, modemTransport := net.Pipe()
	client := NewClient(clientTransport)
	defer client.Close()
	defer modemTransport.Close()

	go func() {
		reader := bufio.NewReader(modemTransport)
		for range 2 {
			command, _ := reader.ReadString('\r')
			_, _ = modemTransport.Write([]byte(command + "\nOK\r\n"))
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	results := make(chan error, 2)
	go func() { _, err := client.Send(ctx, "AT+ONE"); results <- err }()
	go func() { _, err := client.Send(ctx, "AT+TWO"); results <- err }()
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("Send() error = %v", err)
		}
	}
}

func TestClientHonorsCancellation(t *testing.T) {
	clientTransport, modemTransport := net.Pipe()
	client := NewClient(clientTransport)
	defer client.Close()
	defer modemTransport.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := client.Send(ctx, "AT")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Send() error = %v, want context.Canceled", err)
	}
}
