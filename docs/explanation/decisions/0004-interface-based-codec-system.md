# ADR-0004: Interface-basiertes Codec-System

## Status

Vorgeschlagen

## Kontext

Ortus soll verschiedene Geo-Datenformate lesen und schreiben konnen:
- GPX (GPS Exchange Format)
- KML (Keyhole Markup Language)
- GeoJSON
- CSV (mit Koordinaten)

Es wird ein erweiterbares System benotigt, das:
1. Neue Formate einfach hinzufugen lasst
2. Einheitliche Verarbeitung ermoglicht
3. Format-spezifische Details kapselt

## Entscheidung

Wir implementieren ein **Interface-basiertes Codec-System** mit folgender Struktur:

```go
// pkg/format/format.go
package format

import "io"

// Track reprasentiert eine Sequenz von Geo-Punkten
type Track struct {
    Name     string
    Points   []geo.Point
    Metadata map[string]string
}

// Reader liest Geo-Daten aus einem Stream
type Reader interface {
    Read(r io.Reader) ([]Track, error)
}

// Writer schreibt Geo-Daten in einen Stream
type Writer interface {
    Write(w io.Writer, tracks []Track) error
}

// Codec kombiniert Reader und Writer
type Codec interface {
    Reader
    Writer
}
```

### Format-Implementierungen

```
pkg/format/
|-- format.go        # Interfaces und Track-Typ
|-- gpx/
|   |-- gpx.go       # GPX Codec Implementation
|   |-- reader.go    # GPX-spezifisches Parsing
|   +-- writer.go    # GPX-spezifisches Schreiben
|-- geojson/
|   +-- geojson.go   # GeoJSON Codec
|-- kml/
|   +-- kml.go       # KML Codec
+-- csv/
    +-- csv.go       # CSV Codec
```

### Registry-Pattern fur Format-Erkennung

```go
// internal/repository/registry.go
type FormatRegistry struct {
    codecs map[string]format.Codec // Extension -> Codec
}

func NewFormatRegistry() *FormatRegistry {
    r := &FormatRegistry{codecs: make(map[string]format.Codec)}

    // Standard-Formate registrieren
    r.Register(".gpx", gpx.NewCodec())
    r.Register(".geojson", geojson.NewCodec())
    r.Register(".kml", kml.NewCodec())
    r.Register(".csv", csv.NewCodec())

    return r
}

func (r *FormatRegistry) Register(ext string, codec format.Codec) {
    r.codecs[ext] = codec
}

func (r *FormatRegistry) Get(ext string) (format.Codec, bool) {
    codec, ok := r.codecs[ext]
    return codec, ok
}
```

## Konsequenzen

### Positiv

- **Erweiterbarkeit**: Neue Formate ohne Anderung bestehenden Codes
- **Testbarkeit**: Jeder Codec einzeln testbar
- **Einheitlichkeit**: Alle Formate uber gleiches Interface
- **Separation of Concerns**: Format-Details gekapselt

### Negativ

- **Informationsverlust**: Nicht alle Format-Features in Track abbildbar
- **Overhead**: Interface-Indirektion
- **Komplexitat**: Mehr Dateien und Packages

### Mitigationen

- `Metadata map[string]string` fur format-spezifische Daten
- Options-Pattern fur format-spezifische Konfiguration:

```go
type GPXCodec struct {
    Version       string // "1.0" oder "1.1"
    IncludeTime   bool
    IncludeElevation bool
}

func NewGPXCodec(opts ...GPXOption) *GPXCodec {
    c := &GPXCodec{Version: "1.1", IncludeTime: true}
    for _, opt := range opts {
        opt(c)
    }
    return c
}
```

## Alternativen

1. **Direkte Format-Funktionen**: `ReadGPX()`, `WriteGPX()` etc. - verworfen wegen mangelnder Erweiterbarkeit
2. **Generics-basiert**: `Codec[T]` mit typ-spezifischen Tracks - verworfen wegen erhohter Komplexitat
3. **Externe Bibliothek (go-gpx etc.)**: Weniger Kontrolle - fur spater evaluieren

## Implementierungshinweise

### Track als Intermediate Representation

Track dient als **kanonische Darstellung** zwischen Formaten:

```
GPX File --> GPX Codec --> Track --> GeoJSON Codec --> GeoJSON File
```

### Fehlerbehandlung

```go
// Format-spezifische Fehler
var (
    ErrInvalidFormat = errors.New("invalid format")
    ErrUnsupportedVersion = errors.New("unsupported format version")
)

// Parsing-Fehler mit Position
type ParseError struct {
    Line    int
    Column  int
    Message string
}
```

## Referenzen

- [GPX Schema](https://www.topografix.com/gpx.asp)
- [GeoJSON Specification](https://geojson.org/)
- [KML Reference](https://developers.google.com/kml/documentation/kmlreference)
- [Go io.Reader/Writer Patterns](https://go.dev/tour/methods/21)
