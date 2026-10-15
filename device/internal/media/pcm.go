package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// PCMEndpoint provides full-duplex 8 kHz, signed 16-bit, mono PCM audio.
type PCMEndpoint struct {
	source     io.Reader
	sink       io.Writer
	close      func() error
	closeOnce  sync.Once
	closeError error
}

// Read captures PCM from the modem audio endpoint.
func (endpoint *PCMEndpoint) Read(buffer []byte) (int, error) {
	return endpoint.source.Read(buffer)
}

// Write plays PCM into the modem audio endpoint.
func (endpoint *PCMEndpoint) Write(buffer []byte) (int, error) {
	return endpoint.sink.Write(buffer)
}

// Close stops capture and playback exactly once.
func (endpoint *PCMEndpoint) Close() error {
	endpoint.closeOnce.Do(func() {
		endpoint.closeError = endpoint.close()
	})
	return endpoint.closeError
}

type audioProcess interface {
	StdoutPipe() (io.ReadCloser, error)
	StdinPipe() (io.WriteCloser, error)
	Start() error
	Stop()
}

type execAudioProcess struct {
	command *exec.Cmd
}

func (process *execAudioProcess) StdoutPipe() (io.ReadCloser, error) {
	return process.command.StdoutPipe()
}

func (process *execAudioProcess) StdinPipe() (io.WriteCloser, error) {
	return process.command.StdinPipe()
}

func (process *execAudioProcess) Start() error {
	return process.command.Start()
}

func (process *execAudioProcess) Stop() {
	if process.command.Process != nil {
		_ = process.command.Process.Kill()
	}
	_ = process.command.Wait()
}

type audioProcessFactory func(context.Context, string, ...string) audioProcess

// OpenALSAEndpoint starts arecord and aplay for one ALSA full-duplex device.
func OpenALSAEndpoint(ctx context.Context, captureDevice string, playbackDevice string) (*PCMEndpoint, error) {
	return openALSAEndpoint(ctx, captureDevice, playbackDevice, func(ctx context.Context, executable string, arguments ...string) audioProcess {
		return &execAudioProcess{command: exec.CommandContext(ctx, executable, arguments...)}
	})
}

func openALSAEndpoint(ctx context.Context, captureDevice string, playbackDevice string, factory audioProcessFactory) (*PCMEndpoint, error) {
	captureDevice = strings.TrimSpace(captureDevice)
	playbackDevice = strings.TrimSpace(playbackDevice)
	if captureDevice == "" || playbackDevice == "" {
		return nil, errors.New("ALSA capture and playback devices are required")
	}
	arguments := func(device string) []string {
		return []string{
			"--quiet",
			"--device", device,
			"--file-type", "raw",
			"--format", "S16_LE",
			"--rate", "8000",
			"--channels", "1",
		}
	}
	capture := factory(ctx, "arecord", arguments(captureDevice)...)
	source, err := capture.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("open ALSA capture pipe: %w", err)
	}
	if err := capture.Start(); err != nil {
		_ = source.Close()
		return nil, fmt.Errorf("start ALSA capture: %w", err)
	}

	playback := factory(ctx, "aplay", arguments(playbackDevice)...)
	sink, err := playback.StdinPipe()
	if err != nil {
		_ = source.Close()
		capture.Stop()
		return nil, fmt.Errorf("open ALSA playback pipe: %w", err)
	}
	if err := playback.Start(); err != nil {
		_ = sink.Close()
		_ = source.Close()
		capture.Stop()
		return nil, fmt.Errorf("start ALSA playback: %w", err)
	}

	return &PCMEndpoint{
		source: source,
		sink:   sink,
		close: func() error {
			_ = sink.Close()
			_ = source.Close()
			playback.Stop()
			capture.Stop()
			return nil
		},
	}, nil
}

// OpenRawPCMEndpoint opens a modem-provided full-duplex PCM character device.
func OpenRawPCMEndpoint(path string) (*PCMEndpoint, error) {
	return openRawPCMEndpoint(path, func(path string) (io.ReadWriteCloser, error) {
		return os.OpenFile(path, os.O_RDWR, 0)
	})
}

func openRawPCMEndpoint(path string, open func(string) (io.ReadWriteCloser, error)) (*PCMEndpoint, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("raw PCM device path is required")
	}
	device, err := open(path)
	if err != nil {
		return nil, fmt.Errorf("open raw PCM device: %w", err)
	}
	return &PCMEndpoint{source: device, sink: device, close: device.Close}, nil
}
