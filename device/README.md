# sim-deck-device

`sim-deck-device` controls one Quectel modem, encrypts messages at the endpoint,
and bridges cellular voice audio to an end-to-end encrypted WebRTC session.

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
- one stable raw PCM path and the exact ALSA control, capture, and playback
  device nodes for that modem;
- one persistent `/var/lib/sim-deck` volume;
- one short-lived, one-time pairing token.

Never share or copy a state volume between two running modem containers. The
volume contains the modem endpoint's UUID, Ed25519 signing identity, X25519
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

Use `SIM_DECK_AUDIO_MODE=raw-pcm` only when the modem firmware exposes a tested
full-duplex raw PCM character device. That mode opens `SIM_DECK_PCM_DEVICE`,
which defaults to `/dev/sim-deck/pcm`, directly. Audio mode is configured per
container; one modem may use UAC/ALSA while another uses raw PCM.

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

## Pairing with the server

The owner-authenticated server endpoint `POST /v1/device-pairing-tokens` creates
a token valid for ten minutes. Create a different token for every modem and
write only the token value to the matching file under `secrets/`. Keep those
files mode `0600` and do not place tokens in environment variables or commit
them.

On first startup, the agent:

1. creates and durably stores a UUID, Ed25519 identity, X25519 command key, and pending credential;
2. sends the public identities and one-time token to the configured server over
   HTTPS;
3. stores the server-assigned SIM numeric ID after the server confirms pairing;
4. becomes ready only after the binding has been persisted.

If the request is interrupted, the same pending credential is reused so the
one-time token can be retried safely. A bound state volume refuses to connect to
a different server URL. To move intentionally to another server, revoke the old
device and pair with a new empty state volume.

After the first successful binding, the consumed token is no longer needed.
The secret mount may be removed from that service; subsequent starts use the
credential in its private state volume.

## Encrypted call commands

After pairing, the agent authenticates with its device credential and polls for
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

`compose.example.yaml` defines two fully isolated modem services. Create
`secrets/modem-1-pairing-token` and `secrets/modem-2-pairing-token`, then set the
server URL and host device group IDs:

```sh
export SIM_DECK_SERVER_URL=https://sim-deck.example
export SIM_DECK_DIALOUT_GID="$(getent group dialout | cut -d: -f3)"
export SIM_DECK_AUDIO_GID="$(getent group audio | cut -d: -f3)"
docker compose -f compose.example.yaml up --build
```

The server sees two independent device UUIDs and allocates the smallest free SIM
numeric ID to each. To add another modem, copy one service block and change all
host device paths, the state volume, the pairing secret, and the host health
port. Do not add multiple modem mappings to one container.

The local readiness endpoints in the example are
`http://127.0.0.1:18081/healthz` and `http://127.0.0.1:18082/healthz`. Readiness
becomes healthy only after the AT port is initialized and server binding has
succeeded.

Plain HTTP is rejected by default. It can be enabled only for an isolated local
development network with `SIM_DECK_ALLOW_INSECURE_HTTP=true`; production pairing
must use HTTPS.
