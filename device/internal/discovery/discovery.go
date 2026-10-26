// Package discovery discovers modem hardware exposed to a node agent.
package discovery

import (
	"context"
	"fmt"
	"strings"
)

// Modem describes one physical modem and the host nodes used by its worker.
type Modem struct {
	Serial        string
	ATDevice      string
	AudioControl  string
	AudioCapture  string
	AudioPlayback string
	ALSACapture   string
	ALSAPlayback  string
}

// Discover returns all supported modem devices visible to the node agent.
func Discover(ctx context.Context, deviceRoot, sysfsRoot string) ([]Modem, error) {
	return discover(ctx, deviceRoot, sysfsRoot)
}

// Tree renders a stable diagnostic tree suitable for one structured log field.
func Tree(modems []Modem, nodeID, platform string) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "node\n├── platform: %s\n├── node_id: %s\n└── devices", platform, nodeID)
	for index, modem := range modems {
		branch := "├"
		if index == len(modems)-1 {
			branch = "└"
		}
		fmt.Fprintf(&builder, "\n    %s── modem-%d\n        ├── usb_serial: %s\n        ├── at: %s\n        ├── audio_control: %s\n        ├── audio_capture: %s\n        ├── audio_playback: %s\n        ├── alsa_capture: %s\n        └── alsa_playback: %s", branch, index+1, modem.Serial, modem.ATDevice, modem.AudioControl, modem.AudioCapture, modem.AudioPlayback, modem.ALSACapture, modem.ALSAPlayback)
	}
	return builder.String()
}
