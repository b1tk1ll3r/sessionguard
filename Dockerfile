FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/sessionguard-master ./cmd/master

FROM alpine:3.24
RUN apk add --no-cache ca-certificates && addgroup -S sessionguard && adduser -S -G sessionguard sessionguard && mkdir -p /var/lib/sessionguard /etc/sessionguard && chown -R sessionguard:sessionguard /var/lib/sessionguard /etc/sessionguard
COPY --from=build /out/sessionguard-master /usr/local/bin/sessionguard-master
USER sessionguard
VOLUME ["/var/lib/sessionguard"]
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --retries=3 CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/sessionguard-master"]
CMD ["-config", "/etc/sessionguard/master.json"]
