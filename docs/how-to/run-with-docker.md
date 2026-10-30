# Run with Docker

## docker run

```bash
docker run -d \
  -p 8080:8080 \
  -v /path/to/geopackages:/data \
  -e ORTUS_STORAGE_LOCAL_PATH=/data \
  ghcr.io/jobrunner/ortus:latest
```

## docker compose

```yaml
services:
  ortus:
    image: ghcr.io/jobrunner/ortus:latest
    ports:
      - "8080:8080"
      - "9090:9090"  # metrics
    volumes:
      # Must be writable: ortus builds R-tree indexes inside the GeoPackages on
      # first load, and SQLite writes a journal alongside the DB. Not read-only.
      - ./data:/data
    environment:
      ORTUS_STORAGE_LOCAL_PATH: /data
      ORTUS_LOGGING_LEVEL: info
      ORTUS_SERVER_CORS_ALLOWED_ORIGINS: "https://example.com,https://*.myapp.com"
```

## Verify before deploying

Released images are signed (cosign keyless) by the Docker Release workflow on
`master` and carry SBOM + SLSA provenance attestations. A tag such as `2.0.2`
or `latest` is mutable; a digest is not. So verify the tag once, then deploy
the digest it pointed to:

```bash
nix develop                    # provides cosign + trivy
make verify-image TAG=2.0.2    # or TAG=2.0.2-ubuntu
```

`make verify-image` (`scripts/verify-image.sh`):

1. checks the signature against the **exact** signer identity
   `https://github.com/jobrunner/ortus/.github/workflows/docker-release.yml@refs/heads/master`
   — an image signed by any other workflow or branch fails,
2. takes the digest from the verified signature (not from a second registry
   lookup),
3. scans exactly that digest with Trivy and fails on fixable CRITICAL/HIGH
   findings, the same policy as the CI gate,
4. prints the reference to deploy, e.g.
   `ghcr.io/jobrunner/ortus@sha256:dbbc2f22…`.

Put that digest reference into `docker run` / `image:` instead of the tag. The
Docker host needs nothing extra: it pulls by digest, and the registry
guarantees the content matches it. An update is then a deliberate change of
the digest after the next `make verify-image`, not a silent re-pull of
`latest`.

Without the Makefile, the signature check alone is:

```bash
cosign verify ghcr.io/jobrunner/ortus:2.0.2 \
  --certificate-identity https://github.com/jobrunner/ortus/.github/workflows/docker-release.yml@refs/heads/master \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

The SBOM and provenance are BuildKit attestations in the image index (covered
by the signature, which signs the index digest):

```bash
docker buildx imagetools inspect ghcr.io/jobrunner/ortus:2.0.2 --format '{{ json .SBOM }}'
docker buildx imagetools inspect ghcr.io/jobrunner/ortus:2.0.2 --format '{{ json .Provenance }}'
```
