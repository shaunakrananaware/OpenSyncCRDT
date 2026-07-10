# OpenSyncCRDT — standard image (opensynccrdt/server:latest).
#
# A general-purpose runtime on debian-slim: familiar base, a shell for
# debugging, CA certificates for outbound webhook TLS.
#
# The binary is built once and fully statically linked. It must be built against
# glibc (not musl): the automerge-go dependency ships a prebuilt Rust archive
# that references glibc's LFS64 symbols (stat64/open64), which musl does not
# provide. `-tags netgo` gives a pure-Go DNS resolver so the static binary
# resolves webhook hostnames without needing glibc's NSS at runtime. The result
# runs unchanged on debian, alpine, or distroless.
#
# Build from the repository root:
#   docker build -f deploy/docker/Dockerfile -t opensynccrdt/server:latest .

FROM golang:1.23-bookworm AS build

WORKDIR /src

# Cache module downloads separately from source.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=docker
RUN CGO_ENABLED=1 go build -tags netgo \
    -ldflags "-s -w -X main.version=${VERSION} -linkmode external -extldflags '-static'" \
    -o /out/opensynccrdt ./cmd/opensynccrdt

# --- runtime ---------------------------------------------------------------
FROM debian:12-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --uid 10001 --create-home --shell /usr/sbin/nologin opensync

COPY --from=build /out/opensynccrdt /usr/local/bin/opensynccrdt

# Data directory for the default SQLite database. Mount a volume here to
# persist documents across container restarts.
RUN mkdir -p /data && chown opensync:opensync /data
VOLUME ["/data"]
ENV DATA_DIR=/data

USER opensync
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/opensynccrdt"]
