# syntax=docker/dockerfile:1

FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/wakewrap ./cmd/wakewrap

FROM alpine:3.22
LABEL org.opencontainers.image.source="https://github.com/bourgils/wakewrap"
LABEL org.opencontainers.image.description="On-demand TCP wake and idle shutdown wrapper for Docker containers"
ENV DOCKER_HOST=tcp://127.0.0.1:2375 \
    WAKE_IDLE=15m \
    WAKE_PULL=missing \
    WAKE_STOP_TIMEOUT=10s \
    WAKE_START_TIMEOUT=60s \
    WAKE_DISCOVERY_TIMEOUT=60s \
    WAKE_DISCOVERY_SETTLE=1s \
    WAKE_CONTROL_NETWORKS=wake-control \
    WAKE_EXCLUDED_MOUNTS=/docker-control,/var/run/docker.sock \
    WAKE_HEALTH_PORT=18080 \
    WAKE_DISCOVERY_CONCURRENCY=256 \
    WAKE_UPSTREAM_TLS=false \
    WAKE_UPSTREAM_TLS_INSECURE_SKIP_VERIFY=false
RUN apk add --no-cache ca-certificates \
    && addgroup -S -g 65532 wakewrap \
    && adduser -S -D -H -u 65532 -G wakewrap wakewrap
COPY --from=build /out/wakewrap /usr/local/bin/wakewrap
USER 65532:65532
HEALTHCHECK --interval=10s --timeout=5s --start-period=10m --retries=3 \
    CMD ["sh", "-c", "wget -q -O /dev/null \"http://127.0.0.1:${WAKE_HEALTH_PORT:-18080}/healthz\""]
ENTRYPOINT ["/usr/local/bin/wakewrap"]
