# ADR 0011: Remote Storage Synchronization

## Status

Akzeptiert

## Kontext

Wenn GeoPackages in Remote-Storage (S3/Azure/HTTP) gespeichert werden, muss Ortus in der Lage sein, neue Packages zu erkennen und zu laden, ohne einen Container-Neustart zu erfordern.

Das Hot-Reload-Feature (ADR-0009) funktioniert nur für lokalen Storage mit Dateisystem-Events. Für Remote-Storage wird ein anderer Mechanismus benötigt.

## Entscheidung

Wir implementieren einen SyncService mit folgenden Eigenschaften:

1. **Periodischer Sync**: Konfigurierbares Intervall (Default: 1h)
2. **Optional**: Kann per Konfiguration aktiviert werden
3. **API-Endpoint**: `POST /api/v1/sync` mit Rate-Limiting (30s Cooldown)
4. **Bidirektional**: Neue Packages werden geladen, gelöschte werden entfernt

### Architektur

```
SyncService
├── Scheduler (Ticker-basiert)
├── Rate-Limiter (Mutex-basiert, 30s Cooldown)
└── Registry.Sync() (delegiert an PackageRegistry)
```

### Komponenten

- **SyncService** (`internal/application/sync_service.go`): Scheduler mit Lifecycle-Management
- **PackageRegistry.Sync()** (`internal/application/registry.go`): Sync-Logik
- **HTTP Handler** (`internal/adapters/http/handlers.go`): API-Endpoint

### Konfiguration

```yaml
sync:
  enabled: false      # Default: deaktiviert
  interval: "1h"      # Standard: 1 Stunde
```

### Rate-Limiting

- API-Requests: Max. 2/Minute (30s Cooldown)
- Implementierung: Einfacher Timestamp-Check mit Mutex
- Keine externen Dependencies nötig

### API

```
POST /api/v1/sync

Response (200 OK):
{
  "packages_added": 2,
  "packages_removed": 1,
  "packages_total": 5,
  "synced_at": "2025-12-22T12:00:00Z",
  "next_scheduled_at": "2025-12-22T13:00:00Z"
}

Response (429 Too Many Requests):
Retry-After: 30
Rate limit exceeded
```

## Alternativen

1. **Webhook/Event-basiert**: Cloud-Provider Events (S3 Notifications, Azure Event Grid)
   - Vorteil: Sofortige Benachrichtigung bei Änderungen
   - Nachteil: Komplexere Infrastruktur, Provider-spezifisch

2. **Polling ohne Rate-Limit**:
   - Vorteil: Einfachere Implementierung
   - Nachteil: DoS-Risiko bei API-Missbrauch

3. **Kein API-Trigger**: Nur periodischer Sync
   - Vorteil: Noch einfacher
   - Nachteil: Weniger flexibel für Benutzer

## Konsequenzen

### Positiv

- Neue GeoPackages werden automatisch erkannt
- Gelöschte Remote-Packages werden automatisch entladen und lokale Cache-Dateien gelöscht
- Kein Container-Neustart erforderlich
- API ermöglicht sofortigen Sync bei Bedarf
- Rate-Limiting verhindert Missbrauch
- Graceful Shutdown gewährleistet

### Negativ

- Zusätzliche Netzwerk-Requests durch Polling
- Latenz zwischen Upload/Löschung und Verfügbarkeit (bis zu Intervall-Zeit)

## Referenzen

- [ADR-0006](0006-object-storage-integration.md) - Object Storage Integration
- [ADR-0009](0009-hot-reload-file-watching.md) - Hot-Reload und File-Watching
