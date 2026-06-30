# Implementation plan — Gazetteer & Bearing

| | |
|---|---|
| **Status** | Refined plan, ready to implement |
| **Branch** | `feat/gazetteer` (this plan lives here; milestones branch off it) |
| **Supersedes** | external `SPEC-ortus-gazetteer.md` (draft), reconciled with the real ortus code |
| **Relates to** | ADR-0012 (`Package`→`Source` vocabulary), ADR-0005 (GeoPackage architecture) |

> **How to use this.** A fresh session can start at **M0** below. Each milestone
> is its own PR with the established cadence (`make verify` + `mkdocs build
> --strict` green → CI + Copilot review + threads resolved → merge). The
> **Open decisions** carry a recommended default so work can proceed; confirm
> with the owner at the noted milestone before it hardens.

---

## 1. The decision that shapes everything

ortus today is **schema-agnostic**: `QueryService.QueryPoint` does point-in-polygon
against *any* GeoPackage polygon layer (`ST_Contains`/`MbrContains`) without
knowing its columns. That genericity is a virtue (ADR-0005/0012) — a thematic
[`Source`](decisions/0012-source-vocabulary-migration.md) is just "a file with layers".

The gazetteer is the opposite — it is **opinionated**: it needs a `places` point
layer with a name + prominence signal, admin polygon layers with name + level,
KNN, ellipsoidal distance, and azimuth. It imposes a **contract on the GeoPackage**.

**Resolution (carries through the whole design):** the gazetteer is **not** a
generic thematic `Source`. It is a **distinct capability fed by one dedicated,
manifest-described GeoPackage** (`osm-admin-layers-places`) that does **not** flow
through the generic source-discovery/sync. This keeps the generic core untouched
and isolates the opinionated part. This is the same pattern ortus already uses
for [raster bundles](../reference/raster-bundle.md): an opinionated source whose
structure is declared by a manifest.

The seam from the spec (thematic ⟂ gazetteer, both on a shared geo layer) is kept
— expressed in ortus's hexagonal idiom, **not** the spec's flat layout.

---

## 2. Spec → ortus (hexagonal) mapping

The spec is sound; only the layout changes so depguard / ADR-0001 / ADR-0002 stay intact.

| Spec | ortus (hexagonal) |
|---|---|
| `internal/geo` (sole cgo consumer) | **one** adapter owns cgo; `geo.SpatialDB` becomes an **output port** `SpatialIndex` (`QueryKNN`/`PointInPolygon`/`DistanceKM`/`Azimuth`), implemented by the SpatiaLite adapter |
| `geo.Point` | existing `domain.Coordinate` (lon/lat, EPSG:4326) — reuse, don't add |
| `internal/thematic` | the existing generic `QueryService` + `SpatialSource` port — **unchanged** (optionally formalized behind its interface) |
| `internal/gazetteer` | `domain` (Place, Fix, compass/label — pure) + **input port** `Gazetteer` (Locate/Bearing) + `application/GazetteerService` |
| `salience/` | a **pure** strategy in the application layer (no cgo), injectable |
| `api/` | existing `adapters/http` + `adapters/mcp` gain gazetteer endpoints |

**Boundaries preserved:** cgo lives in one adapter behind a port; salience/compass
are pure domain; `thematic` and `gazetteer` depend only on ports, never on each
other. The semantic seam is real and depguard-clean.

---

## 3. Package layout (within the existing tree)

```
internal/
  domain/
    gazetteer.go        Place, Fix, BearingOptions, compass quantization, label build (PURE)
  ports/
    output/
      spatialindex.go   SpatialIndex port (KNN / PiP / DistanceKM / Azimuth)  ← "geo.SpatialDB"
    input/
      gazetteer.go      Gazetteer port (Locate, Bearing)
  application/
    gazetteer/
      service.go        GazetteerService (orchestrates port + salience)
      salience/
        salience.go     Salience interface
        ranked.go       OSM-rank-based strategy (default)
        weighted.go     population-log strategy (alternative)
  adapters/
    spatialite/         the cgo SpatialIndex impl (KNN/Distance/Azimuth/PiP)
                        — may extend the existing geopackage adapter instead of a new pkg; decide in M1
    gazetteerdata/      loads the dedicated places/admin GeoPackage + manifest
  config/               gazetteer.* keys
```

> The current `adapters/geopackage` already owns cgo + SpatiaLite. **M1 decides**
> whether the `SpatialIndex` impl extends that adapter (add KNN/azimuth/distance
> methods) or lives in a sibling `adapters/spatialite`. Default: **extend the
> existing geopackage adapter** — it already is the single cgo consumer; a second
> cgo adapter would split that ownership.

---

## 4. The GeoPackage contract — `osm-admin-places.gpkg` (verified against the real file)

ortus stays generic; the gazetteer source carries a small **manifest**
(analogous to the raster-bundle manifest) so the mapping is explicit and
versioned, not hard-coded.

The contract below was **verified against the actual generated file** (2026-06-30,
3.1 GiB, EPSG:4326, R-tree indexes present on both layers, SpatiaLite 5.1.0):

- **`places`** (Point, 422,557 features):
  - `place` — class, **exactly three values**: `village` (400,910 ≈ 95%), `town` (19,787), `city` (1,860).
  - **No `population` column.** Prominence = the `place` class only.
  - `name` (99.4% populated — the reliable label field), plus *sparse* localized
    `name_de`/`name_en`/`name_fr`/`name_el` (`name_de` ~88% empty → use only when
    present), `country*`, `osm_id`.
- **`admin_levels`** (MultiPolygon, 364,244 features — **a single layer**, not per-level layers):
  - `admin_level` — string, OSM levels `2`–`12`. **Level `8` = municipality/Gemeinde**
    (155,243 polygons, name ~100% complete). Coarser 6/7 and finer 9/10 also present.
  - `name` (+ localized), `country_iso`, `osm_id`.

> OSM `admin_level` semantics vary by country (the municipality is not always 8),
> so the manifest maps a **target level**, defaulting to 8, with per-country overrides.

**Gazetteer manifest** (declares which layer/column plays which role):

```yaml
# ortus-gazetteer.yaml (shipped alongside the GeoPackage)
places:
  layer: places
  name_column: name        # localized name_* used only when present
  rank_column: place       # village | town | city
  # no population_column — this dataset has none
admin:
  layer: admin_levels
  level_column: admin_level
  name_column: name
  municipality_level: "8"  # default; per-country overrides allowed
```

**Open decision 1 — prominence source (ADR-0017) → RESOLVED by the data.** The
file has **no population at all**, only the 3-class `place` rank. So salience is
**rank-based** (`city > town > village`) — see §6. The population-log model stays
implemented as an *alternative* strategy for a future where GeoNames population is
merged in, but it cannot be the default given this data.

---

## 5. Interfaces (Go, ortus-idiomatic)

```go
// ports/output — the sole cgo-backed primitives ("geo.SpatialDB")
type SpatialIndex interface {
    QueryKNN(ctx context.Context, layer string, p domain.Coordinate, k int, maxKM float64) ([]domain.Feature, error)
    PointInPolygon(ctx context.Context, layer string, p domain.Coordinate) ([]domain.Feature, error)
    DistanceKM(a, b domain.Coordinate) (float64, error)          // SpatiaLite Distance(g1,g2,1)
    Azimuth(from, to domain.Coordinate) (float64, error)         // ST_Azimuth, rad→deg, 0=N 90=E
}

// domain — pure
type Place struct { ID, Name, FeatureCode string; Population int64; Geom domain.Coordinate }
type Fix struct { Reference Place; DistanceKM, Azimuth float64; Compass, Label string }
type BearingOptions struct { RadiusKM float64; MaxCandidates, CompassPoints int; InsideLabelKM float64 }

// ports/input
type Gazetteer interface {
    Locate(ctx context.Context, p domain.Coordinate) (*Place, error)     // reverse geocode → municipality (PiP)
    Bearing(ctx context.Context, p domain.Coordinate, opts BearingOptions) (*Fix, error)
}

// application/gazetteer/salience — pure, swappable
type Salience interface { Rank(p domain.Coordinate, cands []Candidate) []Scored }
```

---

## 6. Salience, metrics, label — good practice for *this* data

The data gives only a coarse 3-class rank (`city > town > village`) and **no
population**. With 95% of points being `village`, a plain nearest-neighbour pick
is useless ("0.8 km N {nearest hamlet}"), and a continuous population score has
nothing to anchor it. So the **recommended default is rank-stratified selection
with class-specific reach radii** — it directly encodes "a city is findable from
far, a village only when you're basically in it" and is interpretable/tunable:

```
pick the nearest CITY    within R_city    (start ~60 km), else
     the nearest TOWN    within R_town    (start ~18 km), else
     the nearest VILLAGE within R_village (start ~5 km),  else
     widen the radii / fall back to Locate() (admin municipality)
```

This resolves the spec's transition cases naturally: city 8 km **beats** village
1 km (city wins outright); city 80 km **loses** to town 5 km (city outside
`R_city`, town inside `R_town`). The three radii are the tunable knobs (M5),
config-injectable, replacing the un-anchorable `w_dist`. Within a class,
distance decides; remaining ties → name, then `osm_id` (deterministic).

- **`ranked` strategy** (default, above) — uses only `place` + distance. Built first.
- **`weighted` strategy** (alternative, `score = w_pop·log(pop+1) − w_dist·distance_km`)
  — kept behind the same `Salience` interface for a future GeoNames-population merge;
  **not usable on this dataset** (no population). All weights config-injectable.
- **Distance** ellipsoidal (`Distance(g1,g2,1)`); **bearing** via `ST_Azimuth`,
  convention *reference→point* ("E von Würzburg" = point east of Würzburg);
  **quantize** to 8/16 points: `idx = round(az/(360/N)) mod N`.
- **Label** `{round(dist)} km {compass} {name}` → "4 km E Würzburg", using the
  native `name` (localized `name_*` only when present). **Inside threshold**
  (e.g. <1 km) → "in/bei {name}", no bearing. **Rounding** <10 km to 0.5 km,
  else 1 km (configurable).

---

## 7. `Bearing()` flow

Because `village` is 95% of points, a single small-`k` KNN would never surface
the salient city. So the service does **class-stratified nearest queries** — one
per class, each cheap — and applies the reach rule:

```
1. For class in [city, town, village]:
     QueryKNN(layer="places", class=class, p, k=1, maxKM=R_class)   → nearest of that class
2. DistanceKM(p, cand) for the hits                                  → []Candidate
3. Salience.Rank(p, candidates)  (reach rule: best class within its radius wins)
4. top-1 = reference
   ├─ no hit in any radius → widen radii once, else fall back to Locate()
   ├─ DistanceKM < InsideLabelKM → "in/bei {name}" (no bearing)
   └─ else: Azimuth(reference, p) → compass → label
5. return Fix
```

Class-stratified queries imply `SpatialIndex.QueryKNN` takes an optional
attribute filter (e.g. `place = ?`) so each class query stays index-cheap rather
than pulling a huge `k` of villages. Steps 1–2 live in the spatialite adapter
(behind the port), step 3 in salience (pure), steps 4–5 in domain (pure).

---

## 8. Milestones (reframed — M0 is greenfield, not refactor)

There is **no existing gazetteer code to move** (confirmed by grep). The generic
thematic PiP stays as-is.

- **M0 — Seam + skeleton.** `domain` gazetteer types + `ports` (SpatialIndex, Gazetteer) + a `GazetteerService` skeleton, **disabled by default** (no endpoint, no data load). Thematic path untouched. *Gate:* existing tests + depguard + `make verify` green; gazetteer compiled but inert.
- **M1 — `SpatialIndex` (cgo).** `QueryKNN`/`DistanceKM`/`Azimuth`/`PointInPolygon` on the SpatiaLite adapter. `VirtualKNN2` is **confirmed available** (SpatiaLite 5.1.0, see §12.3), so KNN is native — no bbox-prefilter fallback. *Gate:* unit tests against a small fixture GeoPackage.
- **M2 — Gazetteer data + `Locate()`.** `gazetteerdata` loader + manifest parsing; `Locate()` (municipality via PiP). Prominence source decided (ADR-0017). *Gate:* eval vs gold-set (§10).
- **M3 — Bearing end-to-end.** `Salience` (`ranked` default) + `Bearing()` + compass + label + edge cases. *Gate:* label snapshot tests.
- **M4 — API.** http + mcp endpoints, DTOs, config-injected weights/options. *Gate:* integration test.
- **M5 — Tuning & eval.** Calibrate the `w_dist` steepness / rank thresholds against field scenarios.

---

## 9. ADRs to write (renumbered — 0013 is taken by cog-reader)

| ADR | Title | When |
|---|---|---|
| ADR-0014 | Gazetteer as an internal capability (not a separate service, not a generic Source) | M0 |
| ADR-0015 | Salience model: prominence-weighted distance discounting | M2 |
| ADR-0016 | Bearing convention & compass quantization | M3 |
| ADR-0017 | Prominence source: OSM rank vs GeoNames population *(open)* | before M2 |

---

## 10. Test & eval strategy

- **Unit:** compass quantization (azimuth table), rounding rules, tie-break determinism.
- **Gold-set:** curated `coordinate → expected label` fixtures from real field sites — e.g. the dry-grassland sites near Astheim/Volkach should bear on a *findable* reference (Volkach / Würzburg), not the nearest hamlet. Hardest test for `w_dist` steepness.
- **Snapshot:** stable labels across releases (surfaces regressions on dataset updates).
- **Property:** azimuth(ref→point) vs back-azimuth consistent; distance symmetric.

---

## 11. Harness obligations (this feature must satisfy the existing gates)

The repo's harness (see [Technical debt](technical-debt.md)) applies to the new code:

- **depguard:** cgo only in the spatialite adapter, behind `SpatialIndex`; salience/compass pure. No `thematic`↔`gazetteer` import.
- **Coverage floors:** new packages get floors in `.coverage-floors` (domain/application high; the cgo adapter at the I/O-adapter tier).
- **goleak:** the gazetteer-data loader holds a `*sql.DB` → must `Close` (add `TestMain`/`Close`, like the transformer fix).
- **Fuzz:** new parse boundaries get fuzz targets — the **manifest parser** and the **label/compass** builder.
- **Config-drift:** new `gazetteer.*` keys → add to `config.yaml.example` (the `TestConfigExampleNoDrift` gate enforces sync).
- **Licenses:** any new dependency (KNN/PROJ helpers) must be in the `go-licenses` allowlist.
- **Docs:** the API endpoint + the GeoPackage contract get a how-to + reference page under `docs/`, and `mkdocs build --strict` must stay green.

---

## 12. Open decisions (confirm at the noted milestone)

1. ~~**Prominence source** (ADR-0017)~~ — **RESOLVED by the data (2026-06):** the
   `osm-admin-places.gpkg` has no population column, only the 3-class `place` rank
   (`village`/`town`/`city`). Salience is **rank-based**; the population-log model
   is an alternative strategy reserved for a future GeoNames merge. See §4, §6.
2. **One GeoPackage** — confirmed in shape by the real file: a single `places`
   layer plus a single `admin_levels` layer (not multiple `admin_*` tables;
   `admin_level` is a column, municipality = `"8"`). Manifest contract in §4 reflects
   this. *Confirm the artifact is published as one file at M2.*
3. ~~**VirtualKNN2 availability** vs R-tree fallback~~ — **RESOLVED by a read-only
   spike (2026-06):** the deployed SpatiaLite is **5.1.0 with `VirtualKNN2`
   present and working**, and the file already carries R-tree indexes. M1 uses
   native KNN; the bbox-prefilter fallback is dropped (kept only as a note should a
   target platform ship an older SpatiaLite).
4. **>2 GiB distribution** — versioned object-storage URL, loaded as a dedicated artifact (not via generic sync discovery). Confirm at M2. *(The real file is ~3.1 GiB, so this path is mandatory, not hypothetical.)*
5. **SpatialIndex impl location** — *recommended: extend the existing geopackage adapter* (single cgo owner) vs a new `adapters/spatialite`; decide at M1.
