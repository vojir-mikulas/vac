# Build UI
FROM node:22-alpine AS ui
WORKDIR /src
RUN corepack enable
COPY ui/package.json ui/pnpm-lock.yaml ./ui/
WORKDIR /src/ui
RUN pnpm install --frozen-lockfile
COPY ui/ ./
RUN pnpm build

# Build Go binary with embedded UI
FROM golang:1.24-alpine AS api
WORKDIR /src
COPY api/go.mod api/go.sum* ./api/
WORKDIR /src/api
RUN go mod download
COPY api/ ./
COPY --from=ui /src/ui/dist ./internal/ui/dist
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/vac-api .

# Runtime
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=api /out/vac-api /vac-api
EXPOSE 3000
USER nonroot:nonroot
ENTRYPOINT ["/vac-api"]
