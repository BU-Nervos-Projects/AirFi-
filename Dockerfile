# Build stage
FROM golang:1.23-bookworm AS builder

WORKDIR /app

# Install build dependencies for CGO (required by go-sqlite3)
RUN apt-get update && apt-get install -y \
    gcc \
    libc6-dev \
    && rm -rf /var/lib/apt/lists/*

# Copy go mod files first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build binaries with CGO enabled
ENV CGO_ENABLED=1
RUN go build -o /bin/backend ./cmd/backend
RUN go build -o /bin/hostcli ./cmd/hostcli

# Runtime stage
FROM debian:bookworm-slim

WORKDIR /app

# Install runtime dependencies
RUN apt-get update && apt-get install -y \
    ca-certificates \
    curl \
    && rm -rf /var/lib/apt/lists/*

# Copy binaries from builder
COPY --from=builder /bin/backend /app/backend
COPY --from=builder /bin/hostcli /app/hostcli

# Copy web assets
COPY --from=builder /app/web /app/web

# Copy config example (optional, can be mounted)
COPY --from=builder /app/config /app/config

# Create directories for runtime data
RUN mkdir -p /app/data /app/keys

# Set environment variables
ENV PORT=8080
ENV GIN_MODE=release
ENV DB_PATH=/app/data/airfi.db
ENV KEYS_DIR=/app/keys

# Expose port
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD curl -f http://localhost:8080/health || exit 1

# Run backend server
CMD ["/app/backend"]
