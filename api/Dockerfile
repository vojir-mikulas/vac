# syntax=docker/dockerfile:1.7

# Build context is the repo root (see compose.yaml).

# Build UI (pnpm workspace; lock + workspace config live at the root)
FROM node:22-alpine AS ui
RUN corepack enable
WORKDIR /src
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY ui/package.json ui/
RUN pnpm install --frozen-lockfile --filter ui...
COPY ui/ ui/
RUN pnpm --filter ui build

# Build Go binary with the embedded UI
FROM golang:1.25-alpine AS api
WORKDIR /src/api
COPY api/go.mod api/go.sum ./
RUN go mod download
COPY api/ ./
COPY --from=ui /src/api/internal/ui/dist ./internal/ui/dist
RUN CGO_ENABLED=0 go build -tags embedui -ldflags="-s -w" -o /out/vac-api .

# Runtime — debian-slim because Phase 2 needs git + docker CLI on PATH.
# Distroless cannot host these (no apt, no libc shims, no shell).
FROM debian:bookworm-slim AS runtime

ENV DEBIAN_FRONTEND=noninteractive

# Base tools + Docker CLI repo prerequisites.
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        git \
        gnupg && \
    rm -rf /var/lib/apt/lists/*

# Docker CLI + Compose v2 plugin. CLI only — no daemon inside the container;
# the host's /var/run/docker.sock is bind-mounted at runtime.
RUN install -m 0755 -d /etc/apt/keyrings && \
    curl -fsSL https://download.docker.com/linux/debian/gpg -o /etc/apt/keyrings/docker.asc && \
    chmod a+r /etc/apt/keyrings/docker.asc && \
    echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/debian bookworm stable" \
        > /etc/apt/sources.list.d/docker.list && \
    apt-get update && \
    apt-get install -y --no-install-recommends \
        docker-ce-cli \
        docker-compose-plugin && \
    rm -rf /var/lib/apt/lists/*

# Non-root vac user. UID/GID 1000 is conventional and matches most Linux
# desktops. Supplementary group membership for the docker socket is added at
# `docker run` time via `--group-add ${DOCKER_GID}` (see compose.yaml).
RUN groupadd --system --gid 1000 vac && \
    useradd  --system --uid 1000 --gid vac \
             --home-dir /var/lib/vac --create-home \
             --shell /usr/sbin/nologin vac

# Pre-create the repo working directory with vac ownership so the mounted
# named volume inherits these permissions on first use.
RUN mkdir -p /var/lib/vac/repos && chown vac:vac /var/lib/vac/repos

COPY --from=api /out/vac-api /usr/local/bin/vac-api

USER vac:vac
WORKDIR /var/lib/vac

EXPOSE 3000
ENTRYPOINT ["/usr/local/bin/vac-api"]
