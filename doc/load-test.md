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
