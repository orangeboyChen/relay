# sim-deck-device

`sim-deck-device` controls one Quectel modem, encrypts messages at the endpoint, and bridges cellular voice audio to an end-to-end encrypted WebRTC session.

## Development

```sh
go test ./...
```

## Container

One container controls exactly one modem. Create stable udev symlinks for the
modem's AT and PCM serial interfaces, then map only those devices and `/dev/snd`.
The container does not require `--privileged` or Linux capabilities.

```sh
docker buildx build --platform linux/amd64,linux/arm64 -t sim-deck/device:dev .
SIM_DECK_DIALOUT_GID="$(getent group dialout | cut -d: -f3)" \
SIM_DECK_AUDIO_GID="$(getent group audio | cut -d: -f3)" \
docker compose -f compose.example.yaml up
```

The local readiness endpoint is `GET /healthz`. It becomes healthy after the
AT port is open and PDU mode has been enabled.
