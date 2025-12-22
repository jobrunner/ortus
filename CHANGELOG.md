# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/jobrunner/ortus/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/jobrunner/ortus/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/jobrunner/ortus/releases/tag/v0.1.0
