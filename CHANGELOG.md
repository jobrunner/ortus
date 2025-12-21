# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Docker CI/CD pipeline with multi-architecture support (amd64, arm64)
- Automated Docker image builds and security scanning
- Claude Code hooks for local Docker validation (hadolint, trivy)
- VERSION file for centralized version management
- CHANGELOG.md for tracking changes

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

[Unreleased]: https://github.com/jobrunner/ortus/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/jobrunner/ortus/releases/tag/v0.1.0
