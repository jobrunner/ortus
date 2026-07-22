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
| `ORTUS_QUERY_TIMEOUT` | `30s` | Per-query timeout |
| `ORTUS_QUERY_MAX_FEATURES` | `1000` | Max features returned per query |
| `ORTUS_QUERY_WITH_GEOMETRY` | `false` | Include feature geometry (WKT) in query results |
| `ORTUS_SERVER_READ_TIMEOUT` | `30s` | HTTP read timeout |
| `ORTUS_SERVER_WRITE_TIMEOUT` | `30s` | HTTP write timeout |
| `ORTUS_SERVER_SHUTDOWN_TIMEOUT` | `10s` | Graceful-shutdown timeout |
| `ORTUS_SERVER_FRONTEND_ENABLED` | `true` | Serve the mini query frontend at `GET /` |
| `ORTUS_MCP_ENABLED` | `false` | Enable the MCP server |
| `ORTUS_MCP_HOST` | `127.0.0.1` | MCP bind host (non-loopback requires a token) |
| `ORTUS_MCP_PORT` | `9091` | MCP server port |
| `ORTUS_MCP_PATH` | `/mcp` | MCP HTTP path |
| `ORTUS_MCP_TOKEN` | — | MCP bearer token (env only, never the config file) |
| `ORTUS_QUERY_SQLITE_CACHE_MODE` | `private` | SQLite cache mode (`private`/`shared`) |
| `ORTUS_QUERY_SQLITE_BUSY_TIMEOUT_MS` | `5000` | Busy timeout (ms) before a locked-DB query errors |
| `ORTUS_QUERY_SQLITE_JOURNAL_MODE` | (file's) | Journal mode (e.g. `WAL`); empty leaves the file's mode |
| `ORTUS_QUERY_SQLITE_MAX_OPEN_CONNS` | `0` | Max open connections per source (`0` = unlimited) |
| `ORTUS_QUERY_SQLITE_MAX_IDLE_CONNS` | `4` | Max idle connections per source |

From the storage path (`storage.local_path` or the remote bucket/prefix) ortus
loads only two file types: **`.gpkg`** (vector GeoPackage sources) and **`.zip`**
(raster bundles, see [Raster bundle](raster-bundle.md)). Other files are ignored.
The gazetteer GeoPackage is loaded separately via its own paths (see
[Gazetteer](#gazetteer)), not from the storage path.

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
  timeout: 30s             # per-query timeout
  max_features: 1000       # cap on features returned per query
  with_geometry: false     # include feature geometry (WKT) in results
  sqlite:
    cache_mode: private      # private favours read concurrency; shared serialises
    busy_timeout_ms: 5000    # wait on a locked DB before erroring
    journal_mode: ""         # e.g. WAL; empty leaves the file's existing mode
    max_open_conns: 0        # per source; 0 = unlimited
    max_idle_conns: 4
```

A complete example lives in [`config.yaml.example`](https://github.com/jobrunner/ortus/blob/master/config.yaml.example);
a test (`TestConfigExampleNoDrift`) keeps it in sync with the code.

Point-in-polygon matching is boundary-inclusive (`ST_Covers`): a point exactly on
a polygon edge belongs to that polygon, and a point on a border between two
different regions returns both. Fragments of the same region (e.g. from an
`ST_Subdivide`-tiled source) are deduplicated by their attributes, so a tiled
source returns the same features as its un-tiled original — tiling stays an
opt-in packaging choice. One caveat: with `query.with_geometry: true`, a tiled
source returns the matched subdivision fragment's geometry, not the original
whole polygon; likewise the returned `id` is the kept fragment's fid (post-dedup)
and may not match an un-tiled package's feature id.

## SQLite tuning

The `query.sqlite.*` keys tune how each GeoPackage is opened. Defaults favour
read concurrency (`private` cache) and are safe to leave as-is. To calibrate for
your data and hardware, see **[Run a load test](../how-to/run-a-load-test.md)** —
a setting that wins there maps one-to-one onto these keys.

## Raster

Settings for the raster-bundle adapter (COG `*.zip` sources):

```yaml
raster:
  max_bundle_extract_gib: 8   # per-bundle extracted-size cap (decompression-bomb guard)
  extract_cache_dir: ""       # "" = unpack to OS temp every start; set = persistent content-addressed cache
  extract_cache_prune: false  # remove old cached extractions of a source after a new one loads
```

- `raster.max_bundle_extract_gib` bounds the total bytes extracted from one
  bundle when it is unpacked. The default (8 GiB) protects the host from a
  corrupt/hostile archive. Raise it for large **trusted** bundles such as
  continental DEM tile sets — e.g. the West-Palearctic elevation bundle unpacks
  to ~40 GiB, so set `40`–`64` there. A bundle that exceeds the cap fails to load
  with a clear error rather than filling the disk.
- `raster.extract_cache_dir` turns on a **persistent, content-addressed
  extraction cache**. When empty (default) a bundle is unpacked into an OS temp
  dir on **every** start — for the ~48 GB DEM that is minutes of unzip each
  restart. Point this at a **durable, mounted volume** (Hetzner Cloud Volume in
  prod, a Docker named volume in dev) and a bundle is unpacked **once** into
  `<dir>/<id>@<fingerprint>` and reused across restarts **and** image updates.
  The fingerprint is derived from the ZIP's central directory (per-entry name +
  CRC + size — cheap, no decompression), so a re-extract happens **only** when
  the archive content actually changes (a tile added or modified). The DEM `.zip`
  stays read-only; the cache dir must be writable and sized for the *unpacked*
  data (~2–3× the zip). Sizing still respects `max_bundle_extract_gib`.
- `raster.extract_cache_prune` (default `false`) deletes older
  `<id>@<fingerprint>` dirs after a new fingerprint loads, to bound disk use.
  **Leave it off if container starts can overlap** (a rolling update where the
  old container is still serving from its extraction) — pruning would pull the
  tiles out from under it. Prune offline, or only where starts never overlap.

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
    salience: composite         # composite (default) | rank
    composite:                  # composite-strategy tuning (calibrated defaults shown)
      candidate_radius_km: 120
      pop_weight: 1.0
      wiki_weight: 0.3
      decay_per_km: 0.04
      capital_scale: 0.8
      # class_prior: { city: 4.3, town: 3.3, village: 2.3 }          # base score when population is unknown
      # capital_bonus: { "2": 2.0, "3": 1.5, "4": 1.2, "5": 0.6, "6": 0.4, "7": 0.2, "yes": 2.0 }
  elevation:                    # optional: height above sea level at the query point
    bundle_path: /data/gazetteer/copernicus-dem-westpalearctic.zip   # gazetteer-owned DEM bundle; "" = feature off
    layer: elevation            # continuous elevation layer id in that bundle
    accuracy_layer: accuracy    # optional per-point accuracy layer (e.g. HEM); "" = off
    tile_cache_size: 64         # open-tile LRU bound for multi-tile DEMs
    vertical_datum: EGM2008
    accuracy_m: 4.0             # vertical accuracy constant (used when accuracy_layer is unset)
    accuracy_basis: "GLO-30 LE90 (absolute)"
    per_point_accuracy_basis: "Copernicus HEM (per-pixel 1σ)"
    horizontal_accuracy_m: 6.0
    surface_model: DSM
  warmup:                         # startup self-warmup (on by default)
    enabled: true
    lon: 9.93                     # point your dataset AND DEM cover
    lat: 49.79
```

- `geopackage_path` and `manifest_path` are **required** when `enabled: true`;
  startup fails fast otherwise.
- `bearing.salience` picks the anchor-selection strategy. **`composite`** (default)
  scores each candidate by prominence vs proximity —
  `pop_weight·log10(1+population) + capital_scale·capitalBonus + wiki_weight·[wikidata] − decay_per_km·km` —
  so a prominent city a moderate distance away beats an obscure village next door. It
  needs the enriched `population`/`capital`/`wikidata` columns (from `make enrich-places`)
  and falls back to the place class where they are absent. **`rank`** is the original
  class-then-distance behaviour (uses `reach_*_km` + `prefer_nearest_km`). The composite
  strategy gathers candidates within `candidate_radius_km` (a flat radius for all classes)
  and lets the distance decay, not a hard per-class cap, shape the result. Both strategies
  constrain anchors to the query point's country when it can be determined (skipped only
  where the point lies in no polygon, e.g. open sea), and to its state-equivalent unit when
  the manifest's `bearing_constraint_tier` resolves. `class_prior` and `capital_bonus`
  optionally override the calibrated tables (base score for places without population, and
  the per-OSM-capital-rank bonus); each is a *partial* override — only the listed keys change.
- `level_reference_path` (optional) enriches each admin level with its semantic
  `equivalent`, country-specific `local_term`, and `equivalent_description`.
  Without it, Locate still returns the raw hierarchy.
- An optional `islands:` block in `ortus-gazetteer.yaml` (`layer`, `name_column`)
  names the island(s) whose polygon contains the query point, surfaced as the
  response `islands` array. `name_column` defaults to the admin layer's when
  omitted. Without the block, no island lookup runs and `islands` is `null`.
- `name_source_manifest_path` (optional) populates the response-wide `sources`
  block that describes each name-romanization/provenance code. Without it, each
  record still carries its raw `name_source` code but the descriptions are empty.
- The dataset attribution shown in the response comes from the optional `license:`
  block in `ortus-gazetteer.yaml` (name/url/attribution) — set it so clients get
  the attribution they must display.
- `elevation` (optional) adds the height above sea level at the query point to the
  gazetteer response. `bundle_path` is the **gazetteer-owned** DEM bundle (a
  `.zip`): it is opened **out of competition** — directly, like `geopackage_path`,
  **not** through the source pool — so it never appears in `GET /api/v1/sources`
  and is never point-in-polygon queried; it is reachable only through the gazetteer.
  Empty leaves the feature off. **A missing or unopenable bundle is non-fatal:**
  startup continues and the DEM-derived features (`elevation` **and** `exposure`)
  simply stay silent (`null`) until the path is valid. `layer` is its continuous
  layer (default `elevation`); an optional `accuracy_layer` (e.g. a Copernicus
  Height Error Mask) supplies a *per-point* vertical accuracy, else the `accuracy_m`
  constant is reported. `accuracy_basis`/`per_point_accuracy_basis`,
  `horizontal_accuracy_m`, `vertical_datum` and `surface_model` are surfaced verbatim.
  The DEM's own license (from its bundle manifest) is reported separately from the
  gazetteer dataset license. **Ops:** keep the DEM `.zip` in the gazetteer folder,
  **not** in `storage.local_path` — otherwise it also loads as a normal pool source
  (a WARN fires) and gets listed + double-queried.
- `warmup` (on by default) runs the internal gazetteer lookups (Locate, Islands,
  Bearing, Exposure, Elevation) at `(lon, lat)` during
  startup, **before the server accepts traffic**, so the first real request isn't
  cold (the cause of a "Load failed" first request right after a deploy: the initial
  SpatiaLite/DEM open exceeded the client/proxy timeout). Point `lon`/`lat` at a
  coordinate your dataset **and** DEM cover; set `enabled: false` if you run no
  gazetteer/DEM. It runs synchronously and therefore **delays readiness by up to
  ~30 s** — but it is time-bounded (never blocks indefinitely) and best-effort (a
  warmup error is logged at WARN, startup still proceeds).
- The dataset and its sidecars are built by the `build-gazetteer-package` skill
  (see `.claude/skills/build-gazetteer-package/`).
