# Sim Deck Deployment

This guide deploys the control plane and connects one or more Linux modem
hosts. The iPhone connects to the server only. It never needs a modem IP,
USB path, pairing token, or SIM ICCID.

## Topology

```text
iPhone  -- HTTPS / WebSocket / TURN -->  relay-control host
                                          |-- PostgreSQL
                                          |-- coturn
Linux modem host -- HTTPS ------------->  |
  |-- modem-1 container                 |
  |-- modem-2 container                 |
```

The server routes authenticated, opaque ciphertext. SMS, call commands, and
voice media remain end-to-end encrypted. coturn relays encrypted WebRTC media;
it does not decrypt it.

## Requirements

- A Linux host with Docker Engine and Docker Compose v2.
- A DNS name and TLS reverse proxy for `PUBLIC_BASE_URL`.
- TCP 443 reachable by the iPhone and modem hosts.
- TURN TCP/UDP 3478 and the configured UDP relay range reachable by the
  iPhone and modem hosts.
- A persistent filesystem for PostgreSQL and attachments.
- The `relay-node` Linux host for each USB modem. Follow its
  [`docs/deployment.md`](../../relay-node/docs/deployment.md) after this stack
  is healthy.

Do not use `localhost` or `127.0.0.1` for `PUBLIC_BASE_URL`; those addresses
would refer to the client itself. Do not expose PostgreSQL to the network.

## Prepare secrets

Run these commands from the `relay-control` checkout. Keep the recovery private
key outside Docker and outside backups containing the database. The server only
receives the matching public key. The Compose stack requires
`secrets/admin_password.txt` on first startup; it is intentionally not an
optional bind mount.

```sh
mkdir -p secrets
umask 077
openssl rand -hex 32 > secrets/postgres_password.txt
openssl rand -base64 32 > secrets/admin_password.txt
openssl rand -hex 32 > secrets/turn_shared_secret.txt
openssl genpkey -algorithm ED25519 -out secrets/recovery-private.pem
openssl pkey -in secrets/recovery-private.pem -pubout -outform DER \
  | tail -c 32 | openssl base64 -A | tr -d '=' \
  > secrets/recovery_public_key.txt
cp .env.example .env
```

Replace the example values in `.env`:

| Variable | Required | Meaning |
| --- | --- | --- |
| `POSTGRES_PASSWORD` | Yes | Database password. Use the same value in `postgres_password.txt` if you keep that file as a reference. |
| `PUBLIC_BASE_URL` | Yes | HTTPS URL reachable from both the iPhone and modem hosts. |
| `TURN_URL` | Yes | Public TURN URL, normally `turn:hostname:3478?transport=udp`. |
| `TURN_EXTERNAL_IP` | Yes | Public address coturn advertises. Use `public/private` when behind NAT. |
| `TURN_PORT` | No | TURN control port, default `3478`. |
| `TURN_MIN_PORT` / `TURN_MAX_PORT` | No | UDP relay range, default `49152-49252`. |
| `SERVER_PORT` | No | Host loopback port for the HTTP container, default `8443`. |
| `*_FILE` secret paths | Yes | Paths to the files created above. |

Set `POSTGRES_PASSWORD` in `.env` to the generated database password. The
Compose file uses it to construct the private PostgreSQL connection string.

The base stack intentionally does not publish server HTTP on a public
interface. Put a TLS reverse proxy on the same host and forward its upstream to
`127.0.0.1:${SERVER_PORT}`. The proxy must preserve WebSocket upgrade headers.
Set `PUBLIC_BASE_URL` to the proxy's HTTPS URL, not the loopback upstream.

## Start the control plane

```sh
docker compose config
docker compose up --build -d
docker compose ps
curl --fail-with-body http://127.0.0.1:8443/healthz
```

The health check only proves that the HTTP process is listening. Confirm that
PostgreSQL is healthy before onboarding a modem:

```sh
docker compose logs --tail=100 postgres server turn
docker compose exec postgres pg_isready -U simdeck -d simdeck
```

The server initializes its schema at startup; there is no separate migration
command in this deployment.

On the first successful startup the owner account is `admin`. Use the password
stored in `secrets/admin_password.txt` once, then immediately sign in from the
iOS app, register a passkey, and remove the temporary password from operational
notes. The server's generated-password fallback is only used by custom
launches that omit `ADMIN_PASSWORD_FILE`; the base Compose file always mounts
the explicit secret file.

Recovery requires the private key on the Linux host; it is not mounted into the
server container.

## Enable APNs and PushKit (optional)

SMS and call synchronization work without APNs while the app is active. To
enable background SMS notifications and incoming CallKit wakes, create the
Apple `.p8` key and run:

```sh
docker compose -f compose.yaml -f compose.apns.yaml up --build -d
```

Set `APNS_KEY_ID`, `APNS_TEAM_ID`, `APNS_TOPIC` (the `.voip` bundle identifier
for PushKit), and `APNS_PRIVATE_KEY_FILE` in `.env`. Never commit the `.p8` key.

## Add modem devices

Device admission is an explicit owner action in the iOS app:

1. Sign in and open **My > Device Management > Add Device**.
2. Enable **Device Discovery**. The server opens a ten-minute admission window.
3. On the Linux modem host, run the `relay-node` installer. It discovers every
   connected Quectel modem, derives each device UUID from the modem hardware and
   all physical slot keys, and starts one isolated container per modem.
4. In the app, compare the candidate's derived UUID and identity fingerprint
   with the container log, then choose **Allow**.
5. Repeat approval for each intended modem.
6. Disable **Device Discovery** as soon as the intended candidates are admitted.

No value is copied from the modem host into the app. In particular, do not set
`SIM_DECK_DEVICE_UUID`, enter an ICCID, or expose a modem address. The UUID is
immutable for that physical modem; the visible numeric SIM ID is allocated by
the server after approval and can be changed in the app later.

The same state volume and modem reuse the existing credential after a restart.
Deleting the volume or moving it to another modem creates a new cryptographic
identity and requires approval again. Revoke the old device in the app before
replacing it.

## Verify the binding

On the modem host, the device container should become healthy only after AT
initialization, hardware identity derivation, and server approval:

```sh
docker compose --env-file /etc/sim-deck/.env \
  -f /etc/sim-deck/compose.yaml ps
docker compose --env-file /etc/sim-deck/.env \
  -f /etc/sim-deck/compose.yaml logs --tail=100 modem-1
```

In the app, the device-management screen must show the same immutable device
UUID, assigned numeric ID, slot list, and online state. A modem container must
not be marked ready while it is waiting for approval.

## Operations

### Restart and upgrade

Keep the named `postgres-data`, `attachment-data`, and modem state volumes.
Pull or build a versioned image, then restart the relevant service. Do not run
two containers against the same modem state volume.

```sh
docker compose pull
docker compose up -d
docker compose ps
```

### Backups

Back up PostgreSQL and the encrypted attachment volume together. Back up the
recovery private key separately with stricter access controls. A database or
attachment export without endpoint keys must not reveal phone numbers, message
text, pictures, or audio.

### Removal

Revoke a device in **My > Device Management** before unplugging or deleting its
container. Removing a slot does not delete the SIM identity; deleting an offline
SIM is a separate action in the app. Never use `docker compose down -v` on a
production deployment unless you intentionally want to destroy the database,
attachments, and device credentials.

## Troubleshooting

| Symptom | Check |
| --- | --- |
| App cannot sign in | `PUBLIC_BASE_URL` DNS/TLS, reverse-proxy WebSocket upgrade, and `docker compose logs server`. |
| Modem candidate never appears | Discovery switch still on, modem host can reach the HTTPS URL, and the device container log has no TLS or `device_discovery_closed` error. |
| TURN call setup fails | DNS for `TURN_URL`, firewall for 3478 TCP/UDP and the whole relay range, and `TURN_EXTERNAL_IP` matching the address coturn advertises. |
| Server is healthy but app is offline | The proxy may point at the wrong loopback `SERVER_PORT`; verify `curl` locally and the proxy upstream. |
| Admin password lost | Use the host-only recovery procedure; do not mount the recovery private key into Docker. |
| Candidate has wrong hardware | Reject it, stop that modem service, and verify the Linux installer mapped stable USB serial and UAC nodes to the expected container. |

