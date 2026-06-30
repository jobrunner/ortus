# Getting started

This tutorial gets you from nothing to a working point query against a local
GeoPackage.

## 1. Install

From source:

```bash
git clone https://github.com/jobrunner/ortus.git
cd ortus
make build
```

Or pull the container image:

```bash
docker pull ghcr.io/jobrunner/ortus:latest
```

## 2. Start the server

Point ortus at a directory of `.gpkg` files:

```bash
./ortus --storage-path=./data
```

It scans the directory, builds spatial (R-tree) indexes on first load, and
serves on `http://localhost:8080`. Add a custom port and CORS origins if you're
calling it from a browser app:

```bash
./ortus --port=8080 --cors=https://example.com,*.myapp.com
```

## 3. Run a point query

Ask which features contain a coordinate (WGS84 lon/lat):

```bash
curl "http://localhost:8080/api/v1/query?lon=13.405&lat=52.52"
```

You'll get the matching features from every loaded source:

```json
{
  "coordinate": { "x": 13.405, "y": 52.52, "srid": 4326 },
  "results": [
    {
      "source_id": "districts",
      "features": [{ "id": 42, "layer": "districts", "properties": { "name": "Mitte" } }],
      "feature_count": 1
    }
  ],
  "total_features": 1
}
```

## 4. Check health

```bash
curl "http://localhost:8080/health"        # detailed status, per-source
curl "http://localhost:8080/health/ready"  # Kubernetes readiness
```

## Next steps

- Query a specific source, filter properties, use projected coordinates → **[HTTP API reference](../reference/http-api.md)**.
- Tune behaviour via flags / env / config → **[Configuration reference](../reference/configuration.md)**.
- Load from S3/Azure/HTTP → **[Load from object storage](../how-to/configure-storage.md)**.
- Run it in a container → **[Run with Docker](../how-to/run-with-docker.md)**.
