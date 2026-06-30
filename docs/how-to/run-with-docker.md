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
      ORTUS_SERVER_CORS_ALLOWED_ORIGINS: "https://example.com,*.myapp.com"
```

Released images are signed and carry SBOM + provenance attestations — see the
release notes for `cosign verify` / `cosign download sbom`.
