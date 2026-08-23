# Performance-Gate

`perf/baseline.json` ist der Performance-Vertrag des Gazetteer-Endpunkts:
eine feste Anfragemenge plus ein Budget pro Span. `make perf-gate` fährt die
Anfragen, liest die entstandenen Spans über MCP zurück und vergleicht sie.

## Warum Aufrufzahlen und nicht nur Millisekunden

Das Gate budgetiert zwei verschiedene Dinge, und die Unterscheidung ist der
ganze Punkt:

| | Prüfung | Warum |
|---|---|---|
| `max_per_trace` | **hart**, exakt | Wie viele SpatiaLite-Roundtrips ein Request macht, ist eine Eigenschaft des Codes, nicht der Maschine. Eine Regression von 6 auf 512 Queries wird beim ersten Lauf erkannt. |
| `max_p95_ms` | lose (Toleranz ×3) | Wanduhr auf geteilten CI-Runnern schwankt. Zeiten fangen Größenordnungen, keine 20 %. |

Ein Gate nur auf Millisekunden müsste seine Toleranz so weit öffnen, dass es
nie auslöst. Ein Gate auf Aufrufzahlen feuert, sobald ein N+1 entsteht.

## Voraussetzungen

Die Zielinstanz braucht **beides**:

```yaml
tracing:
  enabled: true          # sonst bleibt der Ringpuffer leer
  service_name: ortus
mcp:
  enabled: true          # sonst ist span_summary nicht erreichbar
  host: 127.0.0.1        # Loopback braucht kein ORTUS_MCP_TOKEN
```

Ohne Tracing bricht das Gate mit einer klaren Meldung ab statt grün zu
behaupten, es sei alles in Ordnung.

## Benutzen

```sh
make perf-gate PERF_BASE=http://127.0.0.1:8099
make perf-gate-update PERF_BASE=http://127.0.0.1:8099   # bewusste Änderung
```

`-update` schreibt die Baseline aus dem aktuellen Lauf neu. Das ist eine
Review-pflichtige Änderung: der Diff zeigt, welches Budget sich bewegt hat.
`_known_issues` bleibt dabei erhalten.

## `_known_issues`

Eine Baseline hält fest, was der Code **heute** tut — sie billigt es nicht.
Bekannte Defekte in den aktuellen Zahlen stehen deshalb ausdrücklich in
`_known_issues`, damit ein committetes Budget von 235 Aufrufen pro Request
nicht als „so gewollt" gelesen wird.

## Verwandt

- `make trace-coverage` — stellt sicher, dass überhaupt Spans entstehen. Die
  beiden Gates greifen ineinander: ohne Instrumentierung hätte das
  Performance-Gate nichts zu messen und würde stillschweigend grün bleiben.
- `.claude/skills/perf-test/SKILL.md` — Lastmessung mit Vegeta und Tempo.
