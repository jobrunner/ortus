#!/usr/bin/env bash
# Smoke-Test gegen eine laufende ortus-Instanz — nach jedem Deploy und in CI gegen
# das frisch gebaute Image. Nur GET-Requests, verändert nichts.
#
#   scripts/smoke.sh https://ortus.fieldworksdiary.org
#   make smoke URL=https://ortus.fieldworksdiary.org
#
# Prüft, was ein Blick in die Logs direkt nach dem Deploy nicht zeigen kann: dass
# die Koordinatentransformation auch NACH einer Lastspitze noch funktioniert. Bis
# v2.0.2 kippte der Transformer-Pool bei parallelen Requests dauerhaft; danach
# fehlten bei srid≠4326 und mgrs wgs84, gazetteer und alle Quellen in fremder
# SRID — bei HTTP 200.
#
# Ablauf: auf /health ready warten → Lastspitze (parallele srid=25832-Requests) →
# denselben Punkt als lon/lat, x/y/srid=25832 und mgrs abfragen. Alle drei müssen
# wgs84 (passend zum Punkt), gazetteer und DIESELBEN Quellen liefern.
#
# Konfiguration über Umgebungsvariablen (Default: Frankfurt am Main):
#   SMOKE_LON, SMOKE_LAT          Punkt in WGS84
#   SMOKE_UTM_X, SMOKE_UTM_Y      derselbe Punkt in EPSG:25832
#   SMOKE_MGRS                    derselbe Punkt als MGRS (WGS84/UTM)
#   SMOKE_EXPECT_SOURCES          kommagetrennt; müssen in allen drei Antworten stehen
#   SMOKE_GAZETTEER=1             gazetteer-Block verlangen (0: Instanz ohne Gazetteer)
#   SMOKE_BURST=50                Anzahl paralleler Requests der Lastspitze
#   SMOKE_READY_TIMEOUT=2100      Sekunden Warten auf ready (DEM-Entpacken dauert)
set -euo pipefail

BASE="${1:?Usage: smoke.sh <base-url>}"
BASE="${BASE%/}"
LON="${SMOKE_LON:-8.68}"
LAT="${SMOKE_LAT:-50.1}"
UTM_X="${SMOKE_UTM_X:-477114.161}"
UTM_Y="${SMOKE_UTM_Y:-5549798.286}"
MGRS="${SMOKE_MGRS:-32UMA7711449798}"
EXPECT="${SMOKE_EXPECT_SOURCES:-}"
GAZ="${SMOKE_GAZETTEER:-1}"
BURST="${SMOKE_BURST:-50}"
READY_TIMEOUT="${SMOKE_READY_TIMEOUT:-2100}"

for tool in curl jq; do
	command -v "$tool" >/dev/null || { echo "smoke: $tool fehlt" >&2; exit 2; }
done

fails=0
ok()   { printf '  ✅ %s\n' "$*"; }
bad()  { printf '  ❌ %s\n' "$*"; fails=$((fails + 1)); }
warn() { printf '  ⚠️  %s\n' "$*"; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# get <url> <out-file> → HTTP-Status auf stdout
get() { curl -s -o "$2" -w '%{http_code}' --max-time 30 "$1" || true; }

echo "==> ortus smoke test gegen $BASE"

# --- 1. Bereitschaft -----------------------------------------------------------
deadline=$((SECONDS + READY_TIMEOUT))
until [[ "$(get "$BASE/health" "$TMP/health.json")" == 200 ]] && jq -e '.ready == true' "$TMP/health.json" >/dev/null 2>&1; do
	if (( SECONDS >= deadline )); then
		bad "/health nicht ready nach ${READY_TIMEOUT}s: $(cat "$TMP/health.json" 2>/dev/null | head -c 300)"
		echo "==> FEHLGESCHLAGEN"; exit 1
	fi
	sleep 10
done
ok "/health ready"

# --- 2. Lastspitze ---------------------------------------------------------------
burst_url="$BASE/api/v1/query?x=$UTM_X&y=$UTM_Y&srid=25832"
seq "$BURST" | xargs -P "$BURST" -I{} curl -s -o /dev/null --max-time 60 "$burst_url" || true
ok "Lastspitze: $BURST parallele Requests (srid=25832)"

# --- 3. Drei Eingabeformen, ein Punkt -------------------------------------------
form_url() {
	case "$1" in
		lonlat) echo "$BASE/api/v1/query?lon=$LON&lat=$LAT" ;;
		utm)    echo "$BASE/api/v1/query?x=$UTM_X&y=$UTM_Y&srid=25832" ;;
		mgrs)   echo "$BASE/api/v1/query?mgrs=$MGRS" ;;
	esac
}
for form in lonlat utm mgrs; do
	f="$TMP/$form.json"
	code="$(get "$(form_url "$form")" "$f")"
	if [[ "$code" != 200 ]]; then
		bad "$form: HTTP $code $(head -c 200 "$f")"
		continue
	fi
	# MGRS ist auf 1 m gerundet → Toleranz ~1e-4°.
	if jq -e --argjson lon "$LON" --argjson lat "$LAT" '
		def abs: if . < 0 then -. else . end;
		.wgs84 != null and ((.wgs84.lon - $lon) | abs) < 1e-4 and ((.wgs84.lat - $lat) | abs) < 1e-4' "$f" >/dev/null; then
		ok "$form: wgs84 passt zum Punkt"
	else
		bad "$form: wgs84 fehlt oder falsch: $(jq -c '.wgs84' "$f")"
	fi
	if [[ "$GAZ" == 1 ]]; then
		if jq -e '.gazetteer != null' "$f" >/dev/null; then ok "$form: gazetteer vorhanden"; else bad "$form: gazetteer fehlt"; fi
	fi
	jq -r '[.results[].source_id] | sort | join(",")' "$f" > "$TMP/$form.sources"
done

# Inhalt prüfen, nicht die Dateigröße: jq schreibt auch für results: [] eine
# Zeile, und drei leere Listen wären sonst "dieselben Quellen".
ref="$(cat "$TMP/lonlat.sources" 2>/dev/null || true)"
if [[ -n "$ref" ]]; then
	ok "Quellen (lon/lat): $ref"
	for form in utm mgrs; do
		[[ -f "$TMP/$form.sources" ]] || continue
		got="$(cat "$TMP/$form.sources")"
		if [[ "$got" == "$ref" ]]; then ok "$form: dieselben Quellen"; else bad "$form: Quellen weichen ab: $got"; fi
	done
else
	bad "lon/lat: keine Quelle getroffen — Punkt außerhalb der Daten oder Quellen verloren"
fi
if [[ -n "$EXPECT" ]]; then
	before=$fails
	IFS=',' read -ra want <<<"$EXPECT"
	for s in "${want[@]}"; do
		for form in lonlat utm mgrs; do
			[[ -f "$TMP/$form.sources" ]] || continue
			if ! tr ',' '\n' < "$TMP/$form.sources" | grep -qxF -- "$s"; then bad "$form: erwartete Quelle $s fehlt"; fi
		done
	done
	(( fails == before )) && ok "erwartete Quellen in allen Antworten: $EXPECT"
fi

# --- 4. Gazetteer-Endpunkt mit projizierter Eingabe ------------------------------
if [[ "$GAZ" == 1 ]]; then
	code="$(get "$BASE/api/v1/gazetteer?x=$UTM_X&y=$UTM_Y&srid=25832" "$TMP/gaz.json")"
	if [[ "$code" == 200 ]]; then ok "/gazetteer srid=25832: HTTP 200"; else bad "/gazetteer srid=25832: HTTP $code $(head -c 200 "$TMP/gaz.json")"; fi
fi

# --- 5. Health-Komponente --------------------------------------------------------
get "$BASE/health" "$TMP/health.json" >/dev/null
case "$(jq -r '.components.transformer // "absent"' "$TMP/health.json")" in
	ok)     ok "/health components.transformer = ok" ;;
	absent) warn "/health ohne components.transformer (Version vor dem Transformer-Health-Check)" ;;
	*)      bad "/health components.transformer = $(jq -r '.components.transformer' "$TMP/health.json")" ;;
esac

if (( fails > 0 )); then
	echo "==> FEHLGESCHLAGEN ($fails)"
	exit 1
fi
echo "==> OK"
