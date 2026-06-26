# ADR-0012: Migration der Ubiquitous Language von „Package" zu „Source"

## Status

Akzeptiert — vollständig umgesetzt (Stufe A+B in #49, Stufe C in #50).

## Kontext

Mit der Generalisierung des Domänenmodells (`domain.GeoPackage` → `domain.Source`
mit `Kind` vector|raster) und dem neuen `SpatialSource`-Port kann ortus künftig
nicht nur GeoPackages, sondern auch Raster-Bundles als Datenquellen einbinden
(siehe [Raster Bundle Spec](../raster-bundle/README.md)).

Das Domänenmodell ist umbenannt, **die übrige Codebasis spricht aber weiterhin
GeoPackage-zentrisch von „Package"**:

- Anwendungsschicht: `PackageRegistry`, `packageEntry`, `LoadPackage`,
  `UnloadPackage`, `ListPackages`, `GetPackage`, `GetPackageStatus`,
  `ReadyPackageIDs`, `PackageCount`, `IsLoaded`, `derivePackageID`,
  `PackageHealth`.
- Driving-Port-Interface: `input.PackageRegistry`.
- Observability: Span-Attribute `ortus.package.id` / `ortus.package.path`,
  Span-Namen `PackageRegistry.*` / `Repository.*`, Metriken
  `ortus.packages.loaded` / `ortus.packages.ready`.
- HTTP-API: Route `/api/v1/packages`, JSON-Felder `package_id`, `package_name`,
  `packages_added` etc.
- MCP-Tools: `list_packages`, `get_package`, `get_package_layers`.

Sobald ein Raster-Bundle geladen wird, ist „package" irreführend: In Logs,
Metriken, API-Antworten und MCP-Tools erschiene ein GeoTIFF als „package". Das
verwässert die fachliche Sprache genau dort, wo Außenstehende (Dashboards,
API-Clients, AI-Agenten) sie lesen.

Ein sofortiges, vollständiges Umbenennen ist jedoch riskant: Ein Teil der
Bezeichner ist rein intern (compiler-geprüft, kein externer Vertrag), ein
anderer Teil ist ein **veröffentlichter Vertrag** (Metrik-/Span-Namen, JSON,
Tool-Namen), dessen Änderung Dashboards, Alerts und Clients bricht.

## Entscheidung

Die Migration wird **gestuft** durchgeführt und nach Vertragsbindung getrennt.
Sie wird **bewusst nicht** Teil des Raster-Bundle-Branches, um diesen fokussiert
zu halten und keine Halb-Umbenennung (Go-Symbol sagt „Source", Span-String sagt
„Package") entstehen zu lassen.

### Stufe A — interne Symbole (nicht-Vertrag), eigener mechanischer PR

Compiler-geprüft, keine externe Sichtbarkeit. In einem dedizierten Rename-PR:

| heute | neu |
|---|---|
| `PackageRegistry` | `SourceRegistry` |
| `packageEntry` | `sourceEntry` |
| `LoadPackage` / `UnloadPackage` | `LoadSource` / `UnloadSource` |
| `ListPackages` / `GetPackage` / `GetPackageStatus` | `ListSources` / `GetSource` / `GetSourceStatus` |
| `ReadyPackageIDs` / `PackageCount` / `IsLoaded` | `ReadySourceIDs` / `SourceCount` / `IsLoaded` |
| `derivePackageID` / `DerivePackageID` | `deriveSourceID` / `DeriveSourceID` |
| `input.PackageRegistry` | `input.SourceRegistry` |
| `PackageHealth`, `HealthDetails.PackagesLoaded/Ready` | `SourceHealth`, `.SourcesLoaded/Ready` |

Voraussetzung: erst zusammen mit Stufe B, da die Span-Namen `PackageRegistry.*`
aus den Methoden abgeleitet bzw. fest verdrahtet sind — sonst entsteht die
Symbol-vs-String-Inkonsistenz.

### Stufe B — Observability-Verträge (Span-/Metriknamen), koordiniert

Geänderte Namen brechen Dashboards/Alerts. Daher:

- Neue Namen: `ortus.source.id`/`.path`, `ortus.sources.loaded`/`.ready`,
  Span-Namen `SourceRegistry.*`.
- Im CHANGELOG als Breaking-Change für Observability ankündigen; in einem
  Minor-Release bündeln; Migrationshinweis für Grafana/Alert-Regeln beilegen.
- Der Span-Contract-Test (`telemetry/coverage_test.go`, `wantSpans`) wird
  mitgezogen.

### Stufe C — öffentliche API & MCP-Tools (Breaking), versioniert

Höchste Vertragsbindung — externe Clients und AI-Agenten hängen daran:

- HTTP: `/api/v1/packages` → `/api/v1/sources`, JSON `package_id` → `source_id`
  usw. Umsetzung über **`/api/v2`** oder eine Übergangsphase mit Doppel-Ausgabe
  (alte + neue Felder) und Deprecation-Fenster.
- MCP-Tools: `list_packages`/`get_package`/`get_package_layers` →
  `list_sources`/`get_source`/`get_source_layers`. Tool-Namen sind Teil des
  Agenten-Vertrags → alte Namen für eine Übergangszeit als Alias registrieren.
- Erfordert Produktentscheidung + Major-/Minor-Versionssprung.

### Reihenfolge

A+B gemeinsam als ein interner Rename-PR (nach Abschluss des Raster-Adapters,
damit beide Quelltypen die neue Sprache von Anfang an konsistent nutzen). C als
separater, versionierter API-PR mit Deprecation-Fenster.

## Konsequenzen

**Positiv**
- Fachsprache passt zur Abstraktion; Raster-Quellen werden nicht als „package"
  fehlbenannt — in Code, Logs, Metriken, API und MCP einheitlich „source".
- Trennung nach Vertragsbindung verhindert überraschende Breaks.

**Negativ / Risiken**
- Stufe A+B ist ein großer, breit streuender (aber mechanischer) Diff.
- Stufe C bricht Clients/Dashboards/Agenten → zwingend versioniert + Deprecation.
- Bis zur Umsetzung bleibt die „package"-Sprache bestehen; neue Codepfade
  (Raster-Adapter) sollten intern bereits „source" verwenden, um die spätere
  Stufe A zu verkleinern.

**Offen (Produktentscheidung)**
- `/api/v2` vs. Doppel-Ausgabe für Stufe C.
- Länge des Deprecation-Fensters für MCP-Tool-Aliasse.
