FROM golang:1.23.5-alpine AS go-builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Build all three strategies
RUN CGO_ENABLED=0 GOOS=linux go build -o copycat ./strategies/copycat/main.go
RUN CGO_ENABLED=0 GOOS=linux go build -o momentum ./strategies/momentum/main.go
RUN CGO_ENABLED=0 GOOS=linux go build -o whale_follow ./strategies/whale_follow/main.go

FROM python:3.11-slim
WORKDIR /app

# Install PostgreSQL client for debugging
RUN apt-get update && apt-get install -y postgresql-client && rm -rf /var/lib/apt/lists/*

# Copy Go binaries
COPY --from=go-builder /app/copycat /app/momentum /app/whale_follow /usr/local/bin/

# Copy Python scripts
COPY python_scripts/ /app/python_scripts/
RUN cd /app/python_scripts && pip install requests tabulate

# Copy configs and other files
COPY configs/ /app/configs/
COPY run_search.sh /app/
RUN chmod +x /app/run_search.sh

# Create a simple entrypoint
RUN echo '#!/bin/bash\n\
copycat --config /app/configs/test-copycat.json &\n\
momentum --config /app/configs/test-momentum.json &\n\
whale_follow --config /app/configs/test-whale.json &\n\
wait -n\n\
exit $?' > /app/entrypoint.sh
RUN chmod +x /app/entrypoint.sh

ENTRYPOINT ["/app/entrypoint.sh"]
