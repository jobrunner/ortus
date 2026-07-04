# Reference / Spec: name romanization & per-name provenance

**Status:** fully implemented for every script in the Western Palearctic. `make romanize` does the
deterministic scripts (Cyrillic UA/RU/BY/BG/RS/MK, Greek, Georgian, Armenian) **and** the
curated-first **Arabic/Hebrew** cascade (OSM `name:en`/`name:fr` → machine transliteration).
`make romanize-gazetteers` then upgrades the residual machine-transliterated rows with the
external gazetteers **GNS → GeoNames** (matched by native name + nearest coordinate): of the
~4,000 residual rows with no OSM Latin tag, ~22 % gain an authoritative romanization
(`gns-bgn`/`geonames`); the rest keep the machine transliteration (auditable via `name_source`).
**Consumers:** specimen labels **and citations in publications**, via ortus reverse-geocoding.
**Scope:** the Western Palearctic today, the **full Palearctic** over the coming years
(more scripts will be added — see [Future scripts](#future-scripts)).

## Why this exists

Place names from this dataset are printed on specimen labels **and cited in scientific
publications**. That raises the bar beyond "a findable label" to four hard requirements:

1. **Consistency** — the same source name always yields the same Latin form.
2. **Reproducibility** — the romanization is produced by a *documented* standard, not by
   ad-hoc per-mapper choices, so a third party can reproduce it.
3. **Citability** — a publication can state *"localities romanized per «system X»"* and,
   per record, know exactly which system produced a given name.
4. **Preserve the original** — the native-script endonym is kept (both as a search anchor
   and as citeable content — original script in parentheses is standard scholarly practice
   for non-Latin regions).

This is why the primary Latin `name` is **not** taken from OSM `name:en` first: `name:en`
is mapper-dependent and mixes exonyms ("Cairo", "Moscow") with transliterations, so it is
inconsistent across neighbouring features and not defensible as a citation standard. We use
a **documented standard per script**, applied uniformly, and record its identity per row.

## Census of the current data (2026 vintage)

Of **786,801** names across both layers, **161,857 (20.6 %) are non-Latin** and require
romanization (measured by Unicode script of `name`, `scripts/script_census.py`):

| Script | Rows | Share | Main countries |
|---|---|---|---|
| Latin | 622,115 | 79.1 % | already Latin — kept verbatim |
| Cyrillic | 110,791 | 14.1 % | UA 33k, RU 27k, BY 26k, RS 9.5k, BG 7.6k, MK 3.4k, BA 2.6k |
| Arabic | 34,679 | 4.4 % | IQ 13k, SA 6.1k, TN 3.3k, DZ 2.1k, OM 2.1k, EG 1.4k, KW, JO |
| Greek | 11,070 | 1.4 % | GR 10.5k, CY 0.6k |
| Georgian | 3,519 | 0.4 % | GE |
| Hebrew | 1,472 | 0.2 % | IL 1.3k, PS |
| Armenian | 326 | 0.0 % | AM |

(Plus ~2,800 empty names.) So the work is dominated by **Cyrillic** (deterministic, easy)
and **Arabic** (the hard case — see below).

## Target schema (both layers)

| Column | Meaning |
|---|---|
| `name` | The romanized endonym — **always Latin script**, one documented standard per script, **diacritics preserved**. This is the label/citation string. |
| `name_native` | The original-script endonym (verbatim OSM `name`). **NULL** where `name` was already Latin (i.e. no romanization happened). Search anchor + citeable original. |
| `name_source` | **Per-row provenance of `name`** — a controlled code identifying the method + standard/source that produced it (see vocabulary below). Returned by the reverse-geocoding request so the consumer stores, per record, *where this name came from*. |

`name` is guaranteed 100 % Latin and non-empty (the transliteration fallback guarantees
coverage — cf. the ~56 % of Russian places that have no OSM Latin tag at all).
`name_native` + the record's coordinate + `osm_id` remain the durable, exact locators.

> Applies to the `name` of **both** layers (settlement names *and* admin/region names are
> cited). The extra `admin_levels` `name_de/en/fr/el` columns are retained for now as
> romanization inputs / multilingual display; reducing them is a separate later decision.

## `name_source` controlled vocabulary

Format `<method>-<standard|tag>`; a closed set (enforced by `make verify`). Documented so a
publication can map a code to a citable standard.

| Code | Meaning |
|---|---|
| `latin-osm` | OSM `name` was already Latin; kept verbatim (Central Europe, RO, TR, AZ, …). |
| `osm-latn` | OSM script-specific Latin tag used verbatim (`name:sr-Latn`, `name:uk-Latn`, …). *Reserved — in the vocabulary but not yet produced (requires extracting those tags in the pipeline).* |
| `osm-en` | OSM `name:en` used (curated). |
| `osm-fr` | OSM `name:fr` used (curated; notably the Maghreb). |
| `gns-bgn` | NGA GEOnet Names Server, BGN/PCGN romanization. |
| `geonames` | GeoNames alternate/ASCII name. |
| `translit-uk-2010` | Ukrainian National system (2010, UN 2012). |
| `translit-bg-2009` | Bulgarian Streamlined System (2009, UN 2012). |
| `translit-be-2007` | Belarusian national instruction (2007, UN-adopted). |
| `translit-ru-bgn` | Russian BGN/PCGN 1947 (matches GNS/cartographic use). |
| `translit-sr-latn` | Serbian → official Serbian (Gaj) Latin. |
| `translit-mk-nat` | Macedonian national/ISO romanization. |
| `translit-el-843` | Greek ELOT 743 / UN / ISO 843. |
| `translit-ka-2002` | Georgian national system (2002, UN-adopted). |
| `translit-hy-bgn` | Armenian BGN/PCGN 1981. |
| `translit-he-ungegn` | Hebrew UNGEGN. |
| `translit-ar-bgn` | Arabic BGN/PCGN (last-resort; see Arabic caveat). |

Future additions: `translit-zh-pinyin`, `translit-ja-hepburn`, `translit-ko-rr`.

## Standard, source and cascade per script

The **cascade differs by script** because deterministic scripts should be romanized
*uniformly by the standard* (consistency for citations), whereas Arabic/Hebrew lose
information under machine transliteration and are better served by curated names.

| Script | Standard for `name` | Cascade (first hit wins → `name_source`) |
|---|---|---|
| **Latin** (already) | — keep verbatim, preserve diacritics | `latin-osm` |
| **Cyrillic** | per-country national system (UA-2010, BG-2009, BE-2007, RU-BGN; **RS/BA/ME → Serbian Latin**, MK → national) | currently always `translit-<std>` (uniform by standard, for citation consistency); `osm-latn` is **reserved** — a future refinement preferring a matching `name:*-Latn` tag |
| **Greek** | ELOT 743 | `translit-el-843` |
| **Georgian** | national 2002 | `translit-ka-2002` |
| **Armenian** | BGN/PCGN 1981 | `translit-hy-bgn` |
| **Arabic** | *curated-first* | `osm-en` → `osm-fr` → `gns-bgn` → `geonames` → `translit-ar-bgn` |
| **Hebrew** | *curated-first* | `osm-en` → `gns-bgn` → `geonames` → `translit-he-ungegn` |

The country→system mapping for Cyrillic is explicit (a table in the build), because the
correct system depends on the **language**, not just the script: `UA→uk-2010`,
`RU→ru-bgn`, `BY→be-2007`, `BG→bg-2009`, `RS/BA/ME→sr-latn`, `MK→mk-nat`.

### The Arabic caveat (honest limitation)

Arabic script omits short vowels, so **machine transliteration is inherently lossy** (e.g.
القاهرة → "Al-Qahrh" not "Al-Qāhirah"; measured against OSM `name:en`, the machine form
agrees only ~0.8 %). For the 34,679 Arabic names we therefore prefer **human-curated forms**
(OSM `name:en`/`name:fr`, then the authoritative gazetteers) and use machine transliteration
only as a last resort. In the shipped build the OSM Latin tags already cover ~89 % of Arabic
rows; of the ~4,000 residual rows the gazetteer step (`make romanize-gazetteers`) upgrades a
further ~22 % to `gns-bgn`/`geonames`, leaving the rest (small hamlets and descriptive names
absent from both gazetteers) as `translit-ar-bgn`. This residual inconsistency is documented,
not hidden — `name_source` makes it auditable per row (a record tagged `translit-ar-bgn` is
machine-guessed; one tagged `gns-bgn` is authoritative).

## Diacritics & digraphic languages (policy)

- **Diacritics: kept** in `name` (București, Chișinău, Iași, Kärnten, İzmir). Correct and
  standard for citations. A consumer that needs an ASCII search key folds at query time —
  we do not store a folded copy (re-derivable).
- **Digraphic languages** use their **official Latin**, not a re-transliteration of Cyrillic:
  Serbian (Beograd, not "Belgrad"), and — when the Palearctic reaches Central Asia —
  Kazakh/Azerbaijani/Turkmen (Latin-transitioning). Prefer `name:*-Latn` where present.

## Sources & licensing (recorded in DATA_SOURCES.md)

- **NGA GNS (GEOnet Names Server)** — BGN/PCGN romanization — **public domain** (US Gov).
  Queried live from the GNS ArcGIS REST service (`scripts/gazetteers.py`), cached locally.
- **GeoNames** — Latin `name`/`asciiname` from the per-country bulk dumps — **CC BY 4.0**
  (attribution required).
- **Machine transliteration** is done by **hand-written, per-standard tables in
  `scripts/romanize.py`** (not an external engine such as ICU): each national/UN/BGN system
  is a small explicit table + positional rules, so the mapping is transparent, auditable and
  dependency-free (pure Python stdlib).
- **OSM** tags (`name`, `name:en`, `name:fr`) — ODbL (already the base).

Derived `name` values remain part of the ODbL derivative database. GNS adds no obligation;
GeoNames adds the CC BY attribution line — now embedded in the GeoPackage metadata and
asserted by `make verify`.

## Reproducibility / how to cite

Because `name` is produced by a documented standard and every row records its `name_source`,
a publication can state, e.g.:

> *Locality names are given in the romanized endonym form of the reverse-geocoding dataset
> (OSM + Natural Earth, ODbL), romanized per the UN/national system for each script; the
> per-record `name_source` field identifies the system used.*

## How it plugs into the build

Two idempotent targets, run **after `link-hierarchy`** (they need the final `name`s):

**`make romanize`** (`scripts/romanize.py --apply`) — offline, pure stdlib:

1. add columns `name_native`, `name_source` (guarded) on both layers; `name_source` gets a
   column `DEFAULT 'latin-osm'`, so the ~622k already-Latin rows need no rewrite;
2. classify each `name` by Unicode script (same buckets as `scripts/script_census.py`);
3. already-Latin → `name_source='latin-osm'`, `name_native=NULL`;
4. non-Latin → set `name_native=name`, compute `name` per the cascade for that script (a
   `_latin_ok` guard rejects mis-tagged non-Latin `name:en`/`name:fr`), set `name_source`.

**`make romanize-gazetteers`** (`scripts/romanize_gazetteers.py --apply`) — network, cached:
upgrades the residual machine-transliterated Arabic/Hebrew rows via
`scripts/gazetteers.py` (GNS ArcGIS query → GeoNames bulk dump), matched by folded native
name + nearest coordinate. Re-derivable from `name_native`, so it is idempotent and offline
on re-run (downloads are cached under `temp/gazetteers/`).

**`make verify`** asserts: `name_source` ∈ vocabulary (or NULL); `name_native` set for every
`translit-*` row; **`name` is 100 % Latin** (`romanize.py --check`, which also reports the
per-`name_source` counts and any unhandled NULLs); and — because GeoNames data is now baked
into `name` values — the CC BY attribution is present in the GeoPackage metadata.

Implemented in census-weight order: **Cyrillic** (deterministic, 110k rows, biggest win),
then Greek/Georgian/Armenian (deterministic), then the curated **Arabic/Hebrew** cascade and
its GNS/GeoNames gazetteer step.

## Future scripts

Palearctic expansion will add, in rough priority: **Central Asian Cyrillic→Latin**
(KZ/KG/TJ, digraphic transitions), then **CJK** — Chinese **Pinyin** (`translit-zh-pinyin`),
Japanese **Hepburn** (`translit-ja-hepburn`), Korean **Revised Romanization**
(`translit-ko-rr`). The schema and `name_source` mechanism already accommodate them; only
new cascade rows and vocabulary codes are added.
