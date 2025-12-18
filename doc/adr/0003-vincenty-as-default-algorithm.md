# ADR-0003: Vincenty als Standard-Algorithmus

## Status

Vorgeschlagen

## Kontext

Fur die Berechnung von Entfernungen zwischen geographischen Punkten gibt es verschiedene Algorithmen mit unterschiedlichen Trade-offs:

| Algorithmus | Genauigkeit | Performance | Komplexitat |
|-------------|-------------|-------------|-------------|
| Flat Earth | Gering (nur kurze Distanzen) | Sehr hoch | Niedrig |
| Haversine | Mittel (Kugelmodell) | Hoch | Niedrig |
| Vincenty | Hoch (Ellipsoid) | Mittel | Hoch |
| Karney | Sehr hoch | Mittel | Sehr hoch |

Die Anwendung Ortels soll geodatische Berechnungen mit hoher Prazision ermoglichen.

## Entscheidung

Wir verwenden **Vincenty-Formeln** als Standard-Algorithmus fur Distanzberechnungen:

```go
// pkg/geo/vincenty.go
type Vincenty struct {
    Ellipsoid Ellipsoid
}

func (v Vincenty) Distance(from, to Point) float64 {
    // Vincenty inverse formula
    // Iterative Losung fur geodatische Distanz auf Ellipsoid
}

func (v Vincenty) Bearing(from, to Point) (initial, final float64) {
    // Azimuth-Berechnung
}
```

### Begrundung

1. **Genauigkeit**: Sub-Millimeter-Genauigkeit fur alle Distanzen
2. **Standard**: Weit verbreitet in GIS-Anwendungen
3. **WGS84-kompatibel**: Optimal fur GPS-Koordinaten
4. **Dokumentiert**: Gut dokumentiert und verstanden

### Fallback-Strategie

Bei Nicht-Konvergenz (antipodiale Punkte) wird auf Haversine zuruckgefallen:

```go
func (v Vincenty) Distance(from, to Point) float64 {
    dist, err := v.vincentyInverse(from, to)
    if err != nil {
        // Fallback fur Sonderfalle
        return Haversine{Radius: v.Ellipsoid.SemiMajorAxis}.Distance(from, to)
    }
    return dist
}
```

## Konsequenzen

### Positiv

- **Prazision**: Maximale Genauigkeit fur praktische Anwendungen
- **Vertrauen**: Bewahrter Algorithmus in der Geodasie
- **Flexibilitat**: Verschiedene Ellipsoide konfigurierbar

### Negativ

- **Performance**: ~10x langsamer als Haversine
- **Komplexitat**: Iterativer Algorithmus, schwerer zu debuggen
- **Edge Cases**: Behandlung von Sonderfallen erforderlich

### Mitigationen

- Haversine als schnelle Alternative fur unkritische Berechnungen anbieten
- Caching fur wiederholte Berechnungen
- Klare Dokumentation der Genauigkeits-Trade-offs

## Alternativen

1. **Haversine als Default**: Einfacher, aber weniger genau - als Option behalten
2. **Karney (GeographicLib)**: Noch genauer, aber komplexer - fur spater evaluieren
3. **Externe Bibliothek**: Weniger Kontrolle, aber getestet - verworfen fur Lernzwecke

## Referenzen

- [Vincenty, T. (1975): Direct and Inverse Solutions of Geodesics on the Ellipsoid](https://www.ngs.noaa.gov/PUBS_LIB/inverse.pdf)
- [Wikipedia: Vincenty's formulae](https://en.wikipedia.org/wiki/Vincenty%27s_formulae)
- [Movable Type: Calculate distance and bearing between two Latitude/Longitude points](https://www.movable-type.co.uk/scripts/latlong-vincenty.html)
