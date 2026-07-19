# Ortus

Ortus is a Go REST service for **point queries on GeoPackage and raster
sources**: it returns the geographic features (or raster values) that contain a
given coordinate, across many sources at once, using spatial SQLite
(SpatiaLite).

## Features

- **Point queries** — features containing a coordinate, via `ST_Contains`
- **Many sources at once** — query across multiple GeoPackages / raster bundles
- **Coordinate transformation** — automatic projection to a layer's SRID
- **Hot-reload** — detects added/removed local sources
- **Object storage** — load from S3, Azure Blob, or HTTP, with periodic sync
- **TLS/HTTPS** — optional, via Let's Encrypt (CertMagic)
- **Observability** — Prometheus metrics + OpenTelemetry tracing
- **MCP** — an in-process Model Context Protocol server for AI agents
- **Rate limiting & CORS** — configurable

## Quick start

```bash
make build
./ortus --storage-path=./data
curl "http://localhost:8080/api/v1/query?lon=13.405&lat=52.52"
```

The Claude Code skills under `.claude/skills/` are symlinks into the
`third_party/claude-skills` git submodule. Clone with `--recurse-submodules`
(or run `git submodule update --init` once), otherwise the symlinks dangle —
and, because some skills back tooling, `make doc-drift` (its script lives at
`.claude/skills/doc-drift-check/scripts/`) fails and the pre-PR doc-drift guard
hook is skipped:

```bash
git clone --recurse-submodules https://github.com/jobrunner/ortus.git
# existing checkout:
git submodule update --init third_party/claude-skills
# bump to the latest skills (uses the branch pinned in .gitmodules: main):
git submodule update --remote third_party/claude-skills \
  && git add third_party/claude-skills && git commit -m "chore(skills): bump claude-skills"
```

## Documentation

Full docs follow the [Diátaxis](https://diataxis.fr/) framework and build with
MkDocs Material (`make docs`, or `make docs-serve` for live preview):

- **[Getting started](docs/tutorials/getting-started.md)** and other **[tutorials](docs/tutorials/index.md)**
- **[How-to guides](docs/how-to/index.md)** — Docker, object storage, sync, TLS, rate limiting, load testing
- **[Reference](docs/reference/index.md)** — [configuration](docs/reference/configuration.md), [HTTP API](docs/reference/http-api.md), [MCP tools](docs/reference/mcp.md), [observability](docs/reference/observability.md)
- **[Explanation](docs/explanation/index.md)** — [architecture](docs/explanation/architecture.md), [decisions (ADRs)](docs/explanation/decisions/index.md), [technical-debt policy](docs/explanation/technical-debt.md)

Contributing? See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

MIT License.
