# Reference: pipeline stages

What each stage does, in order. (Rationale lives in
[Explanation](../explanation/schema-normalization.md); this is the factual sequence.)

## Base build (`make`)

1. `osmium tags-filter` — extract admin boundaries (`r/boundary=administrative`) and
   places (`n/place=village,town,city`) from each PBF.
2. `ogr2ogr` — convert to GeoJSON, selecting attributes: names in de/en/fr/el,
   `admin_level`, `iso_code`.
3. `ogr2ogr` — import into the GeoPackage as `admin_levels` (MULTIPOLYGON) and
   `places` (POINT).
4. Import Natural Earth 10m admin-0 countries (temporary table) and **spatial-join**
   country name + ISO onto both layers (`ST_Contains`; centroid for admin).
5. Post-processing — fix known Natural Earth issues (`-99` ISO codes, divided or
   disputed-territory names), pattern-based ISO fixes for coastal areas.
6. ISO-to-country fallback for coastal places whose centroid falls in the sea.
7. Drop the temporary Natural Earth table; create GeoPackage RTree spatial indexes.
8. Write metadata (license/attribution).

## Incremental add (`make add-country`)

Extract → append → **dedupe by `osm_id`** → spatial-join country (with `ST_Buffer` for
coastal robustness, only where `country IS NULL`) → ISO fallback → rebuild indexes.

## Normalization (`make normalize-schema`)

1. Import Natural Earth; build a canonical `country_lookup` (ISO → names).
2. Drop the GeoPackage RTree triggers (so plain-`sqlite3` attribute UPDATEs don't fire
   spatial functions the bare binary lacks).
3. Rename `places.iso_code` → `country_iso`; add `country_de/en/fr/el` to
   `admin_levels`; drop the sparse `admin_levels.iso_code`.
4. Backfill canonical country names on both layers from `country_lookup`.
5. Clean junk: `-99`/empty → NULL; non-numeric `admin_level` → NULL; `CY-NC` → `CY`.
6. Nearest-country fallback for residual NULLs; re-apply canonical names; Svalbard → SJ.
7. Coverage fills (per `FILL_COUNTRIES`: full-outline clip; all others: interior holes)
   with manual RTree maintenance.
8. Drop temporary tables.

## Post-processing (after normalization)

- `make link-hierarchy` — add the relational FK chain (`places.admin_id`,
  `admin_levels.parent_id`) by same-country point-in-polygon; drop the redundant `places`
  country-name columns; build FK indexes.
- `make srid-metadata` — `InitSpatialMetaData(1)` to populate the native SpatiaLite
  `spatial_ref_sys` (SRID 4326 resolves without the `unknown SRID` warning).
- `make romanize` — romanize non-Latin `name`s to Latin (documented standard per script:
  Cyrillic per-country, Greek/Georgian/Armenian, curated-first Arabic/Hebrew), keeping the
  original in `name_native` and the method in `name_source`. Offline, pure stdlib.
- `make romanize-gazetteers` — upgrade the machine-transliterated Arabic/Hebrew rows with
  NGA GNS (BGN/PCGN) then GeoNames, matched by native name + nearest coordinate. Network;
  downloads cached under `temp/gazetteers/` (offline + idempotent on re-run). See
  [romanization](romanization.md).

## QA & records

- `make metadata` — embed license/attribution into `gpkg_metadata`.
- `make provenance` — write `PROVENANCE.txt`.
- `make verify` — assert schema/CRS/attributes/coverage/license.

## Key tools & idioms

- `GeomFromGPB()` / `AsGPB()` — convert between GeoPackage geometry blobs and SpatiaLite.
- RTree bounding-box prefilter before `ST_Contains`/`ST_Distance`.
- `ST_MakeValid` guards the rare invalid polygon during unions.
