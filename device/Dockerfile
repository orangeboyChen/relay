# syntax=docker/dockerfile:1.7

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
ARG TARGETOS=linux
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/sim-deck-device ./cmd/sim-deck-device

FROM alpine:3.22
RUN apk add --no-cache alsa-utils ca-certificates \
    && addgroup -S -g 65532 simdeck \
    && adduser -S -D -H -u 65532 -G simdeck simdeck \
    && install -d -o simdeck -g simdeck -m 0700 /var/lib/sim-deck
COPY --from=build /out/sim-deck-device /usr/local/bin/sim-deck-device
USER simdeck:simdeck
VOLUME ["/var/lib/sim-deck"]
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=30s --retries=3 \
    CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/sim-deck-device"]
