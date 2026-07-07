# ADR-0016: Peilungs-Konvention, Grenz-Constraint & Compass-Quantisierung

## Status

Akzeptiert

## Kontext

Die Peilung soll eine Koordinate relativ zu einem findbaren Ort ausdrücken („4 km E
Würzburg") und dabei nur Anker nutzen, die **innerhalb derselben Verwaltungsgrenze** liegen
(kein Cross-Border-Unsinn). Mehrere Umsetzungsdetails waren zu entscheiden: Richtungs­konvention,
Distanzmetrik, KNN-Mechanik, und wie der Grenz-Constraint technisch greift.

## Entscheidung

- **Richtung:** Azimut **Referenz→Punkt** via SpatiaLite `ST_Azimuth` (Radiant → Grad,
  0=N, 90=O im Uhrzeigersinn). „E Würzburg" heißt: der Punkt liegt östlich von Würzburg.
- **Compass:** Quantisierung auf 8 oder 16 Punkte (`round(az / (360/N)) mod N`), rein im Domain.
- **Distanz:** ellipsoidal, `Distance(g1, g2, 1)` — dieselbe Metrik wie die KNN-Ordnung.
  Anzeige gerundet (< 10 km auf 0,5 km, sonst 1 km).
- **KNN:** **R-tree-Bbox-Vorfilter + exakte `Distance` (Radius + ORDER BY)**, klassen-gefiltert,
  **nicht** `VirtualKNN2`. Grund: jede Gazetteer-Abfrage ist klassen- und grenz-beschränkt,
  und KNN2 kann **kein Attribut-Prädikat** in die Nachbarsuche schieben. Der gefilterte
  Radius-Scan ist hier das richtige Werkzeug, kein Fallback.
- **Grenz-Constraint: relational, nicht als SQL-`IN`-Menge.** Statt `admin_id IN (alle
  Gemeinden unter dem Bundesland)` zu materialisieren, wird der **Tier-Vorfahr** (Default
  `state`) des Anfragepunkts (aus den enthaltenden Admin-Polygonen) mit dem Tier-Vorfahren
  jedes Kandidaten (via `ResolveChain` über `parent_id`) verglichen. Es fallen nur ≤ 3
  Kandidaten an (einer pro Klasse), also ≤ 3 günstige Ketten-Walks — keine neue Port-Methode,
  keine Descendant-Abfrage. `k > 1` pro Klasse lässt einen näheren Anker jenseits der Grenze
  zugunsten eines innerhalb überspringen. Der Tier ist **semantisch** (über das Sidecar,
  `equivalent`), nicht die feste Zahl 4.
- **„in" vs „prope" (aktualisiert):** Ob wir *im* Ort oder nur *in der Nähe* sind,
  entscheidet **Verwaltungs-Containment**, nicht die Distanz. Liegt der Abfragepunkt
  in der eigenen Admin-Einheit des Ankers → „in {name}" (Flag `inside=true`); sonst
  nah, aber außerhalb (unter `InsideLabelKM`, Default 1 km) → richtungsloses
  „prope {name}"; andernfalls das Richtungs-Label. Präfixe folgen der Etiketten-
  Konvention: lateinisch „in" bzw. „prope" (etablierter Fundort-Terminus für „bei").

## Konsequenzen

**Positiv**
- Der Grenz-Constraint nutzt die vorhandene `ResolveChain` und bleibt bei ≤ 3 Kandidaten billig.
- Attribut-Filter (Klasse) und Radius fallen in einen Query; die Domäne bleibt cgo-frei.

**Negativ / Risiken**
- Ellipsoidale `Distance` braucht auflösbare SRID-4326-Metadaten (`spatial_ref_sys`/
  `gpkg_spatial_ref_sys`), sonst NULL → leere Radien. Beim Start geprüft (`VerifySRID`,
  [Plan §4](../gazetteer-plan.md)); nicht-fatal, aber laut gewarnt.
- Kandidaten mit unbekannter `admin_id` (Coverage-Loch) werden aus dem Constraint
  ausgeschlossen, nicht stillschweigend zugelassen.
