# syntax=docker/dockerfile:1
# Multi-arch Dockerfile (amd64, arm64, arm/v7 supported)
ARG TARGETPLATFORM
ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT

############
# Builder
############
FROM --platform=$BUILDPLATFORM golang:1.24.7-alpine3.22 AS builder
ARG TARGETARCH
ARG TARGETVARIANT

ENV GOPATH=/go
WORKDIR /go/src/project

# build deps
RUN apk add --no-cache git ca-certificates bash && update-ca-certificates

# cache modules
COPY go.mod go.sum ./
RUN go mod download

# copy source
COPY . .

# Build the two binaries into /out for the current target platform
RUN set -eux; \
    mkdir -p /out; \
    ARCH="${TARGETARCH}"; \
    GOARM=""; \
    if [ "$ARCH" = "arm" ] && [ -n "$TARGETVARIANT" ]; then \
      # convert v7 -> 7 etc.
      GOARM="$(echo ${TARGETVARIANT} | sed 's/^v//')"; \
    fi; \
    echo "Building for ARCH=${ARCH} GOARM=${GOARM:-unset}"; \
    if [ -n "$GOARM" ]; then \
      CGO_ENABLED=0 GOOS=linux GOARCH=${ARCH} GOARM=${GOARM} GO111MODULE=on  \
        go build -a -installsuffix cgo -ldflags '-w' -o /out/leader-elector ./election/example/main.go; \
      CGO_ENABLED=0 GOOS=linux GOARCH=${ARCH} GOARM=${GOARM} GO111MODULE=off \
        go build -a -installsuffix cgo -ldflags '-w' -o /out/hostname hostname.go; \
    else \
      CGO_ENABLED=0 GOOS=linux GOARCH=${ARCH} GO111MODULE=on \
        go build -a -installsuffix cgo -ldflags '-w' -o /out/leader-elector ./election/example/main.go; \
      CGO_ENABLED=0 GOOS=linux GOARCH=${ARCH} GO111MODULE=off \
        go build -a -installsuffix cgo -ldflags '-w' -o /out/hostname hostname.go; \
    fi

############
# Final image
############
FROM --platform=$TARGETPLATFORM golang:1.24.7-alpine3.22

MAINTAINER Instana Engineering <support@instana.com>

# runtime deps and non-root user
RUN apk add --no-cache ca-certificates bash net-tools && update-ca-certificates && \
    addgroup -S appgroup && adduser -S -u 1001 -G appgroup appuser

# copy built binaries and runtime assets
COPY --from=builder /out/leader-elector /app/server
COPY --from=builder /out/hostname /app/hostname
COPY election/run.sh /app/run.sh

RUN chmod +x /app/run.sh /app/server /app/hostname

ENV KLOG_V=4

USER 1001
ENTRYPOINT ["/app/run.sh"]

