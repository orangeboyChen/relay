#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/install-linux-device.sh
source "$script_dir/install-linux-device.sh"

fixture="$(mktemp -d)"
mkdir -p "$fixture/bin" "$fixture/dev/snd"
touch "$fixture/dev/snd/controlC2" "$fixture/dev/snd/pcmC2D0c" "$fixture/dev/snd/pcmC2D0p"
printf '%s\n' '#!/usr/bin/env bash' \
  'case "$*" in' \
  '  *controlC2*) printf "%s\n" "ID_VENDOR_ID=2c7c" "ID_SERIAL_SHORT=0123456789ABCDEF" ;;' \
  '  *ttyUSB2*) printf "%s\n" "ID_VENDOR_ID=2c7c" "ID_SERIAL_SHORT=0123456789ABCDEF" "ID_USB_INTERFACE_NUM=02" "ID_PATH=pci-test-usb-0:1:1.2" ;;' \
  'esac' \
  > "$fixture/bin/udevadm"
chmod +x "$fixture/bin/udevadm"
PATH="$fixture/bin:$PATH"

declare -A audio_control=() audio_capture=() audio_playback=()
discover_audio_nodes "$fixture/dev"
[[ "${audio_control[0123456789ABCDEF]}" == "$fixture/dev/snd/controlC2" ]]
[[ "${audio_capture[0123456789ABCDEF]}" == "$fixture/dev/snd/pcmC2D0c" ]]
[[ "${audio_playback[0123456789ABCDEF]}" == "$fixture/dev/snd/pcmC2D0p" ]]

rule="$(write_tty_rule 0123456789ABCDEF 02 1)"
grep -Fq 'ENV{ID_MM_DEVICE_IGNORE}="1"' <<< "$rule"
grep -Fq 'ENV{ID_USB_INTERFACE_NUM}=="02"' <<< "$rule"
grep -Fq 'SYMLINK+="sim-deck/modem-1-at"' <<< "$rule"

touch "$fixture/dev/ttyUSB2"
inventory="$(SIM_DECK_DEV_ROOT="$fixture/dev" main)"
grep -Fq 'Discovered 1 modem(s). No files were changed.' <<< "$inventory"
grep -Fq "at_device: $fixture/dev/ttyUSB2" <<< "$inventory"
grep -Fq "audio_capture: $fixture/dev/snd/pcmC2D0c" <<< "$inventory"

printf '%s\n' 'install-linux-device tests passed'
