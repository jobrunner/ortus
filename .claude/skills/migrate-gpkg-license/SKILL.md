---
name: migrate-gpkg-license
description: >-
  Audit an existing ortus vector GeoPackage (.gpkg) for embedded license/
  attribution and, if it is missing or in a legacy/unparseable form, research the
  real license (name, canonical URL, attribution) from the file's own metadata and
  the data provider, then embed it in the structured form ortus reads. Use when a
  deployed .gpkg shows no license in GET /api/v1/sources or /api/v1/query, or after
  inheriting packages built before the JSON-license contract. The skill may (and
  should) do web research to confirm the license and find its URL. Not for building
  a new package (use build-ortus-package), rasters (build-geotiff-package), or the
  gazetteer (build-gazetteer-package, whose license comes from its manifest).
---

# Migrate an existing GeoPackage's license into the ortus contract

ortus surfaces per-source license/attribution in `GET /api/v1/sources` and every
`GET /api/v1/query` response (and the built-in frontend renders it) — **but only
when the license is embedded in the exact form the adapter reads.** Packages
built before that contract (or by other tools) usually carry their license as
free text or ISO-19139/QGIS XML, which ortus does **not** parse into a license.
This skill checks one package and, if needed, migrates it in place.

Requires `ortus >= v0.20.1` at serving time (the release that reads this row).

## The contract (must match the adapter exactly)

ortus reads the license from the single `gpkg_metadata` row where **both**:
- `mime_type = 'application/json'`, **and**
- `md_standard_uri = 'https://ortus.dev/schema/dataset-metadata.json'`

holding `{"license":{"name","url","attribution"}}` (+ optional top-level
`"description"`). JSON under any other URI is ignored; a plain-text/XML row only
becomes the description. Adapter: `internal/adapters/geopackage/repository.go`
(`readMetadata` / `datasetMetadata` / `ortusMetadataURI`). This is the same
contract `build-ortus-package` writes at build time — this skill is the
after-the-fact migration for packages that predate it.

## When to use / not use

- **Use** on an existing `.gpkg` that shows no attribution in ortus, or one you
  inherited/built with the old free-text metadata.
- **Not** for building a fresh package (embed the license at build time via
  `build-ortus-package`), rasters (`build-geotiff-package`), or the gazetteer
  (`build-gazetteer-package` — its license comes from `ortus-gazetteer.yaml`).

## Workflow

Bundled helpers (this skill's `scripts/`): `inspect-metadata.py` (read-only
audit) and `embed-license.py` (additive, idempotent writer).

### 1. Audit

```bash
python3 scripts/inspect-metadata.py <package.gpkg>
```

Branch on the `STATUS:` line:
- **`MIGRATED`** — an ortus row already exists and parses. Show its license and
  stop, unless the user wants to re-derive/correct it (then continue).
- **`NEEDS_MIGRATION`** / **`NO_METADATA`** — continue below.
- **`NOT_A_GPKG`** — wrong file/type; stop.

The audit prints every existing metadata row in full — read them for license
clues before researching.

### 2. Extract clues from the existing metadata

Pull whatever the file already states:
- **Free text** (old ortus/GDAL): `Title:` / `Source:` / `License:` / `Origin:` /
  `Attribution:` / `Use constraints:` segments.
- **ISO-19139** (`gmd:` / isotc211): `gmd:useConstraints`, `gmd:otherConstraints`,
  `CI_ResponsibleParty` / `pointOfContact`, `gmd:lineage`.
- **QGIS/GDAL XML**: `<license>`, `<rights>`, `<attribution>`, `<title>`,
  `<abstract>`, and any provider/record id or URL.

Note any dataset/record identifier (an EEA record UUID, a BMEL/Thünen catalog id,
a UNEP-WCMC service name, a DOI) — it is the key to authoritative verification.

### 3. Research the real license — REQUIRED, do not guess a URL

Determine `name`, a canonical `url`, and `attribution`:

- **Resolve known tokens** to their canonical URL from the table below.
- **Verify at the provider, don't assume.** When the metadata names a provider or
  record, `WebFetch` that authoritative record/page (or `WebSearch` for it) and
  confirm the actual license, attribution and a linkable URL. Words like
  "typisch/typically <X>" in the source are a *hypothesis to confirm*, not a fact
  — a real case read "typisch dl-de/by-2-0" but the provider's catalog record said
  **GeoNutzV**. Another read "Source: … WWF ecoregions" and the provider service
  declared the **UNEP-WCMC General Data License** (non-commercial).
- **`WebSearch` for the URL** whenever you only have a license code/name and no
  link. Prefer the license steward's own page (creativecommons.org, govdata.de,
  opendatacommons.org, the provider's terms page).
- **No formal license?** Some datasets only have custom terms. Leave `name` empty
  (or a short descriptor) and put the full terms + required citation in
  `attribution`; still link a terms URL if one exists. Capture non-commercial /
  share-alike / attribution obligations in the attribution text.

Always report which source you verified against (cite the URL).

### 4. Confirm before writing

License text is outward-facing — a wrong license is worse than none. Present the
derived `{name, url, attribution, description}`, cite the authoritative source,
and **flag every inference or assumption**. Get the user's confirmation (or
correction) before writing — especially for non-SPDX or provider-specific terms.

Preview the exact payload without touching the file:

```bash
python3 scripts/embed-license.py --gpkg <package.gpkg> \
  --name "…" --url "…" --attribution "…" --description "…" --dry-run
```

### 5. Embed

```bash
python3 scripts/embed-license.py --gpkg <package.gpkg> \
  --name "CC-BY-4.0" \
  --url "https://creativecommons.org/licenses/by/4.0/" \
  --attribution "European Environment Agency (EEA)" \
  --description "EEA Biogeographical Regions, Europe 2016"
```

Additive + idempotent: existing rows are preserved; an existing ortus row is
updated, otherwise a new row (+ `gpkg_metadata_reference`) is inserted. `--name`
and `--url` may be empty strings.

### 6. Verify

Re-run `inspect-metadata.py` — expect `STATUS: MIGRATED` with the intended
license. For a full check, load the file with `ortus >= v0.20.1` and confirm the
`license` block appears in `GET /api/v1/sources` and `GET /api/v1/query` (a
missing license logs a load-time warning).

### 7. Operator note

The migration edits the `.gpkg` in place. Redeploy the migrated file to the
storage path (keep the same filename stem so the source ID is unchanged); the
local watcher hot-reloads it, remote storage picks it up on sync. Repeat for
every affected package in the storage path.

## Reference: common geodata licenses (name → canonical URL)

Starting points — **always confirm the dataset's actual license at its
provider** (step 3); a package's stated code can be wrong.

| name | canonical URL |
|---|---|
| `CC-BY-4.0` | https://creativecommons.org/licenses/by/4.0/ |
| `CC0-1.0` | https://creativecommons.org/publicdomain/zero/1.0/ |
| `CC-BY-SA-4.0` | https://creativecommons.org/licenses/by-sa/4.0/ |
| `ODbL-1.0` | https://opendatacommons.org/licenses/odbl/1-0/ |
| `dl-de/by-2-0` (Datenlizenz Deutschland – Namensnennung 2.0) | https://www.govdata.de/dl-de/by-2-0 |
| `dl-de/zero-2-0` | https://www.govdata.de/dl-de/zero-2-0 |
| `GeoNutzV` (Geodatennutzungsverordnung des Bundes) | https://sg.geodatenzentrum.de/web_public/gdz/lizenz/geonutzv.pdf |
| `UNEP-WCMC General Data License` (non-commercial) | https://www.unep-wcmc.org/en/general-data-license |
