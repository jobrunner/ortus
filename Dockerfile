# Build stage
FROM ghcr.io/jobrunner/spatialite-base-image:1.4.0 AS builder

# Install Go
RUN apk add --no-cache go

WORKDIR /build

# Copy go mod files first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build with CGO enabled for SpatiaLite
RUN CGO_ENABLED=1 GOOS=linux go build \
    -ldflags="-s -w -X main.version=$(git describe --tags --always --dirty 2>/dev/null || echo dev) \
              -X main.commit=$(git rev-parse --short HEAD 2>/dev/null || echo none) \
              -X main.buildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    -o ortus ./cmd/ortus

# Runtime stage
FROM ghcr.io/jobrunner/spatialite-base-image:1.4.0

# Create non-root user
RUN addgroup -S ortus && adduser -S ortus -G ortus

# Create directories
RUN mkdir -p /app/data /app/cache && \
    chown -R ortus:ortus /app

WORKDIR /app

# Copy binary from builder
COPY --from=builder /build/ortus /app/ortus

# Copy default config if exists
COPY --from=builder /build/config.yaml* /app/ 2>/dev/null || true

# Set ownership
RUN chown -R ortus:ortus /app

USER ortus

# Expose ports
EXPOSE 8080 443

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health/live || exit 1

# Default command
ENTRYPOINT ["/app/ortus"]
CMD ["--help"]
