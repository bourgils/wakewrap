# syntax=docker/dockerfile:1

FROM ghcr.io/tecnativa/docker-socket-proxy:v0.5.0@sha256:1f5038b54f06c3e18422902cf00ba21803d1c97805aae032e5e6673d532d3459

LABEL org.opencontainers.image.source="https://github.com/bourgils/wakewrap"
LABEL org.opencontainers.image.description="Loopback-only Docker socket proxy preset for WakeWrap"

ENV BIND_CONFIG=127.0.0.1:2375 \
    CONTAINERS=1 \
    IMAGES=1 \
    NETWORKS=1 \
    POST=1 \
    EVENTS=0

HEALTHCHECK --interval=10s --timeout=5s --start-period=10s --retries=3 \
    CMD ["wget", "-q", "-O", "/dev/null", "http://127.0.0.1:2375/_ping"]
