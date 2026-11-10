#!/usr/bin/env bash
# Regeneriert die Smoke-Test-Quellen in testdata/smoke/: je eine Mini-GeoPackage
# in EPSG:4326 und EPSG:25832 um denselben Punkt. Zwei SRIDs, damit der Smoke-Test
# die Koordinatentransformation in beide Richtungen erzwingt.
# Braucht sqlite3 und SPATIALITE_LIBRARY_PATH (Nix-Shell oder Docker-Image).
set -euo pipefail
cd "$(dirname "$0")/.."
lib="${SPATIALITE_LIBRARY_PATH:?SPATIALITE_LIBRARY_PATH muss gesetzt sein}"

build() { # <datei> <srid> <minx> <miny> <maxx> <maxy>
	rm -f "$1"
	sqlite3 "$1" \
		-cmd ".parameter set :lib '$lib'" \
		-cmd ".parameter set :srid $2" \
		-cmd ".parameter set :minx $3" -cmd ".parameter set :miny $4" \
		-cmd ".parameter set :maxx $5" -cmd ".parameter set :maxy $6" \
		< testdata/smoke/fixtures.sql > /dev/null
}

build testdata/smoke/smoke-wgs.gpkg 4326 9.50 49.74 9.55 49.78
build testdata/smoke/smoke-utm.gpkg 25832 535000 5510000 540000 5515000
ls -l testdata/smoke/*.gpkg
