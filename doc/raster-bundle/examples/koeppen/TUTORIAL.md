# Tutorial: Build the Köppen-Geiger raster bundle and serve it from ortus

A complete, end-to-end worked example: take the published **Köppen-Geiger 1 km**
climate classification, turn it into an **ortus raster bundle**, load it into a
running ortus, and verify point queries return the right climate class.

Every command below was run for real against the 1 km dataset; the verification
results at the end are the actual responses.

> **Scope note.** ortus only *consumes* bundles — it is not a bundle factory.
> Building bundles belongs in a per-dataset pipeline (this folder is the
> reference). Do **not** commit downloaded source data or built artifacts into
> the ortus repo; keep them in a scratch dir (see [.gitignore](.gitignore)).

## Prerequisites

- **GDAL** ≥ 3 (`gdal_translate`, `gdalinfo`, `gdallocationinfo`)
- **Python 3** (for `gen_manifest.py`; stdlib only)
- **Go** + this repo (to `make build` ortus)
- `curl`, `unzip`, `zip`

A scratch working directory, e.g.:

```bash
mkdir -p /tmp/koeppen && cd /tmp/koeppen
```

## Step 1 — Get the source data

The Köppen-Geiger maps (Beck et al. 2018, *Scientific Data*) are published on
figshare. The V1 archive (~68 MB) contains the present-day map at several
resolutions plus `legend.txt`:

```bash
curl -sL -o beck_kg_v1.zip "https://ndownloader.figshare.com/files/12407516"
unzip -o beck_kg_v1.zip Beck_KG_V1_present_0p0083.tif legend.txt
```

`Beck_KG_V1_present_0p0083.tif` is the 0.0083° (~1 km) present-day map;
`legend.txt` maps pixel values 1–30 to the Köppen classes.

> **V3** (1991–2020, current) lives at
> `https://figshare.com/ndownloader/files/61012822` — a much larger
> multi-period/scenario archive. The steps below are identical; pick the
> `*_1991_2020_0p00833333.tif` map from it.

## Step 2 — Inspect the source (this is where the gotchas surface)

```bash
gdalinfo Beck_KG_V1_present_0p0083.tif | grep -iE 'Size is|Type=|ColorInterp|NoData|COMPRESSION|ID\["EPSG'
```

Real output and what each line means for the bundle:

| Observation | Consequence |
|---|---|
| `Size is 43200, 21600`, `Type=Byte` | 933 M categorical pixels, 1 byte each. |
| `ColorInterp=Palette` | The band value **is** the class index (1–30) with a colour table attached. The COG conversion must **keep the palette / single band**, not expand to RGB — otherwise band 1 becomes the red channel, not the class. |
| no `NoData` line; legend starts at **1** | Value **0 = ocean / no class**. The manifest must declare `nodata: 0`, otherwise a query over the sea hits the "unmapped value" error. |
| WKT says WGS 84 but there is **no explicit EPSG code** | We stamp `EPSG:4326` during conversion (`-a_srs`) so the bundle's CRS is unambiguous. |
| `COMPRESSION=PACKBITS` | Irrelevant for input; the **output must be LZW** (see Step 3). |

Confirm the value→class legend (the regex in `gen_manifest.py` expects this shape):

```text
    1:  Af   Tropical, rainforest                  [0 0 255]
    4:  BWh  Arid, desert, hot                      [255 0 0]
   30: EF   Polar, frost                            [102 102 102]
```

## Step 3 — Build the Cloud Optimized GeoTIFF (LZW, not DEFLATE)

```bash
gdal_translate -of COG \
  -a_srs EPSG:4326 \
  -co COMPRESS=LZW \
  -co BLOCKSIZE=512 \
  -co RESAMPLING=NEAREST \
  -co OVERVIEW_RESAMPLING=NEAREST \
  Beck_KG_V1_present_0p0083.tif koeppen.cog.tif
```

Why these flags:

- **`-co COMPRESS=LZW`** — **mandatory.** ortus's COG reader (`tingold/gocog`,
  [ADR-0013](../../../adr/0013-cog-reader-library.md)) reads LZW and uncompressed
  tiles correctly but **fails on GDAL's DEFLATE tiles**. LZW stays lossless and
  compressed (here 14 MB).
- **`-a_srs EPSG:4326`** — stamps the explicit EPSG code the source lacked. For a
  source in a *different* projection (e.g. ESDAC `EPSG:3035`), reproject first
  with `gdalwarp -t_srs EPSG:4326 -r near` — **`-r near`** because categorical
  classes must never be interpolated.
- **`RESAMPLING=NEAREST`** for the overviews, same reason.

Verify the band is preserved (single Byte band, palette kept):

```bash
gdalinfo koeppen.cog.tif | grep -iE 'Band 1|ColorInterp|COMPRESSION'
# Band 1 Block=512x512 Type=Byte, ColorInterp=Palette ; COMPRESSION=LZW   ✓
```

## Step 4 — Generate the manifest from `legend.txt`

[`gen_manifest.py`](gen_manifest.py) parses the official legend and emits the
`mapping:` block (value → code/description/group/colour), with string values
quoted to avoid YAML's "Norway problem" (`no`/`yes`/`NO` parsing as booleans):

```bash
python3 gen_manifest.py legend.txt \
  --id koeppen-geiger-1980-2016 \
  --name "Köppen-Geiger climate classification 1980–2016 (Beck et al. 2018, V1)" \
  > ortus-raster.yaml
```

> **Naming — avoid the "latest" trap.** A Köppen "present-day" map is a
> classification computed over a fixed **reference period**, *not* "now". For
> Beck et al. 2018 (V1) that period is **1980–2016**; V3 is **1991–2020**. Bake
> the period into the source `id` (`koeppen-geiger-1980-2016`) — never call it
> `…-present` or `…-latest`, or a future release silently becomes "the" Köppen
> source. The `id` must also equal the bundle filename (Step 5).

The result (see the committed [`ortus-raster.yaml`](ortus-raster.yaml)) declares:

```yaml
id: koeppen-geiger-1980-2016   # period in the id; MUST equal the bundle filename stem
crs: EPSG:4326                 # authoritative; must match the COG's actual CRS
layers:
  - id: classification
    file: koeppen.cog.tif
    band: 1
    nodata: 0                  # 0 = ocean / no class → no feature, not an error
    sampling: nearest          # only nearest is supported
    mapping:
      1:  { code: "Af",  description: "Tropical, rainforest", group: "Tropical", ... }
      ...                       # all 30 classes
```

Every pixel value that can occur **must** be either mapped or equal to `nodata`;
an unmapped value is surfaced as a hard query error (raster and legend disagree).

## Step 5 — Assemble the bundle (ZIP)

The bundle filename stem **must equal the manifest `id`** — ortus derives the
source id from the filename and cross-checks it against the manifest:

```bash
zip -j koeppen-geiger-1980-2016.zip ortus-raster.yaml koeppen.cog.tif
```

→ `koeppen-geiger-1980-2016.zip` (the manifest `id` is `koeppen-geiger-1980-2016`).

## Step 6 — Load it into ortus

ortus discovers `.zip` (and `.gpkg`) files in its storage path, unzips the
bundle, validates the manifest against the embedded JSON Schema, opens the COG,
and registers the source — all or nothing.

```bash
# from the ortus repo root:
make build

mkdir -p /tmp/koeppen/data
cp koeppen-geiger-1980-2016.zip /tmp/koeppen/data/

./ortus --storage-path /tmp/koeppen/data --port 8099 --log-level info &
```

The log should show the source register:

```json
{"level":"INFO","msg":"package loaded","id":"koeppen-geiger-1980-2016","layers":1}
```

Check readiness and that the source is listed:

```bash
curl -s localhost:8099/health/ready          # {"status":"ok"}
curl -s localhost:8099/api/v1/sources        # id=koeppen-geiger-1980-2016, ready:true
```

## Step 7 — Verify with point queries

Query is `GET /api/v1/query?lon=<lon>&lat=<lat>` (WGS84 by default). Cross-check
against the raw raster with `gdallocationinfo` as an oracle:

```bash
# oracle (pixel value from the source raster):
printf -- "-62 -4\n10 23\n13.4 52.5\n-30 5\n" \
  | gdallocationinfo -valonly -l_srs EPSG:4326 Beck_KG_V1_present_0p0083.tif

# ortus:
curl -s "localhost:8099/api/v1/query?lon=13.4&lat=52.5" | python3 -m json.tool
```

Verified results (oracle pixel value → ortus response):

| Location | lon, lat | oracle | ortus `code` / `description` |
|---|---|---|---|
| Amazon | −62, −4 | `1` | **Af** — Tropical, rainforest |
| Sahara | 10, 23 | `4` | **BWh** — Arid, desert, hot |
| Berlin | 13.4, 52.5 | `26` | **Dfb** — Cold, no dry season, warm summer |
| Mid-Atlantic | −30, 5 | `0` | *(no feature — nodata)* |

The Berlin response in full — note `id` is the raw pixel value, the mapped
attributes land in `properties`, and the license/attribution is carried through:

```json
{
  "coordinate": {"srid": 4326, "x": 13.4, "y": 52.5},
  "results": [{
    "source_id": "koeppen-geiger-1980-2016",
    "source_name": "Köppen-Geiger climate classification 1980–2016 (Beck et al. 2018, V1)",
    "features": [{
      "id": 26, "layer": "classification",
      "properties": {"code": "Dfb", "description": "Cold, no dry season, warm summer",
                     "group": "Cold", "color": "#37C8FF"}
    }],
    "feature_count": 1,
    "license": {"name": "CC-BY-4.0", "attribution": "Beck et al. (2018), Scientific Data 5:180214",
                "url": "https://www.gloh2o.org/koppen/"}
  }],
  "total_features": 1
}
```

Stop the server when done: `pkill -f 'ortus --storage-path /tmp/koeppen/data'`.

## What to watch out for (checklist)

1. **LZW, never DEFLATE** for the COG — gocog can't read GDAL's DEFLATE tiles.
2. **Filename stem == manifest `id`** — or ingest fails with a clear mismatch error.
   Encode the reference **period** in the id (`…-1980-2016`), never `present`/`latest`.
3. **`crs` is authoritative** — it must match the COG's real CRS. If the source
   lacks an EPSG code, stamp it (`-a_srs`); if it is in another projection,
   reproject with `gdalwarp ... -r near` *before* building the COG.
4. **Palette rasters**: keep the single class-index band; don't let the COG
   conversion expand the palette to RGB.
5. **`nodata`**: declare the no-class sentinel (here `0`). Any pixel value that
   actually occurs must be mapped or equal to `nodata`, else the query errors.
6. **Nearest only** — categorical classes must not be interpolated.
7. **Quote string codes** in the mapping (`gen_manifest.py` does this).
8. **Don't commit data/artifacts** to the ortus repo — only the tutorial and the
   pipeline scripts belong here.

## One-shot script

[`build.sh`](build.sh) chains Steps 1–5 (download → COG → manifest → validate →
zip). Run it, then jump to Step 6:

```bash
./build.sh   # produces koeppen-geiger-1980-2016.zip in this folder
```
