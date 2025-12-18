# Architekturplan - Ortels

## 1. Executive Summary

Ortels ist ein Go-basierter Geo-Service mit REST-API, der Punktabfragen auf GeoPackage-Dateien ermöglicht. Der Service nimmt Koordinaten in verschiedenen Projektionen entgegen, führt geometriebasierte Abfragen durch und liefert Features mit Lizenz- und Attributionsinformationen zurück.

**Architektur-Stil:** Hexagonale Architektur (Ports & Adapters)

**Kern-Features:**
- OpenAPI-konforme REST-Schnittstelle
- Punktabfragen auf GeoPackages mit Koordinatentransformation
- Hot-Reload von GeoPackages zur Laufzeit
- Object-Storage-Integration (AWS S3/Azure Blob Storage) und HTTP-Download
- Automatische Spatial-Index-Erstellung
- Prometheus-Metriken und strukturiertes Logging

**Qualitätsattribute:**
- Modularität: Klare Trennung durch Ports & Adapters
- Testbarkeit: Interface-basiertes Design ermöglicht Mocking
- Skalierbarkeit: Stateless-Design für horizontale Skalierung
- Sicherheit: TLS/Let's Encrypt via CertMagic, Rate-Limiting
- Beobachtbarkeit: Metriken, Logging, Healthchecks

**Technologie-Stack:**
- HTTP-Router: [github.com/gorilla/mux](https://github.com/gorilla/mux)
- CLI: [github.com/spf13/cobra](https://github.com/spf13/cobra)
- Konfiguration: [github.com/spf13/viper](https://github.com/spf13/viper)
- TLS/Let's Encrypt: [github.com/caddyserver/certmagic](https://github.com/caddyserver/certmagic)

---

## 2. System-Kontext (C4 Level 1)

```
                                    +------------------+
                                    |                  |
                                    |   API-Clients    |
                                    |  (Web, Mobile,   |
                                    |   andere Apps)   |
                                    |                  |
                                    +--------+---------+
                                             |
                                             | HTTPS/REST
                                             v
+------------------+               +------------------+               +------------------+
|                  |               |                  |               |                  |
|  Object Storage  |<------------->|     ORTELS       |<------------->|   GeoPackages    |
| (AWS S3/Azure    |   Download    |     Service      |   Read-Only   |   (lokal)        |
|  Blob/HTTP)      |   beim Start  |                  |   Abfragen    |                  |
+------------------+               +--------+---------+               +------------------+
                                             |
                                             | TLS-Zertifikate
                                             v
                                    +------------------+
                                    |                  |
                                    |  Let's Encrypt   |
                                    |  (via CertMagic) |
                                    |                  |
                                    +------------------+
```

### Externe Schnittstellen

| System | Richtung | Protokoll | Beschreibung |
|--------|----------|-----------|--------------|
| API-Clients | Input | HTTPS/REST | Punktabfragen, OpenAPI-Spec |
| Object Storage | Input | AWS S3/Azure SDK/HTTP | GeoPackage-Download beim Start |
| GeoPackages | I/O | SQLite/SpatiaLite | Geodaten lesen |
| Let's Encrypt | I/O | ACME (CertMagic) | Zertifikatsverwaltung |
| Prometheus | Output | HTTP | Metriken-Export |

---

## 3. Container-Diagramm (C4 Level 2)

```
+------------------------------------------------------------------------------+
|                              Ortels-Container                                 |
|  Base: ghcr.io/jobrunner/spatialite-base-image:1.4.0                         |
+------------------------------------------------------------------------------+
|                                                                              |
|  +------------------+    +------------------+    +------------------+         |
|  |   HTTP-Server    |    |   Background-    |    |   Metrics-       |         |
|  |  (gorilla/mux)   |    |   Workers        |    |   Server         |         |
|  |                  |    |                  |    |   (Prometheus)   |         |
|  |  - REST-API      |    |  - File-Watcher  |    |                  |         |
|  |  - OpenAPI-Spec  |    |  - Index-Builder |    |  :9090/metrics   |         |
|  |  - Healthcheck   |    |  - Storage-Sync  |    |                  |         |
|  |                  |    |                  |    |                  |         |
|  +--------+---------+    +--------+---------+    +------------------+         |
|           |                       |                                          |
|           v                       v                                          |
|  +-----------------------------------------------------------------------+   |
|  |                         Application Core                               |   |
|  |                                                                       |   |
|  |  +-------------+    +-------------+    +-------------+                |   |
|  |  |   Query-    |    |  GeoPackage-|    |  Registry-  |                |   |
|  |  |   Service   |--->|   Service   |--->|   Service   |                |   |
|  |  +-------------+    +-------------+    +-------------+                |   |
|  |                                                                       |   |
|  +-----------------------------------------------------------------------+   |
|           |                       |                       |                  |
|           v                       v                       v                  |
|  +------------------+    +------------------+    +------------------+         |
|  |   SpatiaLite-    |    |   Object-Storage-|    |   File-System-   |         |
|  |   Repository     |    |   Client         |    |   Watcher        |         |
|  |                  |    |  (AWS S3/Azure/  |    |   (fsnotify)     |         |
|  |                  |    |   HTTP)          |    |                  |         |
|  +------------------+    +------------------+    +------------------+         |
|           |                       |                       |                  |
+-----------+-----------------------+-----------------------+------------------+
            |                       |                       |
            v                       v                       v
    +---------------+       +---------------+       +---------------+
    |  GeoPackages  |       | AWS S3/Azure/ |       |  /data/gpkg   |
    |  (SQLite)     |       | HTTP-Server   |       |  Verzeichnis  |
    +---------------+       +---------------+       +---------------+
```

---

## 4. Komponenten-Architektur (C4 Level 3)

### 4.1 Hexagonale Architektur - Übersicht

```
+-------------------------------------------------------------------------+
|                           Primary Adapters (Driving)                     |
|  +-------------+  +-------------+  +-------------+  +-------------+     |
|  |    HTTP     |  |    CLI      |  |   gRPC      |  |   Health    |     |
|  |   Handler   |  |   (Cobra)   |  |  (future)   |  |   Handler   |     |
|  +------+------+  +------+------+  +------+------+  +------+------+     |
|         |                |                |                |             |
+---------+----------------+----------------+----------------+-------------+
          |                |                |                |
          v                v                v                v
+---------+----------------+----------------+----------------+-------------+
|                              INPUT PORTS                                 |
|  +------------------+  +------------------+  +------------------+        |
|  |  QueryPort       |  |  GeoPackagePort  |  |  HealthPort      |        |
|  |  (Interface)     |  |  (Interface)     |  |  (Interface)     |        |
|  +--------+---------+  +--------+---------+  +--------+---------+        |
|           |                     |                     |                  |
+-----------+---------------------+---------------------+------------------+
            |                     |                     |
            v                     v                     v
+-----------+---------------------+---------------------+------------------+
|                         APPLICATION CORE (Domain)                        |
|                                                                          |
|  +------------------+  +------------------+  +------------------+        |
|  |   QueryService   |  | GeoPackageService|  | RegistryService  |        |
|  |                  |  |                  |  |                  |        |
|  |  - PointQuery    |  |  - LoadPackage   |  |  - Register      |        |
|  |  - Transform     |  |  - BuildIndex    |  |  - Unregister    |        |
|  |  - GetFeatures   |  |  - GetMetadata   |  |  - List          |        |
|  +--------+---------+  +--------+---------+  +--------+---------+        |
|           |                     |                     |                  |
|           v                     v                     v                  |
|  +------------------+  +------------------+  +------------------+        |
|  |     Domain       |  |     Domain       |  |     Domain       |        |
|  |   GeoPackage     |  |     Feature      |  |    Coordinate    |        |
|  |   Layer          |  |     Metadata     |  |    Projection    |        |
|  +------------------+  +------------------+  +------------------+        |
|                                                                          |
+-----------+---------------------+---------------------+------------------+
            |                     |                     |
            v                     v                     v
+-----------+---------------------+---------------------+------------------+
|                             OUTPUT PORTS                                 |
|  +------------------+  +------------------+  +------------------+        |
|  | GeoPackageRepo   |  | StoragePort      |  | FileWatcherPort  |        |
|  | (Interface)      |  | (Interface)      |  | (Interface)      |        |
|  +--------+---------+  +--------+---------+  +--------+---------+        |
|           |                     |                     |                  |
+-----------+---------------------+---------------------+------------------+
            |                     |                     |
            v                     v                     v
+-----------+---------------------+---------------------+------------------+
|                        Secondary Adapters (Driven)                       |
|  +-------------+  +-------------+  +-------------+  +-------------+     |
|  | SpatiaLite- |  |   AWS S3-   |  |   Azure-    |  |  fsnotify-  |     |
|  |  Adapter    |  |   Adapter   |  |   Adapter   |  |  Adapter    |     |
|  +-------------+  +-------------+  +-------------+  +-------------+     |
|                   +-------------+                                        |
|                   |   HTTP-     |                                        |
|                   |   Adapter   |                                        |
|                   +-------------+                                        |
+--------------------------------------------------------------------------+
```

### 4.2 Package-Struktur

```
ortels/
|-- cmd/
|   +-- ortels/
|       +-- main.go                    # Entry-Point, DI, Bootstrap (Cobra)
|
|-- internal/
|   |-- adapters/                      # Adapter-Implementierungen
|   |   |-- primary/                   # Driving Adapters
|   |   |   |-- http/
|   |   |   |   |-- server.go          # HTTP-Server-Setup (gorilla/mux)
|   |   |   |   |-- router.go          # Route-Definitionen
|   |   |   |   |-- middleware/
|   |   |   |   |   |-- ratelimit.go   # Rate-Limiting
|   |   |   |   |   |-- logging.go     # Request-Logging
|   |   |   |   |   |-- cors.go        # CORS-Handler
|   |   |   |   |   +-- recovery.go    # Panic-Recovery
|   |   |   |   |-- handlers/
|   |   |   |   |   |-- query.go       # Punktabfrage-Handler
|   |   |   |   |   |-- packages.go    # GeoPackage-Info-Handler
|   |   |   |   |   |-- health.go      # Healthcheck-Handler
|   |   |   |   |   +-- openapi.go     # OpenAPI-Spec-Handler
|   |   |   |   +-- dto/
|   |   |   |       |-- request.go     # Request-DTOs
|   |   |   |       +-- response.go    # Response-DTOs
|   |   |   +-- cli/
|   |   |       +-- root.go            # Cobra-Root-Command
|   |   |       +-- serve.go           # Cobra-Serve-Command
|   |   |
|   |   +-- secondary/                 # Driven Adapters
|   |       |-- spatialite/
|   |       |   |-- repository.go      # SpatiaLite-Repository
|   |       |   |-- queries.go         # SQL-Queries
|   |       |   +-- index.go           # Index-Management
|   |       |-- storage/
|   |       |   |-- s3.go              # AWS S3-Adapter
|   |       |   |-- azure.go           # Azure-Blob-Adapter
|   |       |   |-- http.go            # HTTP-Download-Adapter (index.txt)
|   |       |   +-- local.go           # Lokaler Dateisystem-Adapter
|   |       +-- watcher/
|   |           +-- fsnotify.go        # File-System-Watcher
|   |
|   |-- application/                   # Application Services (Use Cases)
|   |   |-- query/
|   |   |   |-- service.go             # Query-Service
|   |   |   +-- service_test.go
|   |   |-- geopackage/
|   |   |   |-- service.go             # GeoPackage-Management
|   |   |   |-- loader.go              # Package-Laden und Indexierung
|   |   |   +-- service_test.go
|   |   +-- registry/
|   |       |-- service.go             # GeoPackage-Registry
|   |       +-- service_test.go
|   |
|   |-- domain/                        # Domain-Modelle (Entities, Value Objects)
|   |   |-- geopackage.go              # GeoPackage-Entity
|   |   |-- layer.go                   # Layer-Entity
|   |   |-- feature.go                 # Feature-Entity
|   |   |-- coordinate.go              # Coordinate-Value-Object
|   |   |-- projection.go              # Projection-Value-Object
|   |   |-- metadata.go                # Metadata-Value-Object
|   |   |-- license.go                 # License-Value-Object
|   |   +-- errors.go                  # Domain-Fehler
|   |
|   |-- ports/                         # Port-Definitionen (Interfaces)
|   |   |-- input/                     # Input Ports (Primary)
|   |   |   |-- query.go               # QueryPort-Interface
|   |   |   |-- geopackage.go          # GeoPackagePort-Interface
|   |   |   +-- health.go              # HealthPort-Interface
|   |   +-- output/                    # Output Ports (Secondary)
|   |       |-- repository.go          # GeoPackageRepository-Interface
|   |       |-- storage.go             # StoragePort-Interface
|   |       +-- watcher.go             # FileWatcherPort-Interface
|   |
|   |-- config/                        # Konfigurationsmanagement (Viper)
|   |   |-- config.go                  # Config-Struct
|   |   |-- loader.go                  # Viper-Config-Loader
|   |   |-- validation.go              # Config-Validierung
|   |   +-- defaults.go                # Default-Werte
|   |
|   +-- infrastructure/                # Infrastruktur-Utilities
|       |-- tls/
|       |   |-- manager.go             # TLS-Manager
|       |   +-- certmagic.go           # CertMagic-Integration
|       |-- logging/
|       |   |-- logger.go              # Logging-Setup
|       |   +-- levels.go              # Log-Level-Definitionen
|       +-- metrics/
|           |-- prometheus.go          # Prometheus-Metriken
|           +-- collector.go           # Custom Collectors
|
|-- api/
|   +-- openapi/
|       +-- spec.yaml                  # OpenAPI-3.0-Spezifikation
|
|-- deployments/
|   |-- docker/
|   |   |-- Dockerfile                 # Multi-Stage-Dockerfile
|   |   +-- docker-compose.yml         # Development-Setup
|   +-- kubernetes/
|       |-- deployment.yaml
|       |-- service.yaml
|       +-- configmap.yaml
|
|-- testdata/
|   |-- geopackages/                   # Test-GeoPackages
|   +-- fixtures/                      # Test-Fixtures
|
+-- doc/
    |-- ARCHITECTURE.md
    |-- ARCHITECTURE_PLAN.md
    |-- DEVELOPMENT.md
    +-- adr/
        |-- 0001-standard-go-project-layout.md
        |-- 0002-hexagonal-architecture-elements.md
        |-- 0003-vincenty-as-default-algorithm.md
        |-- 0004-interface-based-codec-system.md
        |-- 0005-geopackage-based-architecture.md
        |-- 0006-object-storage-integration.md
        |-- 0007-configuration-management.md
        |-- 0008-tls-certmagic.md
        +-- 0009-hot-reload-file-watching.md
```

### 4.3 Abhängigkeits-Graph

```
                         +------------------+
                         |    cmd/ortels/   |
                         |     main.go      |
                         +--------+---------+
                                  |
         +------------------------+------------------------+
         |                        |                        |
         v                        v                        v
+--------+--------+      +--------+--------+      +--------+--------+
|   adapters/     |      |   application/  |      |     config/     |
|   primary/http  |      |                 |      |     (Viper)     |
+-----------------+      +--------+--------+      +-----------------+
         |                        |
         v                        v
+--------+--------+      +--------+--------+
|    ports/       |      |    domain/      |
|    input/       |      |                 |
+-----------------+      +-----------------+
         |
         v
+--------+--------+
|    ports/       |
|    output/      |
+-----------------+
         |
         v
+--------+--------+
|   adapters/     |
|   secondary/    |
+-----------------+
```

**Abhängigkeitsregel:**
- Adapters -> Ports -> Application -> Domain
- Domain hat KEINE Abhängigkeiten
- Ports definieren Interfaces, Adapters implementieren sie

---

## 5. Port-Definitionen

### 5.1 Input Ports (Primary)

```go
// internal/ports/input/query.go
package input

import (
    "context"
    "github.com/jobrunner/ortels/internal/domain"
)

// QueryPort definiert die Schnittstelle für Geo-Abfragen
type QueryPort interface {
    // PointQuery führt eine Punktabfrage auf allen registrierten GeoPackages durch
    PointQuery(ctx context.Context, req PointQueryRequest) (*PointQueryResponse, error)

    // PointQueryByPackage führt eine Punktabfrage auf einem spezifischen GeoPackage durch
    PointQueryByPackage(ctx context.Context, packageID string, req PointQueryRequest) (*PointQueryResponse, error)
}

// PointQueryRequest enthält die Abfrageparameter
type PointQueryRequest struct {
    Longitude   float64            // Längengrad
    Latitude    float64            // Breitengrad
    SourceSRID  int                // Quell-Koordinatensystem (z.B. 4326 für WGS84)
    Properties  []string           // Optional: nur bestimmte Properties zurückgeben
}

// PointQueryResponse enthält die Abfrageergebnisse
type PointQueryResponse struct {
    Results     []PackageResult    // Ergebnisse pro GeoPackage
    RequestInfo RequestInfo        // Request-Metadaten
}

// PackageResult enthält die Features eines GeoPackages
type PackageResult struct {
    PackageID   string             // GeoPackage-Identifier
    PackageName string             // GeoPackage-Name
    Features    []domain.Feature   // Gefundene Features
    License     domain.License     // Lizenzinformationen
    Attribution string             // Attribution-Text
}

// RequestInfo enthält Metadaten zur Anfrage
type RequestInfo struct {
    QueryCoordinate domain.Coordinate // Abgefragte Koordinate
    TransformedTo   int               // Ziel-SRID falls transformiert
    ProcessingTime  time.Duration     // Verarbeitungszeit
}
```

```go
// internal/ports/input/geopackage.go
package input

import (
    "context"
    "github.com/jobrunner/ortels/internal/domain"
)

// GeoPackagePort definiert die Schnittstelle für GeoPackage-Verwaltung
type GeoPackagePort interface {
    // ListPackages gibt alle registrierten GeoPackages zurück
    ListPackages(ctx context.Context) ([]domain.GeoPackage, error)

    // GetPackage gibt ein spezifisches GeoPackage zurück
    GetPackage(ctx context.Context, id string) (*domain.GeoPackage, error)

    // GetPackageLayers gibt die Layer eines GeoPackages zurück
    GetPackageLayers(ctx context.Context, id string) ([]domain.Layer, error)

    // GetPackageMetadata gibt die Metadaten eines GeoPackages zurück
    GetPackageMetadata(ctx context.Context, id string) (*domain.Metadata, error)
}
```

```go
// internal/ports/input/health.go
package input

import "context"

// HealthPort definiert die Schnittstelle für Health-Checks
type HealthPort interface {
    // Ready prüft ob der Service bereit ist (alle Indizes erstellt)
    Ready(ctx context.Context) (HealthStatus, error)

    // Live prüft ob der Service lebt
    Live(ctx context.Context) (HealthStatus, error)
}

// HealthStatus repräsentiert den Gesundheitsstatus
type HealthStatus struct {
    Status      string            // "healthy", "degraded", "unhealthy"
    Checks      map[string]Check  // Einzelne Health-Checks
    Version     string            // Service-Version
}

// Check repräsentiert einen einzelnen Health-Check
type Check struct {
    Status   string        // "pass", "fail"
    Duration time.Duration // Check-Dauer
    Message  string        // Optional: Fehlermeldung
}
```

### 5.2 Output Ports (Secondary)

```go
// internal/ports/output/repository.go
package output

import (
    "context"
    "github.com/jobrunner/ortels/internal/domain"
)

// GeoPackageRepository definiert die Schnittstelle für GeoPackage-Datenzugriff
type GeoPackageRepository interface {
    // Open öffnet ein GeoPackage (read-only)
    Open(ctx context.Context, path string) (*GeoPackageHandle, error)

    // Close schließt ein GeoPackage
    Close(handle *GeoPackageHandle) error

    // GetLayers liest die Layer aus gpkg_contents
    GetLayers(ctx context.Context, handle *GeoPackageHandle) ([]domain.Layer, error)

    // HasSpatialIndex prüft ob ein Layer einen Spatial-Index hat
    HasSpatialIndex(ctx context.Context, handle *GeoPackageHandle, layer string) (bool, error)

    // CreateSpatialIndex erstellt einen Spatial-Index für einen Layer
    CreateSpatialIndex(ctx context.Context, handle *GeoPackageHandle, layer, geomColumn string) error

    // PointQuery führt eine Punktabfrage auf einem Layer durch
    PointQuery(ctx context.Context, handle *GeoPackageHandle, layer string, coord domain.Coordinate) ([]domain.Feature, error)

    // GetMetadata liest Metadaten aus gpkg_metadata
    GetMetadata(ctx context.Context, handle *GeoPackageHandle) (*domain.Metadata, error)

    // GetSRID gibt den SRID eines Layers zurück
    GetSRID(ctx context.Context, handle *GeoPackageHandle, layer string) (int, error)
}

// GeoPackageHandle repräsentiert eine offene GeoPackage-Verbindung
type GeoPackageHandle struct {
    ID       string
    Path     string
    ReadOnly bool
    // interne Felder...
}
```

```go
// internal/ports/output/storage.go
package output

import (
    "context"
    "io"
)

// StoragePort definiert die Schnittstelle für Object Storage
type StoragePort interface {
    // List listet alle GeoPackages im Storage
    List(ctx context.Context) ([]StorageObject, error)

    // Download lädt ein GeoPackage herunter
    Download(ctx context.Context, key string, dest io.Writer) error

    // GetMetadata gibt Metadaten eines Objekts zurück
    GetMetadata(ctx context.Context, key string) (*StorageObjectMeta, error)
}

// StorageObject repräsentiert ein Objekt im Storage
type StorageObject struct {
    Key          string    // Objekt-Schlüssel
    Size         int64     // Größe in Bytes
    LastModified time.Time // Letzte Änderung
    ETag         string    // Entity-Tag
}

// StorageObjectMeta enthält Metadaten eines Objekts
type StorageObjectMeta struct {
    ContentType  string
    ContentLength int64
    Metadata     map[string]string
}
```

```go
// internal/ports/output/watcher.go
package output

import "context"

// FileWatcherPort definiert die Schnittstelle für File-System-Überwachung
type FileWatcherPort interface {
    // Watch startet die Überwachung eines Verzeichnisses
    Watch(ctx context.Context, path string) (<-chan FileEvent, error)

    // Stop beendet die Überwachung
    Stop() error
}

// FileEvent repräsentiert ein Dateisystem-Ereignis
type FileEvent struct {
    Type     FileEventType // Ereignis-Typ
    Path     string        // Dateipfad
    Filename string        // Dateiname
}

// FileEventType definiert die Ereignis-Typen
type FileEventType int

const (
    FileCreated FileEventType = iota
    FileModified
    FileDeleted
)
```

---

## 6. Domain-Modelle

```go
// internal/domain/geopackage.go
package domain

import "time"

// GeoPackage repräsentiert ein registriertes GeoPackage
type GeoPackage struct {
    ID          string       // Eindeutiger Identifier
    Name        string       // Anzeigename
    Path        string       // Dateipfad
    Size        int64        // Dateigröße in Bytes
    Layers      []Layer      // Feature-Layers
    Metadata    Metadata     // Paket-Metadaten
    License     License      // Lizenzinformationen
    Indexed     bool         // Sind alle Indizes erstellt?
    LoadedAt    time.Time    // Ladezeitpunkt
    LastQueried time.Time    // Letzte Abfrage
}

// Layer repräsentiert einen Feature-Layer
type Layer struct {
    Name           string    // Layer-Name aus gpkg_contents.table_name
    Description    string    // Beschreibung
    GeometryColumn string    // Name der Geometrie-Spalte
    GeometryType   string    // Geometrie-Typ (POINT, POLYGON, etc.)
    SRID           int       // Spatial Reference ID
    HasIndex       bool      // Hat Spatial-Index?
    FeatureCount   int64     // Anzahl Features
    Extent         *Extent   // Bounding Box
}

// Extent repräsentiert die räumliche Ausdehnung
type Extent struct {
    MinX float64
    MinY float64
    MaxX float64
    MaxY float64
    SRID int
}
```

```go
// internal/domain/feature.go
package domain

// Feature repräsentiert ein Geo-Feature
type Feature struct {
    ID         int64                  // Feature-ID
    LayerName  string                 // Zugehöriger Layer
    Geometry   Geometry               // Geometrie
    Properties map[string]interface{} // Attributdaten
}

// Geometry repräsentiert eine Geometrie
type Geometry struct {
    Type        string      // WKT-Typ (Point, Polygon, etc.)
    WKT         string      // Well-Known Text
    WKB         []byte      // Well-Known Binary
    SRID        int         // Spatial Reference ID
    Coordinates Coordinate  // Für Punkt-Geometrien
}
```

```go
// internal/domain/coordinate.go
package domain

// Coordinate repräsentiert eine Geokoordinate
type Coordinate struct {
    X    float64 // Längengrad oder Rechtswert
    Y    float64 // Breitengrad oder Hochwert
    Z    float64 // Höhe (optional)
    SRID int     // Spatial Reference ID
}

// NewWGS84Coordinate erstellt eine WGS84-Koordinate
func NewWGS84Coordinate(lon, lat float64) Coordinate {
    return Coordinate{X: lon, Y: lat, SRID: 4326}
}

// Projection repräsentiert ein Koordinatensystem
type Projection struct {
    SRID        int    // EPSG-Code
    Name        string // Projektionsname
    Proj4       string // Proj4-String
    WKT         string // Well-Known Text
}

// CommonProjections enthält häufig verwendete Projektionen
var CommonProjections = map[int]Projection{
    4326:  {SRID: 4326, Name: "WGS 84"},
    3857:  {SRID: 3857, Name: "Web Mercator"},
    25832: {SRID: 25832, Name: "ETRS89 / UTM zone 32N"},
    25833: {SRID: 25833, Name: "ETRS89 / UTM zone 33N"},
    31466: {SRID: 31466, Name: "DHDN / Gauß-Krüger zone 2"},
    31467: {SRID: 31467, Name: "DHDN / Gauß-Krüger zone 3"},
}
```

```go
// internal/domain/metadata.go
package domain

// Metadata enthält GeoPackage-Metadaten
type Metadata struct {
    Title        string            // Titel
    Description  string            // Beschreibung
    Creator      string            // Ersteller
    CreatedAt    time.Time         // Erstellungsdatum
    Version      string            // Version
    Keywords     []string          // Schlüsselwörter
    Custom       map[string]string // Benutzerdefinierte Metadaten
}

// License enthält Lizenzinformationen
type License struct {
    Name        string // Lizenzname (z.B. "CC BY 4.0")
    URL         string // Link zur Lizenz
    Attribution string // Attribution-Text
}
```

```go
// internal/domain/errors.go
package domain

import "errors"

var (
    // ErrPackageNotFound wird zurückgegeben wenn ein GeoPackage nicht gefunden wird
    ErrPackageNotFound = errors.New("geopackage not found")

    // ErrLayerNotFound wird zurückgegeben wenn ein Layer nicht gefunden wird
    ErrLayerNotFound = errors.New("layer not found")

    // ErrInvalidCoordinate wird zurückgegeben bei ungültigen Koordinaten
    ErrInvalidCoordinate = errors.New("invalid coordinate")

    // ErrUnsupportedProjection wird zurückgegeben bei unbekannter Projektion
    ErrUnsupportedProjection = errors.New("unsupported projection")

    // ErrIndexCreationFailed wird zurückgegeben wenn Index-Erstellung fehlschlägt
    ErrIndexCreationFailed = errors.New("spatial index creation failed")

    // ErrNotReady wird zurückgegeben wenn der Service noch nicht bereit ist
    ErrNotReady = errors.New("service not ready")
)
```

---

## 7. Datenfluss-Diagramme

### 7.1 Startup-Sequenz

```
                           Container Start
                                 |
                                 v
+------------------+    +------------------+    +------------------+
|  Config laden    |    |  Storage prüfen  |    |  TLS-Setup       |
|  (Viper: CLI >   |--->|  (AWS S3/Azure/  |--->|  (CertMagic      |
|   Env > File)    |    |   HTTP konf.?)   |    |   oder Certs)    |
+------------------+    +--------+---------+    +------------------+
                                 |
              +------------------+------------------+
              |                  |                  |
              v                  v                  v
+------------------+  +------------------+  +------------------+
|  AWS S3: Lade    |  |  HTTP: Lade      |  |  Lokal: Scanne   |
|  GeoPackages     |  |  index.txt und   |  |  /data/gpkg/     |
|  aus Bucket      |  |  dann .gpkg-     |  |  Verzeichnis     |
|                  |  |  Dateien via     |  |                  |
|                  |  |  curl            |  |                  |
+--------+---------+  +--------+---------+  +--------+---------+
              |                  |                  |
              +------------------+------------------+
                                 |
                                 v
                    +------------------+
                    |  Für jedes       |
                    |  GeoPackage:     |
                    +--------+---------+
                             |
         +-------------------+-------------------+
         |                   |                   |
         v                   v                   v
+--------+--------+ +--------+--------+ +--------+--------+
|  Layer aus      | |  Spatial-Index  | |  Metadaten      |
|  gpkg_contents  | |  vorhanden?     | |  laden          |
|  lesen          | |  Sonst CREATE   | |                 |
+-----------------+ +-----------------+ +-----------------+
         |                   |                   |
         +-------------------+-------------------+
                             |
                             v
                +------------------+
                |  GeoPackage in   |
                |  Registry        |
                |  registrieren    |
                +--------+---------+
                         |
                         v
                +------------------+
                |  Read-Only       |
                |  öffnen          |
                +--------+---------+
                         |
                         v
                +------------------+
                |  HTTP-Server     |
                |  starten         |
                |  (erst wenn alle |
                |  Indizes fertig) |
                +------------------+
                         |
                         v
                +------------------+
                |  File-Watcher    |
                |  starten         |
                +------------------+
```

### 7.2 HTTP-Download-Ablauf (index.txt)

```
                    HTTP-Storage-Adapter
                            |
                            v
              +---------------------------+
              |  GET {base_url}/index.txt |
              +-------------+-------------+
                            |
                            v
              +---------------------------+
              |  Parse index.txt          |
              |  (eine .gpkg pro Zeile)   |
              +-------------+-------------+
                            |
         +------------------+------------------+
         |                  |                  |
         v                  v                  v
+--------+--------+ +-------+--------+ +-------+--------+
| GET file1.gpkg  | | GET file2.gpkg | | GET file3.gpkg |
| -> /data/gpkg/  | | -> /data/gpkg/ | | -> /data/gpkg/ |
+-----------------+ +----------------+ +----------------+
```

**index.txt-Format:**
```
gemeinden.gpkg
bodenarten.gpkg
schutzgebiete.gpkg
```

### 7.3 Punktabfrage-Sequenz

```
Client              HTTP Handler         QueryService         Repository
   |                     |                    |                    |
   | GET /query?         |                    |                    |
   | lon=11.5&lat=48.1   |                    |                    |
   | &srid=4326          |                    |                    |
   |-------------------->|                    |                    |
   |                     | ParseRequest       |                    |
   |                     |----------+         |                    |
   |                     |<---------+         |                    |
   |                     |                    |                    |
   |                     | PointQuery(ctx,    |                    |
   |                     |   PointQueryReq)   |                    |
   |                     |------------------->|                    |
   |                     |                    |                    |
   |                     |                    | For each Package:  |
   |                     |                    |                    |
   |                     |                    | Transform Coord    |
   |                     |                    | if SRID differs    |
   |                     |                    |----------+         |
   |                     |                    |<---------+         |
   |                     |                    |                    |
   |                     |                    | PointQuery(handle, |
   |                     |                    |   layer, coord)    |
   |                     |                    |------------------->|
   |                     |                    |                    |
   |                     |                    |                    | SELECT * FROM layer
   |                     |                    |                    | WHERE ST_Contains(
   |                     |                    |                    |   geom,
   |                     |                    |                    |   GeomFromText(
   |                     |                    |                    |     'POINT(...)',
   |                     |                    |                    |     srid))
   |                     |                    |                    |----------+
   |                     |                    |                    |<---------+
   |                     |                    |<-------------------|
   |                     |                    | []Feature          |
   |                     |                    |                    |
   |                     |                    | Collect + enrich   |
   |                     |                    | with License       |
   |                     |                    |----------+         |
   |                     |                    |<---------+         |
   |                     |                    |                    |
   |                     |<-------------------|                    |
   |                     | PointQueryResponse |                    |
   |                     |                    |                    |
   |                     | ToJSON             |                    |
   |                     |----------+         |                    |
   |                     |<---------+         |                    |
   |<--------------------|                    |                    |
   | HTTP 200            |                    |                    |
   | JSON Response       |                    |                    |
```

### 7.4 Hot-Reload-Sequenz

```
FileSystem              Watcher              Registry            Repository
    |                      |                    |                    |
    | .gpkg file           |                    |                    |
    | created/deleted      |                    |                    |
    |--------------------->|                    |                    |
    |                      | FileEvent          |                    |
    |                      |                    |                    |
    |                      | if FileCreated:    |                    |
    |                      |                    |                    |
    |                      | Validate .gpkg     |                    |
    |                      |----------+         |                    |
    |                      |<---------+         |                    |
    |                      |                    |                    |
    |                      | LoadPackage()      |                    |
    |                      |------------------->|                    |
    |                      |                    | Open(path)         |
    |                      |                    |------------------->|
    |                      |                    |<-------------------|
    |                      |                    | handle             |
    |                      |                    |                    |
    |                      |                    | GetLayers(handle)  |
    |                      |                    |------------------->|
    |                      |                    |<-------------------|
    |                      |                    |                    |
    |                      |                    | For each Layer:    |
    |                      |                    | CreateSpatialIndex |
    |                      |                    | (if needed)        |
    |                      |                    |------------------->|
    |                      |                    |<-------------------|
    |                      |                    |                    |
    |                      |                    | Register(package)  |
    |                      |                    |----------+         |
    |                      |<-------------------|<---------+         |
    |                      |                    |                    |
    |                      | if FileDeleted:    |                    |
    |                      |                    |                    |
    |                      | Unregister(id)     |                    |
    |                      |------------------->|                    |
    |                      |                    | Close(handle)      |
    |                      |                    |------------------->|
    |                      |                    |<-------------------|
    |                      |                    | Remove from map    |
    |                      |<-------------------|                    |
```

---

## 8. API-Design

### 8.1 REST-Endpunkte

| Methode | Pfad | Beschreibung |
|---------|------|--------------|
| GET | `/api/v1/query` | Punktabfrage auf allen GeoPackages |
| GET | `/api/v1/query/{packageId}` | Punktabfrage auf einem GeoPackage |
| GET | `/api/v1/packages` | Liste aller registrierten GeoPackages |
| GET | `/api/v1/packages/{packageId}` | Details zu einem GeoPackage |
| GET | `/api/v1/packages/{packageId}/layers` | Layer eines GeoPackages |
| GET | `/api/v1/packages/{packageId}/metadata` | Metadaten eines GeoPackages |
| GET | `/health/ready` | Readiness-Check |
| GET | `/health/live` | Liveness-Check |
| GET | `/metrics` | Prometheus-Metriken |
| GET | `/api/openapi.yaml` | OpenAPI-Spezifikation |

### 8.2 Request/Response-Beispiele

#### Punktabfrage

**Request:**
```http
GET /api/v1/query?lon=11.576124&lat=48.137154&srid=4326 HTTP/1.1
Host: ortels.example.com
Accept: application/json
```

**Response (200 OK):**
```json
{
  "request": {
    "coordinate": {
      "x": 11.576124,
      "y": 48.137154,
      "srid": 4326
    },
    "processing_time_ms": 45
  },
  "results": [
    {
      "package_id": "administrative-boundaries",
      "package_name": "Administrative Grenzen Deutschland",
      "license": {
        "name": "Datenlizenz Deutschland - Namensnennung - Version 2.0",
        "url": "https://www.govdata.de/dl-de/by-2-0",
        "attribution": "GeoBasis-DE / BKG (2024)"
      },
      "features": [
        {
          "id": 42,
          "layer": "gemeinden",
          "geometry": {
            "type": "MultiPolygon",
            "srid": 25832
          },
          "properties": {
            "name": "München",
            "ags": "09162000",
            "population": 1512491,
            "area_km2": 310.71
          }
        }
      ]
    },
    {
      "package_id": "soil-types",
      "package_name": "Bodenarten",
      "license": {
        "name": "CC BY 4.0",
        "url": "https://creativecommons.org/licenses/by/4.0/",
        "attribution": "Bayerisches Landesamt für Umwelt"
      },
      "features": [
        {
          "id": 1247,
          "layer": "bodenarten",
          "geometry": {
            "type": "Polygon",
            "srid": 25832
          },
          "properties": {
            "bodenart": "Lehm",
            "code": "L"
          }
        }
      ]
    }
  ]
}
```

#### GeoPackage-Liste

**Request:**
```http
GET /api/v1/packages HTTP/1.1
Host: ortels.example.com
Accept: application/json
```

**Response (200 OK):**
```json
{
  "packages": [
    {
      "id": "administrative-boundaries",
      "name": "Administrative Grenzen Deutschland",
      "path": "/data/gpkg/admin_boundaries.gpkg",
      "size_bytes": 524288000,
      "layer_count": 4,
      "indexed": true,
      "loaded_at": "2024-01-15T08:30:00Z",
      "license": {
        "name": "Datenlizenz Deutschland - Namensnennung",
        "attribution": "GeoBasis-DE / BKG"
      }
    }
  ],
  "total": 1
}
```

#### Healthcheck

**Request:**
```http
GET /health/ready HTTP/1.1
Host: ortels.example.com
```

**Response (200 OK):**
```json
{
  "status": "healthy",
  "version": "1.0.0",
  "checks": {
    "geopackages": {
      "status": "pass",
      "packages_loaded": 5,
      "all_indexed": true
    },
    "storage": {
      "status": "pass",
      "type": "s3",
      "connected": true
    }
  }
}
```

**Response (503 Service Unavailable - beim Start):**
```json
{
  "status": "unhealthy",
  "version": "1.0.0",
  "checks": {
    "geopackages": {
      "status": "fail",
      "message": "Creating spatial indices: 2/5 complete",
      "packages_loaded": 5,
      "all_indexed": false
    }
  }
}
```

### 8.3 Fehler-Responses

```json
{
  "error": {
    "code": "INVALID_COORDINATE",
    "message": "Latitude must be between -90 and 90",
    "details": {
      "field": "lat",
      "value": 91.5,
      "constraint": "[-90, 90]"
    }
  }
}
```

| HTTP-Status | Error-Code | Beschreibung |
|-------------|------------|--------------|
| 400 | INVALID_COORDINATE | Ungültige Koordinaten |
| 400 | INVALID_SRID | Unbekannte Projektion |
| 404 | PACKAGE_NOT_FOUND | GeoPackage existiert nicht |
| 429 | RATE_LIMITED | Rate-Limit überschritten |
| 503 | SERVICE_NOT_READY | Indizes werden erstellt |
| 500 | INTERNAL_ERROR | Interner Fehler |

---

## 9. Konfigurationsschema

### 9.1 Konfigurationsstruktur (Viper)

```go
// internal/config/config.go
package config

import "time"

// Config enthält die gesamte Anwendungskonfiguration
type Config struct {
    Server     ServerConfig     `mapstructure:"server"`
    GeoPackage GeoPackageConfig `mapstructure:"geopackage"`
    Storage    StorageConfig    `mapstructure:"storage"`
    TLS        TLSConfig        `mapstructure:"tls"`
    Logging    LoggingConfig    `mapstructure:"logging"`
    Metrics    MetricsConfig    `mapstructure:"metrics"`
    RateLimit  RateLimitConfig  `mapstructure:"rateLimit"`
}

// ServerConfig enthält HTTP-Server-Einstellungen
type ServerConfig struct {
    Host            string        `mapstructure:"host"`
    Port            int           `mapstructure:"port"`
    ReadTimeout     time.Duration `mapstructure:"readTimeout"`
    WriteTimeout    time.Duration `mapstructure:"writeTimeout"`
    ShutdownTimeout time.Duration `mapstructure:"shutdownTimeout"`
}

// GeoPackageConfig enthält GeoPackage-Einstellungen
type GeoPackageConfig struct {
    Directory     string        `mapstructure:"directory"`
    WatchInterval time.Duration `mapstructure:"watchInterval"`
    IndexTimeout  time.Duration `mapstructure:"indexTimeout"`
}

// StorageConfig enthält Object-Storage-Einstellungen
type StorageConfig struct {
    Type        string `mapstructure:"type"` // local, s3, azure, http

    // AWS S3-Einstellungen
    S3Bucket    string `mapstructure:"s3Bucket"`
    S3Region    string `mapstructure:"s3Region"`
    S3Endpoint  string `mapstructure:"s3Endpoint"`
    S3AccessKey string `mapstructure:"s3AccessKey"`
    S3SecretKey string `mapstructure:"s3SecretKey"`

    // Azure-Einstellungen
    AzureContainer        string `mapstructure:"azureContainer"`
    AzureAccountName      string `mapstructure:"azureAccountName"`
    AzureAccountKey       string `mapstructure:"azureAccountKey"`
    AzureConnectionString string `mapstructure:"azureConnectionString"`

    // HTTP-Download-Einstellungen
    HTTPBaseURL string `mapstructure:"httpBaseUrl"` // Base-URL für index.txt und .gpkg-Dateien
}

// TLSConfig enthält TLS-Einstellungen (CertMagic)
type TLSConfig struct {
    Enabled  bool   `mapstructure:"enabled"`
    CertFile string `mapstructure:"certFile"`
    KeyFile  string `mapstructure:"keyFile"`

    // Let's Encrypt via CertMagic
    LetsEncrypt      bool     `mapstructure:"letsEncrypt"`
    LetsEncryptEmail string   `mapstructure:"letsEncryptEmail"`
    Domains          []string `mapstructure:"domains"`
    CacheDir         string   `mapstructure:"cacheDir"`
}

// LoggingConfig enthält Logging-Einstellungen
type LoggingConfig struct {
    Level  string `mapstructure:"level"` // none, error, info, debug, verbose
    Format string `mapstructure:"format"` // json, text
    Output string `mapstructure:"output"`

    // Verbose-Einstellungen
    LogQueries   bool `mapstructure:"logQueries"`
    LogRequests  bool `mapstructure:"logRequests"`
    LogResponses bool `mapstructure:"logResponses"`
}

// MetricsConfig enthält Metriken-Einstellungen
type MetricsConfig struct {
    Enabled bool   `mapstructure:"enabled"`
    Port    int    `mapstructure:"port"`
    Path    string `mapstructure:"path"`
}

// RateLimitConfig enthält Rate-Limit-Einstellungen
type RateLimitConfig struct {
    Enabled           bool    `mapstructure:"enabled"`
    RequestsPerSecond float64 `mapstructure:"requestsPerSecond"`
    Burst             int     `mapstructure:"burst"`
}
```

### 9.2 Viper-Konfiguration laden

```go
// internal/config/loader.go
package config

import (
    "github.com/spf13/viper"
)

func LoadConfig() (*Config, error) {
    viper.SetConfigName("config")
    viper.SetConfigType("yaml")
    viper.AddConfigPath(".")
    viper.AddConfigPath("/etc/ortels")

    // Umgebungsvariablen-Prefix
    viper.SetEnvPrefix("ORTELS")
    viper.AutomaticEnv()

    // Defaults setzen
    setDefaults()

    // Config-Datei lesen (optional)
    if err := viper.ReadInConfig(); err != nil {
        if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
            return nil, err
        }
    }

    var cfg Config
    if err := viper.Unmarshal(&cfg); err != nil {
        return nil, err
    }

    return &cfg, nil
}

func setDefaults() {
    viper.SetDefault("server.host", "0.0.0.0")
    viper.SetDefault("server.port", 8080)
    viper.SetDefault("server.readTimeout", "30s")
    viper.SetDefault("server.writeTimeout", "30s")
    viper.SetDefault("server.shutdownTimeout", "30s")

    viper.SetDefault("geopackage.directory", "/data/gpkg")
    viper.SetDefault("geopackage.watchInterval", "10s")
    viper.SetDefault("geopackage.indexTimeout", "10m")

    viper.SetDefault("storage.type", "local")

    viper.SetDefault("tls.enabled", false)
    viper.SetDefault("tls.letsEncrypt", false)
    viper.SetDefault("tls.cacheDir", "/var/cache/ortels/certs")

    viper.SetDefault("logging.level", "info")
    viper.SetDefault("logging.format", "json")
    viper.SetDefault("logging.output", "stdout")
    viper.SetDefault("logging.logRequests", true)

    viper.SetDefault("metrics.enabled", true)
    viper.SetDefault("metrics.port", 9090)
    viper.SetDefault("metrics.path", "/metrics")

    viper.SetDefault("rateLimit.enabled", true)
    viper.SetDefault("rateLimit.requestsPerSecond", 10)
    viper.SetDefault("rateLimit.burst", 20)
}
```

### 9.3 Konfigurations-Priorität

```
+------------------+
|   CLI-Flags      |  Höchste Priorität
|  --rate-limit=10 |  (via Cobra)
+--------+---------+
         |
         v
+--------+---------+
| Umgebungsvariablen|
| ORTELS_RATE_LIMIT |
+--------+---------+
         |
         v
+--------+---------+
|   Config-File    |
| config.yaml/.env |
+--------+---------+
         |
         v
+--------+---------+
|   Defaults       |  Niedrigste Priorität
|   (im Code)      |
+------------------+
```

### 9.4 CLI mit Cobra

```go
// cmd/ortels/main.go
package main

import (
    "github.com/spf13/cobra"
    "github.com/spf13/viper"
)

var rootCmd = &cobra.Command{
    Use:   "ortels",
    Short: "GeoPackage Point Query Service",
    Long:  `Ortels ist ein Go-Service für Punktabfragen auf GeoPackage-Dateien.`,
}

var serveCmd = &cobra.Command{
    Use:   "serve",
    Short: "Startet den HTTP-Server",
    RunE:  runServe,
}

func init() {
    rootCmd.AddCommand(serveCmd)

    // Flags mit Viper binden
    serveCmd.Flags().String("host", "0.0.0.0", "HTTP-Server Host")
    serveCmd.Flags().Int("port", 8080, "HTTP-Server Port")
    serveCmd.Flags().String("gpkg-dir", "/data/gpkg", "GeoPackage-Verzeichnis")
    serveCmd.Flags().Float64("rate-limit", 10, "Requests pro Sekunde")
    serveCmd.Flags().String("log-level", "info", "Log-Level: none, error, info, debug, verbose")
    serveCmd.Flags().Bool("tls", false, "TLS aktivieren")
    serveCmd.Flags().String("tls-cert", "", "TLS-Zertifikatsdatei")
    serveCmd.Flags().String("tls-key", "", "TLS-Schlüsseldatei")
    serveCmd.Flags().Bool("letsencrypt", false, "Let's Encrypt via CertMagic aktivieren")
    serveCmd.Flags().String("letsencrypt-email", "", "E-Mail für Let's Encrypt")
    serveCmd.Flags().StringSlice("domains", nil, "Domains für Let's Encrypt")
    serveCmd.Flags().String("storage-type", "local", "Storage-Typ: local, s3, azure, http")
    serveCmd.Flags().String("s3-bucket", "", "AWS S3-Bucket-Name")
    serveCmd.Flags().String("s3-region", "", "AWS S3-Region")
    serveCmd.Flags().String("http-base-url", "", "Base-URL für HTTP-Download (index.txt)")
    serveCmd.Flags().Int("metrics-port", 9090, "Prometheus-Metriken-Port")
    serveCmd.Flags().String("config", "", "Pfad zur Config-Datei")

    viper.BindPFlags(serveCmd.Flags())
}

func main() {
    if err := rootCmd.Execute(); err != nil {
        os.Exit(1)
    }
}
```

### 9.5 Umgebungsvariablen

| Variable | Default | Beschreibung |
|----------|---------|--------------|
| `ORTELS_HOST` | `0.0.0.0` | HTTP-Server-Host |
| `ORTELS_PORT` | `8080` | HTTP-Server-Port |
| `ORTELS_GPKG_DIR` | `/data/gpkg` | GeoPackage-Verzeichnis |
| `ORTELS_STORAGE_TYPE` | `local` | Storage-Typ (local/s3/azure/http) |
| `ORTELS_S3_BUCKET` | - | AWS S3-Bucket-Name |
| `ORTELS_S3_REGION` | - | AWS S3-Region |
| `ORTELS_S3_ENDPOINT` | - | AWS S3-Custom-Endpoint (MinIO etc.) |
| `ORTELS_S3_ACCESS_KEY` | - | AWS S3-Access-Key |
| `ORTELS_S3_SECRET_KEY` | - | AWS S3-Secret-Key |
| `ORTELS_HTTP_BASE_URL` | - | Base-URL für HTTP-Download |
| `ORTELS_AZURE_CONTAINER` | - | Azure-Container-Name |
| `ORTELS_AZURE_ACCOUNT_NAME` | - | Azure-Account-Name |
| `ORTELS_AZURE_ACCOUNT_KEY` | - | Azure-Account-Key |
| `ORTELS_TLS_ENABLED` | `false` | TLS aktivieren |
| `ORTELS_TLS_CERT_FILE` | - | TLS-Zertifikatspfad |
| `ORTELS_TLS_KEY_FILE` | - | TLS-Schlüsselpfad |
| `ORTELS_LETSENCRYPT` | `false` | Let's Encrypt aktivieren |
| `ORTELS_LETSENCRYPT_EMAIL` | - | Let's Encrypt-E-Mail |
| `ORTELS_DOMAINS` | - | Domains (kommasepariert) |
| `ORTELS_LOG_LEVEL` | `info` | Log-Level |
| `ORTELS_LOG_FORMAT` | `json` | Log-Format (json/text) |
| `ORTELS_LOG_QUERIES` | `false` | SQL-Queries loggen |
| `ORTELS_LOG_REQUESTS` | `true` | HTTP-Requests loggen |
| `ORTELS_LOG_RESPONSES` | `false` | HTTP-Responses loggen |
| `ORTELS_RATE_LIMIT` | `10` | Requests pro Sekunde |
| `ORTELS_RATE_LIMIT_BURST` | `20` | Burst-Größe |
| `ORTELS_METRICS_ENABLED` | `true` | Prometheus-Metriken |
| `ORTELS_METRICS_PORT` | `9090` | Metriken-Port |

---

## 10. Fehlerbehandlung

### 10.1 Error-Hierarchie

```go
// internal/domain/errors.go
package domain

import (
    "errors"
    "fmt"
)

// Basis-Fehlertypen (Sentinel Errors)
var (
    ErrNotFound           = errors.New("not found")
    ErrInvalidInput       = errors.New("invalid input")
    ErrUnsupported        = errors.New("unsupported operation")
    ErrInternal           = errors.New("internal error")
    ErrUnavailable        = errors.New("service unavailable")
)

// Spezifische Fehler
var (
    ErrPackageNotFound       = fmt.Errorf("geopackage: %w", ErrNotFound)
    ErrLayerNotFound         = fmt.Errorf("layer: %w", ErrNotFound)
    ErrInvalidCoordinate     = fmt.Errorf("coordinate: %w", ErrInvalidInput)
    ErrInvalidSRID           = fmt.Errorf("srid: %w", ErrInvalidInput)
    ErrUnsupportedProjection = fmt.Errorf("projection: %w", ErrUnsupported)
    ErrNotReady              = fmt.Errorf("indices not ready: %w", ErrUnavailable)
)

// ValidationError für detaillierte Validierungsfehler
type ValidationError struct {
    Field      string
    Value      interface{}
    Constraint string
    Message    string
}

func (e ValidationError) Error() string {
    return fmt.Sprintf("validation error for %s: %s (value: %v, constraint: %s)",
        e.Field, e.Message, e.Value, e.Constraint)
}

func (e ValidationError) Unwrap() error {
    return ErrInvalidInput
}

// QueryError für Abfragefehler
type QueryError struct {
    PackageID string
    Layer     string
    Err       error
}

func (e QueryError) Error() string {
    return fmt.Sprintf("query error in package %s, layer %s: %v",
        e.PackageID, e.Layer, e.Err)
}

func (e QueryError) Unwrap() error {
    return e.Err
}
```

### 10.2 Error-Handling in Adaptern

```go
// internal/adapters/primary/http/handlers/error_handler.go
package handlers

import (
    "encoding/json"
    "errors"
    "net/http"

    "github.com/jobrunner/ortels/internal/domain"
)

// ErrorResponse repräsentiert eine Fehlerantwort
type ErrorResponse struct {
    Error ErrorDetail `json:"error"`
}

type ErrorDetail struct {
    Code    string                 `json:"code"`
    Message string                 `json:"message"`
    Details map[string]interface{} `json:"details,omitempty"`
}

// HandleError konvertiert Domain-Fehler zu HTTP-Responses
func HandleError(w http.ResponseWriter, err error) {
    var statusCode int
    var errResp ErrorResponse

    switch {
    case errors.Is(err, domain.ErrNotFound):
        statusCode = http.StatusNotFound
        errResp = ErrorResponse{
            Error: ErrorDetail{
                Code:    "NOT_FOUND",
                Message: err.Error(),
            },
        }

    case errors.Is(err, domain.ErrInvalidInput):
        statusCode = http.StatusBadRequest
        errResp = ErrorResponse{
            Error: ErrorDetail{
                Code:    "INVALID_INPUT",
                Message: err.Error(),
            },
        }
        // ValidationError-Details extrahieren
        var valErr domain.ValidationError
        if errors.As(err, &valErr) {
            errResp.Error.Details = map[string]interface{}{
                "field":      valErr.Field,
                "value":      valErr.Value,
                "constraint": valErr.Constraint,
            }
        }

    case errors.Is(err, domain.ErrUnavailable):
        statusCode = http.StatusServiceUnavailable
        errResp = ErrorResponse{
            Error: ErrorDetail{
                Code:    "SERVICE_UNAVAILABLE",
                Message: err.Error(),
            },
        }

    default:
        statusCode = http.StatusInternalServerError
        errResp = ErrorResponse{
            Error: ErrorDetail{
                Code:    "INTERNAL_ERROR",
                Message: "An internal error occurred",
            },
        }
    }

    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(statusCode)
    json.NewEncoder(w).Encode(errResp)
}
```

---

## 11. Metriken

### 11.1 Prometheus-Metriken

```go
// internal/infrastructure/metrics/prometheus.go
package metrics

import (
    "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/promauto"
)

var (
    // HTTP-Metriken
    HTTPRequestsTotal = promauto.NewCounterVec(
        prometheus.CounterOpts{
            Name: "ortels_http_requests_total",
            Help: "Total number of HTTP requests",
        },
        []string{"method", "path", "status"},
    )

    HTTPRequestDuration = promauto.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "ortels_http_request_duration_seconds",
            Help:    "HTTP request duration in seconds",
            Buckets: prometheus.DefBuckets,
        },
        []string{"method", "path"},
    )

    // Query-Metriken
    QueryTotal = promauto.NewCounterVec(
        prometheus.CounterOpts{
            Name: "ortels_queries_total",
            Help: "Total number of point queries",
        },
        []string{"package_id", "status"},
    )

    QueryDuration = promauto.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "ortels_query_duration_seconds",
            Help:    "Query duration in seconds",
            Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1},
        },
        []string{"package_id"},
    )

    FeaturesReturned = promauto.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "ortels_features_returned",
            Help:    "Number of features returned per query",
            Buckets: []float64{0, 1, 5, 10, 50, 100, 500, 1000},
        },
        []string{"package_id"},
    )

    // GeoPackage-Metriken
    GeoPackagesLoaded = promauto.NewGauge(prometheus.GaugeOpts{
        Name: "ortels_geopackages_loaded",
        Help: "Number of loaded GeoPackages",
    })

    GeoPackagesIndexed = promauto.NewGauge(prometheus.GaugeOpts{
        Name: "ortels_geopackages_indexed",
        Help: "Number of fully indexed GeoPackages",
    })

    IndexCreationDuration = promauto.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "ortels_index_creation_duration_seconds",
            Help:    "Spatial index creation duration",
            Buckets: []float64{1, 5, 10, 30, 60, 120, 300, 600},
        },
        []string{"package_id", "layer"},
    )

    // Storage-Metriken
    StorageDownloadDuration = promauto.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "ortels_storage_download_duration_seconds",
            Help:    "Object storage download duration",
            Buckets: []float64{1, 5, 10, 30, 60, 120, 300},
        },
        []string{"storage_type"},
    )

    StorageDownloadBytes = promauto.NewCounterVec(
        prometheus.CounterOpts{
            Name: "ortels_storage_download_bytes_total",
            Help: "Total bytes downloaded from object storage",
        },
        []string{"storage_type"},
    )
)
```

---

## 12. Container-Architektur

### 12.1 Dockerfile

```dockerfile
# Dockerfile
# Multi-Stage-Build für Ortels

# ==============================================================================
# Stage 1: Build
# ==============================================================================
FROM golang:1.22-alpine AS builder

# Build-Dependencies
RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /build

# Dependency-Caching
COPY go.mod go.sum ./
RUN go mod download

# Source-Code kopieren
COPY . .

# Build mit optimierten Flags
ARG VERSION=dev
ARG BUILD_TIME=unknown
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-w -s -X main.Version=${VERSION} -X main.BuildTime=${BUILD_TIME}" \
    -o ortels \
    ./cmd/ortels

# ==============================================================================
# Stage 2: Runtime
# ==============================================================================
FROM ghcr.io/jobrunner/spatialite-base-image:1.4.0

# Metadata
LABEL org.opencontainers.image.title="Ortels" \
      org.opencontainers.image.description="GeoPackage Point Query Service" \
      org.opencontainers.image.source="https://github.com/jobrunner/ortels"

# Non-root-User (bereits im Base-Image vorhanden: spatialite:10001)
# Verzeichnisse erstellen
RUN mkdir -p /data/gpkg /var/cache/ortels/certs && \
    chown -R spatialite:spatialite /data /var/cache/ortels

# Binary aus Build-Stage kopieren
COPY --from=builder /build/ortels /usr/local/bin/ortels

# Zeitzone-Daten kopieren
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

# CA-Zertifikate für HTTPS
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Arbeitsverzeichnis
WORKDIR /app

# Als Non-root-User ausführen
USER spatialite

# Ports
EXPOSE 8080 9090

# Healthcheck
HEALTHCHECK --interval=30s --timeout=10s --start-period=60s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health/live || exit 1

# Volumes
VOLUME ["/data/gpkg", "/var/cache/ortels/certs"]

# Entry-Point
ENTRYPOINT ["/usr/local/bin/ortels"]

# Default-Argumente
CMD ["serve", "--gpkg-dir=/data/gpkg", "--log-level=info"]
```

### 12.2 Docker Compose (Development)

```yaml
# docker-compose.yml
version: "3.9"

services:
  ortels:
    build:
      context: .
      dockerfile: deployments/docker/Dockerfile
      args:
        VERSION: "dev"
        BUILD_TIME: "${BUILD_TIME:-unknown}"
    ports:
      - "8080:8080"   # API
      - "9090:9090"   # Metrics
    volumes:
      - ./testdata/geopackages:/data/gpkg:ro
      - cert-cache:/var/cache/ortels/certs
    environment:
      - ORTELS_LOG_LEVEL=debug
      - ORTELS_LOG_QUERIES=true
      - ORTELS_RATE_LIMIT=100
    healthcheck:
      test: ["CMD", "wget", "--spider", "-q", "http://localhost:8080/health/live"]
      interval: 30s
      timeout: 10s
      retries: 3
      start_period: 60s
    restart: unless-stopped

  # Optional: MinIO für AWS S3-Entwicklung
  minio:
    image: minio/minio:latest
    command: server /data --console-address ":9001"
    ports:
      - "9000:9000"   # AWS S3-API
      - "9001:9001"   # Console
    volumes:
      - minio-data:/data
    environment:
      - MINIO_ROOT_USER=minioadmin
      - MINIO_ROOT_PASSWORD=minioadmin
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:9000/minio/health/live"]
      interval: 30s
      timeout: 20s
      retries: 3

  # Optional: Prometheus
  prometheus:
    image: prom/prometheus:latest
    ports:
      - "9091:9090"
    volumes:
      - ./deployments/prometheus/prometheus.yml:/etc/prometheus/prometheus.yml:ro
    command:
      - "--config.file=/etc/prometheus/prometheus.yml"

volumes:
  cert-cache:
  minio-data:
```

---

## 13. SQL-Queries

### 13.1 GeoPackage-Initialisierung

```sql
-- Layer aus gpkg_contents auslesen
SELECT
    table_name,
    data_type,
    identifier,
    description,
    srs_id
FROM gpkg_contents
WHERE data_type = 'features';

-- Geometrie-Spalte ermitteln
SELECT
    column_name,
    geometry_type_name,
    srs_id
FROM gpkg_geometry_columns
WHERE table_name = ?;

-- Spatial-Index prüfen
SELECT COUNT(*)
FROM sqlite_master
WHERE type = 'table'
  AND name = 'rtree_' || ? || '_' || ?;

-- Spatial-Index erstellen
SELECT CreateSpatialIndex(?, ?);
-- Beispiel: SELECT CreateSpatialIndex('gemeinden', 'geom');

-- Feature-Count ermitteln
SELECT COUNT(*) FROM ?;

-- Extent ermitteln
SELECT
    MIN(MbrMinX(geom)) as min_x,
    MIN(MbrMinY(geom)) as min_y,
    MAX(MbrMaxX(geom)) as max_x,
    MAX(MbrMaxY(geom)) as max_y
FROM ?;
```

### 13.2 Metadaten auslesen

```sql
-- Metadaten aus gpkg_metadata und gpkg_metadata_reference
SELECT
    m.id,
    m.md_scope,
    m.md_standard_uri,
    m.mime_type,
    m.metadata
FROM gpkg_metadata m
JOIN gpkg_metadata_reference r ON m.id = r.md_file_id
WHERE r.table_name IS NULL OR r.table_name = ?;

-- Extension-Informationen
SELECT
    extension_name,
    definition,
    scope
FROM gpkg_extensions
WHERE table_name IS NULL OR table_name = ?;
```

### 13.3 Punktabfragen

```sql
-- Punktabfrage OHNE Koordinatentransformation (SRID stimmt überein)
SELECT
    fid,
    *  -- oder spezifische Spalten
FROM ?  -- Layer-Name
WHERE ST_Contains(geom, GeomFromText('POINT(? ?)', ?));
-- Beispiel: WHERE ST_Contains(geom, GeomFromText('POINT(11.576124 48.137154)', 4326))

-- Punktabfrage MIT Koordinatentransformation
SELECT
    fid,
    *
FROM ?  -- Layer-Name
WHERE ST_Contains(
    geom,
    Transform(
        GeomFromText('POINT(? ?)', ?),  -- Quell-Koordinate mit Quell-SRID
        ?  -- Ziel-SRID (SRID des Layers)
    )
);
-- Beispiel: Transform(GeomFromText('POINT(11.576124 48.137154)', 4326), 25832)

-- Punktabfrage mit R-Tree-Index-Hint
SELECT
    fid,
    *
FROM ? t
WHERE t.ROWID IN (
    SELECT id FROM rtree_?_geom
    WHERE minx <= ? AND maxx >= ?
      AND miny <= ? AND maxy >= ?
)
AND ST_Contains(t.geom, GeomFromText('POINT(? ?)', ?));
```

### 13.4 SRID-Ermittlung

```sql
-- SRID eines Layers ermitteln
SELECT srs_id
FROM gpkg_geometry_columns
WHERE table_name = ?;

-- SRID-Definition abrufen
SELECT
    srs_name,
    srs_id,
    organization,
    organization_coordsys_id,
    definition
FROM gpkg_spatial_ref_sys
WHERE srs_id = ?;
```

---

## 14. Implementierungs-Roadmap

### Phase 1: Foundation

| Priorität | Package | Aufgabe | Abhängigkeiten |
|-----------|---------|---------|----------------|
| 1.1 | `internal/config` | Config-Struct und Viper-Defaults | - |
| 1.2 | `internal/config` | Viper-Config-Loader (CLI, Env, File) | 1.1 |
| 1.3 | `cmd/ortels` | Cobra-CLI-Setup | 1.2 |
| 1.4 | `internal/infrastructure/logging` | Logging-Setup mit slog | 1.1 |
| 1.5 | `internal/domain` | Domain-Modelle (Coordinate, GeoPackage, Feature) | - |
| 1.6 | `internal/ports` | Port-Interfaces definieren | 1.5 |

**Deliverable:** Lauffähiges Grundgerüst mit Konfiguration und Logging

### Phase 2: GeoPackage Core

| Priorität | Package | Aufgabe | Abhängigkeiten |
|-----------|---------|---------|----------------|
| 2.1 | `internal/adapters/secondary/spatialite` | SpatiaLite-Repository | 1.5, 1.6 |
| 2.2 | `internal/adapters/secondary/spatialite` | Layer-Erkennung (gpkg_contents) | 2.1 |
| 2.3 | `internal/adapters/secondary/spatialite` | Spatial-Index-Prüfung/Erstellung | 2.1 |
| 2.4 | `internal/adapters/secondary/spatialite` | Punktabfrage mit ST_Contains | 2.1 |
| 2.5 | `internal/adapters/secondary/spatialite` | Koordinatentransformation | 2.4 |
| 2.6 | `internal/adapters/secondary/spatialite` | Metadaten-Auslesen | 2.1 |

**Deliverable:** Funktionierende GeoPackage-Abfragen mit Indexierung

### Phase 3: Application Services

| Priorität | Package | Aufgabe | Abhängigkeiten |
|-----------|---------|---------|----------------|
| 3.1 | `internal/application/registry` | GeoPackage-Registry-Service | 2.1, 1.6 |
| 3.2 | `internal/application/geopackage` | GeoPackage-Loader-Service | 3.1, 2.2, 2.3 |
| 3.3 | `internal/application/query` | Query-Service | 3.1, 2.4, 2.5 |
| 3.4 | `internal/application` | Service-Integration und Tests | 3.1-3.3 |

**Deliverable:** Vollständige Business-Logik-Schicht

### Phase 4: HTTP-API

| Priorität | Package | Aufgabe | Abhängigkeiten |
|-----------|---------|---------|----------------|
| 4.1 | `internal/adapters/primary/http` | HTTP-Server-Setup (gorilla/mux) | 1.1-1.4 |
| 4.2 | `internal/adapters/primary/http/handlers` | Query-Handler | 3.3 |
| 4.3 | `internal/adapters/primary/http/handlers` | Package-Info-Handler | 3.1 |
| 4.4 | `internal/adapters/primary/http/handlers` | Health-Handler | 3.1, 3.2 |
| 4.5 | `internal/adapters/primary/http/middleware` | Logging-Middleware | 1.4 |
| 4.6 | `internal/adapters/primary/http/middleware` | Rate-Limit-Middleware | 1.1 |
| 4.7 | `api/openapi` | OpenAPI-Spezifikation | 4.2-4.4 |

**Deliverable:** Vollständige REST-API

### Phase 5: Object Storage

| Priorität | Package | Aufgabe | Abhängigkeiten |
|-----------|---------|---------|----------------|
| 5.1 | `internal/adapters/secondary/storage` | Storage-Port-Implementation (AWS S3) | 1.6 |
| 5.2 | `internal/adapters/secondary/storage` | Azure-Blob-Storage-Adapter | 1.6 |
| 5.3 | `internal/adapters/secondary/storage` | HTTP-Download-Adapter (index.txt) | 1.6 |
| 5.4 | `internal/adapters/secondary/storage` | Local-Storage-Adapter | 1.6 |
| 5.5 | `internal/application/geopackage` | Storage-Integration | 5.1-5.4, 3.2 |

**Deliverable:** GeoPackage-Download aus Object Storage und HTTP

### Phase 6: Hot-Reload & Watching

| Priorität | Package | Aufgabe | Abhängigkeiten |
|-----------|---------|---------|----------------|
| 6.1 | `internal/adapters/secondary/watcher` | fsnotify-Adapter | 1.6 |
| 6.2 | `internal/application/geopackage` | Hot-Reload-Integration | 6.1, 3.2 |
| 6.3 | `internal/application/registry` | Thread-safe-Registry-Updates | 6.2, 3.1 |

**Deliverable:** Automatisches Erkennen von GeoPackage-Änderungen

### Phase 7: TLS & Security

| Priorität | Package | Aufgabe | Abhängigkeiten |
|-----------|---------|---------|----------------|
| 7.1 | `internal/infrastructure/tls` | TLS-Manager | 1.1 |
| 7.2 | `internal/infrastructure/tls` | CertMagic-Integration (Let's Encrypt) | 7.1 |
| 7.3 | `internal/adapters/primary/http` | TLS-Server-Konfiguration | 7.1, 7.2, 4.1 |

**Deliverable:** HTTPS mit optionalem Let's Encrypt via CertMagic

### Phase 8: Observability

| Priorität | Package | Aufgabe | Abhängigkeiten |
|-----------|---------|---------|----------------|
| 8.1 | `internal/infrastructure/metrics` | Prometheus-Metriken definieren | - |
| 8.2 | `internal/infrastructure/metrics` | Metriken in Services integrieren | 8.1, 3.1-3.3 |
| 8.3 | `internal/adapters/primary/http` | Metriken-Endpoint | 8.1, 4.1 |
| 8.4 | `internal/adapters/primary/http/middleware` | Request-Metriken-Middleware | 8.1, 4.1 |

**Deliverable:** Vollständiges Monitoring

### Phase 9: Container & Deployment

| Priorität | Package | Aufgabe | Abhängigkeiten |
|-----------|---------|---------|----------------|
| 9.1 | `deployments/docker` | Multi-Stage-Dockerfile | Alle vorherigen |
| 9.2 | `deployments/docker` | Docker-Compose-Setup | 9.1 |
| 9.3 | `deployments/kubernetes` | Kubernetes-Manifeste | 9.1 |
| 9.4 | `.github/workflows` | CI/CD-Pipeline | 9.1-9.3 |

**Deliverable:** Production-ready-Container und Deployment

### Phase 10: Dokumentation & Polish

| Priorität | Package | Aufgabe | Abhängigkeiten |
|-----------|---------|---------|----------------|
| 10.1 | `doc/` | API-Dokumentation vervollständigen | 4.7 |
| 10.2 | `doc/` | Deployment-Guide | 9.1-9.3 |
| 10.3 | - | Integration-Tests | Alle vorherigen |
| 10.4 | - | Performance-Tests | 10.3 |
| 10.5 | - | Security-Audit | 7.1-7.3 |

**Deliverable:** Produktionsreife Version 1.0

---

## 15. Qualitäts-Metriken

### Code-Qualität

| Metrik | Ziel | Tool |
|--------|------|------|
| Test-Coverage | >= 80% | `go test -cover` |
| Cyclomatic Complexity | <= 15 | golangci-lint |
| Cognitive Complexity | <= 20 | golangci-lint |
| Duplicated Code | < 100 Tokens | golangci-lint (dupl) |
| Documentation Coverage | 100% exportierte Typen | godoc |

### Performance-Ziele

| Metrik | Ziel |
|--------|------|
| Punktabfrage (einzelnes GeoPackage) | < 50ms (P99) |
| Punktabfrage (alle GeoPackages, 10 Pakete) | < 200ms (P99) |
| Startup-Zeit (ohne Index-Erstellung) | < 5s |
| Memory-Footprint (10 GeoPackages, je 100MB) | < 500MB |

### API-Design-Checkliste

- [ ] Alle Endpunkte dokumentiert (OpenAPI)
- [ ] Konsistente Fehlerantworten
- [ ] Pagination wo sinnvoll
- [ ] Rate-Limiting implementiert
- [ ] CORS-Headers konfigurierbar
- [ ] Request-Tracing (X-Request-ID)

---

## 16. ADR-Referenzen

| ADR | Titel | Status |
|-----|-------|--------|
| ADR-0001 | Standard-Go-Project-Layout | Akzeptiert |
| ADR-0002 | Hexagonal-Architecture-Elements | Akzeptiert |
| ADR-0003 | Vincenty als Standard-Algorithmus | Akzeptiert |
| ADR-0004 | Interface-basiertes Codec-System | Akzeptiert |
| ADR-0005 | GeoPackage-basierte Architektur | Akzeptiert |
| ADR-0006 | Object-Storage-Integration | Akzeptiert |
| ADR-0007 | Configuration-Management (Viper/Cobra) | Akzeptiert |
| ADR-0008 | TLS und CertMagic | Akzeptiert |
| ADR-0009 | Hot-Reload und File-Watching | Akzeptiert |
| ADR-0010 | HTTP-Router (gorilla/mux) | Akzeptiert |

---

## Anhang A: Glossar

| Begriff | Beschreibung |
|---------|--------------|
| GeoPackage | SQLite-basiertes Datenbankformat für Geodaten (OGC-Standard) |
| SRID | Spatial Reference ID - eindeutiger Identifier für ein Koordinatensystem |
| WGS84 | World Geodetic System 1984 - globales Koordinatensystem (EPSG:4326) |
| ST_Contains | SpatiaLite-Funktion zur Prüfung ob eine Geometrie eine andere enthält |
| Spatial-Index | R-Tree-Index für effiziente räumliche Abfragen |
| Hot-Reload | Automatisches Neuladen bei Dateiänderungen ohne Neustart |
| Port | Interface-Definition in hexagonaler Architektur |
| Adapter | Konkrete Implementierung eines Ports |
| CertMagic | Go-Bibliothek für automatisches TLS-Zertifikat-Management |
| Viper | Go-Bibliothek für Konfigurationsmanagement |
| Cobra | Go-Bibliothek für CLI-Applikationen |
| gorilla/mux | Go-HTTP-Router und URL-Matcher |

## Anhang B: Externe Referenzen

- [GeoPackage Specification](https://www.geopackage.org/spec/)
- [SpatiaLite Documentation](https://www.gaia-gis.it/fossil/libspatialite/index)
- [OpenAPI Specification](https://spec.openapis.org/oas/v3.1.0)
- [Hexagonal Architecture](https://alistair.cockburn.us/hexagonal-architecture/)
- [Prometheus Metrics](https://prometheus.io/docs/concepts/metric_types/)
- [gorilla/mux](https://github.com/gorilla/mux)
- [spf13/cobra](https://github.com/spf13/cobra)
- [spf13/viper](https://github.com/spf13/viper)
- [CertMagic](https://github.com/caddyserver/certmagic)
