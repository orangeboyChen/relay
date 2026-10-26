# Linux Modem Deployment

This guide runs `relay-node` on a Linux host with one or more Quectel USB
modems. The recommended Docker deployment uses one node container that maps
`/dev` and read-only `/sys`; the container discovers all modems and starts one
isolated worker per modem. The operator supplies the server URL and image tag;
no host discovery script is required.

The modem containers and the iPhone both connect outbound to `relay-control`.
The iPhone does not connect to a modem host and does not need a modem IP, USB
path, device UUID, SIM ICCID, or pairing token.

## Before plugging in the modem

Prepare:

- A supported 64-bit Linux host with Docker Engine and Docker Compose v2.
- Permission for the Docker daemon to bind the host `/dev` and `/sys`.
- Network access from the host to the server's HTTPS URL.
- The server stack already running with **Device Discovery** available in the
  iOS app. See [`relay-control/docs/deployment.md`](../../relay-control/docs/deployment.md).
- One persistent `/etc/sim-deck` directory and Docker volumes. Do not put the
  state volume on ephemeral storage.

The image runs as an unprivileged user and drops all Linux capabilities. The
node container maps the whole `/dev` only so it can discover hot-plugged modem
and ALSA nodes; do not use `--privileged`. ALSA PCM nodes are associated through
the matching USB serial in `/sys` because Linux does not copy USB identity
properties onto every `pcmC*` device node.

## Start the automatic node container

```sh
SIM_DECK_SERVER_URL=https://sim-deck.example.com \
  docker compose -f compose.node.yaml up -d
docker compose -f compose.node.yaml logs -f node
```

The first log entry contains a tree like `node → modem-1 → AT/audio nodes` for
every discovered modem. Each worker then logs its derived device identity and
waits for owner approval independently. The health endpoint is ready only after
all discovered workers are ready.

If a modem is added or removed, recreate the node container so the startup
topology is rescanned:

```sh
docker compose -f compose.node.yaml up -d --force-recreate node
```

The installer below remains a read-only Linux diagnostic tool or an explicit
single-modem deployment helper; it is not needed by the automatic node mode.

## Verify and enable modem audio

Voice requires the Quectel USB Audio Class (UAC) interfaces. Run the AT query
from a temporary serial tool before deploying the container:

```text
AT+QCFG="usbcfg"
```

Copy the value returned by that exact modem. Change only its final UAC flag to
`1`, send the command, and reboot the modem. Then verify the composition and
voice route again:

```text
AT+QCFG="usbcfg"
AT+QPCMV=1,2
```

On Linux, both playback and capture must exist before installation:

```sh
cat /proc/asound/cards
aplay -l
arecord -l
```

The expected voice format is signed 16-bit little-endian, 8 kHz, mono. If UAC
is absent, stop and use a firmware/configuration that exposes it; do not guess
an ALSA node or map a random PCM device. The installer intentionally rejects a
modem without all three UAC nodes.

## Discover all modems

Plug in every modem that should run on this host. By default, the script only
reports Quectel USB serial identities and matching AT and UAC nodes. It does
not write udev rules, create `/etc/sim-deck`, restart services, or start a
container:

```sh
./scripts/install-linux-device.sh
```

Use this read-only mode to collect host paths for a Kubernetes or other
externally managed deployment.

## Install a Docker Compose deployment

Compose installation is an explicit opt-in. With `--install-compose`, the
script creates:

- `/etc/sim-deck/.env` with the server URL and HTTP safety setting;
- `/etc/sim-deck/compose.yaml` with one service and state volume per modem;
- `/etc/udev/rules.d/99-sim-deck.rules` with stable `/dev/sim-deck/modem-N-*`
  links.

Run it with a versioned image:

```sh
sudo SIM_DECK_SERVER_URL=https://sim-deck.example.com \
  SIM_DECK_DEVICE_IMAGE=ghcr.io/orangeboychen/sim-deck-device:x.y.z \
  ./scripts/install-linux-device.sh \
  --install-compose \
  --output-dir /etc/sim-deck
```

To start immediately, add `--start`. The installer prints the number of
modems found and the generated Compose command. It exits instead of guessing
when a serial identity is missing, duplicated, or missing an audio node.

For an isolated development LAN or encrypted VPN only, HTTP can be enabled:

```sh
sudo SIM_DECK_SERVER_URL=http://server.lan:8443 \
  SIM_DECK_ALLOW_INSECURE_HTTP=true \
  SIM_DECK_DEVICE_IMAGE=ghcr.io/orangeboychen/sim-deck-device:x.y.z \
  ./scripts/install-linux-device.sh --install-compose
```

Never enable that option on an untrusted network. Production deployments use
HTTPS even though message and call payloads are end-to-end encrypted.

### What the installer does not ask for

The following values are derived or persisted automatically:

| Value | Source |
| --- | --- |
| Device UUID | Modem equipment identity and the sorted set of physical SIM-slot keys. |
| Slot identity | Modem-reported physical slot index and a local opaque slot key. |
| SIM identity | The modem's current ICCID is hashed locally; the raw ICCID is not sent. |
| AT and ALSA paths | udev properties for the modem's stable USB serial. |
| Server credential and signing keys | Generated in the private state volume after discovery. |

Do not set `SIM_DECK_DEVICE_UUID` for a new deployment. It remains only as a
compatibility override for an existing state volume. Do not enter a SIM ICCID
or a modem IP address in Docker or the iOS app.

## Start the generated stack

The generated service names and health ports are deterministic:

```sh
docker compose --env-file /etc/sim-deck/.env \
  -f /etc/sim-deck/compose.yaml up -d
docker compose --env-file /etc/sim-deck/.env \
  -f /etc/sim-deck/compose.yaml ps
```

`modem-1` is reachable only from the host at `127.0.0.1:18081`, `modem-2` at
`127.0.0.1:18082`, and so on. These are local readiness endpoints, not app
endpoints. Check one after it is approved:

```sh
curl --fail-with-body http://127.0.0.1:18081/healthz
docker compose --env-file /etc/sim-deck/.env \
  -f /etc/sim-deck/compose.yaml logs --tail=100 modem-1
```

The container stays `starting` until AT initialization, hardware identity
derivation, server binding, and SIM synchronization have completed.

## Approve devices in the app

For the first start:

1. In Sim Deck, open **My > Device Management > Add Device**.
2. Turn on **Device Discovery**.
3. Start one service, for example `docker compose ... up -d modem-1`.
4. Wait for its candidate to appear in the app. Compare the displayed derived
   device UUID and identity fingerprint with the container log.
5. Tap **Allow**, then confirm the assigned numeric SIM ID and slot details.
6. Start and approve each remaining service one at a time.
7. Turn off **Device Discovery** when all intended modems are approved.

The server performs the binding. The app never opens a direct socket to the
modem host. Encrypted SMS and call commands are routed through the server, and
encrypted WebRTC media uses the server's TURN service.

## Multiple modems

One container controls exactly one modem. The installer maps each physical USB
serial to its own service, for example:

```text
modem-1 -> /dev/sim-deck/modem-1-at + modem-1-audio-*
modem-2 -> /dev/sim-deck/modem-2-at + modem-2-audio-*
```

Each service has a separate `modem-N-state` volume. Never share a state volume,
AT path, or ALSA node between services. The server assigns a distinct visible
numeric SIM ID after each approval.

When a modem is added or removed, unplug or stop the affected services and
rerun the installer. It backs up the previous udev rules before replacing
them. Review the generated Compose file before starting it, especially after a
hardware change.

## Updates, replacement, and removal

To update the agent, edit the generated Compose image to a new version or rerun
the installer with the new tag, then recreate the affected service:

```sh
docker compose --env-file /etc/sim-deck/.env \
  -f /etc/sim-deck/compose.yaml up -d --force-recreate modem-1
```

Keep the state volume when updating the same physical modem. It contains the
device signing key, command key, server credential, slot identity, and numeric
ID. Removing it causes a new identity and requires App approval again.

Before replacing or retiring a modem, revoke the device in the app, stop its
service, and remove only that service's state volume if deletion is intended:

```sh
docker compose --env-file /etc/sim-deck/.env \
  -f /etc/sim-deck/compose.yaml stop modem-1
docker volume ls --filter name=modem-1-state
docker volume rm <exact-modem-1-state-volume-name>
```

Verify the exact volume name with `docker volume ls` before deleting anything.
Deleting a modem slot does not delete the SIM identity; manage offline SIMs in
the app separately.

## Environment reference

The installer writes only the values needed for a normal Linux deployment:

| Variable | Default | When to change it |
| --- | --- | --- |
| `SIM_DECK_SERVER_URL` | none | Required. Use the server's reachable HTTPS URL. |
| `SIM_DECK_ALLOW_INSECURE_HTTP` | `false` | Development on a trusted LAN/VPN only. |
| `SIM_DECK_AT_DEVICE` | generated stable path | Only for a manually maintained Compose deployment. |
| `SIM_DECK_AUDIO_MODE` | `alsa` | Use `raw-pcm` only with a separately verified full-duplex PCM device, or `remote-pcm` with mutual TLS for a development bridge. |
| `SIM_DECK_STATE_DIRECTORY` | `/var/lib/sim-deck` | Keep this path on the private service volume. |
| `SIM_DECK_COMMAND_POLL_INTERVAL` | `1s` | Optional latency/load tuning. |
| `SIM_DECK_DISCOVERY_POLL_INTERVAL` | `2s` | Optional discovery tuning. |

The optional TLS AT and remote PCM variables are for development adapters and
must point at absolute paths mounted read-only. They are not needed when the
Linux host maps the modem's serial and ALSA devices directly.

## Troubleshooting

| Symptom | Check |
| --- | --- |
| `No Quectel modem ... stable USB serial` | `udevadm info --query=property --name=/dev/ttyUSB*`; confirm `ID_SERIAL_SHORT` exists and the modem is not hidden behind a USB hub that drops serial data. |
| Missing UAC nodes | `cat /proc/asound/cards`, `aplay -l`, `arecord -l`; enable UAC in the modem USB composition and reboot it. |
| Candidate does not appear | Discovery is enabled in the app, the server URL is reachable, and logs do not show TLS, DNS, or `device_discovery_closed`. |
| Container remains `starting` | Inspect `docker compose logs modem-N`; approval, AT readiness, SIM synchronization, or TURN configuration may still be pending. |
| Wrong modem assigned to a service | Stop the stack, rerun the installer, and verify the generated stable links. Never swap only `ttyUSB` numbers by hand. |
| Calls connect without audio | Confirm both UAC playback/capture nodes are mapped to the same modem service and that the server TURN port and relay range are open. |
| HTTP server URL rejected | Use HTTPS, or explicitly set `SIM_DECK_ALLOW_INSECURE_HTTP=true` only on a trusted development network. |

