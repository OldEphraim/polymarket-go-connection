FROM golang:1.23.5-alpine AS go-builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .

# Build the orchestrator
RUN CGO_ENABLED=0 GOOS=linux go build -o orchestrator ./main.go

# Dynamically build all strategies found in strategies/ directory
RUN mkdir -p /app/bin && \
    for dir in strategies/*/; do \
        if [ -f "$dir/main.go" ]; then \
            strategy_name=$(basename "$dir"); \
            echo "Building strategy: $strategy_name"; \
            CGO_ENABLED=0 GOOS=linux go build -o "/app/bin/$strategy_name" "./$dir/main.go"; \
        fi \
    done

FROM python:3.11-slim
WORKDIR /app

# Install PostgreSQL client for debugging
RUN apt-get update && apt-get install -y postgresql-client && rm -rf /var/lib/apt/lists/*

# Copy the orchestrator
COPY --from=go-builder /app/orchestrator /app/

# Copy all strategy binaries to PATH
COPY --from=go-builder /app/bin/* /usr/local/bin/

# Copy configs and Python scripts
COPY configs/ /app/configs/
COPY python_scripts/ /app/python_scripts/
COPY run_search.sh /app/
RUN chmod +x /app/run_search.sh

# Install Python dependencies
RUN cd /app/python_scripts && pip install requests tabulate

# Default to prod when running in Docker
ENV RUN_TYPE=prod

# The entrypoint will use the --binary flag since we have compiled binaries
ENTRYPOINT ["/app/orchestrator", "--binary"]
CMD ["--run", "prod"]
