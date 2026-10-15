# syntax=docker/dockerfile:1.7

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
ARG TARGETOS=linux
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/sim-deck-device ./cmd/sim-deck-device

FROM scratch
COPY --from=build /out/sim-deck-device /sim-deck-device
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/sim-deck-device"]
