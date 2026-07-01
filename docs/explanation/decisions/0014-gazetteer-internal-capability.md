# ADR-0014: Gazetteer als interne Capability (kein eigener Service, keine generische Source)

## Status

Akzeptiert

## Kontext

ortus ist eine **generische, schema-agnostische Punkt-in-Polygon-Engine**: sie fragt
beliebige GeoPackage-/Raster-Quellen an einer Koordinate ab, ohne deren Schema zu kennen.
Das neue Feature — Reverse-Geocoding auf die Verwaltungseinheit plus **Peilung** („4 km E
Würzburg") — ist dagegen **meinungsstark**: es setzt ein bestimmtes Dataset voraus (ein
`places`-Layer mit Rang-Klasse, ein `admin_levels`-Layer mit `admin_level`/`parent_id`).

Drei Wege standen zur Wahl:

1. **Separater Microservice** — eigener Deploy/Betrieb, doppelte GeoPackage-/cgo-Infra.
2. **Neuer generischer Source-Typ** im bestehenden Source-Pool — würde die schema-agnostische
   Query-Engine mit Gazetteer-Schemawissen verunreinigen.
3. **Interne Capability** im selben Prozess, sauber hinter Ports gekapselt.

## Entscheidung

**Interne Capability (3).** Konkret, hart an die bestehende hexagonale Architektur gebunden:

- Ein **Input-Port** `input.Gazetteer` (`Locate`, `Bearing`), implementiert von
  `application/gazetteer.Service`.
- Ein **Output-Port** `output.SpatialIndex` (KNN/PiP/Distanz/Azimut/`ResolveChain`) für die
  cgo-Primitiven — implementiert im **bestehenden** `geopackage`-Adapter, damit cgo/SpatiaLite
  **ein einziger Owner** bleibt (kein zweiter cgo-Adapter).
- Das Gazetteer-GeoPackage wird **„außer Konkurrenz"** geladen — als eigenes, dediziertes
  Dataset, **nicht** im generischen Source-Pool. Die generische Query-Engine lernt es nie kennen.
- **Disabled by default** (`gazetteer.enabled=false`): das Feature ist inert, bis Pfade
  konfiguriert sind.
- Die HTTP-Komposition (`sources` + `admin` + `bearing`) passiert in der Adapter-/HTTP-Schicht;
  der generische Pfad bleibt unangetastet (eigener `/gazetteer`-Endpunkt; opt-in
  `with-gazetteer=1` auf `/query`).

## Konsequenzen

**Positiv**
- Der generische PiP-Pfad bleibt schema-agnostisch und unverändert; depguard-Grenzen bleiben intakt.
- Ein cgo-Owner; keine Betriebskosten eines zweiten Service.
- Feature inert bis konfiguriert — kein Risiko für Bestands-Deployments.

**Negativ / Risiken**
- Der Prozess trägt (bei aktivem Feature) ein zweites, großes GeoPackage im Speicher/FD-Budget.
- Die Capability ist meinungsstark und an eine Dataset-Kontrakt (Manifest, [ADR-0016](0016-bearing-convention-compass.md))
  gebunden — Schema-Drift des Datasets bricht sie. Das Manifest macht den Kontrakt explizit und versioniert.
