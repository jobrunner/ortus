# Entwicklungsdokumentation

## Voraussetzungen

- [Nix](https://nixos.org/download.html) mit Flakes aktiviert
- [direnv](https://direnv.net/) (empfohlen)

## Schnellstart

```bash
# Repository klonen
git clone https://github.com/jobrunner/ortels.git
cd ortels

# direnv erlauben (aktiviert automatisch die Nix-Umgebung)
direnv allow

# Alternativ ohne direnv:
nix develop

# Build und Test
make check
```

## Projektstruktur

```
ortels/
├── cmd/
│   └── ortels/         # Hauptanwendung Entry-Point
│       └── main.go
├── internal/           # Private Packages (nicht exportiert)
├── pkg/                # Öffentliche Packages (exportiert)
├── doc/                # Dokumentation
├── testdata/           # Testdaten
├── .github/            # GitHub Actions (CI/CD)
├── flake.nix           # Nix Flake Definition
├── flake.lock          # Nix Flake Lock
├── go.mod              # Go Module Definition
├── go.sum              # Go Module Checksums
├── Makefile            # Build-Automatisierung
├── .golangci.yml       # Linter-Konfiguration
├── .claude/            # Claude Code Konfiguration
│   └── settings.json   # Claude Code Hooks
└── .envrc              # direnv Konfiguration
```

## Make-Targets

| Target | Beschreibung |
|--------|--------------|
| `make build` | Baue die Anwendung |
| `make test` | Führe alle Tests aus |
| `make lint` | Führe Linter aus |
| `make check` | Alle Qualitätsprüfungen (vor Commit) |
| `make test-coverage` | Tests mit Coverage-Report |
| `make security-check` | Security-Analyse |
| `make clean` | Räume Build-Artefakte auf |
| `make help` | Zeige alle verfügbaren Targets |

## Qualitätsstandards

### Automatische Prüfungen via Claude Code Hooks

Claude Code führt automatisch folgende Prüfungen durch:

1. **PostToolUse Hook**: Nach jeder Dateiänderung werden Go-Dateien formatiert und gelintet
2. **Formatierung**: `gofmt`, `goimports`
3. **Linting**: `golangci-lint` mit umfangreicher Konfiguration
4. **Security**: `gosec`, `govulncheck` (via `make security-check`)

### Code-Style

- Befolge [Effective Go](https://go.dev/doc/effective_go)
- Befolge [Go Code Review Comments](https://github.com/golang/go/wiki/CodeReviewComments)
- Maximale zyklomatische Komplexität: 15
- Maximale kognitive Komplexität: 20

### Commit Messages

Wir verwenden [Conventional Commits](https://www.conventionalcommits.org/):

```
<type>(<scope>): <description>

[optional body]

[optional footer(s)]
```

Typen:
- `feat`: Neues Feature
- `fix`: Bugfix
- `docs`: Dokumentation
- `style`: Formatierung (kein Code-Change)
- `refactor`: Refactoring
- `test`: Tests
- `chore`: Wartung

## Testing

### Unit-Tests

```bash
# Alle Tests
make test

# Mit Coverage
make test-coverage

# Nur kurze Tests
make test-unit

# Mit Race Detector
make test-race
```

### Testdatei-Konventionen

- Test-Dateien: `*_test.go`
- Test-Funktionen: `Test<FunctionName>(t *testing.T)`
- Benchmark-Funktionen: `Benchmark<Name>(b *testing.B)`
- Example-Funktionen: `Example<Name>()`

### Table-Driven Tests

```go
func TestAdd(t *testing.T) {
    tests := []struct {
        name     string
        a, b     int
        expected int
    }{
        {"positive numbers", 1, 2, 3},
        {"negative numbers", -1, -2, -3},
        {"zero", 0, 0, 0},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            result := Add(tt.a, tt.b)
            if result != tt.expected {
                t.Errorf("Add(%d, %d) = %d; want %d",
                    tt.a, tt.b, result, tt.expected)
            }
        })
    }
}
```

## Dependencies

### Hinzufügen

```bash
go get github.com/example/package
go mod tidy
```

### Aktualisieren

```bash
make deps-update
```

### Verifizieren

```bash
make deps-verify
```

## Security

### Vulnerability Scanning

```bash
# Prüfe auf bekannte Vulnerabilities
make vuln-check
```

### Secrets in Code

- Niemals Secrets in den Code committen
- Verwende Umgebungsvariablen

## Release

### Lokaler Build

```bash
# Für aktuelles System
make build

# Für alle Plattformen
make build-all
```

### Release mit GoReleaser

```bash
# Dry-Run
make release-dry

# Tatsächliches Release (erfordert Tag)
git tag v1.0.0
make release
```

## Troubleshooting

### Nix-Umgebung wird nicht aktiviert

```bash
# Stelle sicher, dass Flakes aktiviert sind
echo "experimental-features = nix-command flakes" >> ~/.config/nix/nix.conf

# Neu laden
direnv reload
```

### Go-Cache Probleme

```bash
# Cache leeren
go clean -cache -modcache
rm -rf .go/

# Nix-Shell neu starten
exit
nix develop
```
