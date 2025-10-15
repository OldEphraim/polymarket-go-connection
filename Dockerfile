# syntax=docker/dockerfile:1.7

########## builder ##########
FROM golang:1.23.5-alpine AS go-builder
WORKDIR /app

# minimal toolchain for some deps (git); build-base helps if a dep needs cgo later
RUN apk add --no-cache git

# 1) Prime module cache without copying full repo (keeps cache stable)
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

# 2) Now copy the rest
COPY . .

# 3) Build all binaries with build cache mounts
# -trimpath and ldflags shrink binaries and make builds more cacheable
ENV CGO_ENABLED=0 GOOS=linux
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-s -w" -o orchestrator ./main.go

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-s -w" -o /app/bin/gatherer  ./cmd/gatherer/main.go

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-s -w" -o /app/bin/api       ./cmd/api/main.go

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-s -w" -o /app/bin/archiver  ./cmd/archiver/main.go

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-s -w" -o /app/bin/janitor   ./cmd/janitor/main.go

# 4) Build all strategies if present
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    mkdir -p /app/bin && \
    for dir in strategies/*/; do \
      if [ -f "$dir/main.go" ]; then \
        name=$(basename "$dir"); \
        echo "Building strategy: $name"; \
        go build -trimpath -ldflags="-s -w" -o "/app/bin/$name" "./$dir/main.go"; \
      fi; \
    done

# 5) Install goose once (kept in builder, copied later)
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOBIN=/app/bin go install github.com/pressly/goose/v3/cmd/goose@v3.19.1


########## runtime ##########
FROM alpine:latest
WORKDIR /app

# Basic tooling for healthchecks & psql client
RUN apk add --no-cache postgresql-client ca-certificates tzdata curl && update-ca-certificates

# Orchestrator and binaries
COPY --from=go-builder /app/orchestrator /app/
COPY --from=go-builder /app/bin/* /usr/local/bin/

# Configs for strategies/etc
COPY configs/ /app/configs/

ENV RUN_TYPE=prod

# Optionally set a default healthcheck (container-level)
# HEALTHCHECK --interval=30s --timeout=3s --retries=3 \
#   CMD curl -fsS http://localhost:8080/health || exit 1