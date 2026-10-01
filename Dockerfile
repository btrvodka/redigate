# Cross-compiles on the build platform, no emulation is needed for multi-arch images.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/redigate ./cmd/redigate

FROM alpine:3.24

# Links the image published to ghcr.io with the repository.
LABEL org.opencontainers.image.source="https://github.com/btrvodka/redigate" \
      org.opencontainers.image.description="HTTP API for Redis and Valkey: standalone, cluster and sentinel" \
      org.opencontainers.image.licenses="MIT"

# CA certificates for TLS connections to redis come with the base image.
RUN adduser -D -H -u 10001 redigate

COPY --from=build /out/redigate /usr/local/bin/redigate

ENV HTTP_ADDR=:8080 \
    METRICS_ADDR=:9090
EXPOSE 8080 9090
USER redigate
ENTRYPOINT ["/usr/local/bin/redigate"]
