# ADR-0005: GeoPackage-basierte Architektur

## Status

Akzeptiert

## Kontext

Ortels soll Punktabfragen auf Geodaten ermoeglichen. Es muss ein Datenformat gewaehlt werden, das:

1. Effiziente raeumliche Abfragen unterstuetzt
2. Selbstbeschreibend ist (Metadaten, Lizenzinformationen)
3. Als einzelne Datei transportierbar ist
4. Einen etablierten Standard darstellt
5. Mit bestehenden GIS-Tools kompatibel ist
6. Koordinatentransformation unterstuetzt

### Evaluierte Optionen

| Format | Raeumliche Abfragen | Metadaten | Portabilitaet | Standard |
|--------|---------------------|-----------|---------------|----------|
| GeoPackage | Sehr gut (R-Tree, SQL) | Sehr gut (gpkg_metadata) | Einzelne Datei | OGC Standard |
| PostGIS | Sehr gut | Gut | Server erforderlich | De-facto Standard |
| GeoJSON | Keine nativen Indizes | Begrenzt | Einzelne Datei | RFC 7946 |
| Shapefile | Begrenzt | Sehr begrenzt | Mehrere Dateien | Veraltet |
| FlatGeobuf | Gut | Begrenzt | Einzelne Datei | Neu |

## Entscheidung

Wir verwenden **GeoPackage** als einziges Eingabeformat fuer Geodaten.

### Begruendung

1. **Raeumliche Abfragen:**
   - Nativer R-Tree Spatial Index
   - SpatiaLite-Funktionen (ST_Contains, Transform)
   - SQL-basierte Abfragen ermoeglicht Flexibilitaet

2. **Metadaten und Lizenz:**
   - `gpkg_metadata` und `gpkg_metadata_reference` Tabellen
   - Standardisiertes Schema fuer Attribution und Lizenz
   - Keine externe Metadaten-Datei erforderlich

3. **Koordinatensysteme:**
   - `gpkg_spatial_ref_sys` enthaelt SRID-Definitionen
   - Proj4-Strings fuer Transformation verfuegbar
   - Unterstuetzung aller EPSG-Codes

4. **Deployment:**
   - Einzelne SQLite-Datei
   - Kein externer Datenbankserver
   - Einfaches Kopieren/Synchronisieren

5. **Tooling:**
   - QGIS, ArcGIS, GDAL native Unterstuetzung
   - Einfache Erstellung und Bearbeitung

### Architektur-Implikationen

```
                    +-------------------+
                    |   GeoPackage      |
                    |   (.gpkg)         |
                    +-------------------+
                    |                   |
                    | gpkg_contents     |---> Feature-Layer
                    | gpkg_geometry_col |---> Geometrie-Info
                    | gpkg_spatial_ref  |---> Koordinatensysteme
                    | gpkg_metadata     |---> Metadaten
                    | gpkg_metadata_ref |---> Lizenz/Attribution
                    |                   |
                    | rtree_*_geom      |---> Spatial Index
                    |                   |
                    | feature_table_1   |---> Geodaten
                    | feature_table_n   |---> Geodaten
                    |                   |
                    +-------------------+
```

### Punktabfrage-Strategie

```sql
-- Geometrie-basierte Abfrage (NICHT Bounding-Box!)
SELECT *
FROM feature_layer
WHERE ST_Contains(
    geom,
    Transform(
        GeomFromText('POINT(lon lat)', input_srid),
        layer_srid
    )
)
```

**Wichtig:** Wir verwenden `ST_Contains()` statt Bounding-Box-Abfragen, um praezise geometrische Treffer zu erhalten. Der R-Tree-Index wird automatisch als Vorfilter genutzt.

### Index-Erstellung

```sql
-- Pruefung ob Index existiert
SELECT COUNT(*) FROM sqlite_master
WHERE type = 'table' AND name = 'rtree_layername_geom';

-- Index erstellen falls nicht vorhanden
SELECT CreateSpatialIndex('layername', 'geom');
```

## Konsequenzen

### Positiv

- **Einheitliches Format:** Klare Anforderungen an Datenlieferanten
- **Selbstbeschreibend:** Alle Informationen in einer Datei
- **Performant:** R-Tree-Index ermoeglicht schnelle Abfragen
- **Flexibel:** Multiple Layer pro GeoPackage moeglich
- **Offline-faehig:** Kein externer Service erforderlich

### Negativ

- **Vorverarbeitung:** Daten muessen als GeoPackage bereitgestellt werden
- **Groesse:** Bei grossen Datensaetzen kann die Datei mehrere GB gross sein
- **Write-Lock:** SQLite hat Einschraenkungen bei gleichzeitigem Schreiben

### Mitigationen

- **Vorverarbeitung:** GDAL/ogr2ogr kann andere Formate konvertieren
- **Groesse:** Regionale Aufteilung in mehrere GeoPackages
- **Write-Lock:** GeoPackages werden Read-Only geoeffnet

## Technische Details

### Basis-Image

Das Projekt verwendet `ghcr.io/jobrunner/spatialite-base-image:1.4.0`, das bereits enthaelt:
- SQLite3
- SpatiaLite Extension
- GDAL/OGR Tools
- Proj4 fuer Koordinatentransformation

### GeoPackage-Verzeichnis

```
/data/gpkg/
|-- administrative_boundaries.gpkg
|-- soil_types.gpkg
|-- land_use.gpkg
+-- ...
```

### Initialisierung beim Start

1. Verzeichnis nach `.gpkg`-Dateien scannen
2. Fuer jede Datei:
   - Layer aus `gpkg_contents` ermitteln
   - Spatial Index pruefen/erstellen
   - Metadaten und Lizenz laden
   - In Registry registrieren
3. Read-Only oeffnen
4. Server starten (erst wenn alle Indizes fertig)

## Referenzen

- [OGC GeoPackage Specification](https://www.geopackage.org/spec/)
- [GeoPackage Encoding Standard](https://www.ogc.org/standard/geopackage/)
- [SpatiaLite Documentation](https://www.gaia-gis.it/fossil/libspatialite/index)
- [GDAL GeoPackage Driver](https://gdal.org/drivers/vector/gpkg.html)
