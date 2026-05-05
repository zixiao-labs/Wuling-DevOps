# --- builder ---------------------------------------------------------------
# Multi-stage so the runtime image only carries the binary plus libgit2 and
# the git CLI (needed by the smart-HTTP handler to spawn upload-pack /
# receive-pack). cgo is required because the libgit2 wrapper crosses the FFI
# boundary, so we build inside a Debian image with the toolchain installed.
FROM golang:1.25-bookworm AS builder

RUN apt-get update && apt-get install -y --no-install-recommends \
        build-essential \
        pkg-config \
        libgit2-dev \
        ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src

# Pre-fetch deps to keep this layer warm across code edits.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Static-ish build: cgo on (we need libgit2), but pure-Go everywhere else.
ENV CGO_ENABLED=1 GOOS=linux
RUN go build -trimpath -ldflags="-s -w" -o /out/wuling-api ./cmd/wuling-api \
 && go build -trimpath -ldflags="-s -w" -o /out/wuling-migrate ./cmd/wuling-migrate

# --- runtime ---------------------------------------------------------------
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates \
        git \
        libgit2-1.5 \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --system --create-home --uid 10001 wuling

COPY --from=builder /out/wuling-api     /usr/local/bin/wuling-api
COPY --from=builder /out/wuling-migrate /usr/local/bin/wuling-migrate

# Create the repo root as root so the non-root runtime user can read/write it.
ENV WULING_REPO_ROOT=/var/lib/wuling/repos
RUN mkdir -p "$WULING_REPO_ROOT" && chown -R wuling:wuling /var/lib/wuling

USER wuling
WORKDIR /home/wuling

EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/wuling-api"]
