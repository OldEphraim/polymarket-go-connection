# --- builder ---
    FROM golang:1.23.5-alpine AS go-builder
    WORKDIR /app
    COPY go.mod go.sum ./
    RUN go mod download
    COPY . .
    
    # Build binaries
    RUN CGO_ENABLED=0 GOOS=linux go build -o orchestrator ./main.go
    RUN CGO_ENABLED=0 GOOS=linux go build -o /app/bin/gatherer ./cmd/gatherer/main.go
    RUN CGO_ENABLED=0 GOOS=linux go build -o /app/bin/api ./cmd/api/main.go
    
    # Build all strategies
    RUN mkdir -p /app/bin && \
        for dir in strategies/*/; do \
            if [ -f "$dir/main.go" ]; then \
                strategy_name=$(basename "$dir"); \
                echo "Building strategy: $strategy_name"; \
                CGO_ENABLED=0 GOOS=linux go build -o "/app/bin/$strategy_name" "./$dir/main.go"; \
            fi \
        done
    
    # Install goose (static)
    RUN CGO_ENABLED=0 GOBIN=/app/bin go install github.com/pressly/goose/v3/cmd/goose@v3.19.1
    
    # --- runtime ---
    FROM alpine:latest
    WORKDIR /app
    
    # Networking & TLS sanity + curl for API healthcheck
    RUN apk add --no-cache postgresql-client ca-certificates tzdata curl && update-ca-certificates
    
    # Copy orchestrator and binaries
    COPY --from=go-builder /app/orchestrator /app/
    COPY --from=go-builder /app/bin/* /usr/local/bin/
    
    # Copy configs
    COPY configs/ /app/configs/
    
    ENV RUN_TYPE=prod