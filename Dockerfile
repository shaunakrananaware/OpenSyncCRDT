# OpenSyncCRDT — single static binary in a minimal image.
#
# SQLite requires CGO, so we build against musl in an Alpine builder and link
# statically, producing a binary that runs on a scratch/alpine base with no
# libc dependency.

FROM golang:1.23-alpine AS build

# build-base provides gcc/musl for the SQLite CGO dependency.
RUN apk add --no-cache build-base git

WORKDIR /src

# Cache module downloads separately from source.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=docker
RUN CGO_ENABLED=1 go build \
    -ldflags "-s -w -X main.version=${VERSION} -linkmode external -extldflags '-static'" \
    -o /out/opensynccrdt ./cmd/opensynccrdt

# --- runtime ---------------------------------------------------------------
FROM alpine:3.20

# ca-certificates lets the engine make outbound HTTPS webhook calls.
RUN apk add --no-cache ca-certificates && \
    adduser -D -u 10001 opensync

COPY --from=build /out/opensynccrdt /usr/local/bin/opensynccrdt

# Data directory for the default SQLite database. Mount a volume here to
# persist documents across container restarts.
RUN mkdir -p /data && chown opensync:opensync /data
VOLUME ["/data"]
ENV DATA_DIR=/data

USER opensync
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/opensynccrdt"]
