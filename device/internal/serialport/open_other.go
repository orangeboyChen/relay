//go:build !linux

package serialport

import (
	"fmt"
	"os"
)

// openTTY reports that the production serial transport is Linux-only.
func openTTY(_ string, _ int) (*os.File, error) {
	return nil, fmt.Errorf("serial modem access is supported only on Linux")
}
