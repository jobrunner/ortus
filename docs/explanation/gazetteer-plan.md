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

## 4. The GeoPackage contract — `osm-admin-layers-places`

This is the "realistic requirements on a GeoPackage". ortus stays generic; the
gazetteer source carries a small **manifest** (analogous to the raster-bundle
manifest) so the mapping is explicit and versioned, not hard-coded.

**Required layers**

- **`places`** (Point, SRID 4326): `name` (text), prominence signal, representative point geometry, **R-tree index mandatory**.
  - Prominence: OSM `place` class (`city|town|village|hamlet|…`) and *optionally* `population` (often sparse in OSM).
- **`admin_*`** (Polygon, one or more levels): `name`, `admin_level` (OSM convention: 8 = municipality/Gemeinde), geometry.

**Gazetteer manifest** (declares which layer/column plays which role):

```yaml
# ortus-gazetteer.yaml (shipped alongside / inside the GeoPackage)
places:
  layer: places
  name_column: name
  rank_column: place          # OSM place class
  population_column: population # optional
admin:
  - { layer: admin_8, level: municipality, name_column: name }
  - { layer: admin_6, level: district,     name_column: name }
```

**Open decision 1 — prominence source (ADR-0017, currently open).** Because the
data is `osm-admin-layers-places`, **recommended default: rank-based salience**
(§6) using the OSM `place` class with population as a tiebreaker — robust against
OSM's sparse `population`. Switch to population-log (§6) only if GeoNames data is
merged in. *This materially shapes the salience function — confirm before M2.*

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

## 6. Salience, metrics, label (from spec §6, kept)

- **Default strategy `ranked`**: `PPLC/city > town > village > …` gate + population/distance tiebreak (see Open decision 1).
- **Alternative `weighted`**: `score = w_pop·log(pop+1) − w_dist·distance_km`; start `w_pop=1.0`, `w_dist=0.35`; **all weights config-injectable**, never hard-coded.
- **Distance** ellipsoidal (`Distance(g1,g2,1)`); **bearing** via `ST_Azimuth`, convention *reference→point* ("E von Würzburg" = point east of Würzburg); **quantize** to 8/16 points: `idx = round(az/(360/N)) mod N`.
- **Label** `{round(dist)} km {compass} {name}` → "4 km E Würzburg"; **inside threshold** (e.g. <1 km) → "in/bei {name}", no bearing; **no candidate** → widen radius 50→100→200 km, else fall back to `Locate()`; **tie-break** population → distance → id; **rounding** <10 km to 0.5 km, else 1 km (configurable).

---

## 7. `Bearing()` flow

```
1. SpatialIndex.QueryKNN(layer="places", p, k=MaxCandidates, maxKM=RadiusKM)
2. SpatialIndex.DistanceKM(p, cand) for each            → []Candidate
3. Salience.Rank(p, candidates)                          → []Scored, desc
4. top-1 = reference
   ├─ DistanceKM < InsideLabelKM → "in/bei {name}" (no bearing)
   └─ else: Azimuth(reference, p) → compass → label
5. return Fix
```
Steps 1–2 in the spatialite adapter (behind the port), step 3 in salience (pure),
steps 4–5 in domain (pure). Clean responsibility split along the seam.

---

## 8. Milestones (reframed — M0 is greenfield, not refactor)

There is **no existing gazetteer code to move** (confirmed by grep). The generic
thematic PiP stays as-is.

- **M0 — Seam + skeleton.** `domain` gazetteer types + `ports` (SpatialIndex, Gazetteer) + a `GazetteerService` skeleton, **disabled by default** (no endpoint, no data load). Thematic path untouched. *Gate:* existing tests + depguard + `make verify` green; gazetteer compiled but inert.
- **M1 — `SpatialIndex` (cgo).** `QueryKNN`/`DistanceKM`/`Azimuth`/`PointInPolygon` on the SpatiaLite adapter; **verify VirtualKNN2** in the deployed SpatiaLite, else R-tree-bbox-prefilter + manual sort fallback (Open decision 3). *Gate:* unit tests against a small fixture GeoPackage.
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

1. **Prominence source** (ADR-0017) — *recommended: OSM rank-based*; confirm before M2.
2. **One GeoPackage** with `places` + `admin_*` layers — *recommended: yes*; confirm at M2.
3. **VirtualKNN2 availability** vs R-tree fallback — *verify empirically at M1* (a read-only spike against a fixture GeoPackage is cheap).
4. **>2 GiB distribution** — versioned object-storage URL, loaded as a dedicated artifact (not via generic sync discovery). Confirm at M2.
5. **SpatialIndex impl location** — *recommended: extend the existing geopackage adapter* (single cgo owner) vs a new `adapters/spatialite`; decide at M1.
