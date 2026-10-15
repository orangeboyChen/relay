package media

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Linux PCM endpoints", func() {
	It("starts ALSA capture and playback with the required wire format", func() {
		capture := newFakeAudioProcess([]byte("captured"))
		playback := newFakeAudioProcess(nil)
		var commands []recordedAudioCommand
		factory := func(_ context.Context, executable string, arguments ...string) audioProcess {
			commands = append(commands, recordedAudioCommand{executable: executable, arguments: arguments})
			if executable == "arecord" {
				return capture
			}
			return playback
		}

		endpoint, err := openALSAEndpoint(context.Background(), "hw:2,0", "hw:2,1", factory)
		Expect(err).NotTo(HaveOccurred())
		buffer := make([]byte, len("captured"))
		_, err = io.ReadFull(endpoint, buffer)
		Expect(err).NotTo(HaveOccurred())
		Expect(buffer).To(Equal([]byte("captured")))
		_, err = endpoint.Write([]byte("played"))
		Expect(err).NotTo(HaveOccurred())
		Expect(playback.written.String()).To(Equal("played"))
		Expect(commands).To(Equal([]recordedAudioCommand{
			{executable: "arecord", arguments: expectedALSAArguments("hw:2,0")},
			{executable: "aplay", arguments: expectedALSAArguments("hw:2,1")},
		}))
		Expect(capture.started).To(BeTrue())
		Expect(playback.started).To(BeTrue())
		Expect(endpoint.Close()).To(Succeed())
		Expect(endpoint.Close()).To(Succeed())
		Expect(capture.stopCount).To(Equal(1))
		Expect(playback.stopCount).To(Equal(1))
	})

	It("validates ALSA device names before starting processes", func() {
		factoryCalled := false
		factory := func(context.Context, string, ...string) audioProcess {
			factoryCalled = true
			return newFakeAudioProcess(nil)
		}

		_, err := openALSAEndpoint(context.Background(), " ", "hw:0,0", factory)

		Expect(err).To(MatchError("ALSA capture and playback devices are required"))
		Expect(factoryCalled).To(BeFalse())
	})

	It("reports capture setup failures without starting playback", func() {
		capture := newFakeAudioProcess(nil)
		capture.stdoutError = errors.New("pipe unavailable")
		factoryCalls := 0
		factory := func(context.Context, string, ...string) audioProcess {
			factoryCalls++
			return capture
		}

		_, err := openALSAEndpoint(context.Background(), "hw:0,0", "hw:0,0", factory)

		Expect(err).To(MatchError("open ALSA capture pipe: pipe unavailable"))
		Expect(factoryCalls).To(Equal(1))
	})

	It("closes the capture pipe when arecord cannot start", func() {
		capture := newFakeAudioProcess(nil)
		capture.startError = errors.New("binary missing")
		factory := func(context.Context, string, ...string) audioProcess { return capture }

		_, err := openALSAEndpoint(context.Background(), "hw:0,0", "hw:0,0", factory)

		Expect(err).To(MatchError("start ALSA capture: binary missing"))
		Expect(capture.stdout.closed).To(BeTrue())
	})

	It("stops capture when playback setup fails", func() {
		capture := newFakeAudioProcess(nil)
		playback := newFakeAudioProcess(nil)
		playback.stdinError = errors.New("pipe unavailable")
		factory := orderedAudioFactory(capture, playback)

		_, err := openALSAEndpoint(context.Background(), "hw:0,0", "hw:0,0", factory)

		Expect(err).To(MatchError("open ALSA playback pipe: pipe unavailable"))
		Expect(capture.stopCount).To(Equal(1))
		Expect(capture.stdout.closed).To(BeTrue())
	})

	It("stops capture and closes playback when aplay cannot start", func() {
		capture := newFakeAudioProcess(nil)
		playback := newFakeAudioProcess(nil)
		playback.startError = errors.New("audio busy")

		_, err := openALSAEndpoint(context.Background(), "hw:0,0", "hw:0,0", orderedAudioFactory(capture, playback))

		Expect(err).To(MatchError("start ALSA playback: audio busy"))
		Expect(capture.stopCount).To(Equal(1))
		Expect(capture.stdout.closed).To(BeTrue())
		Expect(playback.stdin.closed).To(BeTrue())
	})

	It("opens one full-duplex raw PCM device and preserves close errors", func() {
		device := &fakeReadWriteCloser{reader: bytes.NewBufferString("input"), closeError: errors.New("close failed")}
		var openedPath string
		endpoint, err := openRawPCMEndpoint(" /dev/sim-deck/pcm ", func(path string) (io.ReadWriteCloser, error) {
			openedPath = path
			return device, nil
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(openedPath).To(Equal("/dev/sim-deck/pcm"))
		buffer := make([]byte, 5)
		_, err = io.ReadFull(endpoint, buffer)
		Expect(err).NotTo(HaveOccurred())
		Expect(buffer).To(Equal([]byte("input")))
		_, err = endpoint.Write([]byte("output"))
		Expect(err).NotTo(HaveOccurred())
		Expect(device.writer.String()).To(Equal("output"))
		Expect(endpoint.Close()).To(MatchError("close failed"))
		Expect(endpoint.Close()).To(MatchError("close failed"))
		Expect(device.closeCount).To(Equal(1))
	})

	It("adapts operating-system processes to PCM pipes", func() {
		process := &execAudioProcess{command: exec.CommandContext(context.Background(), "sh", "-c", "printf captured; cat >/dev/null")}
		source, err := process.StdoutPipe()
		Expect(err).NotTo(HaveOccurred())
		sink, err := process.StdinPipe()
		Expect(err).NotTo(HaveOccurred())
		Expect(process.Start()).To(Succeed())
		buffer := make([]byte, len("captured"))
		_, err = io.ReadFull(source, buffer)
		Expect(err).NotTo(HaveOccurred())
		Expect(buffer).To(Equal([]byte("captured")))
		_, err = sink.Write([]byte("played"))
		Expect(err).NotTo(HaveOccurred())
		Expect(sink.Close()).To(Succeed())
		process.Stop()
	})

	It("opens a raw PCM path through the operating system", func() {
		path := filepath.Join(GinkgoT().TempDir(), "pcm")
		Expect(os.WriteFile(path, []byte("audio"), 0o600)).To(Succeed())

		endpoint, err := OpenRawPCMEndpoint(path)

		Expect(err).NotTo(HaveOccurred())
		buffer := make([]byte, len("audio"))
		_, err = io.ReadFull(endpoint, buffer)
		Expect(err).NotTo(HaveOccurred())
		Expect(buffer).To(Equal([]byte("audio")))
		Expect(endpoint.Close()).To(Succeed())
	})

	It("validates and reports raw PCM open failures", func() {
		_, err := openRawPCMEndpoint(" ", func(string) (io.ReadWriteCloser, error) {
			Fail("opener must not be called")
			return nil, nil
		})
		Expect(err).To(MatchError("raw PCM device path is required"))

		_, err = openRawPCMEndpoint("/dev/missing", func(string) (io.ReadWriteCloser, error) {
			return nil, errors.New("not found")
		})
		Expect(err).To(MatchError("open raw PCM device: not found"))
	})
})

type recordedAudioCommand struct {
	executable string
	arguments  []string
}

func expectedALSAArguments(device string) []string {
	return []string{
		"--quiet",
		"--device", device,
		"--file-type", "raw",
		"--format", "S16_LE",
		"--rate", "8000",
		"--channels", "1",
	}
}

type fakeAudioProcess struct {
	stdout      *trackedReadCloser
	stdin       *trackedWriteCloser
	written     bytes.Buffer
	stdoutError error
	stdinError  error
	startError  error
	started     bool
	stopCount   int
}

func newFakeAudioProcess(output []byte) *fakeAudioProcess {
	process := &fakeAudioProcess{}
	process.stdout = &trackedReadCloser{reader: bytes.NewReader(output)}
	process.stdin = &trackedWriteCloser{writer: &process.written}
	return process
}

func (process *fakeAudioProcess) StdoutPipe() (io.ReadCloser, error) {
	return process.stdout, process.stdoutError
}

func (process *fakeAudioProcess) StdinPipe() (io.WriteCloser, error) {
	return process.stdin, process.stdinError
}

func (process *fakeAudioProcess) Start() error {
	process.started = true
	return process.startError
}

func (process *fakeAudioProcess) Stop() {
	process.stopCount++
}

func orderedAudioFactory(processes ...audioProcess) audioProcessFactory {
	index := 0
	return func(context.Context, string, ...string) audioProcess {
		process := processes[index]
		index++
		return process
	}
}

type trackedReadCloser struct {
	reader io.Reader
	closed bool
}

func (closer *trackedReadCloser) Read(buffer []byte) (int, error) {
	return closer.reader.Read(buffer)
}

func (closer *trackedReadCloser) Close() error {
	closer.closed = true
	return nil
}

type trackedWriteCloser struct {
	writer io.Writer
	closed bool
}

func (closer *trackedWriteCloser) Write(buffer []byte) (int, error) {
	return closer.writer.Write(buffer)
}

func (closer *trackedWriteCloser) Close() error {
	closer.closed = true
	return nil
}

type fakeReadWriteCloser struct {
	reader     io.Reader
	writer     bytes.Buffer
	closeError error
	closeCount int
}

func (device *fakeReadWriteCloser) Read(buffer []byte) (int, error) {
	return device.reader.Read(buffer)
}

func (device *fakeReadWriteCloser) Write(buffer []byte) (int, error) {
	return device.writer.Write(buffer)
}

func (device *fakeReadWriteCloser) Close() error {
	device.closeCount++
	return device.closeError
}
