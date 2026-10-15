package modem

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"sim-deck-device/internal/at"
	"sim-deck-device/internal/call"
	"sim-deck-device/internal/sms"
)

const testDeliverPDU = "00040B913105000000F0000862015121436500044F60597D"

func newTestSession(sink MessageSink) (*Session, net.Conn, *at.Client) {
	clientTransport, modemTransport := net.Pipe()
	client := at.NewClient(clientTransport)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	session, err := NewSession(client, sms.NewInbox(time.Hour), sink, logger, 100*time.Millisecond)
	Expect(err).NotTo(HaveOccurred())
	return session, modemTransport, client
}

var _ = Describe("Modem session", func() {
	It("configures notifications and imports valid history around malformed entries", func() {
		received := make(chan sms.ReceivedMessage, 1)
		session, modemTransport, client := newTestSession(MessageSinkFunc(func(_ context.Context, message sms.ReceivedMessage) error {
			received <- message
			return nil
		}))
		DeferCleanup(func() {
			_ = client.Close()
			_ = modemTransport.Close()
		})
		commands := make(chan string, len(initializationCommands)+1)
		go func() {
			reader := bufio.NewReader(modemTransport)
			for index := 0; index < len(initializationCommands)+1; index++ {
				command, _ := reader.ReadString('\r')
				commands <- strings.TrimSuffix(command, "\r")
				if command == "AT+CMGL=4\r" {
					_, _ = modemTransport.Write([]byte("+CMGL: 1,0,,23\r\n" + testDeliverPDU + "\r\n+CMGL: 2,0,,1\r\nnot-hex\r\nOK\r\n"))
				} else {
					_, _ = modemTransport.Write([]byte("OK\r\n"))
				}
			}
		}()

		Expect(session.Initialize(context.Background())).To(Succeed())

		for _, expected := range append(append([]string(nil), initializationCommands...), "AT+CMGL=4") {
			Eventually(commands).Should(Receive(Equal(expected)))
		}
		Eventually(received).Should(Receive(WithTransform(func(message sms.ReceivedMessage) string { return message.Text }, Equal("你好"))))
	})

	It("fails initialization when a required modem command is rejected", func() {
		session, modemTransport, client := newTestSession(MessageSinkFunc(func(context.Context, sms.ReceivedMessage) error { return nil }))
		DeferCleanup(func() {
			_ = client.Close()
			_ = modemTransport.Close()
		})
		go func() {
			reader := bufio.NewReader(modemTransport)
			_, _ = reader.ReadString('\r')
			_, _ = modemTransport.Write([]byte("+CME ERROR: 3\r\n"))
		}()

		err := session.Initialize(context.Background())

		Expect(err).To(MatchError("modem rejected initialization command ATE0: +CME ERROR: 3"))
	})

	It("fails initialization when the modem does not answer before the timeout", func() {
		session, modemTransport, client := newTestSession(MessageSinkFunc(func(context.Context, sms.ReceivedMessage) error { return nil }))
		DeferCleanup(func() {
			_ = client.Close()
			_ = modemTransport.Close()
		})
		go func() {
			reader := bufio.NewReader(modemTransport)
			_, _ = reader.ReadString('\r')
		}()

		err := session.Initialize(context.Background())

		Expect(errors.Is(err, context.DeadlineExceeded)).To(BeTrue())
	})

	It("fails initialization when the history command is rejected", func() {
		session, modemTransport, client := newTestSession(MessageSinkFunc(func(context.Context, sms.ReceivedMessage) error { return nil }))
		DeferCleanup(func() {
			_ = client.Close()
			_ = modemTransport.Close()
		})
		go func() {
			reader := bufio.NewReader(modemTransport)
			for index := 0; index < len(initializationCommands)+1; index++ {
				command, _ := reader.ReadString('\r')
				if command == "AT+CMGL=4\r" {
					_, _ = modemTransport.Write([]byte("ERROR\r\n"))
				} else {
					_, _ = modemTransport.Write([]byte("OK\r\n"))
				}
			}
		}()

		err := session.Initialize(context.Background())

		Expect(err).To(MatchError("modem rejected SMS history import: ERROR"))
	})

	It("returns a history sink failure instead of silently losing the message", func() {
		session, modemTransport, client := newTestSession(MessageSinkFunc(func(context.Context, sms.ReceivedMessage) error {
			return errors.New("outbox unavailable")
		}))
		DeferCleanup(func() {
			_ = client.Close()
			_ = modemTransport.Close()
		})
		go func() {
			reader := bufio.NewReader(modemTransport)
			for index := 0; index < len(initializationCommands)+1; index++ {
				command, _ := reader.ReadString('\r')
				if command == "AT+CMGL=4\r" {
					_, _ = modemTransport.Write([]byte("+CMGL: 1,0,,23\r\n" + testDeliverPDU + "\r\nOK\r\n"))
				} else {
					_, _ = modemTransport.Write([]byte("OK\r\n"))
				}
			}
		}()

		err := session.Initialize(context.Background())

		Expect(err).To(MatchError("deliver imported SMS: outbox unavailable"))
	})

	It("routes direct SMS and concurrent call notifications", func() {
		received := make(chan sms.ReceivedMessage, 1)
		session, modemTransport, client := newTestSession(MessageSinkFunc(func(_ context.Context, message sms.ReceivedMessage) error {
			received <- message
			return nil
		}))
		DeferCleanup(func() {
			_ = client.Close()
			_ = modemTransport.Close()
		})
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() { result <- session.Run(ctx) }()

		_, _ = modemTransport.Write([]byte("+CMT: ,23\r\n" + testDeliverPDU + "\r\nRING\r\n+CCWA: \"10086\",129,1\r\n"))

		Eventually(received).Should(Receive(WithTransform(func(message sms.ReceivedMessage) string { return message.Text }, Equal("你好"))))
		Eventually(func() call.Snapshot { return session.Calls().Snapshot() }).Should(Equal(call.Snapshot{State: call.StateWaiting, Waiting: true}))
		cancel()
		Eventually(result).Should(Receive(MatchError(context.Canceled)))
	})

	It("continues after malformed and informational notifications", func() {
		sinkCalls := make(chan struct{}, 1)
		session, modemTransport, client := newTestSession(MessageSinkFunc(func(context.Context, sms.ReceivedMessage) error {
			sinkCalls <- struct{}{}
			return nil
		}))
		DeferCleanup(func() {
			_ = client.Close()
			_ = modemTransport.Close()
		})
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() { result <- session.Run(ctx) }()

		_, _ = modemTransport.Write([]byte("+CMT: ,1\r\ninvalid\r\n+CDS: 1\r\n00\r\n+CMTI: \"ME\",1\r\n"))
		Consistently(sinkCalls, 50*time.Millisecond).ShouldNot(Receive())
		cancel()
		Eventually(result).Should(Receive(MatchError(context.Canceled)))
	})

	It("returns a direct-message sink failure", func() {
		session, modemTransport, client := newTestSession(MessageSinkFunc(func(context.Context, sms.ReceivedMessage) error {
			return errors.New("outbox unavailable")
		}))
		DeferCleanup(func() {
			_ = client.Close()
			_ = modemTransport.Close()
		})
		result := make(chan error, 1)
		go func() { result <- session.Run(context.Background()) }()

		_, _ = modemTransport.Write([]byte("+CMT: ,23\r\n" + testDeliverPDU + "\r\n"))

		Eventually(result).Should(Receive(MatchError("deliver direct SMS: outbox unavailable")))
	})

	It("returns the terminal serial error", func() {
		session, modemTransport, client := newTestSession(MessageSinkFunc(func(context.Context, sms.ReceivedMessage) error { return nil }))
		DeferCleanup(func() { _ = client.Close() })
		result := make(chan error, 1)
		go func() { result <- session.Run(context.Background()) }()

		_ = modemTransport.Close()

		Eventually(result).Should(Receive(MatchError(io.EOF)))
	})

	It("extracts complete history entries and reports missing payloads", func() {
		pdus, err := historyPDUs([]string{"echo", "+CMGL: 1", "AABB", "+CMGL: 2", "+CMGL: 3", "CCDD"})

		Expect(pdus).To(Equal([]string{"AABB", "CCDD"}))
		Expect(err).To(MatchError(ContainSubstring("response line 3 has no PDU")))
	})

	DescribeTable("classifies notification metadata without exposing payloads",
		func(notification string, expected string) {
			Expect(notificationType(notification)).To(Equal(expected))
		},
		Entry("prefixed", "+CMTI: \"ME\",1", "+CMTI"),
		Entry("multiline", "HEADER\npayload", "multiline"),
		Entry("plain", "READY", "READY"),
	)

	It("validates constructor dependencies", func() {
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		sink := MessageSinkFunc(func(context.Context, sms.ReceivedMessage) error { return nil })
		clientTransport, modemTransport := net.Pipe()
		client := at.NewClient(clientTransport)
		DeferCleanup(func() {
			_ = client.Close()
			_ = modemTransport.Close()
		})

		for _, build := range []func() (*Session, error){
			func() (*Session, error) { return NewSession(nil, sms.NewInbox(time.Hour), sink, logger, time.Second) },
			func() (*Session, error) { return NewSession(client, nil, sink, logger, time.Second) },
			func() (*Session, error) { return NewSession(client, sms.NewInbox(time.Hour), nil, logger, time.Second) },
			func() (*Session, error) { return NewSession(client, sms.NewInbox(time.Hour), sink, nil, time.Second) },
			func() (*Session, error) { return NewSession(client, sms.NewInbox(time.Hour), sink, logger, 0) },
		} {
			_, err := build()
			Expect(err).To(MatchError("modem session dependencies and positive timeout are required"))
		}
	})
})
