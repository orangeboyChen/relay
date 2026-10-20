# relay-control

`relay-control` is the control plane for sim-deck. It stores encrypted event envelopes, manages device and app identities, delivers push notifications, and relays encrypted signaling without accessing message or call content.

For the complete production procedure, including firewall rules, secret
creation, TLS, TURN, modem admission, backups, and recovery, see
[`docs/deployment.md`](docs/deployment.md).

## Development

```sh
go test ./...
./scripts/test-coverage.sh
```

The default suite uses in-memory repositories for API and lifecycle behavior.
It enforces at least 90% statement coverage for that unit-test scope without a
database. PostgreSQL-specific schema constraints and transaction behavior remain
in the focused integration suite because an in-memory repository cannot prove
those database semantics.

Set `TEST_DATABASE_URL` to run the PostgreSQL integration suite and enforce at
least 90% statement coverage across the complete repository:

```sh
TEST_DATABASE_URL='postgres://postgres:postgres@localhost:5432/sim_deck_test?sslmode=disable' \
  ./scripts/test-coverage.sh
```

## Containers

Create `secrets/admin_password.txt` and `secrets/recovery_public_key.txt`, copy
`.env.example` to `.env`, and replace every example secret before starting the
stack. The recovery public key file contains an unpadded base64 Ed25519 public
key. Keep its private key on the Linux host and never mount it into the server
container. Also create the coturn shared secret with `openssl rand -hex 32` and
point `TURN_SHARED_SECRET_FILE` at it. Set `TURN_URL` to the public TURN DNS
name and `TURN_EXTERNAL_IP` to the relay host's public IP (use coturn's
`public/private` form when the host is behind NAT). Open the TURN listening
port and configured UDP relay range on the server firewall. Modem containers
publish no media ports and never need a reachable address of their own.

The base Compose stack does not require Apple credentials, so PostgreSQL,
owner bootstrap, iOS login, and app-controlled modem discovery work before APNs is configured.
VoIP pushes use an Apple token-signing `.p8` key mounted through the optional
`compose.apns.yaml` overlay. Set `APNS_KEY_ID`, `APNS_TEAM_ID`, `APNS_TOPIC`, and
`APNS_PRIVATE_KEY_FILE` in `.env`. The topic must be the iOS bundle identifier
followed by `.voip`. The server forwards only opaque encrypted call deliveries,
expires queued incoming calls after two minutes, and removes device tokens that
APNs reports invalid.

```sh
docker compose up --build
```

Enable APNs after the credentials are ready:

```sh
docker compose -f compose.yaml -f compose.apns.yaml up --build -d
```

The server container supports `linux/amd64` and `linux/arm64`. TLS can terminate in the server with `TLS_CERT_FILE` and `TLS_KEY_FILE`, or in a trusted reverse proxy in front of port 8080.
The Compose file binds the plaintext host port to `127.0.0.1` so it cannot be
reached directly from another machine. Put a trusted TLS reverse proxy on the
host and forward to that loopback port, or configure TLS in the server. A proxy
running in another container should use a private Docker network instead of
publishing the server's plaintext port on a public interface.
The image health check probes `/healthz` over HTTP by default and switches to
HTTPS when `TLS_CERT_FILE` is set. Keep `HTTP_ADDR` on port 8080 when using the
image default, or override the Compose health check together with the listener
address.

If no admin password secret exists during first startup, the server generates a
random password and writes it to the log exactly once. A recovery public key is
always required before the owner account can be created.

### First server start

Create deployment secrets on the Linux host. The recovery private key must
remain outside every container and backup set that contains the server
database:

```sh
mkdir -p secrets
umask 077
openssl rand -base64 32 > secrets/admin_password.txt
openssl genpkey -algorithm ED25519 -out secrets/recovery-private.pem
openssl pkey -in secrets/recovery-private.pem -pubout -outform DER \
  | tail -c 32 \
  | openssl base64 -A \
  | tr -d '=' > secrets/recovery_public_key.txt
cp .env.example .env
```

Set a URL-safe `POSTGRES_PASSWORD`, the externally reachable
`PUBLIC_BASE_URL`, and `SERVER_PORT` in `.env`. For example, generate the
database password with `openssl rand -hex 32`. `PUBLIC_BASE_URL` must be the URL
that both the iPhone and device containers can reach; `localhost` is not valid
for a modem container. `TURN_URL` must resolve to the same server deployment,
and `TURN_EXTERNAL_IP` must be the address coturn advertises for relayed media.

Start the base stack and check readiness:

```sh
docker compose up --build -d
docker compose ps
curl --fail-with-body http://127.0.0.1:8443/healthz
```

The health URL uses HTTP when the provided base Compose stack is used directly.
Use the reverse-proxy HTTPS URL instead when TLS terminates in front of the
container.

### Add modem devices from the iOS app

Device admission is controlled by the owner in the iOS app. A token does not
need to be copied between the app and Docker:

1. Sign in to the server from the app and open **My > Device Management > Add Device**.
2. Turn on **Device Discovery**. The server accepts candidates for ten minutes,
   or until the switch is turned off.
3. Start one modem container. It derives its immutable device UUID from the
   modem's hardware identity and physical SIM-slot set; no UUID, SIM ICCID, or
   pairing token is entered by the operator.
4. Compare the derived UUID and identity fingerprint in the app with the device
   container log, then tap **Allow** or **Don't Allow**.
5. Repeat step 3 for additional modem containers. The Linux installer creates
   one stable device mapping and one private state volume per modem.
6. Turn off **Device Discovery** when the intended devices have joined.

The owner APIs are:

- `POST /v1/device-discovery-session` to open or replace the ten-minute window.
- `GET /v1/device-discovery-session` to read its current state.
- `DELETE /v1/device-discovery-session` to close it immediately.
- `GET /v1/device-discoveries` to list pending candidates.
- `POST /v1/device-discoveries/{device_uuid}/approve` to admit a candidate and
  atomically allocate the smallest free SIM numeric ID.
- `DELETE /v1/device-discoveries/{device_uuid}` to reject a candidate.

The modem submits a signed proof of possession and polls its result with a
private pending credential. The server stores only the credential hash. An
approved device persists its issued server credential in its private state
volume, so restarting the same container does not require another approval.
The server never needs a modem IP address: the iPhone and modem both connect
outbound to this control plane, and encrypted media is relayed through the
configured TURN service.

## License

Copyright (C) 2026 orangeboyChen.

Sim Deck Server is licensed under the GNU Affero General Public License
version 3 only (`AGPL-3.0-only`). See [LICENSE](LICENSE). If you run a modified
version for users over a network, Section 13 requires you to offer those users
the corresponding source code.
