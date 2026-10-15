//go:build linux

package serialport

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// Open opens a Linux TTY and configures it for raw 8-N-1 modem communication.
func Open(path string, baud int) (*os.File, error) {
	speed, err := baudConstant(baud)
	if err != nil {
		return nil, err
	}
	fileDescriptor, err := syscall.Open(path, syscall.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, fmt.Errorf("open serial port %s: %w", path, err)
	}
	file := os.NewFile(uintptr(fileDescriptor), path)
	if err := configure(fileDescriptor, speed); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func configure(fileDescriptor int, speed uint32) error {
	termios := syscall.Termios{}
	if _, _, errno := syscall.Syscall6(
		syscall.SYS_IOCTL,
		uintptr(fileDescriptor),
		uintptr(syscall.TCGETS),
		uintptr(unsafe.Pointer(&termios)),
		0,
		0,
		0,
	); errno != 0 {
		return fmt.Errorf("read serial settings: %w", errno)
	}

	termios.Iflag = syscall.IGNBRK
	termios.Oflag = 0
	termios.Cflag = speed | syscall.CS8 | syscall.CREAD | syscall.CLOCAL
	termios.Lflag = 0
	termios.Cc[syscall.VMIN] = 1
	termios.Cc[syscall.VTIME] = 0
	termios.Ispeed = speed
	termios.Ospeed = speed

	if _, _, errno := syscall.Syscall6(
		syscall.SYS_IOCTL,
		uintptr(fileDescriptor),
		uintptr(syscall.TCSETS),
		uintptr(unsafe.Pointer(&termios)),
		0,
		0,
		0,
	); errno != 0 {
		return fmt.Errorf("configure serial settings: %w", errno)
	}
	return nil
}

func baudConstant(baud int) (uint32, error) {
	speeds := map[int]uint32{
		9600:   syscall.B9600,
		19200:  syscall.B19200,
		38400:  syscall.B38400,
		57600:  syscall.B57600,
		115200: syscall.B115200,
	}
	if speed, ok := speeds[baud]; ok {
		return speed, nil
	}
	return 0, fmt.Errorf("unsupported baud rate %d", baud)
}
