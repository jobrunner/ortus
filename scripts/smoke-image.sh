#!/usr/bin/env bash
# Startet ein ortus-Image mit Test-Fixtures und fährt scripts/smoke.sh dagegen.
# Fängt, was Unit- und Integrationstests nicht sehen: Brüche, die nur im
# Runtime-Image auftreten (SpatiaLite/PROJ des Base-Images, Treiber-Verhalten unter
# Last).
#
#   scripts/smoke-image.sh ghcr.io/jobrunner/ortus:2.0.3
#   make smoke-image IMAGE=ortus:local
#
# Fixtures: testdata/smoke/ (je eine Quelle in EPSG:4326 und EPSG:25832 um
# Wertheim) und das Gazetteer-Fixture aus internal/app/testdata/.
set -euo pipefail
cd "$(dirname "$0")/.."

IMAGE="${1:?Usage: smoke-image.sh <image>}"
PORT="${SMOKE_PORT:-18080}"
NAME="ortus-smoke-$$"

work="$(mktemp -d)"
mkdir -p "$work/packages" "$work/gazetteer"
cp testdata/smoke/*.gpkg "$work/packages/"
# Kopien: ortus schreibt beim Öffnen in die Gazetteer-GeoPackage.
cp internal/app/testdata/gazetteer-fixture.gpkg "$work/gazetteer/gazetteer.gpkg"
cp internal/app/testdata/gazetteer-manifest.yaml internal/app/testdata/gazetteer-sidecar.yaml \
	internal/app/testdata/gazetteer-name-sources.yaml "$work/gazetteer/"
chmod -R a+rwX "$work"

cleanup() {
	status=$?
	if (( status != 0 )); then
		echo "==> Container-Logs ($NAME):"
		docker logs --tail 80 "$NAME" 2>&1 || true
	fi
	docker rm -f "$NAME" >/dev/null 2>&1 || true
	rm -rf "$work"
	exit "$status"
}
trap cleanup EXIT

docker run -d --name "$NAME" -p "127.0.0.1:$PORT:8080" \
	-v "$work/packages:/data/packages" \
	-v "$work/gazetteer:/data/gazetteer" \
	-e ORTUS_STORAGE_LOCAL_PATH=/data/packages \
	-e ORTUS_GAZETTEER_ENABLED=true \
	-e ORTUS_GAZETTEER_GEOPACKAGE_PATH=/data/gazetteer/gazetteer.gpkg \
	-e ORTUS_GAZETTEER_MANIFEST_PATH=/data/gazetteer/gazetteer-manifest.yaml \
	-e ORTUS_GAZETTEER_LEVEL_REFERENCE_PATH=/data/gazetteer/gazetteer-sidecar.yaml \
	-e ORTUS_GAZETTEER_NAME_SOURCE_MANIFEST_PATH=/data/gazetteer/gazetteer-name-sources.yaml \
	"$IMAGE" >/dev/null

SMOKE_LON=9.52316 SMOKE_LAT=49.76028 \
SMOKE_UTM_X=537679.359 SMOKE_UTM_Y=5512109.406 \
SMOKE_MGRS=32UNA3767912109 \
SMOKE_EXPECT_SOURCES=smoke-utm,smoke-wgs \
SMOKE_READY_TIMEOUT="${SMOKE_READY_TIMEOUT:-120}" \
	scripts/smoke.sh "http://127.0.0.1:$PORT"
