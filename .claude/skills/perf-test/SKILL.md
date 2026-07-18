---
name: perf-test
description: >-
  Run a local load test against the current feature's ortus instance and interpret
  the results — latency percentiles, throughput, error rate — plus the traces and
  metrics in Grafana/Tempo. Use when a change is performance-sensitive or you want
  a before/after comparison. Wraps `make dev-perf` (Vegeta through Traefik with a
  per-feature service_name) and the shared observability stack (`make dev-obs`).
  Runs against http://<name>.ortus.local.
---

# Local performance test (Vegeta + Tempo/Prometheus/Grafana)

Measure the running feature instance under HTTP load and use traces/metrics to
find *where* time goes — not just that it's slow.

## Prerequisites
- The feature stack is running (`make dev-new` / `make dev`).
- Observability up: `make dev-obs` (Grafana at http://grafana.ortus.local). Skip
  it and you still get the Vegeta report, just no traces/dashboards.

## Procedure
1. **Baseline first.** For before/after work, capture a run on the unchanged code
   and note the numbers before you optimize.
2. **Run the load test:**
   ```sh
   make dev-perf NAME=<slug>                   # defaults: RATE=200 DURATION=30s
   make dev-perf NAME=<slug> RATE=500 DURATION=1m
   ```
   `dev-perf` turns on tracing for the feature (`service_name=<slug>`), fires
   Vegeta at `http://<slug>.ortus.local` via Traefik, and prints the report.
3. **Read the Vegeta report:** focus on p50/p95/p99 latency, throughput
   (requests/s actually served vs. requested rate), and **Success ratio** — any
   non-2xx means errors under load, investigate before trusting latency.
4. **Correlate in Grafana** (http://grafana.ortus.local): Explore → **Tempo**,
   filter `service.name = "<slug>"` over the run's time window; open the slowest
   traces and find the dominant span (SpatiaLite query? COG decode? serialization?).
   Explore → **Prometheus** for CPU/alloc/goroutine and request-rate panels.
   Logs are in Dozzle (http://logs.ortus.local) and, when a span is active,
   carry the `trace_id` for cross-linking.
5. **Tune the load to the question:** widen the coordinates in
   `deploy/dev/vegeta-targets.txt` to spread R-tree access; raise RATE to find the
   knee; add `/api/v1/query/{sourceId}` lines to exercise one source.

## Interpreting & acting
- **Flat latency, low CPU, requests/s < rate** → contention or a lock/serialized
  path; check the dominant span and goroutine/block profiles.
- **p99 ≫ p50** → tail latency (GC pauses, cold tiles, cache misses); look at the
  slow traces specifically, not the average.
- **Errors rise with rate** → capacity/timeouts; check the error envelope in logs.

Report: the run parameters, the key percentiles + success ratio, the dominant
span from the slowest traces, and a concrete next step. For a fair before/after,
keep RATE/DURATION/targets identical across runs.
