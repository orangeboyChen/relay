# syntax=docker/dockerfile:1.7

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src

COPY server/go.mod server/go.sum ./server/
COPY protocol/sdk/go.mod protocol/sdk/go.sum ./protocol/sdk/
COPY protocol/sdk/gen ./protocol/sdk/gen
RUN cd server \
    && go mod edit -replace=github.com/orangeboyChen/relay-sdk=../protocol/sdk \
    && go mod download
COPY server/cmd ./server/cmd
COPY server/internal ./server/internal
WORKDIR /src/server
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/sim-deck-server ./cmd/sim-deck-server

FROM alpine:3.22
RUN apk add --no-cache ca-certificates curl tzdata && addgroup -S simdeck && adduser -S -G simdeck simdeck
COPY --from=build /out/sim-deck-server /usr/local/bin/sim-deck-server
RUN mkdir -p /var/lib/sim-deck/attachments && chown -R simdeck:simdeck /var/lib/sim-deck
USER simdeck
EXPOSE 8080 49152/udp 49153/tcp
HEALTHCHECK --interval=10s --timeout=3s --start-period=20s --retries=3 \
    CMD if [ -n "$TLS_CERT_FILE" ]; then scheme=https; else scheme=http; fi; \
        curl --fail --silent --show-error --insecure --max-time 2 --output /dev/null "$scheme://127.0.0.1:8080/healthz"
ENTRYPOINT ["/usr/local/bin/sim-deck-server"]
