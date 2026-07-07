# Sync sources from remote storage

With a remote backend (S3/Azure/HTTP), ortus can periodically check for new
sources and download/load them — useful when sources are added after the
container has started.

## Enable periodic sync

```yaml
sync:
  enabled: true   # periodic sync
  interval: "1h"  # e.g. "30m", "1h", "24h"
```

Or via env:

```bash
ORTUS_SYNC_ENABLED=true
ORTUS_SYNC_INTERVAL=1h
```

## Trigger a sync manually

```bash
curl -X POST "http://localhost:8080/api/v1/sync"
```

```json
{ "sources_added": 2, "sources_removed": 1, "sources_total": 5,
  "synced_at": "2025-12-22T12:00:00Z", "next_scheduled_at": "2025-12-22T13:00:00Z" }
```

Sync adds new sources and removes ones that no longer exist remotely. The
endpoint is rate-limited to one trigger per 30 seconds (`429` + `Retry-After: 30`
within the cooldown).

> Sync is for remote backends only. For local storage, hot-reload detects file
> changes automatically.
