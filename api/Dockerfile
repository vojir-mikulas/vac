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
FROM golang:1.24-alpine AS api
WORKDIR /src/api
COPY api/go.mod api/go.sum ./
RUN go mod download
COPY api/ ./
COPY --from=ui /src/api/internal/ui/dist ./internal/ui/dist
RUN CGO_ENABLED=0 go build -tags embedui -ldflags="-s -w" -o /out/vac-api .

# Runtime
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=api /out/vac-api /vac-api
EXPOSE 3000
USER nonroot:nonroot
ENTRYPOINT ["/vac-api"]
