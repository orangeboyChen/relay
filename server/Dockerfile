# syntax=docker/dockerfile:1.7

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/sim-deck-server ./cmd/sim-deck-server

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata && addgroup -S simdeck && adduser -S -G simdeck simdeck
COPY --from=build /out/sim-deck-server /usr/local/bin/sim-deck-server
RUN mkdir -p /var/lib/sim-deck/attachments && chown -R simdeck:simdeck /var/lib/sim-deck
USER simdeck
EXPOSE 8080 49152/udp 49153/tcp
ENTRYPOINT ["/usr/local/bin/sim-deck-server"]
