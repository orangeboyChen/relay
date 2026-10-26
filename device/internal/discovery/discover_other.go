//go:build !linux

package discovery

import (
	"context"
	"errors"
)

func discover(context.Context, string, string) ([]Modem, error) {
	return nil, errors.New("automatic modem discovery is not implemented for this platform")
}
