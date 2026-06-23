# ADR-0013: COG-Reader-Bibliothek (tingold/gocog) + LZW-Pflicht für Bundle-COGs

## Status

Akzeptiert

## Kontext

Der geplante Raster-Adapter ([Implementierungsplan §3.2](../raster-bundle/IMPLEMENTATION_PLAN.md),
[ADR-0012](0012-source-vocabulary-migration.md)) braucht einen Go-Reader, der für eine
Koordinate einen Pixelwert aus einem Cloud Optimized GeoTIFF (COG) liest.

Anforderungen an die Bibliothek:

1. **Random Single-Pixel/Tile-Read ohne Ganzbild-Load** — bei 1-km-global (Köppen
   ~43200×18000 px) ist Vollbild-Dekodierung nicht tragbar.
2. **dtypes** uint8 / int16 / float32 (Köppen ist Byte, ESDAC ganzzahlige Klassen,
   kontinuierliche Raster float).
3. **Tiling-/Overview-aware**, CRS auslesbar.
4. **Gepflegt**, möglichst **pure-Go** (kein GDAL/CGO im Raster-Pfad).

## Evaluierung (Spike)

Getestet gegen **echte GDAL-3.13-erzeugte COGs** (1024×512, Block 256×256, Overviews;
Byte/Int16/Float32; je COMPRESS=NONE/LZW/DEFLATE). Ground Truth via
`gdallocationinfo`. Zwei disjunkte Wert-Quadrate (100 / 200, sonst 0); Abfrage an
(20,20)→100, (80,20)→200, (50,20)→0, (−100,−50)→0.

| Kandidat | Version | Korrekt (GDAL-COG) | COG-nativ | Speicher | dtypes | Gepflegt | Urteil |
|---|---|---|---|---|---|---|---|
| `google/tiff` | 2016-11 | n/a (low-level, kein Geo/COG) | ❌ | — | — | ❌ stale | **raus** |
| `gden173/geotiff` | v0.0.1 | ❌ | ❌ Ganzbild als `[][]float32` | Ganzbild | float32 | ⚠️ getaggt, aber defekt | **raus** |
| `tingold/gocog` | v0.0.0-20251202 | ✅ NONE+LZW · ❌ DEFLATE | ✅ RangeReader/Tiles/Overviews | metadata-only + Fenster | uint8/int16/float32 | ✅ 2025-12 | **adoptieren** |

**`gden173/geotiff`** — `Bounds()` eines tiled COG liefert nur die **erste Kachel**
(−180..−90 / 45..90) als vermeintliches Gesamtbild → gültige Punkte gelten als
„outside"; scheitert zusätzlich an einem unkomprimierten Plain-GeoTIFF
(„unexpected EOF"). Datenmodell `[][]float32` lädt ohnehin das ganze Bild. Untauglich.

**`google/tiff`** — reiner TIFF-Leser von 2016 ohne Geo-/COG-Semantik; Tiling +
GeoKeys müsste man selbst bauen. Untauglich.

**`tingold/gocog`** — Metadaten, CRS (`EPSG:4326`), Größe, `DataType`, Overviews und
`PixelFromPoint` korrekt; `ReadWindow(1×1)` liest punktgenau mit minimalem Speicher
(metadata-only, kein Ganzbild-Load). uint8 ✅, int16 ✅, float32 lesbar (`At()` liefert
das Bitmuster → `math.Float32frombits` je `DataType`). **Einziger Defekt:** die
**DEFLATE**-Tile-Dekompression schlägt fehl (`flate: corrupt input before offset 5`) —
mit hoher Wahrscheinlichkeit ein zlib-Header-vs-raw-`flate`-Bug. **NONE und LZW lesen
fehlerfrei.**

## Entscheidung

**`github.com/tingold/gocog` wird adoptiert.** Da die Bundle-Pipeline die COG-Erzeugung
**selbst kontrolliert** ([raster-bundle](../raster-bundle/)), schreiben wir
**`COMPRESS=LZW`** (oder `NONE`) als Pflicht-Kompression für Bundle-COGs vor und umgehen
den DEFLATE-Bug vollständig (LZW im Spike verifiziert korrekt). `build.sh` wird von
`DEFLATE` auf `LZW` umgestellt.

Begleitend:
- gocog-Commit **pinnen** (`v0.0.0-20251202163215-f108ab4d8e26`) — pre-1.0, API kann sich ändern.
- Lokale Dateien über `gocog.Read(io.ReadSeeker)` lesen (kein `fasthttp` nötig); der
  Range/URL-Modus (`Open`/`ReadFromURL`) braucht einen `*fasthttp.Client` — für das
  ZIP-Bundle-Modell (lokaler Cache) nicht relevant.
- Der Raster-Adapter **kapselt** gocog-/`orb`-Typen hinter den `domain`-Typen, damit die
  Bibliothek austauschbar bleibt.

## Konsequenzen

**Positiv**
- Pure-Go, **kein CGO** im Raster-Pfad; COG-nativ (Tiles/Overviews/Range); geringer
  Speicher; passt exakt zum Punkt-Sampling-Bedarf.
- Deterministisch reproduzierbarer Spike (Fixture per GDAL, Orakel per gdallocationinfo).

**Negativ / Risiken**
- gocog ist **pre-1.0** → Commit gepinnt; API-Drift möglich. Abkapselung im Adapter mindert das.
- **DEFLATE-COGs werden nicht gelesen.** LZW ist vorgeschrieben; externe DEFLATE-COGs
  müssten beim Ingest re-komprimiert werden (`gdal_translate -co COMPRESS=LZW`).
- **Dependency-Footprint (verifiziert nach Einbau):** `fasthttp`, `paulmach/orb`,
  `golang.org/x/image` — **und** über `gocog → orb/maptile → orb/geojson →
  go.mongodb.org/mongo-driver/bson` wird `bson` **in das ortus-Binary kompiliert**
  (ganze Pakete werden gelinkt, auch wenn wir nur `ReadWindow`/`PixelFromPoint`
  nutzen und `ReadTile`/`maptile` nie aufrufen). Das ist ein realer, unschöner
  Bloat. Korrektur eines früheren, falschen Spike-Befunds („nicht im Runtime-Build").
  Folge-Option (empfohlen, niedrige Prio): Upstream-PR, der gocogs `maptile`-Nutzung
  entfernt (`ReadTile(x,y,z int)` statt `maptile.Tile`) — entfernt `geojson`+`bson`.
- Float-Raster erfordern `Float32frombits`-Dekodierung je `DataType` (für die
  kategorialen Bundles irrelevant — dort liefert `At()` den Wert direkt).

**Offen (niedrige Priorität)**
- Upstream-Fix für die DEFLATE-Dekompression (vermutlich `zlib.NewReader` statt
  `flate.NewReader`) beisteuern; würde die LZW-Pflicht zur reinen Empfehlung machen.
