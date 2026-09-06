# Inkrementelles NDJSON-Streaming für POST /query/batch

## Context

Der NDJSON-Modus berechnet heute ALLE Items vorab und streamt nur das fertige
Ergebnis (dokumentierter v1-Trade-off in `streamBatchItems`): Bei 883 Punkten
kommt das erste Byte nach ~27 s. Nach dem write_timeout-Fix (v1.9.3) übersteht
der Sync-Pfad das zwar, aber Proxies mit Idle-Timeouts und ungeduldige Clients
sehen minutenlang nichts. Auftrag (Jo, 2026-09-06): echtes inkrementelles
Streaming.

## Design

- **Nur der NDJSON-Pfad ändert sich.** Sync-JSON bleibt: ein Rutsch.
- **Chunking in Eingabe-Reihenfolge** (Konstante `batchStreamChunkSize = 100`):
  pro Chunk läuft die bestehende Pipeline (resolveBatchInputs →
  resolveBatchResponses → buildBatchItems), die Items werden sofort emittiert
  (Encoder + ResponseController.Flush). NDJSON-Zeilen bleiben in
  Eingabe-Reihenfolge; die DEM-Tile-Locality-Sortierung wirkt nur noch
  chunk-intern (bewusster Trade-off, kommentiert).
- **Erster Chunk vor dem Header**: Fehler im ersten Chunk (z. B. unbekannte
  Source → 404) bleiben saubere HTTP-Fehler; ab dem zweiten Chunk ist der
  Stream begonnen — Fehler brechen ihn ab (geloggt), wie heute bei
  Write-Fehlern. TTFB ≈ eine Chunk-Latenz (~1–2 s bei echten Daten).
- **Dedup**: Die gemeinsame Chunk-Pipeline wird als `buildBatchChunk`
  extrahiert; der Sync-Pfad ruft sie mit dem ganzen Request auf (identisches
  Verhalten, weniger Code in handleQueryBatch). Der Invariant-Fehler („one
  response per coordinate") wird ein sentinel error, beide Pfade behandeln ihn.

## Tests (TDD)

1. Determinismus statt Timing: gated Gazetteer (blockiert nach N Locates) —
   die ersten `chunkSize` Zeilen müssen lesbar sein, BEVOR das Gate öffnet;
   danach Rest + Reihenfolge-/Paritätsvergleich mit dem Sync-Ergebnis.
2. Fehler im ersten Chunk (unknown source) → HTTP-Fehler, kein 200-Stream.
3. Bestehende NDJSON-/Batch-Tests bleiben grün (Format unverändert).

## Doku

- openapi.yaml: Beschreibung des NDJSON-Modus („incrementally, chunkwise")
- http-api.md: Delivery-Absatz aktualisieren (v1-Trade-off-Satz ersetzen).

## Gates

- batch.go Datei-Komplexität (Default-Cap 50) im Blick — notfalls Auslagern.
- Kein API-/Schema-Change; perf-Baseline unberührt (Gazetteer-Endpunkt).
