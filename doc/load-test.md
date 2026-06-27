# Local load testing

ortus serves point queries straight off SQLite/SpatiaLite-backed GeoPackages.
This harness lets you stress that read path **locally** with your own large
files, and measure how the `query.sqlite.*` tuning knobs affect throughput
under concurrency.

The benchmarks live in `internal/adapters/geopackage/loadtest_test.go`. They are
**env-gated**: without `ORTUS_LOADTEST_GPKG` they skip, so `make test` /
`make test-bench` stay green in CI. The test data is intentionally **not** part
of the repository — supply your own.

## What you need

A GeoPackage big enough to be interesting — ideally tens of MB up to several GB,
with a feature layer holding many rows (e.g. parcels, buildings, address
points). The harness builds the R-tree spatial index once (reporting how long
that takes — itself a useful signal for large files) and then hammers
`QueryPoint` at a fixed coordinate.

> Raster bundles (`.zip` of GeoTIFF/COG) are served by a different adapter and
> are not covered by this harness yet; it targets the SQLite vector path, which
> is where read concurrency contends.

## Running it

Minimal — let the harness pick the first layer and query its extent center:

```sh
make load-test ORTUS_LOADTEST_GPKG=/data/big.gpkg
```

Pin the layer and query point explicitly:

```sh
make load-test \
  ORTUS_LOADTEST_GPKG=/data/big.gpkg \
  ORTUS_LOADTEST_LAYER=parcels \
  ORTUS_LOADTEST_X=11.58 ORTUS_LOADTEST_Y=48.13 ORTUS_LOADTEST_SRID=4326
```

Or drive `go test` directly for full control:

```sh
ORTUS_LOADTEST_GPKG=/data/big.gpkg \
go test -run='^$' -bench=BenchmarkLoadTest -benchmem -benchtime=5s \
    -cpu=1,4,8,16 ./internal/adapters/geopackage/
```

`make load-test` accepts `BENCHTIME` (default `3s`) and `CPU` (e.g.
`CPU=1,4,8,16`) to sweep parallelism in one run.

## The two benchmarks

- `BenchmarkLoadTestQueryPointSerial` — single-threaded latency baseline.
- `BenchmarkLoadTestQueryPointConcurrent` — `GOMAXPROCS` goroutines sharing one
  repository, the way ortus serves concurrent HTTP clients. Compare its
  ns/op against the serial baseline as you raise `-cpu` to find the SQLite
  contention knee.

## Tuning knobs

These map one-to-one onto the `query.sqlite.*` config keys, so a setting that
wins here is the setting you put in `config.yaml`:

| Env var                  | Config key                     | Default   | Notes |
| ------------------------ | ------------------------------ | --------- | ----- |
| `ORTUS_LOADTEST_CACHE`   | `query.sqlite.cache_mode`      | `private` | `private` favours read concurrency; `shared` serialises through one cache. |
| `ORTUS_LOADTEST_BUSY_MS` | `query.sqlite.busy_timeout_ms` | `5000`    | How long a query waits on a locked DB before erroring. |
| `ORTUS_LOADTEST_JOURNAL` | `query.sqlite.journal_mode`    | (file's)  | e.g. `WAL`. Empty leaves the file's existing mode. |
| `ORTUS_LOADTEST_MAXOPEN` | `query.sqlite.max_open_conns`  | `0`       | `0` = unlimited. Cap to bound contention / file handles. |
| `ORTUS_LOADTEST_MAXIDLE` | `query.sqlite.max_idle_conns`  | `4`       | Idle connections kept in the pool. |

Query point overrides (default = the queried layer's extent center):
`ORTUS_LOADTEST_X`, `ORTUS_LOADTEST_Y`, `ORTUS_LOADTEST_SRID`.

## Reading the results

```
BenchmarkLoadTestQueryPointConcurrent-16   	   42318	     28140 ns/op	    4096 B/op	      87 allocs/op
```

- **ns/op** — mean wall-clock per query at this parallelism. Watch how it moves
  as you raise `-cpu`: flat = scaling well; rising sharply = lock contention.
- **B/op / allocs/op** — allocation pressure per query (GC load under fire).
- The `b.Logf` lines above the results report the index-build time and the
  resolved query coordinate, so a run is self-documenting.

A practical sweep: run with `CPU=1,4,8,16` at the default `private` cache, then
again with `ORTUS_LOADTEST_CACHE=shared` and/or `ORTUS_LOADTEST_MAXOPEN=4`, and
compare. Pick the combination whose concurrent ns/op stays closest to the serial
baseline, and write those values into `query.sqlite.*`.

---

# Observable load test (Grafana single pane)

The microbenchmark above isolates the SQLite read path. To load-test the **full
stack** — HTTP routing, query service, coordinate transform, repository — and
watch **metrics, traces and logs together in one Grafana**, use the local
observability stack under [`deploy/loadtest/`](../deploy/loadtest/). It runs the
backends in Docker while **ortus itself runs natively** (real arm64, native CGO
SpatiaLite, no container memory cap, no x86 emulation), and drives load with
[Vegeta](https://github.com/tsenart/vegeta).

```
            ┌──────────── your machine (host) ─────────────┐
  vegeta ──▶│  ortus (native, :8080)                       │
 (docker)   │    ├─ /metrics  :2112  ◀── Prometheus scrape │
            │    ├─ OTLP traces ─────▶ Tempo :4318          │
            │    └─ JSON logs ─▶ file ─▶ Promtail ─▶ Loki   │
            └───────────────────────────┬──────────────────┘
                                         ▼
                         Grafana :3000  (Prometheus + Tempo + Loki,
                                         trace_id-correlated)
```

The correlation key is the `trace_id` ortus stamps into every span and (when a
span is active) every JSON log line: in Grafana you can jump metric → trace →
logs and back.

## One-time

- Docker Desktop running.
- Build picks up automatically (`make load-serve` builds first).
- Optionally `brew install vegeta` if you prefer the native driver over the
  containerised one (recommended for very high rates — the bundled image is
  amd64 and runs under emulation on Apple Silicon, which can cap the *driver's*
  throughput, not ortus's).

## Run it

Three terminals (or backgrounded):

```sh
# 1) backends
make load-stack-up
#    Grafana http://localhost:3000  ·  Prometheus :9090  ·  Tempo OTLP :4318

# 2) ortus, native, against your big files (stays in foreground, logs tee'd to
#    deploy/loadtest/logs/ortus.log where Promtail tails them)
make load-serve ORTUS_LOADTEST_DATA=/data/big-sources

# 3) drive load
make load-attack RATE=500 DURATION=60s
#    override TARGETS=other.txt to hit specific coordinates / sources
```

Then open Grafana → dashboard **ortus / ortus — load test**: request rate, p50/
p95/p99 latency (overall and per route), source gauges, and a live log panel.
Click a `trace_id` in a log line to open the trace in Tempo; from a span, use
"Logs for this span" to jump back to Loki.

Tear down with `make load-stack-down` (keeps data volumes) or `make
load-stack-clean` (wipes them).

## Two kinds of run — don't mix them up

`make load-serve` defaults to `SAMPLE=1.0` (every request traced). That's great
for *seeing* where time goes, but at high RPS the span + OTLP export overhead
lowers max throughput and skews latency — so:

| Goal | Command | Why |
| ---- | ------- | --- |
| **Saturation / throughput** | `make load-serve … SAMPLE=0.01` (or `0`) | Tracing overhead near-zero; read RPS + p95/p99 from Prometheus. Metrics are cheap (aggregate), always on. |
| **Diagnosis** | `make load-serve … SAMPLE=1.0` at low `RATE` | Open a trace: see `QueryService.QueryPoint → Repository.QueryPoint → executePointQuery`, `ortus.rtree.used`, `db.statement`, feature counts. |

Vegeta's own report (printed after each `load-attack`) gives exact, per-request
latency percentiles independent of the sampling decision — trust it for the
headline numbers and use Prometheus/Tempo for the *shape* and the *why*.

## Notes

- **Metrics port.** ortus serves `/metrics` on `metrics.port` (default 9090).
  `load-serve` overrides it to **2112** so it doesn't clash with this stack's
  own Prometheus on :9090. The scrape config (`deploy/loadtest/prometheus.yaml`)
  targets `host.docker.internal:2112` accordingly.
- **Histogram buckets.** `ortus.http.request.duration` is recorded in seconds;
  the meter provider sets seconds-scale bucket boundaries (sub-ms … 10s) via an
  OTel View so `histogram_quantile()` resolves real percentiles. (OTel's default
  boundaries assume integer milliseconds and would collapse every sub-5s request
  into one bucket.)
- **Security.** The Grafana here is anonymous-admin, no login — strictly local.
  Never expose this stack.
