# ADR-0001: Standard Go Project Layout

## Status

Akzeptiert

## Kontext

Das Projekt Ortus befindet sich in der Anfangsphase. Es muss eine Verzeichnisstruktur gewahlt werden, die:

1. Go Best Practices entspricht
2. Skalierbar fur zukunftige Erweiterungen ist
3. Klare Trennung zwischen offentlicher API und privater Implementierung bietet
4. Von anderen Go-Entwicklern leicht verstanden wird

## Entscheidung

Wir verwenden das Standard Go Project Layout basierend auf [golang-standards/project-layout](https://github.com/golang-standards/project-layout) mit folgender Struktur:

```
ortus/
|-- cmd/ortus/      # Entry Point (main.go)
|-- internal/        # Private Packages
|-- pkg/             # Offentliche/exportierbare Packages
|-- doc/             # Dokumentation
|-- testdata/        # Test-Fixtures
```

### Abgrenzung pkg/ vs internal/

**pkg/** enthalt:
- Wiederverwendbare Bibliotheken ohne Anwendungskontext
- Stabile APIs, die von externen Projekten importiert werden konnen
- Beispiele: `geo/` (Geo-Berechnungen), `format/` (Datenformate)

**internal/** enthalt:
- Anwendungsspezifische Logik
- Code, der nicht von aussen importiert werden soll
- Beispiele: `config/`, `handler/`, `service/`, `domain/`, `repository/`

## Konsequenzen

### Positiv

- **Klarheit**: Entwickler wissen sofort, wo welcher Code hingehort
- **Kapselung**: Go-Compiler verhindert Import von `internal/` von aussen
- **Wiederverwendbarkeit**: `pkg/` kann in anderen Projekten genutzt werden
- **Konvention**: Bekanntes Layout erleichtert Onboarding

### Negativ

- **Overhead**: Fur sehr kleine Projekte moglicherweise uberdimensioniert
- **Rigiditat**: Struktur sollte fruht festgelegt und eingehalten werden

### Neutral

- Erfordert Disziplin bei der Einordnung neuer Packages

## Alternativen

1. **Flat Layout**: Alle Go-Dateien im Root - verworfen wegen mangelnder Skalierbarkeit
2. **Domain-Driven Layout**: Strukturierung nach Business-Domains - verworfen wegen erhohter Komplexitat fur diesen Use Case
3. **Hexagonal pur**: Ports/Adapters-Verzeichnisse - teilweise ubernommen, aber nicht strikt

## Referenzen

- [golang-standards/project-layout](https://github.com/golang-standards/project-layout)
- [Go Blog: Organizing Go Code](https://go.dev/blog/organizing-go-code)
- [Effective Go](https://go.dev/doc/effective_go)
