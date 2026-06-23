# Implementation Plan — Raster Sources in ortus

Goal: serve point queries against raster bundles (GeoTIFF/COG) through the **same**
pipeline as GeoPackages — one API, one response, vector + raster merged for a point —
by adding a second adapter behind a generalized port, not a second service.

This plan is grounded in the current code (file:line references are to `master`).

## 0. Locked decisions (2026-06-23)

1. **Domain model:** rename `domain.GeoPackage` → `domain.Source` with a `Kind` (§2.2).
2. **COG library:** run the evaluation spike first, record an ADR (§3.2) — pure-Go preferred.
3. **Runtime validation:** embed the JSON Schema (`go:embed`) and validate (§3.1, §5.3).
4. **Sequencing:** start with the behavior-preserving refactor PR (§7 step 2); the COG
   spike runs independently.

---

## 1. Where the code is coupled to GeoPackage today

The architecture is already hexagonal; only three places hard-wire "the source is a
GeoPackage served by one SpatiaLite repo":

| Concern | Location | Coupling |
|---|---|---|
| Output port | `internal/ports/output/geopackage.go:10` | `GeoPackageRepository` interface (`Open/Close/QueryPoint/CreateSpatialIndex/HasSpatialIndex`). |
| Registry currency | `internal/application/registry.go:22,38` | Stores `*domain.GeoPackage`; one single `repo output.GeoPackageRepository` (`:25`). `LoadPackage` calls `repo.Open` (`:102`) then `repo.CreateSpatialIndex` per layer (`:124`). |
| Query dispatch | `internal/application/query.go:19,233` | One single `repo`; `queryLayer` calls `s.repo.QueryPoint(packageID, layer.Name, coord)`. |
| Discovery filter | `internal/adapters/watcher/watcher.go:160,301` | `isGeoPackageFile` accepts only `.gpkg`. |
| Sync id derivation | `internal/application/registry.go:458` | `derivePackageID` = filename without extension. |

Everything downstream is **already source-agnostic** and needs no change:
`domain.Feature` (`feature.go:6`, just `Properties map[string]interface{}`),
`domain.Layer` (`geopackage.go:48`), `QueryResult`/`QueryResponse`
(`metadata.go:55,84`), property filtering (`query.go:299`), SRID transform
(`query.go:256`), HTTP shaping (`handlers.go:285`, only reads `properties`).

That is the whole reason to extend rather than fork: the seam is small and well-placed.

---

## 2. Target architecture

Introduce one generalized port and make the registry source-type-agnostic.

### 2.1 New output port — `SpatialSource`

`internal/ports/output/spatialsource.go`:

```go
type SpatialSource interface {
    // Supports reports whether this adapter can open the given path
    // (e.g. by extension: *.gpkg vs *.zip).
    Supports(path string) bool

    // Open opens a source file and returns its domain representation.
    Open(ctx context.Context, path string) (*domain.Source, error)

    // Prepare does any post-open readiness work (generalizes
    // CreateSpatialIndex). No-op for raster; builds R-tree for GeoPackage.
    Prepare(ctx context.Context, sourceID, layer string) error

    // QueryPoint samples/queries one layer at a coordinate.
    QueryPoint(ctx context.Context, sourceID, layer string, coord domain.Coordinate) ([]domain.Feature, error)

    // Close releases resources for a source.
    Close(ctx context.Context, sourceID string) error
}
```

The existing `GeoPackageRepository` becomes this interface's first implementation
(rename `CreateSpatialIndex`→`Prepare`, add `Supports`). `HasSpatialIndex` stays as a
GeoPackage-internal detail, off the port.

### 2.2 Domain — `Source` (generalize `GeoPackage`)

Rename `domain.GeoPackage` → `domain.Source` and add a `Kind`. This is mechanical but
touches several files; it is the **recommended** path because keeping the name
"GeoPackage" for a GeoTIFF bundle is misleading and the user values maintainability.

```go
type SourceKind string
const ( SourceKindVector SourceKind = "vector"; SourceKindRaster SourceKind = "raster" )

type Source struct {
    ID, Name, Path string
    Kind           SourceKind
    Size           int64
    Layers         []Layer
    Metadata       Metadata
    License        License
    Indexed        bool
    LoadedAt, LastQueried time.Time
}
```

`domain.Layer` is reused as-is. For raster layers set `GeometryType = GeomRaster`
(new const) and `SRID` = the bundle's canonical CRS. `Layer.HasIndex` = true after
`Prepare` (raster `Prepare` sets it immediately).

> Lighter alternative (if the rename diff is unwelcome): keep the name `GeoPackage`,
> just add `Kind`. Same wiring, smaller diff, worse naming. Recommend the rename.

### 2.3 Registry — own the per-source adapter

`PackageRegistry` today holds one `repo`. Change it to hold a set of providers and
remember which one owns each source:

```go
type packageEntry struct {
    Source *domain.Source
    Repo   output.SpatialSource   // the adapter that opened it
    Status domain.GeoPackageStatus
    Error  error
}

type PackageRegistry struct {
    // ...
    providers []output.SpatialSource   // replaces the single `repo`
}
```

- `LoadPackage(path)` (`registry.go:93`): pick `p := first provider where p.Supports(path)`;
  `src, err := p.Open(...)`; store `entry{Source, Repo: p}`; call `p.Prepare(...)` per
  layer (replaces the `CreateSpatialIndex` loop at `:124`).
- Add `func (r *PackageRegistry) Query(ctx, sourceID, layer, coord) ([]domain.Feature, error)`
  that looks up the entry and delegates to `entry.Repo.QueryPoint(...)`.
- `Sync`/`LoadAll`/`derivePackageID` are unchanged **if** we enforce the bundle
  filename rule below.

### 2.4 Query service — ask the registry, drop the repo

`QueryService` loses its `repo` field (`query.go:19`). `queryLayer` (`:233`) calls
`s.registry.Query(ctx, sourceID, layer.Name, coord)` instead of `s.repo.QueryPoint`.
Everything else in `query.go` — coordinate transform (`:256`), property filter (`:299`),
max-features (`:285`), result merge (`query.go:143`) — stays identical and now works for
raster for free.

---

## 3. The raster adapter

`internal/adapters/raster/` — second `SpatialSource` implementation.

### 3.1 Responsibilities

- `Supports(path)` → `strings.HasSuffix(path, ".zip")`.
- `Open(path)`:
  1. Open the ZIP; locate `ortus-raster.yaml` at root.
  2. Parse YAML → generic map → **validate against the embedded JSON Schema**
     (`go:embed doc/raster-bundle/ortus-raster.schema.json`, validated with
     `santhosh-tekuri/jsonschema`). Single source of truth shared with the pipeline.
  3. Enforce ingest invariants beyond the schema: filename stem == manifest `id`;
     unique layer ids; every referenced COG exists, opens, is in `crs`, has `band`;
     mapping keys parse as ints.
  4. Build `domain.Source{Kind: raster, Layers: [...]}`; open + cache each COG handle,
     its inverse geotransform, and its `map[int]map[string]any` mapping table.
- `Prepare(...)` → no-op (set ready). `Close(...)` → close COG handles, remove temp dir.
- `QueryPoint(sourceID, layer, coord)`:
  1. inverse-affine: `(lon,lat) → (col,row)`; out of bounds → `[]` (no match).
  2. sample band with nearest-neighbor; if value == `nodata` → `[]`.
  3. `props, ok := mapping[value]`; `!ok` → **error** (raster/legend disagree — surfaced,
     not hidden, per the spec).
  4. return one `domain.Feature{ID: int64(value), LayerName: layer, Properties: props}`.

### 3.2 COG library — decision needed (spike first)

Pure-Go candidates (no GDAL/CGO; CGO is already in the build via SpatiaLite, but staying
pure-Go keeps the raster path isolated): `github.com/gden173/geotiff`,
`github.com/tingold/gocog`, `github.com/google/tiff`. Acceptance criteria for the spike:
random single-pixel/tile read without loading the whole image, `uint8`+`int16`+`float32`
bands, internal-tiling aware, maintained. **Task:** 1–2 day spike benchmarking these
against a real Köppen COG; pick one, record the decision in `doc/adr/`.

### 3.3 CRS / reprojection

Bundles are pre-reprojected to a canonical CRS (`crs:` in the manifest), so the adapter
never reprojects. A query in another SRID is transformed to the layer SRID by the
**existing** path (`query.go:256` → SpatiaLite transformer), exactly as for vector layers.
No new reprojection dependency.

---

## 4. Cross-cutting changes

| Area | File | Change |
|---|---|---|
| Discovery | `watcher.go:160,301` | `isGeoPackageFile` → `isSupportedSourceFile` (`.gpkg` **or** `.zip`). |
| Wiring | `cmd/ortus/main.go` | Construct both adapters; pass `providers: [geopackageRepo, rasterRepo]` into `NewPackageRegistry`; drop the `repo` arg from `NewQueryService`. |
| Config | `internal/config/config.go`, `config.yaml.example` | Optional `raster:` block (temp-unzip dir, max bundle size, missing-value policy = error\|skip). Defaults work with zero config. |
| MCP | `internal/adapters/mcp/tools_query.go` | No change to query tool; if a "list sources" tool exposes layer geometry types, allow `RASTER`. |
| HTTP | `handlers.go:285` | None required (only reads `properties`). Optionally surface `kind` in list-packages output. |
| OpenAPI | `api/openapi/*` | Document that features may originate from raster sources; `properties` shape unchanged. |
| Telemetry | adapter | Mirror existing span/metric conventions (`Repository.*` → `raster.*`); reuse `packages.loaded/ready` gauges. |

### Bundle filename rule (keeps Sync correct)
`derivePackageID` (`registry.go:458`) derives the id from the filename. Enforce at ingest
that `<stem>.zip` equals manifest `id` (build.sh already names it so). Then
`Sync`/`IsLoaded` dedup (`registry.go:368,376`) works unchanged across both source types.

---

## 5. Risks & decisions to confirm

1. **Domain rename `GeoPackage`→`Source`** (§2.2): recommended but a wide mechanical diff
   (registry, query, handlers, mcp, tests). Confirm before doing it; the additive-`Kind`
   fallback exists.
2. **COG library** (§3.2): blocking on a spike; pure-Go strongly preferred.
3. **Schema validation at runtime**: embed the JSON Schema and validate (recommended,
   one dep) vs hand-rolled Go checks (no dep, drifts from the schema). Recommend embed.
4. **Partial ZIP during upload**: large drops may fire the watcher mid-write; the ZIP/
   validation fails and the source is rejected, then reloaded on the next stable event.
   Debounce is 500ms (`watcher.go:84`). Acceptable; revisit if large remote drops flap.
5. **Missing-value policy**: spec says hard error. Make it configurable (error\|skip) but
   default to error so raster/legend mismatches surface.

---

## 6. Testing

- **Adapter unit tests**: tiny fixture COG (a few px, known geotransform) + inline
  mapping. Assert: correct pixel for known lon/lat; nodata → no feature; unmapped value →
  error; out-of-bounds → no feature; multi-layer selection.
- **Schema tests**: the negative cases already proven for the spec
  (`mapping` xor `value_mapping`, leading-zero keys, unknown fields, non-EPSG crs) — port
  them into a Go test that loads the embedded schema.
- **Registry/query integration**: a vector `.gpkg` and a raster `.zip` loaded together;
  one point query returns merged results from both; property filter + max-features behave.
- **Watcher**: dropping a `.zip` registers; deleting unregisters.
- Keep `make check` (fmt+vet+lint+test) green; add race tests for the adapter's handle cache.

---

## 7. Sequencing (suggested PRs)

1. **Spike**: COG library evaluation + ADR. (no product code)
2. **Refactor, no behavior change**: introduce `SpatialSource` port + `domain.Source`;
   make `GeoPackageRepository` implement it; registry holds `providers` + per-entry repo;
   query asks the registry. Vector behavior identical, full green suite. *(largest diff,
   lowest risk — pure refactor.)*
3. **Raster adapter**: bundle ingest + schema validation + COG sampling, behind the port.
4. **Discovery + config + wiring**: widen watcher to `.zip`, add `raster:` config, wire
   both adapters in `main.go`.
5. **Docs/OpenAPI/MCP polish** + the Köppen bundle end-to-end as the acceptance test.

Step 2 is the keystone and carries the most risk-by-diff-size but no behavior change, so
it is fully covered by the existing test suite. Steps 3–4 are purely additive.
