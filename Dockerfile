# syntax=docker/dockerfile:1

FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/wakewrap ./cmd/wakewrap

FROM alpine:3.22
RUN apk add --no-cache ca-certificates \
    && addgroup -S -g 65532 wakewrap \
    && adduser -S -D -H -u 65532 -G wakewrap wakewrap
COPY --from=build /out/wakewrap /usr/local/bin/wakewrap
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/wakewrap"]
