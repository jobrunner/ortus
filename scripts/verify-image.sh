#!/usr/bin/env bash
#
# verify-image.sh — pre-deploy check of a published ortus image.
#
#   1. Signature: cosign verifies that the image was signed keyless by the
#      Docker Release workflow on master — not merely by "some workflow in
#      this repo" (a PR branch or a workflow_dispatch from another branch
#      would carry a different certificate identity and is rejected).
#   2. Pinning: the digest is taken from the VERIFIED signature payload, not
#      from a second registry lookup, so a tag moved between the two steps
#      cannot slip an unsigned image into the scan or the deploy.
#   3. Vulnerabilities: trivy scans exactly that digest and fails on fixable
#      CRITICAL/HIGH findings (same policy as the CI gate). The digest is a
#      multi-arch index and trivy scans one platform per run, so every
#      published platform is scanned — the host pulling the digest picks its
#      own, and that one must have been checked.
#
# On success it prints the digest-pinned reference to deploy.
#
# Usage: scripts/verify-image.sh [tag]        (default: latest)
#   IMAGE_REPO   image repository   (default: ghcr.io/jobrunner/ortus)
#   SEVERITY     trivy severities   (default: CRITICAL,HIGH)
#   PLATFORMS    platforms to scan  (default: linux/amd64 linux/arm64,
#                as published by docker-release.yml)
set -euo pipefail

TAG="${1:-latest}"
IMAGE_REPO="${IMAGE_REPO:-ghcr.io/jobrunner/ortus}"
SEVERITY="${SEVERITY:-CRITICAL,HIGH}"
PLATFORMS="${PLATFORMS:-linux/amd64 linux/arm64}"

# The exact SAN of the signing certificate, read from a real release
# signature (v2.0.2). Must match docker-release.yml's name and trigger branch.
SIGNER_IDENTITY="https://github.com/jobrunner/ortus/.github/workflows/docker-release.yml@refs/heads/master"
OIDC_ISSUER="https://token.actions.githubusercontent.com"

for tool in cosign trivy jq; do
  command -v "$tool" >/dev/null || {
    echo "verify-image: '$tool' not found — run inside 'nix develop'" >&2
    exit 2
  }
done

ref="${IMAGE_REPO}:${TAG}"

echo "==> Signature: ${ref}"
payload=$(cosign verify "$ref" \
  --certificate-identity "$SIGNER_IDENTITY" \
  --certificate-oidc-issuer "$OIDC_ISSUER" 2>/dev/null) || {
  echo "verify-image: signature check FAILED for ${ref}" >&2
  echo "  expected signer: ${SIGNER_IDENTITY}" >&2
  exit 1
}
digest=$(jq -r '.[0].critical.image."docker-manifest-digest"' <<<"$payload")
[[ "$digest" == sha256:* ]] || {
  echo "verify-image: no digest in the verified signature payload" >&2
  exit 1
}
pinned="${IMAGE_REPO}@${digest}"
echo "    ok — signed by the Docker Release workflow on master"

for platform in $PLATFORMS; do
  echo "==> Vulnerabilities (${SEVERITY}, fixable, ${platform}): ${pinned}"
  trivy image --quiet --ignore-unfixed --severity "$SEVERITY" --exit-code 1 \
    --platform "$platform" "$pinned"
  echo "    ok"
done

echo
echo "Deploy this reference (tag ${TAG} at verification time):"
echo "  ${pinned}"
