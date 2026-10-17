# Builds Ferry for Linux on Ubuntu 22.04, so it runs on 22.04 and anything newer (its glibc, GTK 3, WebKitGTK 4.1).
FROM golang:1.26-bookworm AS go
FROM ubuntu:22.04
RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev dpkg-dev ca-certificates \
 && rm -rf /var/lib/apt/lists/*
COPY --from=go /usr/local/go /usr/local/go
ENV PATH=/usr/local/go/bin:$PATH CGO_ENABLED=1 GOFLAGS=-buildvcs=false
