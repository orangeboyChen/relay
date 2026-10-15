# sim-deck-server

`sim-deck-server` is the control plane for sim-deck. It stores encrypted event envelopes, manages device and app identities, delivers push notifications, and relays encrypted signaling without accessing message or call content.

## Development

```sh
go test ./...
```

## Containers

Create `secrets/admin_password.txt`, copy `.env.example` to `.env`, and replace every example secret before starting the stack.

```sh
docker compose up --build
```

The server container supports `linux/amd64` and `linux/arm64`. TLS can terminate in the server with `TLS_CERT_FILE` and `TLS_KEY_FILE`, or in a trusted reverse proxy in front of port 8080.
