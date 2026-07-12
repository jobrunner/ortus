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

The base layers below were **verified against the actual generated file** (2026-06-30,
3.1 GiB, EPSG:4326, R-tree indexes present on both layers, SpatiaLite 5.1.0). The
**relational columns** (`places.admin_id`, `admin_levels.parent_id`) and the **dropped
country-name columns** come from an agreed rebuild of the GeoPackage project — see
`PLAN-places-admin-hierarchy.md` in the `osm-data` repo for the build spec.

- **`places`** (Point, 422,557 features):
  - `place` — class, **exactly three values**: `village` (400,910 ≈ 95%), `town` (19,787), `city` (1,860).
  - `population`, `capital`, `wikidata` — prominence signals added by `make enrich-places`
    (OSM tags, GeoNames-backfilled population tail); drive the composite anchor salience.
    See [ADR-0018](decisions/0018-composite-salience-prominence.md). (Originally absent — [ADR-0017](decisions/0017-prominence-source-rank.md).)
  - `name` (99.4% populated — the reliable label field), plus *sparse* localized
    `name_de`/`name_en`/`name_fr`/`name_el` (`name_de` ~88% empty → use only when present), `osm_id`.
  - `country_iso` — **kept** as the reliable country anchor (Natural-Earth-derived, 100%).
  - `admin_id` *(rebuild)* — FK → `admin_levels.fid` of the most-local containing
    admin unit (same country); **NULL in coverage holes** → fall back to `country_iso`.
  - The four denormalized country **name** columns (`country`, `country_de/en/fr/el`)
    are **dropped** — derivable from `country_iso`; ortus localizes downstream.
- **`admin_levels`** (MultiPolygon, 364,244 features — **a single layer**, not per-level layers):
  - `admin_level` — string, OSM levels `2`–`12` (or NULL for coverage fills). **Level
    `8` = municipality/Gemeinde** (155,243 polygons, name ~100% complete) *in DE* —
    see the semantic note below. Coarser 6/7 and finer 9/10 also present.
  - `name` (+ localized), `country_iso`, `osm_id`.
  - `parent_id` *(rebuild)* — FK → `admin_levels.fid` of the immediate **broader**
    enclosing unit (same country); **NULL at the top of the chain** (e.g. countries
    with no imported L2 polygon). Walked by ortus to resolve the full 2–8 hierarchy.

> **Admin-level semantics are not a fixed number.** OSM `admin_level` means different
> things per country (municipality is 8 in DE but not universally). ortus does **not**
> hard-code level numbers: it resolves meaning through the **sidecar reference**
> (below), keyed `(country_iso, admin_level) → equivalent`.

> **SRID metadata (found in M1, resolved in M5).** Ellipsoidal `Distance(g1,g2,1)` —
> used for the KNN radius, ordering, and label distance — needs SpatiaLite to resolve
> the SRID 4326 ellipsoid from its **native** `spatial_ref_sys`. A GeoPackage carries
> only `gpkg_spatial_ref_sys`, so SpatiaLite prints `unknown SRID: 4326` and falls back
> to the WGS84 ellipsoid — correct for 4326 data (verified: 1° latitude ≈ 110.6 km),
> but stderr-noisy and fallback-dependent. Two guards: (a) `VerifySRID` probes this at
> startup and asserts 1° latitude is ~110–111 km, so **both** a NULL (radius drops all
> rows) **and** a misapplied SRID (silently wrong distance) fail loudly; (b) the data
> build should ship a native `spatial_ref_sys` (`SELECT InitSpatialMetaData(1)` as a
> final step — see the osm-data `PLAN-places-admin-hierarchy.md`) to remove the warning
> and make the metric metadata-backed. The M1 fixture already initializes it.

**Gazetteer manifest** (declares which layer/column plays which role):

```yaml
# ortus-gazetteer.yaml (shipped alongside the GeoPackage)
places:
  layer: places
  name_column: name          # localized name_* used only when present
  rank_column: place         # village | town | city
  admin_fk: admin_id         # → admin_levels.fid (most-local containing unit)
  country_column: country_iso
  population_column: population   # + capital_column, notability_column (make enrich-places)
admin:
  layer: admin_levels
  level_column: admin_level
  name_column: name
  parent_fk: parent_id       # → admin_levels.fid (broader enclosing unit)
  country_column: country_iso
  # admin-level meaning + bearing constraint tier come from the sidecar:
  level_reference: admin_levels_west_palearctic.yaml
  bearing_constraint_tier: state   # semantic equivalent, resolved per-country
```

**Sidecar reference — `admin_levels_west_palearctic.yaml`** (shipped beside the
GeoPackage, `version: 1`). Maps `(country_iso, admin_level) → { name, equivalent }`
with `equivalent ∈ {country, state, region, province, county, district, municipality,
borough, parish, submunicipality, other}`. ortus uses it for two things:

1. **Locate enrichment** — label each level of the resolved admin chain with its
   meaning (DE L6 → `county` "Landkreis", L8 → `municipality` "Gemeinde").
2. **Bearing boundary constraint** — the constraint tier is **semantic** (`state`,
   the agreed default), resolved per-country, *not* the literal number 4. ortus walks
   the query point's `parent_id` chain, finds the `state`-tier ancestor via this
   mapping, and restricts bearing anchors to places sharing it (see §7).

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
    // Filter is an optional attribute predicate (column, values) — used both for the
    // class query (place IN {city}) and the admin boundary constraint (admin_id IN {…}).
    QueryKNN(ctx context.Context, layer string, p domain.Coordinate, k int, maxKM float64, f *Filter) ([]domain.Feature, error)
    PointInPolygon(ctx context.Context, layer string, p domain.Coordinate) ([]domain.Feature, error)
    // ResolveChain walks admin_levels.parent_id from a starting fid up to the top.
    ResolveChain(ctx context.Context, layer string, fromFID int64) ([]AdminRow, error)
    DistanceKM(a, b domain.Coordinate) (float64, error)          // SpatiaLite Distance(g1,g2,1)
    Azimuth(from, to domain.Coordinate) (float64, error)         // ST_Azimuth, rad→deg, 0=N 90=E
}
type Filter struct { Column string; Values []any }              // e.g. {"place", {"city"}}
type AdminRow struct { FID, ParentFID int64; Level int; Name, CountryISO string }

// domain — pure
type PlaceClass int                                              // ordered: Village < Town < City
type Place struct { Name string; Class PlaceClass; AdminID int64; At domain.Coordinate }
type AdminUnit struct { Level int; Name, Equivalent string }     // Equivalent from the sidecar
type Locality struct { CountryISO string; Chain []AdminUnit }    // resolved 2–8 hierarchy
type Fix struct { Reference Place; DistanceKM, Azimuth float64; Compass, Label string }

// BearingPolicy is DATA, not branches: a reach radius per class + the semantic
// boundary tier. Adding a class = one row; no code change. (See §6/§7.)
type BearingPolicy struct {
    Reach          map[PlaceClass]float64   // km, e.g. {Village:5, Town:18, City:60}
    ConstraintTier string                   // semantic equivalent, default "state"
    InsideLabelKM  float64
    CompassPoints  int                      // 8 or 16
}

// ports/input
type Gazetteer interface {
    Locate(ctx context.Context, p domain.Coordinate) (*Locality, error)  // reverse geocode → full admin chain
    Bearing(ctx context.Context, p domain.Coordinate, pol BearingPolicy) (*Fix, error)
}

// application/gazetteer/salience — pure, swappable
type Salience interface { Rank(p domain.Coordinate, cands []Candidate) []Scored }
```

The composed HTTP response keeps the three concerns as distinct sections —
`sources` (generic PiP, untouched), `admin` (the `Locate` chain, each level labelled
via the sidecar), `bearing` (the `Fix`). Composition happens in the application/HTTP
layer; the generic PiP engine never learns about the gazetteer.

---

## 6. Salience, metrics, label — good practice for *this* data

The data gives only a coarse 3-class rank (`city > town > village`) and **no
population**. With 95% of points being `village`, a plain nearest-neighbour pick
is useless ("0.8 km N {nearest hamlet}"), and a continuous population score has
nothing to anchor it. So the **recommended default is rank-stratified selection
with class-specific reach radii** — it directly encodes "a city is findable from
far, a village only when you're basically in it" and is interpretable/tunable.

The selection is **branch-free**: not an `if city … else if town …` cascade but a
single rule over the `BearingPolicy.Reach` table (§5). A candidate is *eligible* when
`distance ≤ Reach[class]`; among eligible candidates the **most salient class wins**,
distance breaks ties:

```
eligible  = { c | DistanceKM(p, c) ≤ Reach[c.Class] }     // Reach is data, not branches
reference = argmax over eligible by (Class salience, then −distance)
            (none eligible → widen radii once, else fall back to Locate())
```

This resolves the spec's transition cases naturally: city 8 km **beats** village
1 km (city more salient, both eligible); city 80 km **loses** to town 5 km (city not
eligible, town is). Adding a 4th class (`hamlet`) = one row in `Reach`, no code
change. The radii are the tunable knobs (M5), config-injectable, replacing the
un-anchorable `w_dist`. Remaining ties → name, then `osm_id` (deterministic).

- **`ranked` strategy** (default, above) — uses only `place` + distance. Built first.
- **`weighted` strategy** (alternative, `score = w_pop·log(pop+1) − w_dist·distance_km`)
  — kept behind the same `Salience` interface for a future GeoNames-population merge;
  **not usable on this dataset** (no population). All weights config-injectable.
- **Distance** ellipsoidal (`Distance(g1,g2,1)`); **bearing** via `ST_Azimuth`,
  convention *reference→point* ("E von Würzburg" = point east of Würzburg);
  **quantize** to 8/16 points: `idx = round(az/(360/N)) mod N`.
- **Label** `{round(dist)} km {compass} {name}` → "4 km E Würzburg", using the
  native `name` (localized `name_*` only when present). **Inside threshold**
  (e.g. <1 km) → "in/prope {name}", no bearing. **Rounding** <10 km to 0.5 km,
  else 1 km (configurable).

---

## 7. `Bearing()` flow

Because `village` is 95% of points, a single small-`k` KNN would never surface
the salient city. So the service does **class-stratified nearest queries** — one
per class, each cheap — constrained to the same administrative tier, then applies
the reach rule:

```
0. Locate(p): PointInPolygon → most-local admin fid → ResolveChain (parent_id walk)
   → resolve the ConstraintTier ancestor (default "state") via the sidecar mapping
   → allowed = { admin_id … } under that ancestor   (∅ ⇒ skip the constraint, e.g. coverage hole)
1. For class in [city, town, village]:
     QueryKNN("places", p, k=1, maxKM=Reach[class],
              filter = place=class  AND  admin_id IN allowed)   → nearest eligible of that class
2. DistanceKM(p, cand) for the hits                              → []Candidate
3. Salience.Rank(p, candidates)  (branch-free eligibility + most-salient, §6)
4. top-1 = reference
   ├─ no eligible hit → widen radii once, else fall back to Locate()
   ├─ DistanceKM < InsideLabelKM → "in/prope {name}" (no bearing)
   └─ else: Azimuth(reference, p) → compass → label
5. return Fix
```

The boundary constraint is a **relational attribute filter**, not a runtime spatial
join: step 0 resolves the allowed `admin_id` set from the `parent_id` chain + sidecar
tier, and passes it to `QueryKNN` alongside the class predicate. Both fold into the
one `Filter` (§5). Step 0 + 1–2 live in the spatialite adapter (behind the port),
step 3 in salience (pure), steps 4–5 in domain (pure).

---

## 8. Milestones (reframed — M0 is greenfield, not refactor)

There is **no existing gazetteer code to move** (confirmed by grep). The generic
thematic PiP stays as-is.

- **M0 — Seam + skeleton. ✅ done.** `domain` gazetteer types (PlaceClass, Place, AdminUnit, Locality, Fix, BearingPolicy, compass/label) + `ports` (`output.SpatialIndex`, `input.Gazetteer`) + a `GazetteerService` skeleton, **inert** until enabled+wired. Thematic path untouched. Landed depguard/lint/debt green; domain 100% covered.
- **M1 — `SpatialIndex` (cgo). ✅ done.** `QueryKNN` (R-tree bbox + ellipsoidal `Distance` + attribute `Filter`)/`PointInPolygon`/`ResolveChain`/`DistanceKM`/`Azimuth` on the existing `geopackage` adapter (single cgo owner). KNN uses the filtered radius search, **not** VirtualKNN2 (§12.3). *Gate met:* integration tests on a nested places/admin fixture, both R-tree and full-scan paths. See the SRID note in §4.
- **M2 — `Locate()` + manifest/sidecar contract.** `Manifest` + `LevelResolver` (the `(country_iso, admin_level) → equivalent` sidecar abstraction); `Locate()` orchestrated in the service (admin PiP → enriched, most-local-first chain). Works on the *current* real file (admin PiP needs no rebuild). **Verify `Distance(...,1)` against the real file** (§4 SRID note). *Gate:* service tests with a fake index. (Salience + Bearing are M3.)
- **M3 — Bearing end-to-end. ✅ done.** `RankedSalience` (branch-free eligibility + most-salient) + `Bearing()` (class-stratified KNN → boundary constraint via `ResolveChain` state-ancestor compare → compass/label). *Gate met:* service tests with a fake index.
- **M4 — API + wiring. ✅ done.** `gazetteer.*` config (disabled by default) + composition-root wiring (dedicated GeoPackage, out of competition); `GET /api/v1/gazetteer` (Option A) returning `{coordinate, admin, bearing}`; opt-in `with-gazetteer=1` enrichment on `/query` (best-effort); MCP `gazetteer` tool (registered only when wired). *Gate met:* handler tests + MCP client test with a fake gazetteer. The `{admin, bearing}` object is the reusable unit for the future batch endpoint (caller-chosen echo id).
- **M5 — Verification, tuning & eval. ✅ done.** **Validated against the real rebuilt file (2026-07-02).** `VerifySRID` passes (ellipsoidal `Distance` computes correctly despite cosmetic SpatiaLite `unknown SRID` stderr — pre-existing `CastAutomagic` behaviour). `Locate(Würzburg)` returns the full sidecar-enriched chain (Altstadt → county → region → state Bayern → Deutschland). **Calibration resolved:** a **proximity override** (nearest town-or-larger within `PreferNearestKM`, default 5 km, villages excluded) now beats a far city, so a point ~2 km from Volkach yields "prope Volkach" and one near Dettelbach "3 km N Dettelbach", while genuinely rural points still peil to the salient city. Reach radii + override are **config-injectable** (`gazetteer.bearing.*`). Opt-in e2e test + calibration sweep in `internal/app` (env-gated, skips in CI).

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
3. ~~**VirtualKNN2 availability** vs R-tree fallback~~ — **RESOLVED, but the choice
   flipped during M1 implementation.** `VirtualKNN2` *is* present (SpatiaLite 5.1.0)
   and the file carries R-tree indexes — but KNN2 **cannot push an attribute
   predicate** (place-class / admin membership) into the nearest search, and *every*
   gazetteer query is class- and boundary-constrained. So M1 deliberately uses an
   **R-tree bbox pre-filter + exact ellipsoidal `Distance` (radius + ORDER BY)**,
   which supports the filter. This is the right tool here, not a fallback; VirtualKNN2
   would only fit an unfiltered nearest search.
4. **>2 GiB distribution** — versioned object-storage URL, loaded as a dedicated artifact (not via generic sync discovery). Confirm at M2. *(The real file is ~3.1 GiB, so this path is mandatory, not hypothetical.)*
5. **SpatialIndex impl location** — *recommended: extend the existing geopackage adapter* (single cgo owner) vs a new `adapters/spatialite`; decide at M1.
6. **Relational rebuild dependency** — the §4 contract assumes the GeoPackage carries
   `places.admin_id` + `admin_levels.parent_id` and has dropped the four country-name
   columns. This is an **agreed rebuild of the `osm-data` project** (spec:
   `PLAN-places-admin-hierarchy.md` there). Until that ships, M1–M2 run against a
   fixture; the boundary constraint (§7 step 0) is **inert without `parent_id`** —
   the service must degrade to unconstrained class queries when the columns are
   absent. Confirm the rebuilt artifact before M3 (Bearing endpoint).
7. **Boundary constraint tier** — default **`state`** (per the agreed answer: anchors
   stay within the Bundesland/first-order subdivision), resolved per-country via the
   sidecar `equivalent`. Single `BearingPolicy.ConstraintTier` knob; revisit against
   the gold-set if it excludes good cross-border anchors (e.g. Aschaffenburg).
