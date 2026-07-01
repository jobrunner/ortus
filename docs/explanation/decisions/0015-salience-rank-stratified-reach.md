# ADR-0015: Salience — rang-stratifizierte Reichweiten-Radien (branch-frei)

## Status

Akzeptiert

## Kontext

Für die Peilung muss aus den nahen Orten der **beste Anker** gewählt werden. Die
ursprüngliche Spec schlug einen **kontinuierlichen Score** vor
(`score = w_pop·log(pop+1) − w_dist·distance`), der Nähe gegen Prominenz abwägt.

Die empirische Prüfung des realen `osm-admin-places.gpkg` ergab jedoch:
**keine Einwohnerzahl**, nur eine **3-Klassen-Rang-Spalte** (`village`/`town`/`city`),
davon ~95 % `village` ([ADR-0017](0017-prominence-source-rank.md)). Ein kontinuierlicher
population-gewichteter Score hat auf diesen Daten nichts, woran er sich verankern könnte;
eine reine Nächster-Nachbar-Wahl liefert unbrauchbare Anker („0,8 km N {Weiler}").

## Entscheidung

**Rang-stratifizierte Auswahl über klassen-spezifische Reichweiten-Radien**, formuliert als
**branch-freie Regel**, nicht als `if city … else if town …`-Kaskade:

- `BearingPolicy.Reach` ist eine **Datentabelle** `PlaceClass → Radius` (Default 5/18/60 km).
- Ein Kandidat ist *eligible*, wenn `distance ≤ Reach[class]`.
- Unter den eligiblen gewinnt die **salienteste Klasse**; Gleichstand → näher; dann Name
  (deterministisch).

Damit reicht eine Stadt weit als Anker, ein Dorf nur aus der Nähe. Eine vierte Klasse
hinzuzufügen ist **eine Tabellenzeile**, kein Code-Pfad. Die Strategie liegt hinter dem
Interface `SalienceStrategy`; `RankedSalience` ist der Default. Eine population-gewichtete
Variante bleibt als alternative Strategie reserviert (für einen künftigen GeoNames-Merge,
[ADR-0017](0017-prominence-source-rank.md)).

## Konsequenzen

**Positiv**
- Interpretierbar und tunbar: die Radien sind verständliche Knöpfe, kein opakes `w_dist`.
- Branch-frei, DRY, offen für Erweiterung (neue Klasse = Datenzeile).
- Rein (keine I/O) → trivial unit-testbar.

**Negativ / Risiken**
- Die Default-Radien sind fundierte Schätzwerte; die echte Kalibrierung gegen ein Feld-Gold-Set
  steht noch aus (M5, blockiert bis das neu gebaute GeoPackage vorliegt).
- Bewusster Trade-off: eine Stadt bei 55 km schlägt ein Dorf bei 2 km. Wer das enger will,
  senkt `R_city` — wieder ein Knopf, keine Verzweigung.
