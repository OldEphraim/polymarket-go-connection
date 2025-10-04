FROM golang:1.23.5-alpine AS go-builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .

# Build the orchestrator
RUN CGO_ENABLED=0 GOOS=linux go build -o orchestrator ./main.go

# Build the gatherer
RUN CGO_ENABLED=0 GOOS=linux go build -o /app/bin/gatherer ./cmd/gatherer/main.go

# Build all strategies
RUN mkdir -p /app/bin && \
    for dir in strategies/*/; do \
        if [ -f "$dir/main.go" ]; then \
            strategy_name=$(basename "$dir"); \
            echo "Building strategy: $strategy_name"; \
            CGO_ENABLED=0 GOOS=linux go build -o "/app/bin/$strategy_name" "./$dir/main.go"; \
        fi \
    done

# Use a minimal base image since we don't need Python anymore
FROM alpine:latest
WORKDIR /app

# Install PostgreSQL client for debugging
RUN apk add --no-cache postgresql-client

# Copy the orchestrator
COPY --from=go-builder /app/orchestrator /app/

# Copy gatherer and strategy binaries
COPY --from=go-builder /app/bin/* /usr/local/bin/

# Copy configs
COPY configs/ /app/configs/

# Default to prod when running in Docker
ENV RUN_TYPE=prod

ENTRYPOINT ["/app/orchestrator", "--binary"]
CMD ["--run", "test"]