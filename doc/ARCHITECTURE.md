# Architektur

## Übersicht

ortels folgt dem Standard-Layout für Go-Projekte gemäß [golang-standards/project-layout](https://github.com/golang-standards/project-layout).

## Verzeichnisstruktur

### `/cmd`

Hauptanwendungen für dieses Projekt. Der Verzeichnisname sollte dem Namen der gewünschten ausführbaren Datei entsprechen.

```
cmd/
└── ortels/
    └── main.go     # Entry-Point, minimaler Code
```

**Regeln:**
- Minimaler Code in main.go
- Nur Konfiguration und Dependency Injection
- Eigentliche Logik in `/internal` oder `/pkg`

### `/internal`

Private Anwendungs- und Bibliothekscode. Code, den andere nicht importieren sollen.

```
internal/
├── config/         # Konfigurationsmanagement
├── domain/         # Domain-Modelle
├── service/        # Business-Logik
├── repository/     # Datenzugriff
└── handler/        # HTTP/CLI Handler
```

**Regeln:**
- Go-Compiler verhindert Import von außen
- Interne APIs können ohne Rücksicht auf externe Nutzer geändert werden

### `/pkg`

Bibliothekscode, der von externen Anwendungen verwendet werden kann.

```
pkg/
├── geo/            # Geographische Berechnungen
└── format/         # Ausgabeformate
```

**Regeln:**
- Stabile API
- Gute Dokumentation
- Vorsicht beim Export

## Design-Patterns

### Dependency Injection

Wir verwenden Constructor Injection:

```go
type Service struct {
    repo Repository
    log  *slog.Logger
}

func NewService(repo Repository, log *slog.Logger) *Service {
    return &Service{
        repo: repo,
        log:  log,
    }
}
```

### Repository Pattern

Trennung von Datenzugriff und Business-Logik:

```go
type Repository interface {
    Get(ctx context.Context, id string) (*Entity, error)
    Save(ctx context.Context, entity *Entity) error
    Delete(ctx context.Context, id string) error
}
```

### Error Handling

Errors werden mit Kontext angereichert:

```go
import "fmt"

func (s *Service) DoSomething(ctx context.Context, id string) error {
    entity, err := s.repo.Get(ctx, id)
    if err != nil {
        return fmt.Errorf("get entity %s: %w", id, err)
    }
    // ...
}
```

### Context Propagation

Context wird immer als erster Parameter übergeben:

```go
func DoWork(ctx context.Context, input Input) (Output, error) {
    // Prüfe ob Context abgebrochen
    select {
    case <-ctx.Done():
        return Output{}, ctx.Err()
    default:
    }

    // Arbeit ausführen...
}
```

## Logging

Wir verwenden `log/slog` (Go 1.21+):

```go
import "log/slog"

func main() {
    logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
    slog.SetDefault(logger)

    slog.Info("starting application",
        "version", Version,
        "build_time", BuildTime,
    )
}
```

## Testing-Architektur

### Unit Tests

- Testen einzelne Funktionen/Methoden
- Mocking für Dependencies
- Schnell und isoliert

### Integration Tests

- Testen Zusammenspiel mehrerer Komponenten
- Können externe Systeme nutzen (z.B. Test-DB)
- Mit `-short` überspringbar

```go
func TestIntegration(t *testing.T) {
    if testing.Short() {
        t.Skip("skipping integration test")
    }
    // ...
}
```

### Test Fixtures

Testdaten in `/testdata`:

```go
func TestWithFixture(t *testing.T) {
    data, err := os.ReadFile("testdata/sample.json")
    if err != nil {
        t.Fatal(err)
    }
    // ...
}
```

## Konfiguration

Konfiguration durch Umgebungsvariablen und Flags:

```go
type Config struct {
    Port     int    `env:"PORT" envDefault:"8080"`
    LogLevel string `env:"LOG_LEVEL" envDefault:"info"`
    DBPath   string `env:"DB_PATH" envDefault:"ortels.db"`
}
```

Priorität:
1. Command-Line Flags (höchste)
2. Umgebungsvariablen
3. Konfigurationsdatei
4. Defaults (niedrigste)
