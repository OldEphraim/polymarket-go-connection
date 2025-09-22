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

# Create entrypoint that uses prod configs
RUN echo '#!/bin/bash\n\
copycat --config /app/configs/prod-copycat.json &\n\
PID1=$!\n\
momentum --config /app/configs/prod-momentum.json &\n\
PID2=$!\n\
whale_follow --config /app/configs/prod-whale.json &\n\
PID3=$!\n\
\n\
# Keep running and restart if needed\n\
while true; do\n\
  if ps -p $PID1 > /dev/null 2>&1 || ps -p $PID2 > /dev/null 2>&1 || ps -p $PID3 > /dev/null 2>&1; then\n\
    sleep 5\n\
  else\n\
    echo "All strategies stopped. Exiting..."\n\
    exit 1\n\
  fi\n\
done' > /app/entrypoint.sh
RUN chmod +x /app/entrypoint.sh

ENTRYPOINT ["/app/entrypoint.sh"]
