# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.5.1] - 2025-12-27

### Fixed
- Packages now show `ready=true` on server restart when R-tree indexes already exist

## [0.5.0] - 2025-12-26

### Fixed
- GeoPackage spatial index creation now works without `geometry_columns` table
- SpatiaLite's `CreateSpatialIndex()` replaced with direct R-tree virtual table creation
- Query performance improved from ~6 seconds to ~8-150ms for large GeoPackages

### Changed
- Database opened in read-write mode to allow R-tree index creation
- R-tree indexes are persisted in GeoPackage files for faster subsequent starts
- Point queries now use R-tree pre-filter + ST_Contains for precise geometry matching

## [0.4.1] - 2025-12-25

### Added
- `--disable-frontend` CLI flag to disable the web frontend at `/`
- `server.frontend_enabled` config option (default: `true`)
- Environment variable `ORTUS_SERVER_FRONTEND_ENABLED` support

## [0.4.0] - 2025-12-25

### Added
- Embedded web frontend at root path (`/`) for interactive coordinate queries
- Support for major European coordinate systems: WGS84, Web Mercator, ETRS89/UTM zones 32N & 33N, DHDN/Gauß-Krüger zones 2 & 3
- Mobile-first responsive design with dynamic labels adapting to selected coordinate system
- Geolocation button to use current device position
- Expandable result cards with feature properties, geometry preview, and license information

## [0.3.1] - 2025-12-23

### Fixed
- `derivePackageID` edge cases: properly handles empty paths and files named only with extension (e.g., ".gpkg")
- Race condition in package removal: captures both ID and path in single lock acquisition
- Sync service rate limiting: initializes `lastAPISync` to allow immediate first API call
- Concurrent sync prevention: adds mutex to prevent scheduled and API-triggered syncs from running simultaneously
- Watcher event precedence: create events now override pending delete events (handles quick delete+recreate)

### Changed
- Refactored watcher `eventLoop` into smaller functions to reduce cognitive complexity

### Added
- Comprehensive tests for `derivePackageID` edge cases
- Tests for watcher helper functions (`fsnotifyOpToOperation`, `isGeoPackageFile`, `Operation.String`)

## [0.3.0] - 2025-12-22

### Added
- Automatic removal of packages deleted from remote storage during sync
- `packages_removed` field in sync API response
- Proper file deletion detection in local file watcher (fixed fsnotify operation handling)

### Changed
- `Sync()` now returns `SyncStats` with both `Added` and `Removed` counts
- File watcher now correctly uses fsnotify operation types instead of file existence check

### Fixed
- File watcher `determineOperation` now correctly detects file deletions using fsnotify events
- Local cache files are now deleted when packages are removed from remote storage

## [0.2.0] - 2025-12-22

### Added
- Remote Storage Sync: Periodic synchronization with S3/Azure/HTTP to detect and load new GeoPackages
- Sync API endpoint `POST /api/v1/sync` with rate limiting (2 requests/minute, 30s cooldown)
- `SyncConfig` for configurable sync intervals (`ORTUS_SYNC_ENABLED`, `ORTUS_SYNC_INTERVAL`)
- Storage type constants (`StorageTypeLocal`, `StorageTypeS3`, `StorageTypeAzure`, `StorageTypeHTTP`)
- ADR-0011 documenting Remote Storage Sync design decisions
- Docker CI/CD pipeline with multi-architecture support (amd64, arm64)
- Automated Docker image builds and security scanning
- Claude Code hooks for local Docker validation (hadolint, trivy)
- VERSION file for centralized version management
- CHANGELOG.md for tracking changes

### Changed
- HTTP server now accepts optional `SyncService` dependency
- App lifecycle manages SyncService start/stop

## [0.1.0] - 2024-12-21

### Added
- Initial release of Ortus GeoPackage query server
- REST API with point queries (`/api/v1/query`)
- Multiple GeoPackage support with hot-reload
- Automatic coordinate transformation (SRID support)
- Object storage integration (AWS S3, Azure Blob, HTTP, Local)
- TLS/HTTPS with Let's Encrypt via CertMagic
- Prometheus metrics endpoint
- Health checks (`/health`, `/health/live`, `/health/ready`)
- OpenAPI 3.0 specification and Swagger UI
- Multi-platform Docker support (Alpine and Ubuntu variants)
- Configurable geometry output in query results
- Comprehensive unit and integration tests

### Security
- Non-root user in Docker containers
- Read-only GeoPackage access
- CORS configuration support

[Unreleased]: https://github.com/jobrunner/ortus/compare/v0.5.1...HEAD
[0.5.1]: https://github.com/jobrunner/ortus/compare/v0.5.0...v0.5.1
[0.5.0]: https://github.com/jobrunner/ortus/compare/v0.4.1...v0.5.0
[0.4.1]: https://github.com/jobrunner/ortus/compare/v0.4.0...v0.4.1
[0.4.0]: https://github.com/jobrunner/ortus/compare/v0.3.1...v0.4.0
[0.3.1]: https://github.com/jobrunner/ortus/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/jobrunner/ortus/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/jobrunner/ortus/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/jobrunner/ortus/releases/tag/v0.1.0
