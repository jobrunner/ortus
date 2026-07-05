# Configuration

ortus is configured via CLI flags, environment variables, or a config file.

**Precedence:** CLI flags > environment variables > config file > defaults.

## CLI flags

```text
./ortus [flags]

      --config string         Config file path (default: ./config.yaml)
      --host string           HTTP server host (default "0.0.0.0")
      --port int              HTTP server port (default 8080)
      --storage-type string   Storage type: local, s3, azure, http (default "local")
      --storage-path string   Local storage path for GeoPackages (default "./data")
      --cors strings          Allowed CORS origins (e.g. https://example.com,*.sub.domain.tld)
      --tls                   Enable TLS
      --tls-domains strings   TLS domains for Let's Encrypt
      --tls-email string      Email for Let's Encrypt
      --log-level string      Log level: debug, info, warn, error (default "info")
  -h, --help                  Show help
```

Tracing flags (`--tracing`, `--tracing-endpoint`, …) are documented in
[Observability](observability.md).

## Environment variables

All options can be set with the `ORTUS_` prefix:

| Variable | Default | Description |
|----------|---------|-------------|
| `ORTUS_SERVER_HOST` | `0.0.0.0` | HTTP server host |
| `ORTUS_SERVER_PORT` | `8080` | HTTP server port |
| `ORTUS_STORAGE_TYPE` | `local` | Storage type (local/s3/azure/http) |
| `ORTUS_STORAGE_LOCAL_PATH` | `./data` | Path to GeoPackage directory |
| `ORTUS_SERVER_CORS_ALLOWED_ORIGINS` | `[]` | Allowed CORS origins (comma-separated) |
| `ORTUS_LOGGING_LEVEL` | `info` | Log level (debug/info/warn/error) |
| `ORTUS_LOGGING_FORMAT` | `json` | Log format (json/text) |
| `ORTUS_TLS_ENABLED` | `false` | Enable TLS |
| `ORTUS_METRICS_ENABLED` | `true` | Enable Prometheus metrics |
| `ORTUS_METRICS_PORT` | `9090` | Metrics server port |
| `ORTUS_SERVER_READY_WHEN_EMPTY` | `true` | Report ready with zero loaded sources (after initial load) |
| `ORTUS_SERVER_RATE_LIMIT_ENABLED` | `false` | Enable per-IP rate limiting on `/api/v1` |
| `ORTUS_SERVER_RATE_LIMIT_RATE` | `100` | Sustained requests/second per client IP |
| `ORTUS_SERVER_RATE_LIMIT_BURST` | `200` | Token-bucket burst per client IP |
| `ORTUS_SERVER_RATE_LIMIT_TRUSTED_PROXIES` | `[]` | Front-proxy CIDRs allowed to set `X-Forwarded-For` |
| `ORTUS_SYNC_ENABLED` | `false` | Enable periodic remote storage sync |
| `ORTUS_SYNC_INTERVAL` | `1h` | Sync interval (e.g. 30m, 1h, 24h) |
| `ORTUS_QUERY_SQLITE_CACHE_MODE` | `private` | SQLite cache mode (`private`/`shared`) |
| `ORTUS_QUERY_SQLITE_BUSY_TIMEOUT_MS` | `5000` | Busy timeout (ms) before a locked-DB query errors |
| `ORTUS_QUERY_SQLITE_JOURNAL_MODE` | (file's) | Journal mode (e.g. `WAL`); empty leaves the file's mode |
| `ORTUS_QUERY_SQLITE_MAX_OPEN_CONNS` | `0` | Max open connections per source (`0` = unlimited) |
| `ORTUS_QUERY_SQLITE_MAX_IDLE_CONNS` | `4` | Max idle connections per source |

## Config file

Create `config.yaml` in the working directory or pass `--config`:

```yaml
server:
  host: "0.0.0.0"
  port: 8080
  cors:
    allowed_origins:
      - "https://example.com"
      - "*.myapp.com"

storage:
  type: local
  local_path: ./data

logging:
  level: "info"
  format: "json"

metrics:
  enabled: true
  path: "/metrics"

query:
  sqlite:
    cache_mode: private      # private favours read concurrency; shared serialises
    busy_timeout_ms: 5000    # wait on a locked DB before erroring
    journal_mode: ""         # e.g. WAL; empty leaves the file's existing mode
    max_open_conns: 0        # per source; 0 = unlimited
    max_idle_conns: 4
```

A complete example lives in [`config.yaml.example`](https://github.com/jobrunner/ortus/blob/master/config.yaml.example);
a test (`TestConfigExampleNoDrift`) keeps it in sync with the code.

## SQLite tuning

The `query.sqlite.*` keys tune how each GeoPackage is opened. Defaults favour
read concurrency (`private` cache) and are safe to leave as-is. To calibrate for
your data and hardware, see **[Run a load test](../how-to/run-a-load-test.md)** —
a setting that wins there maps one-to-one onto these keys.

## Gazetteer

The gazetteer (reverse geocoding + bearing / "Peilung") is an optional feature,
off by default and inert until enabled. It loads a dedicated places/admin
GeoPackage **separately** from the generic query source pool — it is never a
point-in-polygon source. It powers `GET /api/v1/gazetteer`, the `gazetteer` MCP
tool, and — when enabled — the `gazetteer` block that `GET /api/v1/query` returns
by default (opt out per request with `?with-gazetteer=0`).

```yaml
gazetteer:
  enabled: true
  geopackage_path: /data/gazetteer/osm-admin-places.gpkg      # required when enabled
  manifest_path: /data/gazetteer/ortus-gazetteer.yaml         # required: maps layer/column roles
  level_reference_path: /data/gazetteer/admin_levels_west_palearctic.yaml   # optional: per-country tier meaning
  name_source_manifest_path: /data/gazetteer/name_source_manifest.yaml      # optional: name-source descriptions
  bearing:
    reach_village_km: 5
    reach_town_km: 18
    reach_city_km: 60
    prefer_nearest_km: 5
    inside_label_km: 1
    compass_points: 8
```

- `geopackage_path` and `manifest_path` are **required** when `enabled: true`;
  startup fails fast otherwise.
- `level_reference_path` (optional) enriches each admin level with its semantic
  `equivalent`, country-specific `local_term`, and `equivalent_description`.
  Without it, Locate still returns the raw hierarchy.
- `name_source_manifest_path` (optional) populates the response-wide `sources`
  block that describes each name-romanization/provenance code. Without it, each
  record still carries its raw `name_source` code but the descriptions are empty.
- The dataset attribution shown in the response comes from the optional `license:`
  block in `ortus-gazetteer.yaml` (name/url/attribution) — set it so clients get
  the attribution they must display.
- The dataset and its sidecars are built by the `build-gazetteer-package` skill
  (see `.claude/skills/build-gazetteer-package/`).
