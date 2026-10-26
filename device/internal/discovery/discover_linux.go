//go:build linux

package discovery

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

func discover(ctx context.Context, deviceRoot, sysfsRoot string) ([]Modem, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	deviceRoot = filepath.Clean(strings.TrimSpace(deviceRoot))
	sysfsRoot = filepath.Clean(strings.TrimSpace(sysfsRoot))
	if deviceRoot == "." || deviceRoot == "" || sysfsRoot == "." || sysfsRoot == "" {
		return nil, errors.New("device and sysfs roots are required")
	}

	type candidate struct {
		path            string
		serial          string
		interfaceNumber string
		physical        string
	}
	bySerial := make(map[string][]candidate)
	patterns := []string{filepath.Join(deviceRoot, "ttyUSB*"), filepath.Join(deviceRoot, "ttyACM*")}
	for _, pattern := range patterns {
		paths, err := filepath.Glob(pattern)
		if err != nil {
			return nil, fmt.Errorf("glob serial devices: %w", err)
		}
		for _, path := range paths {
			serial, interfaceNumber, physical, ok := serialDeviceIdentity(path, sysfsRoot)
			if !ok {
				continue
			}
			for _, existing := range bySerial[serial] {
				if existing.physical != physical {
					return nil, fmt.Errorf("duplicate USB serial %q belongs to multiple physical modems", serial)
				}
			}
			bySerial[serial] = append(bySerial[serial], candidate{path: path, serial: serial, interfaceNumber: interfaceNumber, physical: physical})
		}
	}

	serials := make([]string, 0, len(bySerial))
	for serial := range bySerial {
		serials = append(serials, serial)
	}
	sort.Strings(serials)
	result := make([]Modem, 0, len(serials))
	for _, serial := range serials {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		candidates := bySerial[serial]
		sort.SliceStable(candidates, func(i, j int) bool {
			if candidates[i].interfaceNumber == candidates[j].interfaceNumber {
				return candidates[i].path < candidates[j].path
			}
			return candidates[i].interfaceNumber == "02"
		})
		card, ok := soundCardForSerial(serial, deviceRoot, sysfsRoot)
		if !ok {
			continue
		}
		capture, playback, ok := soundPCMNodes(deviceRoot, card)
		if !ok {
			continue
		}
		result = append(result, Modem{
			Serial:        serial,
			ATDevice:      candidates[0].path,
			AudioControl:  filepath.Join(deviceRoot, "snd", "controlC"+card),
			AudioCapture:  capture,
			AudioPlayback: playback,
			ALSACapture:   "hw:" + card + "," + pcmDeviceNumber(capture),
			ALSAPlayback:  "hw:" + card + "," + pcmDeviceNumber(playback),
		})
	}
	if len(result) == 0 {
		return nil, errors.New("no complete Quectel modem topology found")
	}
	return result, nil
}

func serialDeviceIdentity(devicePath, sysfsRoot string) (string, string, string, bool) {
	name := filepath.Base(devicePath)
	path := filepath.Join(sysfsRoot, "class", "tty", name, "device")
	serial := ancestorValue(path, "serial")
	physical := physicalUSBPath(path, sysfsRoot)
	return serial, ancestorValue(path, "bInterfaceNumber"), physical, serial != "" && strings.EqualFold(ancestorValue(path, "idVendor"), "2c7c")
}

func physicalUSBPath(start, sysfsRoot string) string {
	path := start
	if resolved, err := filepath.EvalSymlinks(start); err == nil {
		path = resolved
	}
	for {
		if strings.EqualFold(readNodeValue(path, "idVendor"), "2c7c") && readNodeValue(path, "serial") != "" {
			return path
		}
		parent := filepath.Dir(path)
		if parent == path || (sysfsRoot != "" && !strings.HasPrefix(parent, sysfsRoot)) {
			return path
		}
		path = parent
	}
}

func soundCardForSerial(serial, deviceRoot, sysfsRoot string) (string, bool) {
	paths, _ := filepath.Glob(filepath.Join(deviceRoot, "snd", "controlC*"))
	for _, path := range paths {
		card := strings.TrimPrefix(filepath.Base(path), "controlC")
		if card == filepath.Base(path) || ancestorValue(filepath.Join(sysfsRoot, "class", "sound", "card"+card, "device"), "serial") != serial {
			continue
		}
		return card, true
	}
	return "", false
}

func soundPCMNodes(deviceRoot, card string) (string, string, bool) {
	capture, _ := firstMatching(filepath.Glob(filepath.Join(deviceRoot, "snd", "pcmC"+card+"D*c")))
	playback, _ := firstMatching(filepath.Glob(filepath.Join(deviceRoot, "snd", "pcmC"+card+"D*p")))
	return capture, playback, capture != "" && playback != ""
}

func firstMatching(paths []string, err error) (string, bool) {
	if err != nil || len(paths) == 0 {
		return "", false
	}
	sort.Strings(paths)
	return paths[0], true
}

func pcmDeviceNumber(path string) string {
	base := filepath.Base(path)
	start := strings.Index(base, "D")
	if start < 0 {
		return "0"
	}
	end := start + 1
	for end < len(base) && base[end] >= '0' && base[end] <= '9' {
		end++
	}
	if value := base[start+1 : end]; value != "" {
		if _, err := strconv.Atoi(value); err == nil {
			return value
		}
	}
	return "0"
}

func ancestorValue(start, name string) string {
	path := start
	for {
		if value := readNodeValue(path, name); value != "" {
			return value
		}
		parent := filepath.Dir(path)
		if parent == path {
			return ""
		}
		path = parent
	}
}

func readNodeValue(path, name string) string {
	data, err := os.ReadFile(filepath.Join(path, name))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
