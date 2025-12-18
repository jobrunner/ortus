# Architektur - Ortus

## Uebersicht

Ortus ist ein Go-basierter REST-Service fuer Punktabfragen auf GeoPackage-Dateien. Der Service folgt der **Hexagonal Architecture (Ports & Adapters)** und dem **Standard Go Project Layout**.

```
+-------------------+          +-------------------+          +-------------------+
|   API-Clients     |          |     ORTUS        |          |   GeoPackages     |
|   (REST/HTTP)     |--------->|     Service       |<-------->|   (SpatiaLite)    |
+-------------------+          +-------------------+          +-------------------+
                                        |
                               +--------+--------+
                               |                 |
                               v                 v
                       +---------------+  +---------------+
                       | Object Storage|  | File Watcher  |
                       | (S3/Azure)    |  | (Hot-Reload)  |
                       +---------------+  +---------------+
```

## Kern-Features

- **REST-API:** OpenAPI-konforme Punktabfragen
- **GeoPackage:** Geometriebasierte ST_Contains-Abfragen
- **Koordinatentransformation:** Automatische Projektion in Layer-SRID
- **Hot-Reload:** Automatische Erkennung neuer/entfernter GeoPackages
- **Object Storage:** Laden von GeoPackages aus S3/Azure beim Start
- **TLS:** Optionales HTTPS mit Let's Encrypt

## Verzeichnisstruktur

```
ortus/
|-- cmd/ortus/                        # Entry Point
|   +-- main.go
|
|-- internal/
|   |-- adapters/                      # Adapter-Implementierungen
|   |   |-- primary/                   # Driving Adapters
|   |   |   |-- http/                  # REST-API Handler
|   |   |   +-- cli/                   # CLI (optional)
|   |   +-- secondary/                 # Driven Adapters
|   |       |-- spatialite/            # GeoPackage-Repository
|   |       |-- storage/               # S3/Azure/Local
|   |       +-- watcher/               # File-System Watcher
|   |
|   |-- application/                   # Application Services
|   |   |-- query/                     # Punktabfrage-Service
|   |   |-- geopackage/                # GeoPackage-Management
|   |   +-- registry/                  # Package-Registry
|   |
|   |-- domain/                        # Domain-Modelle
|   |   |-- geopackage.go              # GeoPackage Entity
|   |   |-- feature.go                 # Feature Entity
|   |   |-- coordinate.go              # Coordinate Value Object
|   |   +-- errors.go                  # Domain-Fehler
|   |
|   |-- ports/                         # Port-Interfaces
|   |   |-- input/                     # Input Ports
|   |   +-- output/                    # Output Ports
|   |
|   |-- config/                        # Konfiguration
|   +-- infrastructure/                # TLS, Logging, Metrics
|
|-- api/openapi/                       # OpenAPI-Spezifikation
|-- deployments/                       # Docker, Kubernetes
+-- doc/                               # Dokumentation
    +-- adr/                           # Architecture Decision Records
```

## Hexagonale Architektur

### Abhaengigkeitsrichtung

```
Primary Adapters  -->  Input Ports  -->  Application  -->  Domain
                                              |
                                              v
Secondary Adapters  <--  Output Ports  <------+
```

**Regeln:**
- Domain hat KEINE Abhaengigkeiten
- Ports definieren Interfaces
- Adapters implementieren Ports
- Application orchestriert Domain-Logik

### Ports

**Input Ports** (was der Service anbietet):
- `QueryPort` - Punktabfragen
- `GeoPackagePort` - Package-Informationen
- `HealthPort` - Health-Checks

**Output Ports** (was der Service benoetigt):
- `GeoPackageRepository` - SpatiaLite-Zugriff
- `StoragePort` - Object Storage
- `FileWatcherPort` - Datei-Ueberwachung

## Design-Patterns

### Dependency Injection

```go
// cmd/ortus/main.go
func main() {
    cfg := config.Load()

    // Adapters
    repo := spatialite.NewRepository()
    storage := s3.NewAdapter(cfg.Storage)

    // Services
    registry := registry.NewService(repo)
    query := query.NewService(registry, repo)

    // HTTP Server
    server := http.NewServer(cfg, query, registry)
    server.Start()
}
```

### Repository Pattern

```go
type GeoPackageRepository interface {
    Open(ctx context.Context, path string) (*GeoPackageHandle, error)
    PointQuery(ctx context.Context, handle *GeoPackageHandle, layer string, coord Coordinate) ([]Feature, error)
    Close(handle *GeoPackageHandle) error
}
```

### Error Wrapping

```go
func (s *Service) DoWork(ctx context.Context) error {
    result, err := s.repo.Query(ctx)
    if err != nil {
        return fmt.Errorf("query failed: %w", err)
    }
    return nil
}
```

## Konfiguration

Prioritaet: CLI > Umgebungsvariablen > .env > Defaults

| Variable | Default | Beschreibung |
|----------|---------|--------------|
| `ORTUS_PORT` | `8080` | HTTP-Port |
| `ORTUS_GPKG_DIR` | `/data/gpkg` | GeoPackage-Verzeichnis |
| `ORTUS_STORAGE_TYPE` | `local` | Storage (local/s3/azure) |
| `ORTUS_LOG_LEVEL` | `info` | Log-Level |
| `ORTUS_RATE_LIMIT` | `10` | Requests/Sekunde |

Siehe [ADR-0007](adr/0007-configuration-management.md) fuer vollstaendige Liste.

## Testing

### Unit Tests

```go
func TestQueryService_PointQuery(t *testing.T) {
    mockRepo := &MockRepository{}
    svc := query.NewService(mockRepo)

    result, err := svc.PointQuery(ctx, req)

    assert.NoError(t, err)
    assert.NotEmpty(t, result.Features)
}
```

### Integration Tests

```go
func TestIntegration_PointQuery(t *testing.T) {
    if testing.Short() {
        t.Skip("skipping integration test")
    }
    // Test mit echtem GeoPackage
}
```

## Container

Base-Image: `ghcr.io/jobrunner/spatialite-base-image:1.4.0`

```dockerfile
FROM ghcr.io/jobrunner/spatialite-base-image:1.4.0

COPY ortus /usr/local/bin/ortus

USER spatialite
EXPOSE 8080 9090

ENTRYPOINT ["/usr/local/bin/ortus"]
```

## Weiterführende Dokumentation

- [ARCHITECTURE_PLAN.md](ARCHITECTURE_PLAN.md) - Detaillierter Architekturplan
- [DEVELOPMENT.md](DEVELOPMENT.md) - Entwicklungsdokumentation
- [ADRs](adr/) - Architecture Decision Records

### ADR-Index

| ADR | Titel | Status |
|-----|-------|--------|
| [0001](adr/0001-standard-go-project-layout.md) | Standard Go Project Layout | Akzeptiert |
| [0002](adr/0002-hexagonal-architecture-elements.md) | Hexagonal Architecture | Akzeptiert |
| [0003](adr/0003-vincenty-as-default-algorithm.md) | Vincenty Algorithmus | Akzeptiert |
| [0004](adr/0004-interface-based-codec-system.md) | Interface-basiertes Codec-System | Akzeptiert |
| [0005](adr/0005-geopackage-based-architecture.md) | GeoPackage-basierte Architektur | Akzeptiert |
| [0006](adr/0006-object-storage-integration.md) | Object Storage Integration | Akzeptiert |
| [0007](adr/0007-configuration-management.md) | Configuration Management | Akzeptiert |
| [0008](adr/0008-tls-letsencrypt.md) | TLS und Let's Encrypt | Akzeptiert |
| [0009](adr/0009-hot-reload-file-watching.md) | Hot-Reload und File-Watching | Akzeptiert |
