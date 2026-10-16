# sim-deck-device

`sim-deck-device` controls one Quectel modem and bridges cellular voice audio to
an end-to-end encrypted WebRTC session.

Incoming SMS decoding, multipart assembly, history import, and durable
deduplication are implemented. Complete messages remain in an encrypted local
pending queue until a downstream encrypted synchronization sink acknowledges
them. The server and iOS SMS synchronization path is not implemented yet, so
the agent deliberately reports SMS delivery as unavailable instead of marking
messages delivered or silently discarding their content.

## Development

```sh
go test ./...
go test -race ./...
go vet ./...
make coverage
```

`make coverage` fails when repository-wide statement coverage is below 90%.

## Container model

One container controls exactly one modem. Every container must have all of the
following resources kept separate from every other instance:

- one stable AT device path;
- either one separately verified raw PCM path or the exact ALSA control,
  capture, and playback device nodes for that modem;
- one persistent `/var/lib/sim-deck` volume;
- one operator-assigned `SIM_DECK_DEVICE_UUID`;

Never share or copy a state volume between two running modem containers. The
configured UUID is immutable for that state volume. Startup fails if
`SIM_DECK_DEVICE_UUID` does not match the persisted identity. The volume also
contains the modem endpoint's Ed25519 signing identity, X25519
command-decryption identity, server URL, device credential, and visible SIM numeric ID. Copying it would create two
agents with the same identity. Moving the volume together with the same physical
modem is appropriate when replacing a host.

The image contains CA certificates for HTTPS and runs as UID/GID 65532 with a
read-only root filesystem in the Compose example. It does not need
`--privileged` or Linux capabilities. Only the required character devices are
mapped into each container. The image includes `alsa-utils` for its `arecord`
and `aplay` PCM bridges.

## Audio backends

`SIM_DECK_AUDIO_MODE=alsa` is the default. The agent captures and plays signed
16-bit little-endian, 8 kHz, mono PCM through the ALSA names configured by
`SIM_DECK_ALSA_CAPTURE_DEVICE` and `SIM_DECK_ALSA_PLAYBACK_DEVICE`. Both default
to `hw:0,0`. The Compose example maps only one modem's control, capture, and
playback nodes into each container as card zero, so identical ALSA names remain
isolated even when several containers run on one host.

### Provision Quectel UAC before starting the container

Quectel USB voice has two separate switches:

1. `AT+QCFG="usbcfg"` stores whether the USB Audio Class interface is part of
   the persistent USB composition. Its final value is the UAC flag. A change to
   this composition takes effect after the modem reboots.
2. `AT+QPCMV=1,2` enables the voice PCM route to that sound card for the current
   modem boot. This setting is not persistent. The device agent issues it and
   verifies `+QPCMV: 1,2` on every startup when audio mode is `alsa`.

Provision each modem once from its AT port before starting its container:

```text
AT+QCFG="usbcfg"
+QCFG: "usbcfg",0x2C7C,0x0125,1,1,1,1,1,0,0

AT+QCFG="usbcfg",0x2C7C,0x0125,1,1,1,1,1,0,1
OK
```

The values above are an example. Always copy the values returned by that
specific modem and change only the final UAC flag from `0` to `1`. Changing or
reordering any other flag can remove its AT, diagnostic, modem, or network
interface. Reboot the modem, query `AT+QCFG="usbcfg"` again, and require the
final value to be `1`.

On the Linux host, verify that the re-enumerated modem exposes both playback
and capture before creating udev links:

```sh
cat /proc/asound/cards
aplay -l
arecord -l
```

The EC20/EC25 UAC format is fixed at signed 16-bit, 8 kHz, mono. If the final
UAC flag is `0`, missing, or the firmware rejects `AT+QPCMV=1,2`, do not invent
a PCM serial mapping. Use matching UAC-capable firmware from the module vendor
or a separately verified hardware PCM interface. The agent now refuses ALSA
readiness when the composition is disabled or UAC voice routing cannot be
confirmed.

Use `SIM_DECK_AUDIO_MODE=raw-pcm` only when the modem firmware exposes a tested
full-duplex raw PCM character device. That mode opens `SIM_DECK_PCM_DEVICE`,
which defaults to `/dev/sim-deck/pcm`, directly. The Compose example defaults
to ALSA and therefore does not require or map a raw PCM device. For raw PCM,
remove that service's three `/dev/snd` mappings, add
`HOST_PCM_PATH:/dev/sim-deck/pcm`, and set `SIM_DECK_AUDIO_MODE=raw-pcm`.
Audio mode is configured per container; one modem may use UAC/ALSA while
another uses raw PCM.

## Stable Linux device paths

USB enumeration names such as `/dev/ttyUSB2` and ALSA card numbers can change
after a reboot. Inspect each modem before installing rules:

```sh
udevadm info --attribute-walk --name=/dev/ttyUSB2
udevadm info --attribute-walk --name=/dev/snd/pcmC1D0c
```

Copy `deploy/99-sim-deck.rules.example` to
`/etc/udev/rules.d/99-sim-deck.rules`, then replace the example serial numbers
and USB interface numbers with values from the host. Reload the rules and
reconnect the modems:

```sh
sudo udevadm control --reload-rules
sudo udevadm trigger
```

Verify that every path under `/dev/sim-deck` resolves to the intended modem.
Do not start Compose if an AT, PCM, or audio path is missing or points to another
physical device. If a modem does not expose a unique serial number, replace the
serial match with a stable physical USB-port match from the `udevadm` attribute
walk; never distinguish identical modems by their transient `ttyUSB` number.

For local Docker Desktop end-to-end testing on a host that cannot pass the USB
interface into the Linux VM, `SIM_DECK_AT_DEVICE` may instead use an explicit
`tls://host:port` endpoint. Set `SIM_DECK_AT_TLS_CA_FILE`,
`SIM_DECK_AT_TLS_CERT_FILE`, and `SIM_DECK_AT_TLS_KEY_FILE` to absolute paths
mounted read-only in the container. The agent requires mutual TLS 1.3, validates
the bridge hostname, authenticates itself with a client certificate, and rejects
plaintext network endpoints. This is a development adapter only; Linux
deployments should continue to map the modem TTY directly into the container.

The development TLS bridge used on macOS transports only the EC20 AT bulk
interface. It does not transport the modem's USB Audio Class isochronous
endpoints. Therefore it is suitable for SMS, radio status, and call-control
tests, but it cannot carry call audio. Do not configure `SIM_DECK_PCM_DEVICE`
to `/dev/zero` or another synthetic device to make that setup start; the agent
rejects those paths. Real audio tests require a Linux host with the modem's
ALSA capture and playback nodes mapped into the container (or a separately
verified full-duplex raw PCM device).

## App-controlled discovery

Device admission is controlled from the iOS app and does not require copying a
pairing token into Docker. The intended first-start sequence is:

1. In the app, open device management and enable device discovery. The server
   accepts new candidates for ten minutes or until discovery is disabled.
2. Start one modem container. It connects to the configured server and appears
   as a pending device in the app.
3. Compare the immutable device UUID and public-key fingerprint shown in the
   app with the deployment you started, then tap Allow or Don't Allow.
4. After allowing the candidate, configure its editable device and line names.
   The server assigns the smallest free visible SIM numeric ID atomically.
5. Disable device discovery when no more modems need to be admitted.

On first startup, the agent:

1. validates the operator-supplied `SIM_DECK_DEVICE_UUID` and binds it to the
   private state volume;
2. creates and durably stores an Ed25519 identity, X25519 command key, and random
   pending credential;
3. signs its discovery request and sends only public keys plus the pending
   credential to the server over HTTPS;
4. polls with that pending credential until the owner allows or rejects it;
5. stores the server-assigned SIM numeric ID after approval;
6. becomes ready only after the binding has been persisted.

If the request is interrupted, the same pending credential is reused. If the
server approved the device just before a container restart, the agent retrieves
that result instead of creating a second candidate. A bound state volume
refuses to connect to a different server URL. To move intentionally to another
server, revoke the old device and start with a new private state volume.

## Encrypted call commands

After approval, the agent authenticates with its device credential and polls for
opaque commands. The iPhone encrypts every call operation directly to the
device's X25519 public key with AES-256-GCM. The server can route and expire a
command but cannot read the number, call identifier, DTMF digits, or execution
result.

The agent decrypts a command locally and maps `call.start`, `call.end`,
`call.hold`, `call.resume`, `call.swap`, and `call.dtmf` to the modem's serialized
AT controller. Results are encrypted back to the initiating iPhone before they
leave the container. An upgraded version-one state volume keeps its UUID,
Ed25519 identity, credential, and SIM ID while generating and registering one
new X25519 command key.

`SIM_DECK_COMMAND_POLL_INTERVAL` defaults to `1s`. Lower values reduce command
latency but increase HTTP request volume. Each modem container polls only its
own queue and must keep its state volume private.

## Running two modems

`compose.example.yaml` defines two fully isolated modem services. Set the
server URL, a different UUID for each modem, and the host device group IDs:

```sh
export SIM_DECK_SERVER_URL=https://sim-deck.example
export SIM_DECK_MODEM_1_DEVICE_UUID=01900000-0000-7000-8000-000000000001
export SIM_DECK_MODEM_2_DEVICE_UUID=01900000-0000-7000-8000-000000000002
export SIM_DECK_DIALOUT_GID="$(getent group dialout | cut -d: -f3)"
export SIM_DECK_AUDIO_GID="$(getent group audio | cut -d: -f3)"
docker compose -f compose.example.yaml up --build
```

Generate a different UUID for every modem container and keep it with that
modem's deployment configuration. The server sees two independent device UUIDs and allocates the smallest free SIM
numeric ID to each. To add another modem, copy one service block and change all
host device paths, the configured UUID, the state volume, and the host health
port. Do not add multiple modem mappings to one container.

The local readiness endpoints in the example are
`http://127.0.0.1:18081/healthz` and `http://127.0.0.1:18082/healthz`. Readiness
becomes healthy only after the AT port is initialized and server binding has
succeeded. The image declares the same probe as its Docker health check, so a
container stays `starting` or becomes `unhealthy` instead of appearing ready
while its modem or server binding is unavailable.

Plain HTTP is rejected by default. It can be enabled only for an isolated local
development network or encrypted VPN with
`SIM_DECK_ALLOW_INSECURE_HTTP=true`; production discovery over an untrusted
network must use HTTPS because device credentials still require transport
protection even though message and call content is end-to-end encrypted.

## Start and approve the first Linux modem

Set the server URL and the stable host paths created by the udev rules. These
example paths must be replaced with the paths for the first physical modem:

```sh
export SIM_DECK_SERVER_URL=https://sim-deck.example.com
export SIM_DECK_MODEM_1_DEVICE_UUID=01900000-0000-7000-8000-000000000001
export SIM_DECK_MODEM_1_AT_DEVICE=/dev/sim-deck/modem-1-at
export SIM_DECK_MODEM_1_AUDIO_CONTROL=/dev/sim-deck/modem-1-audio-control
export SIM_DECK_MODEM_1_AUDIO_CAPTURE=/dev/sim-deck/modem-1-audio-capture
export SIM_DECK_MODEM_1_AUDIO_PLAYBACK=/dev/sim-deck/modem-1-audio-playback
export SIM_DECK_DIALOUT_GID="$(getent group dialout | cut -d: -f3)"
export SIM_DECK_AUDIO_GID="$(getent group audio | cut -d: -f3)"
```

For a server reached over plain HTTP on a trusted LAN or encrypted VPN, also
set `SIM_DECK_ALLOW_INSECURE_HTTP=true`. Do not enable that option across an
untrusted network.

Enable device discovery in the iOS app, then start only the first modem service
and inspect its logs:

```sh
docker compose -f compose.example.yaml up --build -d modem-1
docker compose -f compose.example.yaml ps modem-1
docker compose -f compose.example.yaml logs --tail=100 modem-1
```

The container remains `starting` while it is awaiting approval. In the app,
select the pending UUID, compare its fingerprint with the
`identity_fingerprint` value in the container log, and tap Allow. Then verify
readiness:

```sh
curl --fail-with-body http://127.0.0.1:18081/healthz
docker compose -f compose.example.yaml logs --tail=100 modem-1
```

A successful log entry contains `device agent ready`, the immutable device
UUID, and the assigned numeric SIM ID. Disable discovery in the app when the
last intended modem has joined. The server's authenticated
`GET /v1/devices` endpoint and the iOS device-management screen then show the
same device.

The named `modem-1-state` volume must remain attached to this modem. Removing
or replacing it creates new cryptographic keys. Revoke the old device first,
then enable discovery and approve the replacement using the configured UUID.
For a second modem, repeat the process with a distinct UUID, the `modem-2`
paths, and the independent `modem-2-state` volume.
