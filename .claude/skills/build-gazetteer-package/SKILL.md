---
name: build-gazetteer-package
description: >-
  Build the ortus gazetteer GeoPackage (places + admin_levels layers with name
  romanization/provenance, place↔admin hierarchy, gapless country coverage) plus
  its sidecars (ortus-gazetteer.yaml manifest, admin_levels_west_palearctic.yaml
  level reference, name_source_manifest.yaml). Use when creating or refreshing the
  reverse-geocoding + bearing ("Peilung") dataset that ortus loads via
  gazetteer.enabled, or when changing its schema, romanization, or admin-level
  sidecar. This is the dataset behind GET /api/v1/gazetteer and the MCP gazetteer tool.
---

# Build a gazetteer package for ortus

The gazetteer dataset is a dedicated GeoPackage (`osm-admin-places.gpkg`) with two
layers — `places` (POINT settlements) and `admin_levels` (MULTIPOLYGON boundaries) —
plus three YAML sidecars. It is built from OpenStreetMap + Natural Earth by the
**canonical build system in the `osm-data` repo** (clone it separately; set
`OSM_DATA` to its checkout path), a ~1000-line Makefile plus the Python
generators bundled here under `scripts/`.

ortus consumes it through `internal/application/gazetteer` (see `ortus-gazetteer.example.yaml`
and `internal/app/gazetteer.go`). This skill's job is to produce a package that
loads cleanly and matches the manifest contract ortus expects.

## When to use / not use

- **Use** to build or refresh the gazetteer GeoPackage + sidecars, add a country,
  change the romanization cascade, or regenerate the admin-level reference.
- **Not** for a generic vector source (use `build-ortus-package`) or a raster
  (use `build-geotiff-package`). This dataset has a *fixed schema* the gazetteer
  code depends on — do not improvise columns.

## The contract ortus depends on

`ortus-gazetteer.yaml` (the manifest, committed at `data/gazetteer/ortus-gazetteer.yaml`)
maps layer/column *roles*. The GeoPackage MUST provide:

**`places` layer** (POINT, EPSG:4326):
- `name` — romanized, always-Latin display name (never NULL)
- `name_native` — original-script name (NULL/empty when already Latin)
- `name_source` — closed-vocabulary romanization/provenance code (see `reference/romanization.md`)
- `place` — rank: `village | town | city`
- `admin_id` — FK → `admin_levels.fid` of the most-local containing unit
- `country_iso` — ISO 3166-1 alpha-2
- `population`, `capital`, `wikidata` — *optional* prominence signals (from `make enrich-places`)
  that drive the ortus bearing anchor salience (`CompositeSalience`); a package without them
  falls back to rank-only selection

**`admin_levels` layer** (MULTIPOLYGON, EPSG:4326):
- `admin_level` — OSM admin level (text; numeric values used, coverage fills carry NULL)
- `name`, `name_native`, `name_source` — as above
- `parent_id` — FK → `admin_levels.fid` of the next broader unit (walked by ResolveChain)
- `country_iso`

**Both layers** need an rtree spatial index (`rtree_<layer>_geom`) and the file needs
a **native SpatiaLite `spatial_ref_sys`** with SRID 4326 (see `srid-metadata` below) —
without it ellipsoidal `Distance()` fails and every bearing returns nothing.

**Sidecars** (ship beside the .gpkg, pointed at by ortus config):
- `admin_levels_west_palearctic.yaml` — per-country `(admin_level) → {name (local term), equivalent}`
  plus `equivalent_levels[equivalent].description`. Drives `local_term`,
  `equivalent`, and `equivalent_description` in Locate responses.
- `name_source_manifest.yaml` — describes each `name_source` code (`short`, `long`,
  `standard`). Drives the response-wide `sources` block. Closed vocabulary enforced by `make verify`.

The `ortus-gazetteer.yaml` manifest also takes an optional `license:` block
(`name`/`url`/`attribution`) for the dataset as a whole (OSM ODbL + GeoNames +
Natural Earth + GNS). ortus echoes it as the `license` block in the gazetteer
response so clients get the attribution they must display — set it.

## Build pipeline (run in this order — the order is load-bearing)

The canonical driver is the osm-data Makefile. From that repo:

```bash
cd "$OSM_DATA"        # your osm-data checkout

# Full reproducible rebuild from fresh sources to one coherent vintage:
bash scripts/rebuild-all.sh          # (bundled here as scripts/rebuild-all.sh)

# …or step by step (what rebuild-all.sh orchestrates):
make clean                # 1. drop old build
make                      # 2. base build: osmium tags-filter → ogr2ogr GeoJSON → GeoPackage
                          #    (europe + morocco/algeria/tunisia) + Natural Earth spatial join
make normalize-schema     # 3. unify attributes, canonical country_iso, gapless coverage fills
make link-hierarchy       # 4. add places.admin_id + admin_levels.parent_id (RTree-driven PiP)
make srid-metadata        # 5. add native SpatiaLite spatial_ref_sys (SRID 4326)
make romanize             # 6. name/name_native/name_source (scripts/romanize.py)
make romanize-gazetteers  # 7. upgrade Arabic/Hebrew via NGA GNS + GeoNames (scripts/romanize_gazetteers.py; network)
make enrich-places        # 8. places.population/capital/wikidata + GeoNames pop backfill (scripts/enrich_places.py; network)
make metadata             # 9. embed ODbL + OSM + NE + GeoNames attribution in gpkg_metadata
make provenance           # 10. write PROVENANCE.txt (source timestamps + SHA-256)
make verify               # 11. QA harness — MUST exit 0
```

**Ordering invariants (do not reorder):**
1. `normalize-schema` before `link-hierarchy` (linking needs final geometries,
   numeric `admin_level`, final `country_iso`).
2. `link-hierarchy` before `romanize` (romanize expects the final schema).
3. `romanize` before `romanize-gazetteers` (the latter upgrades rows the former tagged).
4. `romanize` before `enrich-places` (the `--geonames` population backfill matches on `name_native`).
5. All of these steps are idempotent and safe to re-run.

**Performance trap:** `link-hierarchy` drops `idx_admin_country_lvl` before the
point-in-polygon passes and recreates it after. Keeping it would make the planner
enumerate every same-country polygon per row (O(n·m), ~12h). The RTree bbox
prefilter with per-row constant bounds is what makes it fast — preserve that.

See `reference/pipeline-stages.md` for the full per-stage explanation and
`reference/geopackage-schema.md` for the exact final column list.

## Generation scripts (bundled under `scripts/`)

Copied from osm-data so this skill carries the generation logic. They operate on
the GeoPackage in place (idempotent, guarded by `ADD COLUMN IF NOT EXISTS`):

- **`romanize.py`** — `python3 romanize.py --apply --gpkg <gpkg>`. Offline, pure
  stdlib. Adds `name_native`/`name_source`, romanizes non-Latin `name` per script
  (Cyrillic national systems, Greek ELOT 743, Georgian 2002, Armenian BGN/PCGN,
  Arabic/Hebrew curated-first cascade). `--check` asserts `name` is 100% Latin and
  reports per-code counts; `--validate` checks the closed vocabulary.
- **`romanize_gazetteers.py`** — `python3 romanize_gazetteers.py --apply --gpkg <gpkg>`.
  Network step (caches under `temp/gazetteers/`). Upgrades residual machine-transliterated
  Arabic/Hebrew rows with NGA GNS (`gns-bgn`) then GeoNames (`geonames`). Imports `gazetteers.py`.
- **`enrich_places.py`** — `python3 enrich_places.py --apply --geonames --gpkg <gpkg>`. Adds
  `places.population`/`capital`/`wikidata` (prominence signals for the ortus bearing anchor
  salience) by re-reading the place PBFs and joining by `osm_id`; `--geonames` backfills the
  sparse MENA village population tail from GeoNames. `--check` reports fill + asserts sanity.
- **`gazetteers.py`** — GNS (ArcGIS REST) + GeoNames dump fetch/cache/index. Used by the above
  (incl. `build_geonames_population_index` for the enrich backfill).
- **`build_admin_level_reference.py`** — regenerates `admin_levels_west_palearctic.yaml`
  (the level sidecar) from the GeoPackage + OSM Wiki tiers. Run when admin coverage changes.
- **`script_census.py`** — QA: counts names by Unicode script. Informational.
- **`rebuild-all.sh`** — orchestrates the full sequence at one coherent vintage.

(The osm-data repo also ships `geonames_lookup.py`, an optional pre-normalization
one-off to fill missing ISO codes; it is not part of the reproducible base flow
and is left in osm-data rather than bundled here.)

These scripts assume the osm-data Makefile has produced the base GeoPackage; they
are the *enrichment* half of the pipeline. For the osmium/ogr2ogr assembly half,
drive the Makefile in osm-data (the recipes are too tied to that repo's layout to
run standalone).

## Inputs & tooling

- **Source data:** OSM PBFs (Geofabrik `europe-latest` + `morocco`/`algeria`/`tunisia`),
  Natural Earth 10m Admin-0 (public domain), and — for the gazetteer upgrade — NGA GNS
  (public domain) + GeoNames per-country dumps (CC BY 4.0, attribution required).
- **CLI tools:** `osmium` (tags-filter), `ogr2ogr`/GDAL, `sqlite3`, `spatialite`,
  `curl`, `unzip`, `python3` (3.13+, stdlib only). See osm-data `pyproject.toml`.
- **Licensing:** the result is an ODbL derivative database. Keep `DATA_SOURCES.md`,
  `DATA_LICENSE.md`, `PROVENANCE.txt`, and the embedded `gpkg_metadata` attribution.

## Wiring it into ortus

Place the four artifacts where ortus can read them and point config at them:

```yaml
gazetteer:
  enabled: true
  geopackage_path: /data/gazetteer/osm-admin-places.gpkg
  manifest_path: /data/gazetteer/ortus-gazetteer.yaml
  level_reference_path: /data/gazetteer/admin_levels_west_palearctic.yaml
  name_source_manifest_path: /data/gazetteer/name_source_manifest.yaml   # optional; enables the sources block
```

The gazetteer GeoPackage is loaded **out of competition** — separately from the
generic query source pool — so it never appears as a PiP source.

## Verify before shipping

1. `make verify` in osm-data — exit 0 (schema, CRS, 100% populated + valid `country_iso`,
   hierarchy integrity, closed `name_source` vocabulary, `name` 100% Latin, attribution present).
2. In ortus, regenerate the committed golden test fixture so CI exercises the new data:
   ```bash
   go run cmd/gazetteer-fixture/main.go \
     -src   /path/to/osm-admin-places.gpkg \
     -manifest data/gazetteer/ortus-gazetteer.yaml \
     -sidecar  data/gazetteer/admin_levels_west_palearctic.yaml \
     -name-sources data/gazetteer/name_source_manifest.yaml \
     -out internal/app/testdata -simplify 0.002
   ```
   Then `go test ./internal/app/ -run TestGazetteerFixtureGolden`, `make verify`,
   `make debt-coverage`.
3. Smoke-test a real query: enable the feature and `GET /api/v1/gazetteer?lon=..&lat=..`,
   confirming `admin.hierarchy[].name_source`, `local_term`, `equivalent_description`,
   and the response-wide `sources` block are populated.
