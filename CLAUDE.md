# Arbeitsanweisungen für ortus

## Analysen laufen über den MCP, nicht über selbstgebaute Skripte

Für **jede** Performance- oder Debugging-Analyse an einer laufenden Instanz sind
die MCP-Diagnose-Tools die erste Wahl — nicht `curl` auf Tempo, nicht ein
Python-Skript, das Span-Bäume aggregiert, nicht `timings_ms` aus der Antwort.

| Tool | Wofür |
|---|---|
| `span_summary` | **Hier anfangen.** Pro Span: Aufrufe/Request, p50/p95, Summe, nach Kosten sortiert. `group_by` teilt einen Span-Namen nach Attribut (z. B. `spatial.layer`). |
| `list_traces` | Die langsamen Ausreißer finden (`min_duration_ms`, `status`, `since_iso`). |
| `get_trace` | Der ganze Span-Baum eines Traces, mit Attributen und Fehlern. |
| `list_active_spans` | Was läuft **jetzt** — die Frage bei Hängern. |
| `tracing_stats` | Puffer-Belegung; zuerst prüfen, ob überhaupt Traces ankommen. |

`per_trace` ist das Feld, auf das es meist ankommt: Perzentile können einen
langsamen Query nicht von hunderten schnellen unterscheiden. Ein Span mit
p95 = 0,9 ms sieht harmlos aus, bis man sieht, dass er 235× pro Request läuft.
Genau so blieb ein N+1 im Gazetteer unentdeckt, während `timings_ms`
„bearing: 54 ms" meldete — eine Zahl für 512 Queries.

**Voraussetzung, beides:**

```yaml
tracing: { enabled: true, service_name: ortus }
mcp:     { enabled: true, host: 127.0.0.1 }     # Loopback braucht kein Token
```

Fehlt eins davon, antworten die Tools „tracing is disabled" oder der Endpunkt
ist nicht erreichbar. Das liest sich wie ein fehlendes Feature — es ist ein
fehlendes Flag.

**Ohne native MCP-Tools** (z. B. weil die Session vor `.mcp.json` startete):
`make trace-summary` ruft dasselbe Tool über JSON-RPC auf. Kein Ersatz-Skript
schreiben — der Servercode ist derselbe, die Zahlen sind vergleichbar.

## Gates, die nicht umgangen werden

- `make verify` ist die maßgebliche Grün-Prüfung. Sie schließt
  `trace-coverage` ein: jede exportierte ctx-Methode eines Application-Service
  und jede `Traced*`-Decorator-Methode muss einen Span öffnen. Ausnahmen
  brauchen `//tracecheck:ignore <Begründung>` im Code — ein Marker ohne
  Begründung zählt nicht.
- `make perf-gate` prüft Span-Budgets gegen `perf/baseline.json`. Aufrufzahlen
  sind **harte** Grenzen (maschinenunabhängig), Zeiten lose Deckel. Die Baseline
  neu zu schreiben ist eine bewusste, Review-pflichtige Änderung.
- Das Suppression-Budget (`scripts/debt-guard.sh`) ist ein Ratchet. Lieber
  refaktorieren als ein `//nolint` hinzufügen.
- Coverage-Floors sind raise-only. Der Fix ist, Tests zu ergänzen.

## Sonstiges

- Conventional Commits sind Pflicht (release-please erzeugt daraus den Changelog).
- Bei großen Umbenennungen lügen die Inline-Diagnosen von gopls; maßgeblich sind
  `go build` und `go test`.
