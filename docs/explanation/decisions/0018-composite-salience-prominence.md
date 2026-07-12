# ADR-0018: Composite-Salience — prominenz-gewichtete Anker-Auswahl

## Status

Akzeptiert. Löst die rang-only-Schlussfolgerung von
[ADR-0017](0017-prominence-source-rank.md) ab und ergänzt
[ADR-0015](0015-salience-rank-stratified-reach.md).

## Kontext

Die rang-stratifizierte Salience ([ADR-0015](0015-salience-rank-stratified-reach.md))
wählte den Peilungs-Anker aus der groben 3-Klassen-Rang-Spalte (`village`/`town`/`city`)
plus Distanz, weil der Datensatz keine Population hatte ([ADR-0017](0017-prominence-source-rank.md)).
Ergebnis in der Praxis: die Peilung nennt oft einen *nahen, aber unbekannten* Ort
(„2 km N Kleinkleckersdorf") statt eines *bekannten, aussagekräftigen* Ankers
(„28 km NW München"). `place=town` beschreibt einen 800-Einwohner-Ort wie einen mit 80.000.

Messung auf den echten OSM-Place-Nodes zeigte, dass OSM sehr wohl brauchbare Prominenz-
Signale trägt — nur wurden sie beim Base-Build am ogr2ogr-`SELECT` verworfen: `population`
(city 99 %, town 87 %, village 54 % in Europa), `wikidata` (city/town ~90–99 %), `capital`
(Admin-Rang des Sitzes). Diese wurden reaktiviert (`make enrich-places`, Join über `osm_id`)
und der dünne MENA-Dorf-Tail per GeoNames (CC BY 4.0) nachgefüllt.

## Entscheidung

**`CompositeSalience` als Default**, hinter dem bestehenden `SalienceStrategy`-Seam; die
rang-basierte `RankedSalience` bleibt als `salience: rank` wählbar.

Score (log10-Einheiten):

```
score = pop_weight·log10(1+population)        (oder Klassen-Prior, wenn population unbekannt)
      + capital_scale·capitalBonus(capital)   (Sitz einer breiteren Admin-Einheit → prominenter)
      + wiki_weight·[wikidata vorhanden]
      − decay_per_km·distance_km
```

Kalibrierte „ausgewogene" Defaults: `pop_weight 1.0`, `wiki_weight 0.3`, `decay_per_km 0.04`,
`capital_scale 0.8`, `candidate_radius_km 120`. `decay_per_km = 0.04` bedeutet: ~33 km
Mehrdistanz wiegen eine 10× kleinere Population auf → eine Großstadt bleibt bis ~80 km
wählbar, eine mittlere Stadt bis ~30 km, ein Dorf nur wenige km. Kein Proximity-Override
(anders als `RankedSalience`) — der Score entscheidet.

**Harte Constraints unverändert bzw. geschärft:** Ein Anker muss (1) im **selben Land** wie
der Query-Punkt liegen und (2) im selben state-Äquivalent, wo eines existiert
([ADR-0016](0016-bearing-convention-compass.md)). Der Länder-Guard wurde explizit gemacht:
das Land des Punkts kommt vom **lokalsten** enthaltenden Admin-Polygon (höchstes
`admin_level`) — deterministisch und robust gegen umstrittene Gebiete, wo das
`admin_level`-2-Outline ein falsches NE-Join-Land tragen kann (z. B. Golan: L2 = PS, lokal = IL).

## Konsequenzen

**Positiv**
- Aussagekräftige Anker: bekannte Städte/Regionalhauptstädte statt naher Weiler.
- Datengetrieben und abwärtskompatibel: fehlen die Prominenz-Spalten (altes Paket), fällt
  die Strategie pro Kandidat auf den Klassen-Prior zurück; `salience: rank` stellt das alte
  Verhalten wieder her.
- Tuning liegt in der Config (`gazetteer.bearing.composite.*`); die Golden-Fixture prüft die
  Default-Kalibrierung auf realen Daten in CI.

**Negativ / Risiken**
- Prominenz-Werte sind so gut wie OSM (garbage-in bei einzelnen Nodes; die log-Skala dämpft
  Ausreißer).
- Der GeoNames-population-Backfill greift v. a. in der Levante; in dorflastigen Wüstenländern
  (DZ/IQ) bleibt der Tail dünn → dort entscheidet der Klassen-Prior.
- Der weitere Kandidatenradius (120 km statt der rang-Reach) macht den Länder-Guard
  notwendig, damit keine Anker über die Grenze gewählt werden.

## Siehe auch

- Bau-Seite: `make enrich-places`, `scripts/enrich_places.py`, `PLAN-bearing-salience.md`
  im `osm-data`-Repo; `build-gazetteer-package`-Skill.
- Code: `internal/application/gazetteer/salience.go` (`CompositeSalience`),
  `bearing.go` (Kandidaten-Sammlung, Länder-Guard), `internal/config` (`bearing.salience`,
  `bearing.composite.*`).
