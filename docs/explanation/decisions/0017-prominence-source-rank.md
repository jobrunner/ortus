# ADR-0017: Prominenz-Quelle — OSM-Rang statt GeoNames-Population

## Status

Akzeptiert (durch die Daten entschieden)

## Kontext

Die Salience ([ADR-0015](0015-salience-rank-stratified-reach.md)) braucht ein Maß für die
Prominenz eines Ortes. Zwei Quellen kamen infrage:

1. **GeoNames-Einwohnerzahl** — kontinuierlich, erlaubt feinkörniges Ranking (auch innerhalb
   einer Klasse).
2. **OSM-`place`-Rang** — die grobe 3-Klassen-Einteilung `village`/`town`/`city`.

Die empirische Prüfung des realen `osm-admin-places.gpkg` (422.557 Orte) ergab: **keine
Population-Spalte**, ausschließlich die 3-Klassen-Rang-Spalte; ~95 % `village`, wenige `town`,
sehr wenige `city`.

## Entscheidung

**OSM-Rang-basiert.** Da die Daten keine Population enthalten, ist der `place`-Rang die einzige
verfügbare Prominenz. Die Salience ist rang-stratifiziert
([ADR-0015](0015-salience-rank-stratified-reach.md)).

GeoNames ist **nicht** definitiv nötig — die Peilung funktioniert mit dem 3-Klassen-Rang gut.
Es bleibt ein **optionales Qualitäts-Upgrade** für die Zukunft: ortus hält dafür eine
**optionale** `population_column`-Naht im Manifest bereit; ist sie vorhanden, kann eine
population-gewichtete Salience-Strategie greifen, sonst rang-basiert. So kann GeoNames später
ins GeoPackage eingebaut werden, ohne dass ortus sich ändern muss.

## Konsequenzen

**Positiv**
- Kein Blocker: das Feature ist mit den vorhandenen Daten sofort umsetzbar.
- Innerhalb einer Klasse entscheidet Nähe — sauber und nachvollziehbar.

**Negativ / Risiken**
- Feinere Unterscheidung innerhalb einer Klasse (zwei etwa gleich weit entfernte Städte) ist
  nicht möglich; „nächste gewinnt". Akzeptabel bei nur 3 Klassen.
- Die population-gewichtete Strategie bleibt implementiert/reserviert, ist auf diesem Dataset
  aber ungenutzt, bis GeoNames gemergt wird.
