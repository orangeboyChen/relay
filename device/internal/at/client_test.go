package at

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type writeFailureTransport struct {
	done      chan struct{}
	closeOnce sync.Once
}

func newWriteFailureTransport() *writeFailureTransport {
	return &writeFailureTransport{done: make(chan struct{})}
}

func (transport *writeFailureTransport) Read([]byte) (int, error) {
	<-transport.done
	return 0, io.EOF
}

func (*writeFailureTransport) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

func (transport *writeFailureTransport) Close() error {
	transport.closeOnce.Do(func() { close(transport.done) })
	return nil
}

var _ = Describe("AT client", func() {
	It("separates unsolicited notifications from a command response", func() {
		clientTransport, modemTransport := net.Pipe()
		client := NewClient(clientTransport)
		DeferCleanup(func() {
			_ = client.Close()
			_ = modemTransport.Close()
		})

		go func() {
			reader := bufio.NewReader(modemTransport)
			_, _ = reader.ReadString('\r')
			_, _ = modemTransport.Write([]byte("AT+CSQ\r\n+CMTI: \"ME\",4\r\n+CSQ: 22,99\r\nOK\r\n"))
		}()

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		response, err := client.Send(ctx, "AT+CSQ")

		Expect(err).NotTo(HaveOccurred())
		Expect(response.OK()).To(BeTrue())
		Expect(response.Lines).To(Equal([]string{"AT+CSQ", "+CSQ: 22,99"}))
		Eventually(client.URCs()).Should(Receive(Equal(`+CMTI: "ME",4`)))
	})

	It("serializes concurrent commands", func() {
		clientTransport, modemTransport := net.Pipe()
		client := NewClient(clientTransport)
		DeferCleanup(func() {
			_ = client.Close()
			_ = modemTransport.Close()
		})

		commands := make(chan string, 2)
		go func() {
			reader := bufio.NewReader(modemTransport)
			for range 2 {
				command, _ := reader.ReadString('\r')
				commands <- command
				_, _ = modemTransport.Write([]byte(command + "\nOK\r\n"))
			}
		}()

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		results := make(chan error, 2)
		go func() { _, err := client.Send(ctx, "AT+ONE"); results <- err }()
		go func() { _, err := client.Send(ctx, "AT+TWO"); results <- err }()

		Eventually(results).Should(Receive(Succeed()))
		Eventually(results).Should(Receive(Succeed()))
		Eventually(commands).Should(Receive(Or(Equal("AT+ONE\r"), Equal("AT+TWO\r"))))
		Eventually(commands).Should(Receive(Or(Equal("AT+ONE\r"), Equal("AT+TWO\r"))))
	})

	It("sends an interactive prompt payload and waits for the final result", func() {
		clientTransport, modemTransport := net.Pipe()
		client := NewClient(clientTransport)
		DeferCleanup(func() {
			_ = client.Close()
			_ = modemTransport.Close()
		})

		go func() {
			reader := bufio.NewReader(modemTransport)
			command, _ := reader.ReadString('\r')
			Expect(command).To(Equal("AT+CMGS=12\r"))
			_, _ = modemTransport.Write([]byte("\r\n> "))
			payload, _ := reader.ReadBytes(0x1a)
			Expect(payload).To(Equal([]byte{'A', 'A', 'B', 'B', 0x1a}))
			_, _ = modemTransport.Write([]byte("\r\n+CMGS: 42\r\n\r\nOK\r\n"))
		}()

		response, err := client.SendPrompt(context.Background(), "AT+CMGS=12", []byte("AABB"))

		Expect(err).NotTo(HaveOccurred())
		Expect(response.OK()).To(BeTrue())
		Expect(response.Lines).To(Equal([]string{"+CMGS: 42"}))
	})

	It("returns a prompt command rejection without writing its payload", func() {
		clientTransport, modemTransport := net.Pipe()
		client := NewClient(clientTransport)
		DeferCleanup(func() {
			_ = client.Close()
			_ = modemTransport.Close()
		})

		go func() {
			reader := bufio.NewReader(modemTransport)
			_, _ = reader.ReadString('\r')
			_, _ = modemTransport.Write([]byte("+CMS ERROR: 304\r\n"))
		}()

		response, err := client.SendPrompt(context.Background(), "AT+CMGS=12", []byte("AABB"))

		Expect(err).NotTo(HaveOccurred())
		Expect(response.Terminal).To(Equal("+CMS ERROR: 304"))
	})

	It("drains a timed-out response before sending the next command", func() {
		clientTransport, modemTransport := net.Pipe()
		client := NewClient(clientTransport)
		DeferCleanup(func() {
			_ = client.Close()
			_ = modemTransport.Close()
		})

		go func() {
			reader := bufio.NewReader(modemTransport)
			_, _ = reader.ReadString('\r')
			time.Sleep(60 * time.Millisecond)
			_, _ = modemTransport.Write([]byte("+SLOW: late\r\nOK\r\n"))
			_, _ = reader.ReadString('\r')
			_, _ = modemTransport.Write([]byte("+SECOND: correct\r\nOK\r\n"))
		}()

		timedContext, cancelTimed := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancelTimed()
		_, err := client.Send(timedContext, "AT+SLOW")
		Expect(errors.Is(err, context.DeadlineExceeded)).To(BeTrue())
		waitingContext, cancelWaiting := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancelWaiting()
		_, err = client.Send(waitingContext, "AT+CANCELED")
		Expect(errors.Is(err, context.DeadlineExceeded)).To(BeTrue())

		secondContext, cancelSecond := context.WithTimeout(context.Background(), time.Second)
		defer cancelSecond()
		response, err := client.Send(secondContext, "AT+SECOND")

		Expect(err).NotTo(HaveOccurred())
		Expect(response.Lines).To(Equal([]string{"+SECOND: correct"}))
	})

	It("delivers direct SMS and status-report URCs with their PDU payloads", func() {
		clientTransport, modemTransport := net.Pipe()
		client := NewClient(clientTransport)
		DeferCleanup(func() {
			_ = client.Close()
			_ = modemTransport.Close()
		})

		go func() {
			reader := bufio.NewReader(modemTransport)
			_, _ = reader.ReadString('\r')
			_, _ = modemTransport.Write([]byte("+CMT: ,23\r\n001122\r\n+CDS: 18\r\nAABBCC\r\n+CSQ: 20,99\r\nOK\r\n"))
		}()

		response, err := client.Send(context.Background(), "AT+CSQ")

		Expect(err).NotTo(HaveOccurred())
		Expect(response.Lines).To(Equal([]string{"+CSQ: 20,99"}))
		Eventually(client.URCs()).Should(Receive(Equal("+CMT: ,23\n001122")))
		Eventually(client.URCs()).Should(Receive(Equal("+CDS: 18\nAABBCC")))
	})

	It("terminates instead of silently dropping an overflowing URC queue", func() {
		clientTransport, modemTransport := net.Pipe()
		client := NewClient(clientTransport)
		DeferCleanup(func() {
			_ = client.Close()
			_ = modemTransport.Close()
		})

		go func() {
			_, _ = modemTransport.Write([]byte(strings.Repeat("RING\r\n", 129)))
		}()

		Eventually(client.Errors(), time.Second).Should(Receive(MatchError(ErrURCOverflow)))
		_, err := client.Send(context.Background(), "AT")
		Expect(errors.Is(err, ErrClosed)).To(BeTrue())
	})

	It("reports a serial EOF to both the pending command and the supervisor", func() {
		clientTransport, modemTransport := net.Pipe()
		client := NewClient(clientTransport)
		DeferCleanup(func() { _ = client.Close() })

		go func() {
			reader := bufio.NewReader(modemTransport)
			_, _ = reader.ReadString('\r')
			_ = modemTransport.Close()
		}()

		_, err := client.Send(context.Background(), "AT")

		Expect(errors.Is(err, io.EOF)).To(BeTrue())
		Eventually(client.Errors()).Should(Receive(MatchError(io.EOF)))
	})

	It("reports an incomplete direct-delivery notification", func() {
		clientTransport, modemTransport := net.Pipe()
		client := NewClient(clientTransport)
		DeferCleanup(func() { _ = client.Close() })

		go func() {
			_, _ = modemTransport.Write([]byte("+CMT: ,23\r\n"))
			_ = modemTransport.Close()
		}()

		Eventually(client.Errors()).Should(Receive(MatchError(ContainSubstring("incomplete direct-delivery URC"))))
	})

	It("validates commands before writing", func() {
		clientTransport, modemTransport := net.Pipe()
		client := NewClient(clientTransport)
		DeferCleanup(func() {
			_ = client.Close()
			_ = modemTransport.Close()
		})

		for _, command := range []string{"", "  ", "AT\rH", "AT\nH"} {
			_, err := client.Send(context.Background(), command)
			Expect(err).To(MatchError("AT command must be one non-empty line"))
		}
	})

	It("validates interactive prompt payloads before writing", func() {
		clientTransport, modemTransport := net.Pipe()
		client := NewClient(clientTransport)
		DeferCleanup(func() {
			_ = client.Close()
			_ = modemTransport.Close()
		})

		_, err := client.SendPrompt(context.Background(), "AT+CMGS=12", nil)
		Expect(err).To(MatchError(ContainSubstring("prompt payload")))
		_, err = client.SendPrompt(context.Background(), "AT+CMGS=12", []byte{'A', 0x1a})
		Expect(err).To(MatchError(ContainSubstring("Ctrl-Z")))
	})

	It("honors cancellation while waiting for an interactive prompt", func() {
		clientTransport, modemTransport := net.Pipe()
		client := NewClient(clientTransport)
		DeferCleanup(func() {
			_ = client.Close()
			_ = modemTransport.Close()
		})
		go func() {
			reader := bufio.NewReader(modemTransport)
			_, _ = reader.ReadString('\r')
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		_, err := client.SendPrompt(ctx, "AT+CMGS=12", []byte("AABB"))

		Expect(errors.Is(err, context.DeadlineExceeded)).To(BeTrue())
	})

	It("returns a contextual write failure and releases the pending command", func() {
		transport := newWriteFailureTransport()
		client := NewClient(transport)
		DeferCleanup(func() { _ = client.Close() })

		_, err := client.Send(context.Background(), "AT")

		Expect(err).To(MatchError("write AT command: write failed"))

		_, err = client.SendPrompt(context.Background(), "AT+CMGS=12", []byte("AABB"))
		Expect(err).To(MatchError("write AT command: write failed"))
	})

	It("honors cancellation before acquiring the command slot", func() {
		clientTransport, modemTransport := net.Pipe()
		client := NewClient(clientTransport)
		DeferCleanup(func() {
			_ = client.Close()
			_ = modemTransport.Close()
		})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := client.Send(ctx, "AT")

		Expect(errors.Is(err, context.Canceled)).To(BeTrue())
	})

	It("closes idempotently and rejects later commands", func() {
		clientTransport, modemTransport := net.Pipe()
		client := NewClient(clientTransport)
		_ = modemTransport.Close()

		Expect(client.Close()).To(Succeed())
		Expect(client.Close()).To(Succeed())
		_, err := client.Send(context.Background(), "AT")
		Expect(errors.Is(err, ErrClosed)).To(BeTrue())
	})
})
