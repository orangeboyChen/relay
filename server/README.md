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

The base Compose stack does not require Apple credentials, so PostgreSQL,
owner bootstrap, iOS login, and modem pairing work before APNs is configured.
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
for a modem container.

Start the base stack and check readiness:

```sh
docker compose up --build -d
docker compose ps
curl --fail-with-body http://127.0.0.1:8443/healthz
```

The health URL uses HTTP when the provided base Compose stack is used directly.
Use the reverse-proxy HTTPS URL instead when TLS terminates in front of the
container.

### Create a one-time modem pairing token

The examples below require `curl` and `jq`. Read the administrator password
without placing it in shell history, sign in, and create a ten-minute token:

```sh
export SIM_DECK_SERVER_URL=https://sim-deck.example.com
read -r -s -p 'sim-deck admin password: ' SIM_DECK_ADMIN_PASSWORD
printf '\n'

session_json="$(
  jq -nc --arg password "$SIM_DECK_ADMIN_PASSWORD" \
    '{username:"admin",password:$password}' \
  | curl --fail-with-body -sS \
      -H 'Content-Type: application/json' \
      --data-binary @- \
      "$SIM_DECK_SERVER_URL/v1/auth/bootstrap"
)"
access_token="$(printf '%s' "$session_json" | jq -er '.access_token')"
unset SIM_DECK_ADMIN_PASSWORD session_json

pairing_json="$(curl --fail-with-body -sS -X POST \
  -H "Authorization: Bearer $access_token" \
  "$SIM_DECK_SERVER_URL/v1/device-pairing-tokens")"
printf '%s\n' "$pairing_json" | jq
pairing_token="$(printf '%s' "$pairing_json" | jq -er '.pairing_token')"
```

Write `pairing_token` to the matching device container's Docker secret file,
start that container before `expires_at`, then clear the shell variables. Use a
different token, state volume, and hardware mapping for every modem.
