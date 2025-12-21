# =============================================================================
# Ortus - Default Dockerfile (Alpine-based)
# Supports: linux/amd64, linux/arm64
#
# Alternative Dockerfiles:
#   - Dockerfile.alpine  (same as this, explicit Alpine)
#   - Dockerfile.ubuntu  (Ubuntu-based variant)
# =============================================================================

# Build arguments for version info
ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_DATE=unknown

# =============================================================================
# Build stage - dev image includes Go 1.24.4 and all build tools
# =============================================================================
FROM ghcr.io/jobrunner/spatialite-base-image:alpine-dev-1.5.0 AS builder

# Re-declare ARGs after FROM
ARG VERSION
ARG COMMIT
ARG BUILD_DATE

USER root

WORKDIR /build

# Copy go module files first (better layer caching)
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build with CGO enabled
# Use ARG values if provided, otherwise fall back to git
RUN CGO_ENABLED=1 go build \
    -ldflags="-s -w -X main.version=${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)} \
              -X main.commit=${COMMIT:-$(git rev-parse --short HEAD 2>/dev/null || echo none)} \
              -X main.buildDate=${BUILD_DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}" \
    -o ortus ./cmd/ortus

# =============================================================================
# Runtime stage - minimal runtime image (SAME VERSION!)
# =============================================================================
FROM ghcr.io/jobrunner/spatialite-base-image:alpine-1.5.0

USER root

# Create non-root user
RUN addgroup -S ortus && adduser -S ortus -G ortus

# Create directories
RUN mkdir -p /app/data /app/cache && \
    chown -R ortus:ortus /app

WORKDIR /app

# Copy only the binary from builder
COPY --from=builder /build/ortus /app/ortus

# Set ownership
RUN chown -R ortus:ortus /app

USER ortus

# Set SpatiaLite library path for the runtime
ENV SPATIALITE_LIBRARY_PATH=/usr/lib/mod_spatialite.so

# Expose ports
EXPOSE 8080 443

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health/live || exit 1

# Default command
ENTRYPOINT ["/app/ortus"]
CMD ["--help"]
