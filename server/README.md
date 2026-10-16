# sim-deck-server

`sim-deck-server` is the control plane for sim-deck. It stores encrypted event envelopes, manages device and app identities, delivers push notifications, and relays encrypted signaling without accessing message or call content.

## Development

```sh
go test ./...
./scripts/test-coverage.sh
```

The default suite uses in-memory repositories for API and lifecycle behavior.
It enforces at least 90% statement coverage for that unit-test scope without a
database. PostgreSQL-specific migrations, constraints, and transaction behavior
remain in the focused integration suite because an in-memory repository cannot
prove those database semantics.

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
container.

VoIP pushes use an Apple token-signing `.p8` key mounted as a Docker secret.
Set `APNS_KEY_ID`, `APNS_TEAM_ID`, `APNS_TOPIC`, and `APNS_PRIVATE_KEY_FILE` in
`.env`. The topic must be the iOS bundle identifier followed by `.voip`. The
server forwards only opaque encrypted call deliveries, expires queued incoming
calls after two minutes, and removes device tokens that APNs reports invalid.

```sh
docker compose up --build
```

The server container supports `linux/amd64` and `linux/arm64`. TLS can terminate in the server with `TLS_CERT_FILE` and `TLS_KEY_FILE`, or in a trusted reverse proxy in front of port 8080.
`SERVER_PORT` controls the published host port without implying which protocol
terminates there. With the provided Compose file the server listens for HTTP on
container port 8080, so use a trusted TLS reverse proxy unless you also mount a
certificate and configure both TLS environment variables.
The image health check probes `/healthz` over HTTP by default and switches to
HTTPS when `TLS_CERT_FILE` is set. Keep `HTTP_ADDR` on port 8080 when using the
image default, or override the Compose health check together with the listener
address.

If no admin password secret exists during first startup, the server generates a
random password and writes it to the log exactly once. A recovery public key is
always required before the owner account can be created.
