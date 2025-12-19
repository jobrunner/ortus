# ADR-0002: Hexagonal Architecture Elements

## Status

Akzeptiert (Erweitert 2024-12)

## Kontext

Für die interne Struktur des Projekts (`internal/`) muss ein Architekturmuster gewählt werden, das:

1. Testbarkeit durch Entkopplung ermöglicht
2. Austauschbarkeit von Komponenten (z.B. Datenzugriff) erlaubt
3. Klare Verantwortlichkeiten definiert
4. Skalierbar für HTTP-API, CLI und zukünftige Interfaces ist
5. Unterstuetzung für multiple Adapter pro Port bietet

## Entscheidung

Wir verwenden eine **vollständige Hexagonal Architecture (Ports & Adapters)** mit klarer Trennung:

```
+-------------------------------------------------------------------------+
|                           Primary Adapters (Driving)                     |
|  +-------------+  +-------------+  +-------------+  +-------------+     |
|  |    HTTP     |  |    CLI      |  |   gRPC      |  |   Health    |     |
|  |   Handler   |  |   Handler   |  |  (future)   |  |   Handler   |     |
|  +------+------+  +------+------+  +------+------+  +------+------+     |
+---------+----------------+----------------+----------------+-------------+
          |                |                |                |
          v                v                v                v
+---------+----------------+----------------+----------------+-------------+
|                              INPUT PORTS                                 |
|  +------------------+  +------------------+  +------------------+        |
|  |  QueryPort       |  |  GeoPackagePort  |  |  HealthPort      |        |
+-----------+---------------------+---------------------+------------------+
            |                     |                     |
            v                     v                     v
+-----------+---------------------+---------------------+------------------+
|                         APPLICATION CORE (Domain)                        |
|  +------------------+  +------------------+  +------------------+        |
|  |   QueryService   |  | GeoPackageService|  | RegistryService  |        |
|  +--------+---------+  +--------+---------+  +--------+---------+        |
|           |                     |                     |                  |
|           v                     v                     v                  |
|  +------------------+  +------------------+  +------------------+        |
|  |     Domain       |  |     Domain       |  |     Domain       |        |
|  |   GeoPackage     |  |     Feature      |  |    Coordinate    |        |
+-----------+---------------------+---------------------+------------------+
            |                     |                     |
            v                     v                     v
+-----------+---------------------+---------------------+------------------+
|                             OUTPUT PORTS                                 |
|  +------------------+  +------------------+  +------------------+        |
|  | GeoPackageRepo   |  | StoragePort      |  | FileWatcherPort  |        |
+-----------+---------------------+---------------------+------------------+
            |                     |                     |
            v                     v                     v
+-----------+---------------------+---------------------+------------------+
|                        Secondary Adapters (Driven)                       |
|  +-------------+  +-------------+  +-------------+  +-------------+     |
|  | SpatiaLite  |  |    S3       |  |   Azure     |  |  fsnotify   |     |
|  |  Adapter    |  |   Adapter   |  |   Adapter   |  |  Adapter    |     |
+--------------------------------------------------------------------------+
```

### Package-Zuordnung

| Package | Rolle | Beschreibung |
|---------|-------|--------------|
| `adapters/primary/http/` | Primary Adapter | HTTP/REST API Handler |
| `adapters/primary/cli/` | Primary Adapter | CLI-Eingabe (optional) |
| `ports/input/` | Input Ports | Interfaces für Driving Adapters |
| `application/` | Application Core | Business-Logik, Use Cases |
| `domain/` | Domain Core | Entities, Value Objects |
| `ports/output/` | Output Ports | Interfaces für Driven Adapters |
| `adapters/secondary/spatialite/` | Secondary Adapter | GeoPackage-Repository |
| `adapters/secondary/storage/` | Secondary Adapter | S3/Azure/Local Storage |
| `adapters/secondary/watcher/` | Secondary Adapter | File-System Watcher |

### Abhängigkeitsregel

Abhängigkeiten zeigen immer **nach innen**:
- `adapters/primary/*` -> `ports/input/` -> `application/` -> `domain/`
- `application/` -> `ports/output/` <- `adapters/secondary/*`
- `domain/` hat KEINE Abhängigkeiten zu anderen Packages

### Interface-Definition

**Input Ports** werden in `ports/input/` definiert:

```go
// internal/ports/input/query.go
type QueryPort interface {
    PointQuery(ctx context.Context, req PointQueryRequest) (*PointQueryResponse, error)
}
```

**Output Ports** werden in `ports/output/` definiert:

```go
// internal/ports/output/repository.go
type GeoPackageRepository interface {
    Open(ctx context.Context, path string) (*GeoPackageHandle, error)
    PointQuery(ctx context.Context, handle *GeoPackageHandle, layer string, coord domain.Coordinate) ([]domain.Feature, error)
    Close(handle *GeoPackageHandle) error
}
```

### Dependency Injection im Main

```go
// cmd/ortus/main.go
func main() {
    // Config laden
    cfg := config.Load()

    // Secondary Adapters (Driven)
    repo := spatialite.NewRepository()
    storage := s3.NewAdapter(cfg.Storage)
    watcher := fsnotify.NewWatcher()

    // Application Services
    registry := registry.NewService(repo, watcher)
    geopackage := geopackage.NewService(registry, storage)
    query := query.NewService(registry, repo)

    // Primary Adapters (Driving)
    httpServer := http.NewServer(cfg.Server, query, registry)

    // Start
    httpServer.Start()
}
```

## Konsequenzen

### Positiv

- **Testbarkeit:** Jede Schicht einzeln mit Mocks testbar
- **Flexibilität:** Adapter austauschbar (SpatiaLite -> PostGIS, S3 -> Azure)
- **Fokussierung:** Domain-Logik vollständig isoliert
- **Erweiterbarkeit:** Neue Interfaces (gRPC) ohne Core-Änderungen
- **Klarheit:** Explizite Ports machen Abhängigkeiten sichtbar

### Negativ

- **Indirektion:** Mehr Interfaces und Packages
- **Boilerplate:** DTOs zwischen Schichten
- **Lernkurve:** Team muss Pattern verstehen

### Mitigationen

- Klare Dokumentation und ADRs
- Code-Generierung für Boilerplate wo sinnvoll
- Regelmaessige Architektur-Reviews

## Verwandte ADRs

- ADR-0005: GeoPackage-basierte Architektur
- ADR-0006: Object Storage Integration
- ADR-0009: Hot-Reload und File-Watching

## Referenzen

- [Alistair Cockburn: Hexagonal Architecture](https://alistair.cockburn.us/hexagonal-architecture/)
- [Go with Domain](https://threedots.tech/series/go-with-domain/)
- [Go Blog: Interface Definitions](https://go.dev/wiki/CodeReviewComments#interfaces)
- [Ports and Adapters Pattern](https://jmgarridopaz.github.io/content/hexagonalarchitecture.html)
