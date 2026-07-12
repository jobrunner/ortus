# Reference: GeoPackage schema

File: `output/osm-admin-places.gpkg` · format: GeoPackage (SQLite) · CRS: **EPSG:4326**.
`admin_levels` carries the full country attribute set; `places` carries only
`country_iso` (its country *names* are dropped as derivable — see below) and links to
its containing admin unit via `admin_id`.

## Layer `admin_levels`

Geometry: `MULTIPOLYGON`. OSM administrative boundaries (admin_level 2–12) plus
country-level coverage-fill features.

| Column | Type | Notes |
|---|---|---|
| `osm_id` | TEXT | OSM id; coverage fills use `ne-fill-<ISO>` |
| `admin_level` | TEXT | numeric string `2`–`12`; **NULL** for coverage fills |
| `name` | TEXT | romanized endonym — **always Latin**, one documented standard per script (see [romanization](romanization.md)) |
| `name_native` | TEXT | original-script endonym (verbatim OSM `name`); **NULL** where `name` was already Latin |
| `name_source` | TEXT | per-row provenance of `name` (controlled code, e.g. `latin-osm`, `translit-uk-2010`, `gns-bgn`; see [manifest](../../name_source_manifest.yaml)) |
| `name_de`, `name_en`, `name_fr`, `name_el` | TEXT | localized names (sparse, from OSM) |
| `country` | TEXT | canonical English country name |
| `country_de`, `country_en`, `country_fr`, `country_el` | TEXT | localized country names |
| `country_iso` | TEXT | **ISO 3166-1 alpha-2**, 100 % populated |
| `parent_id` | INTEGER | `fid` of the immediate broader enclosing admin unit (same country); **NULL** at the top of the chain / for coverage fills. See [Hierarchy](#hierarchy). |
| `geom` | MULTIPOLYGON | EPSG:4326 |

## Layer `places`

Geometry: `POINT`. Settlements only: `place` ∈ {`village`, `town`, `city`}.

| Column | Type | Notes |
|---|---|---|
| `osm_id` | TEXT | OSM node id |
| `place` | TEXT | `village` \| `town` \| `city` |
| `name` | TEXT | romanized endonym — **always Latin**, one documented standard per script (see [romanization](romanization.md)) |
| `name_native` | TEXT | original-script endonym (verbatim OSM `name`); **NULL** where `name` was already Latin |
| `name_source` | TEXT | per-row provenance of `name` (controlled code; see [manifest](../../name_source_manifest.yaml)) |
| `name_de`, `name_en`, `name_fr`, `name_el` | TEXT | localized names (sparse) |
| `population` | INTEGER | OSM `population` (parsed to a non-negative int); sparse MENA village tail backfilled from GeoNames; **NULL** if unknown. Fill: city ~97 %, town ~84 %, village ~50 %. |
| `capital` | TEXT | OSM `capital` rank of the unit this place is the seat of (`2`=country … `8`=municipality, or `yes`); **NULL** if not a seat |
| `wikidata` | TEXT | OSM `wikidata` QID; presence is a notability proxy (~71 % filled); **NULL** if untagged |
| `country_iso` | TEXT | **ISO 3166-1 alpha-2**, 100 % populated |
| `admin_id` | INTEGER | `fid` of the most-local `admin_levels` unit containing the point (same country); **NULL** in coverage holes. See [Hierarchy](#hierarchy). |
| `geom` | POINT | EPSG:4326 |

> **Prominence columns** (`population`, `capital`, `wikidata`) are added by
> `make enrich-places` (after romanize). They exist only on `places` and drive the ortus
> bearing anchor salience (`CompositeSalience`). Values are as-tagged in OSM (garbage-in:
> the odd node carries a wrong population); the log-scaled salience damps outliers. See
> [Field provenance](#field-provenance-source-of-each-field).

> **Dropped from `places`:** the denormalized country *name* columns
> (`country`, `country_de/en/fr/el`) were removed — they are fully derivable from
> `country_iso`. `country_iso` itself is **kept** (it is the reliable country key even
> where the `admin_id`/`parent_id` chain never reaches an `admin_level=2` node).

## Hierarchy

The two integer foreign keys make the dataset relational, not just spatial — so a
consumer (e.g. ortus) can walk the administrative chain without repeated
point-in-polygon queries. Built by `make link-hierarchy` (after `normalize-schema`).

- **`places.admin_id` → `admin_levels.fid`** — the *most-local* admin unit (largest
  numeric `admin_level`) of the same country that contains the point. ~99 % populated;
  NULL where the point falls only in a coverage hole (no real admin polygon).
- **`admin_levels.parent_id` → `admin_levels.fid`** — the *immediate broader* enclosing
  unit (nearest smaller `admin_level`, same country), determined by a representative
  interior point (`ST_PointOnSurface`). Walk `parent_id` repeatedly to get the full
  chain; each hop's `admin_level` tells you the tier. NULL at the top (e.g. the
  `admin_level=2` node, or wherever no broader tier exists — see the *missing L2* set in
  `validation_report.md`).

The chain is **numeric/relational only**. The *meaning* of each `admin_level` per
country (e.g. DE 6 → county "Landkreis", 8 → municipality "Gemeinde") lives in the
sidecar **[`admin_levels_west_palearctic.yaml`](../../admin_levels_west_palearctic.yaml)**,
keyed by `(country_iso, admin_level)`. Indexes: `idx_places_admin`,
`idx_admin_parent`, `idx_admin_country_lvl`.

## Field provenance (source of each field)

Where every field comes from. Three origins: **OSM** (the object's own tags),
**Natural Earth** (assigned by spatial join on the geometry), and **derived**
(computed in the build). Names are never invented — they are exactly as complete as
OSM is.

| Field | Source | Exact origin |
|---|---|---|
| `osm_id` | OSM | object id of the boundary relation / place node |
| `admin_level` *(admin)* | OSM | tag `admin_level`; forced numeric in `normalize-schema` (non-numeric → NULL) |
| `place` *(places)* | OSM | tag `place` (filtered to `village`/`town`/`city`) |
| `population` *(places)* | OSM + GeoNames | OSM tag `population` (re-read from the place PBFs by `make enrich-places`); sparse MENA village tail backfilled from GeoNames (CC BY 4.0), matched by folded native name + nearest populated-place coordinate |
| `capital` *(places)* | OSM | tag `capital` verbatim (admin rank of the seat) |
| `wikidata` *(places)* | OSM | tag `wikidata` (QID) |
| `name` | OSM + derived | OSM tag `name`, then romanized to Latin by `make romanize` / `romanize-gazetteers` (documented standard per script; curated OSM/gazetteer forms for Arabic/Hebrew) |
| `name_native` | OSM | verbatim OSM `name` before romanization; NULL if `name` was already Latin |
| `name_source` | derived | controlled code identifying the romanization method/standard/source for `name` |
| `name_de` | OSM | tag `name:de` |
| `name_en` | OSM | tag `name:en` |
| `name_fr` | OSM | tag `name:fr` |
| `name_el` | OSM | tag `name:el` |
| `country_iso` | Natural Earth | `ISO_A2` of the NE admin-0 country the geometry falls in; sea-centroid gaps resolved by nearest NE country; folded to valid ISO 3166-1 alpha-2 |
| `country` *(admin)* | Natural Earth | canonical `NAME`, re-derived in `normalize-schema` from a single NE lookup keyed by `country_iso` |
| `country_de/en/fr/el` *(admin)* | Natural Earth | NE `NAME_DE/EN/FR/EL` via the same `country_iso` lookup |
| `admin_id` *(places)* | derived | `make link-hierarchy`: most-local containing `admin_levels.fid` (same-country point-in-polygon) |
| `parent_id` *(admin)* | derived | `make link-hierarchy`: immediate broader enclosing `admin_levels.fid` (same-country, via `ST_PointOnSurface`) |
| `geom` | OSM | place node coordinate / boundary multipolygon, stored EPSG:4326 |

Notes:

- The **`name_*`** columns come straight from OSM's `name:<lang>` tags (extracted via
  `hstore_get_value(other_tags, 'name:xx')`) and are **sparse** — only present where
  OSM has that translation. `name` (untagged language) is the most complete.
- The **`country_*`** name columns (on `admin_levels`) are keyed off **one** value,
  `country_iso`, so the same code always yields the same names (no per-feature drift).
  The raw build fills them from the NE spatial join (`NAME`, `NAME_DE`, …, `ISO_A2`);
  `normalize-schema` re-derives the names canonically from `country_iso`. `places`
  keeps only `country_iso` (its name columns are dropped in `link-hierarchy` as
  derivable). See [Schema normalization](../explanation/schema-normalization.md) and the
  [ISO code policy](../explanation/iso-code-policy.md).
- The **`admin_id` / `parent_id`** foreign keys are computed geometrically by
  `make link-hierarchy` and are purely relational; their semantics are in
  [Hierarchy](#hierarchy).
- **Coverage-fill** features (`osm_id = 'ne-fill-<ISO>'`) are the one exception to
  "geom from OSM": their geometry is the Natural Earth country outline clipped against
  OSM admin coverage, and their country attributes are the country's own canonical
  values. See [country boundaries & fills](../explanation/country-boundaries-and-fills.md).

## Spatial indexes

GeoPackage RTree extension on both geometry columns: `rtree_places_geom`,
`rtree_admin_levels_geom`. Use them as a bounding-box prefilter before `ST_Contains` /
`ST_Distance` (see the [reverse-geocode how-to](../how-to/reverse-geocode-a-coordinate.md)).

## Coverage-fill features

Synthetic features added by `normalize-schema` to make the region gapless:
`osm_id = 'ne-fill-<ISO>'`, `admin_level = NULL`, country attributes set, geometry =
the country area not covered by any OSM admin polygon. Non-overlapping with real
features. See [Explanation: country boundaries & fills](../explanation/country-boundaries-and-fills.md).

## Conventions & guarantees

- `country_iso` values are valid ISO 3166-1 alpha-2 (incl. user-assigned `XK`,
  official `SJ`); never the non-standard `CY-NC` — see
  [ISO code policy](../explanation/iso-code-policy.md).
- The country name is unique per `country_iso` (verified).
- These guarantees hold only **after `make normalize-schema`** and are enforced by
  [`make verify`](../how-to/verify-the-geopackage.md).

## Embedded metadata

`gpkg_metadata` carries license + attribution + sources (written by `make metadata`):
license ODbL 1.0; attribution "Contains data (c) OpenStreetMap contributors, ODbL 1.0;
Natural Earth (public domain); this product uses data from GeoNames (geonames.org),
CC BY 4.0; NGA GEOnet Names Server (public domain)". The GeoNames CC BY line is required
because gazetteer romanizations are baked into `name` values; `make verify` asserts it.
