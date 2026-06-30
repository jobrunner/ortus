# ADR-0009: Hot-Reload und File-Watching

## Status

Akzeptiert

## Kontext

GeoPackage-Dateien können zur Laufzeit hinzugefügt, aktualisiert oder entfernt werden. Der Service muss auf diese Änderungen reagieren, ohne neu gestartet werden zu müssen.

### Anforderungen

1. **Automatische Erkennung:** Neue `.gpkg`-Dateien im Verzeichnis erkennen
2. **Entfernung:** Gelöschte GeoPackages aus der Registry entfernen
3. **Update:** Geänderte GeoPackages neu laden (optional)
4. **Thread-Safety:** Sichere gleichzeitige Abfragen während Updates
5. **Performance:** Minimaler Overhead bei unverändertem Zustand

### Evaluierte Optionen

| Option | Vorteile | Nachteile |
|--------|----------|-----------|
| fsnotify | Event-basiert, effizient | OS-abhängig, Event-Limitierungen |
| Polling | Einfach, zuverlässig | CPU/IO-Overhead |
| inotify direkt | Linux-optimiert | Nicht portabel |
| Signal-basiert | Explizite Kontrolle | Manueller Trigger erforderlich |

## Entscheidung

Wir verwenden **fsnotify** für File-System-Events mit **Polling als Fallback**.

### Architektur

```
+-------------------+          +-------------------+          +-------------------+
|   File System     |          |   File Watcher    |          |   Registry        |
|   /data/gpkg/     |          |   (fsnotify)      |          |   Service         |
+-------------------+          +-------------------+          +-------------------+
         |                              |                              |
         | CREATE file.gpkg            |                              |
         +----------------------------->|                              |
         |                              | FileCreated Event            |
         |                              +----------------------------->|
         |                              |                              |
         |                              |                              | Load GeoPackage
         |                              |                              | Create Index
         |                              |                              | Register
         |                              |                              |
         | DELETE file.gpkg            |                              |
         +----------------------------->|                              |
         |                              | FileDeleted Event            |
         |                              +----------------------------->|
         |                              |                              |
         |                              |                              | Unregister
         |                              |                              | Close Handle
```

### FileWatcher Port

```go
// internal/ports/output/watcher.go
type FileWatcherPort interface {
    // Watch startet die Überwachung eines Verzeichnisses
    Watch(ctx context.Context, path string) (<-chan FileEvent, error)

    // Stop beendet die Überwachung
    Stop() error
}

type FileEvent struct {
    Type     FileEventType
    Path     string
    Filename string
}

type FileEventType int

const (
    FileCreated FileEventType = iota
    FileModified
    FileDeleted
)
```

### fsnotify-Adapter

```go
// internal/adapters/secondary/watcher/fsnotify.go
package watcher

import (
    "context"
    "path/filepath"
    "strings"

    "github.com/fsnotify/fsnotify"
)

type FSNotifyWatcher struct {
    watcher *fsnotify.Watcher
    events  chan FileEvent
    done    chan struct{}
}

func NewFSNotifyWatcher() (*FSNotifyWatcher, error) {
    w, err := fsnotify.NewWatcher()
    if err != nil {
        return nil, fmt.Errorf("create watcher: %w", err)
    }

    return &FSNotifyWatcher{
        watcher: w,
        events:  make(chan FileEvent, 100),
        done:    make(chan struct{}),
    }, nil
}

func (w *FSNotifyWatcher) Watch(ctx context.Context, path string) (<-chan FileEvent, error) {
    if err := w.watcher.Add(path); err != nil {
        return nil, fmt.Errorf("add watch: %w", err)
    }

    go w.processEvents(ctx, path)

    return w.events, nil
}

func (w *FSNotifyWatcher) processEvents(ctx context.Context, basePath string) {
    for {
        select {
        case <-ctx.Done():
            return
        case <-w.done:
            return

        case event, ok := <-w.watcher.Events:
            if !ok {
                return
            }

            // Nur .gpkg-Dateien
            if !strings.HasSuffix(event.Name, ".gpkg") {
                continue
            }

            // Temporäre Dateien ignorieren
            filename := filepath.Base(event.Name)
            if strings.HasPrefix(filename, ".") || strings.HasSuffix(filename, ".tmp") {
                continue
            }

            var eventType FileEventType
            switch {
            case event.Op&fsnotify.Create == fsnotify.Create:
                eventType = FileCreated
            case event.Op&fsnotify.Remove == fsnotify.Remove:
                eventType = FileDeleted
            case event.Op&fsnotify.Rename == fsnotify.Rename:
                eventType = FileDeleted // Rename = Delete + Create
            case event.Op&fsnotify.Write == fsnotify.Write:
                eventType = FileModified
            default:
                continue
            }

            w.events <- FileEvent{
                Type:     eventType,
                Path:     event.Name,
                Filename: filename,
            }

        case err, ok := <-w.watcher.Errors:
            if !ok {
                return
            }
            // Log error but continue
            slog.Error("watcher error", "error", err)
        }
    }
}

func (w *FSNotifyWatcher) Stop() error {
    close(w.done)
    return w.watcher.Close()
}
```

### Registry-Integration

```go
// internal/application/registry/service.go
type RegistryService struct {
    packages map[string]*domain.GeoPackage
    mu       sync.RWMutex
    repo     output.GeoPackageRepository
    watcher  output.FileWatcherPort
    logger   *slog.Logger
}

func (s *RegistryService) StartWatching(ctx context.Context, path string) error {
    events, err := s.watcher.Watch(ctx, path)
    if err != nil {
        return err
    }

    go s.handleEvents(ctx, events)
    return nil
}

func (s *RegistryService) handleEvents(ctx context.Context, events <-chan output.FileEvent) {
    for {
        select {
        case <-ctx.Done():
            return

        case event := <-events:
            switch event.Type {
            case output.FileCreated:
                s.handleCreate(ctx, event)
            case output.FileDeleted:
                s.handleDelete(ctx, event)
            case output.FileModified:
                s.handleModified(ctx, event)
            }
        }
    }
}

func (s *RegistryService) handleCreate(ctx context.Context, event output.FileEvent) {
    s.logger.Info("new geopackage detected", "path", event.Path)

    // Warte kurz bis Datei vollständig geschrieben
    time.Sleep(500 * time.Millisecond)

    // Lade GeoPackage
    pkg, err := s.loadPackage(ctx, event.Path)
    if err != nil {
        s.logger.Error("failed to load geopackage",
            "path", event.Path,
            "error", err)
        return
    }

    // Registrieren (thread-safe)
    s.mu.Lock()
    s.packages[pkg.ID] = pkg
    s.mu.Unlock()

    s.logger.Info("geopackage registered",
        "id", pkg.ID,
        "layers", len(pkg.Layers))
}

func (s *RegistryService) handleDelete(ctx context.Context, event output.FileEvent) {
    s.logger.Info("geopackage removed", "path", event.Path)

    // ID aus Pfad ableiten
    id := s.pathToID(event.Path)

    s.mu.Lock()
    defer s.mu.Unlock()

    if pkg, exists := s.packages[id]; exists {
        // Handle schliessen
        s.repo.Close(pkg.Handle)
        delete(s.packages, id)
    }
}

// GetPackage - thread-safe Read
func (s *RegistryService) GetPackage(id string) (*domain.GeoPackage, bool) {
    s.mu.RLock()
    defer s.mu.RUnlock()
    pkg, ok := s.packages[id]
    return pkg, ok
}

// ListPackages - thread-safe Read
func (s *RegistryService) ListPackages() []*domain.GeoPackage {
    s.mu.RLock()
    defer s.mu.RUnlock()

    result := make([]*domain.GeoPackage, 0, len(s.packages))
    for _, pkg := range s.packages {
        result = append(result, pkg)
    }
    return result
}
```

### Debouncing

Um mehrfache Events für dieselbe Datei zu vermeiden (z.B. bei grossen Uploads):

```go
// internal/adapters/secondary/watcher/debouncer.go
type Debouncer struct {
    pending map[string]*time.Timer
    mu      sync.Mutex
    delay   time.Duration
    handler func(FileEvent)
}

func NewDebouncer(delay time.Duration, handler func(FileEvent)) *Debouncer {
    return &Debouncer{
        pending: make(map[string]*time.Timer),
        delay:   delay,
        handler: handler,
    }
}

func (d *Debouncer) Add(event FileEvent) {
    d.mu.Lock()
    defer d.mu.Unlock()

    // Existierenden Timer abbrechen
    if timer, exists := d.pending[event.Path]; exists {
        timer.Stop()
    }

    // Neuen Timer setzen
    d.pending[event.Path] = time.AfterFunc(d.delay, func() {
        d.mu.Lock()
        delete(d.pending, event.Path)
        d.mu.Unlock()

        d.handler(event)
    })
}
```

## Konsequenzen

### Positiv

- **Reaktivität:** Sofortige Reaktion auf Dateiänderungen
- **Effizienz:** Event-basiert statt Polling
- **Thread-Safety:** RWMutex ermöglicht parallele Reads
- **Graceful:** Keine Unterbrechung laufender Abfragen

### Negativ

- **Komplexität:** Event-Handling und Thread-Synchronisation
- **Edge Cases:** Unvollständige Uploads, temporäre Dateien
- **OS-Abhängigkeit:** fsnotify-Verhalten variiert

### Mitigationen

- Debouncing verhindert mehrfache Events
- Delay vor Load wartet auf vollständigen Upload
- Filter für temporäre Dateien (.tmp, Punkt-Prefix)
- Logging aller Watcher-Events für Debugging

## Polling-Fallback

Für Umgebungen wo fsnotify nicht funktioniert:

```go
// internal/adapters/secondary/watcher/polling.go
type PollingWatcher struct {
    interval time.Duration
    state    map[string]time.Time // Path -> ModTime
    events   chan FileEvent
    done     chan struct{}
}

func (w *PollingWatcher) Watch(ctx context.Context, path string) (<-chan FileEvent, error) {
    go func() {
        ticker := time.NewTicker(w.interval)
        defer ticker.Stop()

        for {
            select {
            case <-ctx.Done():
                return
            case <-w.done:
                return
            case <-ticker.C:
                w.scan(path)
            }
        }
    }()

    return w.events, nil
}

func (w *PollingWatcher) scan(path string) {
    entries, _ := os.ReadDir(path)

    currentFiles := make(map[string]struct{})

    for _, entry := range entries {
        if !strings.HasSuffix(entry.Name(), ".gpkg") {
            continue
        }

        fullPath := filepath.Join(path, entry.Name())
        currentFiles[fullPath] = struct{}{}

        info, _ := entry.Info()
        modTime := info.ModTime()

        if lastMod, exists := w.state[fullPath]; !exists {
            // Neue Datei
            w.events <- FileEvent{Type: FileCreated, Path: fullPath}
            w.state[fullPath] = modTime
        } else if modTime.After(lastMod) {
            // Geänderte Datei
            w.events <- FileEvent{Type: FileModified, Path: fullPath}
            w.state[fullPath] = modTime
        }
    }

    // Gelöschte Dateien
    for path := range w.state {
        if _, exists := currentFiles[path]; !exists {
            w.events <- FileEvent{Type: FileDeleted, Path: path}
            delete(w.state, path)
        }
    }
}
```

## Konfiguration

```yaml
geopackage:
  directory: "/data/gpkg"
  watchInterval: 10s    # Für Polling-Fallback
  debounceDelay: 500ms  # Wartezeit nach Events
```

## Referenzen

- [fsnotify](https://github.com/fsnotify/fsnotify)
- [Linux inotify](https://man7.org/linux/man-pages/man7/inotify.7.html)
- [Go sync.RWMutex](https://pkg.go.dev/sync#RWMutex)
