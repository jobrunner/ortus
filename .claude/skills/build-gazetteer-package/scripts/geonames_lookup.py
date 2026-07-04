#!/usr/bin/env python3
"""
GeoNames ISO Code Lookup Script

This script uses the GeoNames API to fill in missing ISO country codes
for places and admin_levels in the OSM GeoPackage.

Requirements:
    pip install requests sqlite3 (sqlite3 is built-in)

Usage:
    python geonames_lookup.py --username YOUR_GEONAMES_USERNAME [options]

GeoNames Account:
    Register for free at: https://www.geonames.org/login
    Then enable web services at: https://www.geonames.org/manageaccount

Source: https://www.geonames.org/export/web-services.html
License: Creative Commons Attribution 4.0 License
"""

import argparse
import json
import logging
import sqlite3
import subprocess
import sys
import time
from pathlib import Path
from urllib.request import urlopen
from urllib.error import URLError, HTTPError
from urllib.parse import urlencode

# Configuration
DEFAULT_GPKG = "output/osm-admin-places.gpkg"
GEONAMES_API = "http://api.geonames.org/countryCodeJSON"
RATE_LIMIT_DELAY = 1.0  # seconds between requests (be respectful to free API)
MAX_RETRIES = 3
BATCH_SIZE = 100  # commit every N updates

logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s - %(levelname)s - %(message)s'
)
logger = logging.getLogger(__name__)


def get_country_code(lat: float, lon: float, username: str, radius: int = 50) -> str | None:
    """
    Query GeoNames API to get country code for coordinates.

    Args:
        lat: Latitude
        lon: Longitude
        username: GeoNames username
        radius: Search radius in km for coastal areas (default 50km)

    Returns:
        ISO 3166-1 alpha-2 country code or None if not found
    """
    params = {
        'lat': lat,
        'lng': lon,
        'username': username,
        'radius': radius  # helps with coastal areas
    }

    url = f"{GEONAMES_API}?{urlencode(params)}"

    for attempt in range(MAX_RETRIES):
        try:
            with urlopen(url, timeout=10) as response:
                data = json.loads(response.read().decode('utf-8'))

                # Check for errors
                if 'status' in data:
                    error_msg = data['status'].get('message', 'Unknown error')
                    if 'user does not exist' in error_msg.lower():
                        logger.error(f"Invalid GeoNames username. Register at https://www.geonames.org/login")
                        sys.exit(1)
                    elif 'hourly limit' in error_msg.lower() or 'daily limit' in error_msg.lower():
                        logger.warning(f"Rate limit reached: {error_msg}")
                        return None
                    else:
                        logger.warning(f"GeoNames error: {error_msg}")
                        return None

                return data.get('countryCode')

        except HTTPError as e:
            logger.warning(f"HTTP error {e.code} for ({lat}, {lon}), attempt {attempt + 1}")
            time.sleep(RATE_LIMIT_DELAY * 2)
        except URLError as e:
            logger.warning(f"URL error for ({lat}, {lon}): {e.reason}, attempt {attempt + 1}")
            time.sleep(RATE_LIMIT_DELAY * 2)
        except json.JSONDecodeError as e:
            logger.warning(f"JSON decode error for ({lat}, {lon}): {e}")
            return None

    return None


def get_coordinates_spatialite(gpkg_path: str, query: str) -> list[tuple]:
    """
    Use spatialite to extract coordinates from GeoPackage.
    Returns list of (id, name, lon, lat) tuples.
    """
    result = subprocess.run(
        ['spatialite', gpkg_path, query],
        capture_output=True,
        text=True
    )

    if result.returncode != 0:
        logger.error(f"Spatialite error: {result.stderr}")
        return []

    rows = []
    for line in result.stdout.strip().split('\n'):
        if line:
            parts = line.split('|')
            if len(parts) >= 4:
                try:
                    fid = int(parts[0])
                    name = parts[1]
                    lon = float(parts[2]) if parts[2] else None
                    lat = float(parts[3]) if parts[3] else None
                    if lon is not None and lat is not None:
                        rows.append((fid, name, lon, lat))
                except (ValueError, IndexError):
                    continue

    return rows


def process_places(gpkg_path: str, username: str, dry_run: bool = False, delay: float = 1.0) -> int:
    """Process places layer and fill in missing ISO codes."""

    logger.info("Extracting places without ISO codes...")

    query = """
    SELECT fid, name,
           ST_X(GeomFromGPB(geom)),
           ST_Y(GeomFromGPB(geom))
    FROM places
    WHERE (iso_code IS NULL OR iso_code = '')
    """

    rows = get_coordinates_spatialite(gpkg_path, query)
    logger.info(f"Found {len(rows)} places without ISO codes")

    if not rows:
        return 0

    conn = sqlite3.connect(gpkg_path)
    cursor = conn.cursor()

    updated = 0
    for i, (fid, name, lon, lat) in enumerate(rows):
        # Rate limiting
        time.sleep(delay)

        country_code = get_country_code(lat, lon, username)

        if country_code:
            if not dry_run:
                cursor.execute(
                    "UPDATE places SET iso_code = ? WHERE fid = ?",
                    (country_code, fid)
                )
                if (updated + 1) % BATCH_SIZE == 0:
                    conn.commit()

            updated += 1
            logger.info(f"[{i+1}/{len(rows)}] {name}: {country_code}")
        else:
            logger.debug(f"[{i+1}/{len(rows)}] {name}: No country found")

        # Progress update
        if (i + 1) % 50 == 0:
            logger.info(f"Progress: {i+1}/{len(rows)} processed, {updated} updated")

    if not dry_run:
        conn.commit()
    conn.close()

    return updated


def process_admin_levels(gpkg_path: str, username: str, dry_run: bool = False, delay: float = 1.0) -> int:
    """Process admin_levels layer and fill in missing country_iso codes."""

    logger.info("Extracting admin_levels without ISO codes...")

    query = """
    SELECT fid, name,
           ST_X(ST_Centroid(GeomFromGPB(geom))),
           ST_Y(ST_Centroid(GeomFromGPB(geom)))
    FROM admin_levels
    WHERE (country_iso IS NULL OR country_iso = '')
    """

    rows = get_coordinates_spatialite(gpkg_path, query)
    logger.info(f"Found {len(rows)} admin_levels without ISO codes")

    if not rows:
        return 0

    conn = sqlite3.connect(gpkg_path)
    cursor = conn.cursor()

    updated = 0
    for i, (fid, name, lon, lat) in enumerate(rows):
        # Rate limiting
        time.sleep(delay)

        country_code = get_country_code(lat, lon, username)

        if country_code:
            if not dry_run:
                cursor.execute(
                    "UPDATE admin_levels SET country_iso = ? WHERE fid = ?",
                    (country_code, fid)
                )
                if (updated + 1) % BATCH_SIZE == 0:
                    conn.commit()

            updated += 1
            logger.info(f"[{i+1}/{len(rows)}] {name}: {country_code}")
        else:
            logger.debug(f"[{i+1}/{len(rows)}] {name}: No country found")

        # Progress update
        if (i + 1) % 50 == 0:
            logger.info(f"Progress: {i+1}/{len(rows)} processed, {updated} updated")

    if not dry_run:
        conn.commit()
    conn.close()

    return updated


def main():
    parser = argparse.ArgumentParser(
        description='Fill missing ISO codes using GeoNames API',
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Examples:
    # Process both layers
    python geonames_lookup.py --username myuser

    # Dry run (no changes)
    python geonames_lookup.py --username myuser --dry-run

    # Process only places
    python geonames_lookup.py --username myuser --places-only

    # Process only admin_levels
    python geonames_lookup.py --username myuser --admin-only

GeoNames Account:
    1. Register at: https://www.geonames.org/login
    2. Enable web services at: https://www.geonames.org/manageaccount
    3. Wait a few minutes for activation

Rate Limits:
    Free accounts have hourly/daily limits. The script uses 1 request/second
    to be respectful. For ~4000 lookups, expect ~1-2 hours runtime.
        """
    )

    parser.add_argument(
        '--username', '-u',
        required=True,
        help='GeoNames username (register at geonames.org)'
    )

    parser.add_argument(
        '--gpkg', '-g',
        default=DEFAULT_GPKG,
        help=f'Path to GeoPackage (default: {DEFAULT_GPKG})'
    )

    parser.add_argument(
        '--dry-run', '-n',
        action='store_true',
        help='Show what would be updated without making changes'
    )

    parser.add_argument(
        '--places-only',
        action='store_true',
        help='Only process places layer'
    )

    parser.add_argument(
        '--admin-only',
        action='store_true',
        help='Only process admin_levels layer'
    )

    parser.add_argument(
        '--delay', '-d',
        type=float,
        default=RATE_LIMIT_DELAY,
        help=f'Delay between API requests in seconds (default: {RATE_LIMIT_DELAY})'
    )

    parser.add_argument(
        '--verbose', '-v',
        action='store_true',
        help='Enable verbose output'
    )

    args = parser.parse_args()

    if args.verbose:
        logging.getLogger().setLevel(logging.DEBUG)

    # Set rate limit delay
    rate_limit = args.delay

    gpkg_path = Path(args.gpkg)
    if not gpkg_path.exists():
        logger.error(f"GeoPackage not found: {gpkg_path}")
        sys.exit(1)

    if args.dry_run:
        logger.info("DRY RUN - no changes will be made")

    total_updated = 0

    # Process places
    if not args.admin_only:
        logger.info("=" * 50)
        logger.info("Processing PLACES layer")
        logger.info("=" * 50)
        updated = process_places(str(gpkg_path), args.username, args.dry_run, rate_limit)
        logger.info(f"Places updated: {updated}")
        total_updated += updated

    # Process admin_levels
    if not args.places_only:
        logger.info("=" * 50)
        logger.info("Processing ADMIN_LEVELS layer")
        logger.info("=" * 50)
        updated = process_admin_levels(str(gpkg_path), args.username, args.dry_run, rate_limit)
        logger.info(f"Admin_levels updated: {updated}")
        total_updated += updated

    logger.info("=" * 50)
    logger.info(f"TOTAL UPDATED: {total_updated}")
    logger.info("=" * 50)

    if args.dry_run:
        logger.info("(Dry run - no actual changes were made)")


if __name__ == '__main__':
    main()
