//go:build linux

package discovery

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverLinuxTopology(t *testing.T) {
	root := t.TempDir()
	devRoot := filepath.Join(root, "dev")
	sysRoot := filepath.Join(root, "sys")
	for _, path := range []string{
		filepath.Join(devRoot, "ttyUSB2"),
		filepath.Join(devRoot, "snd", "controlC2"),
		filepath.Join(devRoot, "snd", "pcmC2D0c"),
		filepath.Join(devRoot, "snd", "pcmC2D0p"),
		filepath.Join(sysRoot, "class", "tty", "ttyUSB2", "device"),
		filepath.Join(sysRoot, "class", "sound", "card2", "device"),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if filepath.Ext(path) == "" || filepath.Base(path) == "device" {
			continue
		}
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, value string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ttySys := filepath.Join(sysRoot, "class", "tty", "ttyUSB2", "device")
	soundSys := filepath.Join(sysRoot, "class", "sound", "card2", "device")
	write(filepath.Join(ttySys, "serial"), "0123456789ABCDEF\n")
	write(filepath.Join(ttySys, "bInterfaceNumber"), "02\n")
	write(filepath.Join(ttySys, "idVendor"), "2c7c\n")
	write(filepath.Join(soundSys, "serial"), "0123456789ABCDEF\n")

	modems, err := Discover(context.Background(), devRoot, sysRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(modems) != 1 {
		t.Fatalf("expected one modem, got %d", len(modems))
	}
	modem := modems[0]
	if modem.Serial != "0123456789ABCDEF" || modem.ATDevice != filepath.Join(devRoot, "ttyUSB2") {
		t.Fatalf("unexpected modem identity: %#v", modem)
	}
	if modem.ALSACapture != "hw:2,0" || modem.ALSAPlayback != "hw:2,0" {
		t.Fatalf("unexpected ALSA mapping: %#v", modem)
	}
}
