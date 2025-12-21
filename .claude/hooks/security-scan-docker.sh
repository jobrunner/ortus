#!/usr/bin/env bash
# Hook: Security scan Docker images with trivy
# Runs when Dockerfile* files are modified
# Note: This builds the image locally first, then scans it

set -euo pipefail

PROJECT_DIR="${CLAUDE_PROJECT_DIR:-$(pwd)}"
MODIFIED_FILE="${CLAUDE_MODIFIED_FILE:-}"

# Only run for Dockerfile changes
if [[ -z "$MODIFIED_FILE" ]] || [[ ! "$MODIFIED_FILE" =~ Dockerfile ]]; then
    exit 0
fi

# Check if trivy is available
if ! command -v trivy &> /dev/null; then
    echo "⚠️  trivy not found. Install with: brew install trivy (macOS) or nix-shell -p trivy"
    echo "   Skipping security scan."
    exit 0
fi

# Check if docker is available
if ! command -v docker &> /dev/null; then
    echo "⚠️  docker not found. Skipping image build and security scan."
    exit 0
fi

echo "🔒 Security scanning Dockerfile: $MODIFIED_FILE"

DOCKERFILE_PATH="$PROJECT_DIR/$MODIFIED_FILE"
if [[ ! -f "$DOCKERFILE_PATH" ]]; then
    echo "⚠️  File not found: $DOCKERFILE_PATH"
    exit 0
fi

# Determine image tag based on Dockerfile name
DOCKERFILE_NAME=$(basename "$MODIFIED_FILE")
if [[ "$DOCKERFILE_NAME" == "Dockerfile" ]]; then
    IMAGE_TAG="ortus:local-test"
elif [[ "$DOCKERFILE_NAME" == "Dockerfile.alpine" ]]; then
    IMAGE_TAG="ortus:local-test-alpine"
elif [[ "$DOCKERFILE_NAME" == "Dockerfile.ubuntu" ]]; then
    IMAGE_TAG="ortus:local-test-ubuntu"
else
    IMAGE_TAG="ortus:local-test-${DOCKERFILE_NAME}"
fi

echo "   Building image for security scan..."
cd "$PROJECT_DIR"

# Build the image (timeout after 5 minutes)
if ! timeout 300 docker build -f "$DOCKERFILE_PATH" -t "$IMAGE_TAG" . > /dev/null 2>&1; then
    echo "⚠️  Docker build failed or timed out. Skipping security scan."
    echo "   You can build manually with: docker build -f $MODIFIED_FILE -t $IMAGE_TAG ."
    exit 0
fi

echo "   Running trivy security scan..."
# Run trivy with severity filter (only HIGH and CRITICAL)
if ! trivy image --severity HIGH,CRITICAL --exit-code 1 "$IMAGE_TAG" 2>&1; then
    echo "❌ Security vulnerabilities found in $IMAGE_TAG"
    echo "   Review the issues above and fix before pushing."
    # Clean up
    docker rmi "$IMAGE_TAG" > /dev/null 2>&1 || true
    exit 1
fi

echo "✅ Security scan passed"

# Clean up test image
docker rmi "$IMAGE_TAG" > /dev/null 2>&1 || true
