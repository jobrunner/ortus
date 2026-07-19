# ortus Makefile
# Alle Standardaufgaben für Entwicklung und CI/CD

.PHONY: all build build-all install run clean help
.PHONY: test test-unit test-integration test-coverage test-race test-bench load-test fuzz bench mutation
.PHONY: load-stack-up load-stack-down load-stack-clean load-serve load-attack
.PHONY: lint lint-go lint-fix vet
.PHONY: security-check vuln-check gosec licenses
.PHONY: fmt format fmt-check
.PHONY: check check-ci verify hooks arch debt debt-guard debt-coverage debt-deadcode
.PHONY: deps deps-update deps-verify
.PHONY: doc doc-serve docs docs-serve doc-drift
.PHONY: release release-dry
.PHONY: ci-local ci-lint ci-test ci-build ci-dry ci-amd64 ci-check

# Variablen
BINARY_NAME := ortus
MODULE := github.com/jobrunner/ortus
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS := -ldflags "-X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME)"

GO := go
GOTEST := gotestsum --format testdox --
GOLINT := golangci-lint

# Verzeichnisse
BUILD_DIR := build
COVERAGE_DIR := coverage

# Lokaler Observability-Lasttest (Grafana/Tempo/Loki/Prometheus + Vegeta)
LOADTEST_DIR := deploy/loadtest
LOADTEST_COMPOSE := docker compose -f $(LOADTEST_DIR)/docker-compose.yaml

# Standard-Target
all: check build

## Build Targets
build: ## Baue die Anwendung
	@echo "Building $(BINARY_NAME)..."
	$(GO) build $(LDFLAGS) -o $(BINARY_NAME) ./cmd/$(BINARY_NAME)

build-all: ## Baue für alle Plattformen
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=amd64 $(GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 ./cmd/$(BINARY_NAME)
	GOOS=linux GOARCH=arm64 $(GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-arm64 ./cmd/$(BINARY_NAME)
	GOOS=darwin GOARCH=amd64 $(GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-amd64 ./cmd/$(BINARY_NAME)
	GOOS=darwin GOARCH=arm64 $(GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64 ./cmd/$(BINARY_NAME)
	GOOS=windows GOARCH=amd64 $(GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-windows-amd64.exe ./cmd/$(BINARY_NAME)

install: build ## Installiere lokal
	$(GO) install $(LDFLAGS) ./cmd/$(BINARY_NAME)

run: build ## Baue und starte die Anwendung
	./$(BINARY_NAME)

## Test Targets
test: ## Führe alle Tests aus
	$(GOTEST) ./...

test-unit: ## Nur Unit-Tests
	$(GOTEST) -short ./...

test-integration: ## Nur Integrationstests
	$(GOTEST) -run Integration ./...

test-coverage: ## Tests mit Coverage-Report
	@mkdir -p $(COVERAGE_DIR)
	$(GO) test -coverprofile=$(COVERAGE_DIR)/coverage.out -covermode=atomic ./...
	$(GO) tool cover -html=$(COVERAGE_DIR)/coverage.out -o $(COVERAGE_DIR)/coverage.html
	$(GO) tool cover -func=$(COVERAGE_DIR)/coverage.out
	@echo "\nCoverage Report: $(COVERAGE_DIR)/coverage.html"

test-race: ## Tests mit Race Detector
	$(GO) test -race ./...

test-bench: ## Benchmarks ausführen
	$(GO) test -bench=. -benchmem ./...

# Hot-path micro-benchmarks that run anywhere (no external data, unlike the
# env-gated load-test benchmarks). Used by the CI Bench job for PR-vs-base
# comparison. Override BENCHCOUNT / BENCHTIME for statistical runs.
BENCH_PKGS := ./internal/domain/ ./internal/adapters/storage/ ./internal/adapters/geopackage/ ./internal/adapters/http/
bench: ## Hot-path Micro-Benchmarks (BENCHCOUNT/BENCHTIME überschreibbar)
	@$(GO) test -run='^$$' -bench=. -benchmem $(if $(BENCHCOUNT),-count=$(BENCHCOUNT),) $(if $(BENCHTIME),-benchtime=$(BENCHTIME),) $(BENCH_PKGS)

mutation: ## Mutation-Testing (gremlins) — hartes Gate pro Paket, wie in der CI
	$(GO) install github.com/go-gremlins/gremlins/cmd/gremlins@v0.5.1
	@rc=0; \
	 gremlins unleash --threshold-efficacy 90 --threshold-mcover 95 ./internal/domain || rc=1; \
	 gremlins unleash --threshold-efficacy 65 --threshold-mcover 88 ./internal/application || rc=1; \
	 exit $$rc

# Fuzz targets at the parse boundaries (untrusted input). Seeds run automatically
# in `make test`/CI; this drives actual fuzzing. Crashers land in the package's
# testdata/fuzz/ and become permanent regression seeds.
FUZZ_TARGETS := internal/domain:FuzzDeriveSourceID \
	internal/adapters/storage:FuzzSafeJoin \
	internal/adapters/geopackage:FuzzExtractGeometryType \
	internal/adapters/http:FuzzParseQueryParams

fuzz: ## Fuzz alle Parse-Boundaries (FUZZTIME je Target überschreibbar, default 30s)
	@set -e; \
	ft="$${FUZZTIME:-30s}"; \
	case "$$ft" in ''|*[!0-9smhun]*) echo "FUZZTIME ungueltig (z.B. 30s, 3m)"; exit 1;; esac; \
	for tgt in $(FUZZ_TARGETS); do \
		pkg=$${tgt%%:*}; fn=$${tgt##*:}; \
		echo "==> fuzz $${fn} ($${pkg}) for $${ft}"; \
		$(GO) test "./$${pkg}/" -run='^$$' -fuzz="^$${fn}$$" -fuzztime="$${ft}"; \
	done

load-test: ## Lokaler Lasttest auf großen Quellen (setze ORTUS_LOADTEST_GPKG; siehe doc/load-test.md)
	@if [ -z "$(ORTUS_LOADTEST_GPKG)" ]; then \
		echo "ORTUS_LOADTEST_GPKG nicht gesetzt — siehe doc/load-test.md"; \
		echo "Beispiel: make load-test ORTUS_LOADTEST_GPKG=/data/big.gpkg ORTUS_LOADTEST_LAYER=parcels"; \
		exit 1; \
	fi
	$(GO) test -run='^$$' -bench=BenchmarkLoadTest -benchmem -benchtime=$(if $(BENCHTIME),$(BENCHTIME),3s) \
		$(if $(CPU),-cpu=$(CPU),) -v ./internal/adapters/geopackage/

load-stack-up: ## Observability-Stack starten (Grafana/Tempo/Loki/Prometheus) — siehe doc/load-test.md
	$(LOADTEST_COMPOSE) up -d
	@echo "Grafana:    http://localhost:3000  (anonym, Admin-Rolle)"
	@echo "Prometheus: http://localhost:9090"
	@echo "Tempo OTLP: localhost:4318 (HTTP) / :4317 (gRPC)"

load-stack-down: ## Observability-Stack stoppen (Daten-Volumes bleiben erhalten)
	$(LOADTEST_COMPOSE) down

load-stack-clean: ## Observability-Stack stoppen UND Daten-Volumes löschen
	$(LOADTEST_COMPOSE) down -v

load-serve: build ## ortus NATIV mit Tracing+Metrics starten (ORTUS_LOADTEST_DATA=Verzeichnis; SAMPLE überschreibbar)
	@if [ -z "$(ORTUS_LOADTEST_DATA)" ]; then \
		echo "ORTUS_LOADTEST_DATA (Verzeichnis mit großen GeoPackages) nicht gesetzt — siehe doc/load-test.md"; \
		exit 1; \
	fi
	@mkdir -p $(LOADTEST_DIR)/logs
	@bash -c 'set -o pipefail; \
		ORTUS_STORAGE_TYPE=local ORTUS_STORAGE_LOCAL_PATH=$(ORTUS_LOADTEST_DATA) \
		ORTUS_LOGGING_FORMAT=json ORTUS_METRICS_ENABLED=true ORTUS_METRICS_PORT=2112 \
		./$(BINARY_NAME) --tracing --tracing-endpoint=localhost:4318 --tracing-transport=http \
			--tracing-sample-ratio=$(if $(SAMPLE),$(SAMPLE),1.0) 2>&1 | tee $(LOADTEST_DIR)/logs/ortus.log'

load-attack: ## Last mit Vegeta erzeugen (RATE, DURATION, TARGETS überschreibbar)
	$(LOADTEST_COMPOSE) run --rm vegeta \
		"vegeta attack -targets=/vegeta/$(if $(TARGETS),$(TARGETS),targets.txt) -rate=$(if $(RATE),$(RATE),200) -duration=$(if $(DURATION),$(DURATION),30s) | tee /vegeta/results.bin | vegeta report"

## Lint & Analyse Targets
lint: lint-go ## Führe alle Linter aus (Alias für lint-go)

lint-go: ## Go Linting mit golangci-lint
	$(GOLINT) run --timeout=5m ./...

lint-fix: ## Linting mit Auto-Fix
	$(GOLINT) run --fix ./...

vet: ## Go vet
	$(GO) vet ./...

## Security Targets
security-check: vuln-check gosec ## Alle Security Checks

vuln-check: ## Prüfe auf bekannte Vulnerabilities
	govulncheck ./...

gosec: ## Security Scanner (via golangci-lint)
	$(GOLINT) run --enable-only gosec ./...

# Allowed dependency licenses (permissive only). First-party packages are
# ignored (the repo itself isn't classified by go-licenses).
ALLOWED_LICENSES := Apache-2.0,MIT,BSD-3-Clause,BSD-2-Clause,ISC,CC0-1.0,MPL-2.0
licenses: ## Lizenz-Compliance der Abhängigkeiten (go install github.com/google/go-licenses@latest)
	go-licenses check ./cmd/$(BINARY_NAME) --allowed_licenses=$(ALLOWED_LICENSES) --ignore $(MODULE)

## Format Targets
fmt: ## Formatiere Go Code
	$(GO) fmt ./...
	goimports -w -local $(MODULE) ./cmd ./internal ./api

format: fmt ## Alias für fmt

fmt-check: ## Prüfe Formatierung ohne zu ändern (CI/Hook)
	@unformatted=$$(gofmt -l cmd internal api); \
	if [ -n "$$unformatted" ]; then \
		echo "❌ Nicht formatiert (gofmt -w ausführen):"; echo "$$unformatted"; exit 1; \
	fi

## Quality Gate
check: fmt vet lint test ## Alle Qualitätsprüfungen (vor Commit)
	@echo "\n✅ Alle Prüfungen bestanden!"

# Kanonische, NICHT-mutierende Grün-Prüfung. Dies ist die maßgebliche Quelle
# für "ist es grün?" — Editor-/LSP-Diagnosen sind bei großen Renames unzuverlässig
# (siehe ADR/Memory); der Compiler entscheidet. Gleiche Schritte wie die CI.
# Bewusst KEIN Aufruf des `build`-Targets (das schreibt ./ortus); stattdessen
# ein binärloser Compile-Check via `go build ./...`.
verify: fmt-check vet lint test arch debt-guard ## Maßgebliche Grün-Prüfung (gofmt-check+vet+compile+test+lint+arch+debt)
	@echo "Compile-Check (go build ./...)…"
	@$(GO) build ./...
	@echo "\n✅ verify bestanden — Compile/Test/Lint/Format/Arch/Debt grün."

# Schulden-Harness: hält technische Schuld niedrig per Ratchet (siehe doc/tech-debt.md).
# `debt-guard` ist schnell (grep-basiert) und in `verify` eingebunden; `debt-coverage`
# fährt einen eigenen Coverage-Lauf und prüft die Per-Paket-Floors; `debt` bündelt beide.
debt: debt-guard debt-coverage ## Schulden-Ratchet: Suppression-Budget + Marker + Coverage-Floors

debt-guard: ## Schnelle Schulden-Checks (Suppression-Budget, Debt-Marker, Storage-Filter)
	@./scripts/debt-guard.sh

debt-coverage: ## Coverage-Floors prüfen (eigener Testlauf)
	@mkdir -p $(COVERAGE_DIR)
	@$(GO) test -coverprofile=$(COVERAGE_DIR)/coverage.out -covermode=atomic ./... >/dev/null
	@./scripts/coverage-gate.sh $(COVERAGE_DIR)/coverage.out

debt-deadcode: ## Advisory: unerreichbarer Code (manuelle Triage — Interface-/Test-Treffer sind False Positives)
	@$(GO) run golang.org/x/tools/cmd/deadcode@v0.45.0 ./cmd/ortus || true

# Architektur-Fitness: hexagonale Import-Grenzen (depguard), Modul-Blocklist
# (gomodguard) und go.mod-Hygiene. Eigenständig aufrufbar für einen fokussierten,
# schnellen Drift-Check; in `verify` und im CI-Job `architecture` eingebunden.
# (depguard/gomodguard laufen auch im vollen `lint`; hier explizit für klare Signale.)
arch: ## Architektur-Fitness: Import-Grenzen + Modul-Hygiene
	$(GOLINT) run --enable-only depguard,gomodguard_v2 ./...
	$(GO) mod tidy -diff
	@echo "✅ arch ok — Import-Grenzen & Modul-Hygiene grün."

hooks: ## Installiere git pre-commit Hook (.githooks)
	git config core.hooksPath .githooks
	@chmod +x .githooks/pre-commit
	@echo "✅ pre-commit Hook aktiv (core.hooksPath=.githooks)."

check-ci: ## CI-optimierte Prüfungen (mit Reports)
	@mkdir -p $(COVERAGE_DIR)
	$(GOLINT) run --output.junit-xml.path=$(COVERAGE_DIR)/lint-report.xml ./... || true
	$(GO) test -v -coverprofile=$(COVERAGE_DIR)/coverage.out -json ./... > $(COVERAGE_DIR)/test-report.json
	$(GO) tool cover -html=$(COVERAGE_DIR)/coverage.out -o $(COVERAGE_DIR)/coverage.html

## Clean
clean: ## Räume Build-Artefakte auf
	$(GO) clean
	rm -f $(BINARY_NAME)
	rm -rf $(BUILD_DIR)
	rm -rf $(COVERAGE_DIR)
	rm -f coverage.out coverage.html

## Dependencies
deps: ## Lade Dependencies
	$(GO) mod download

deps-update: ## Aktualisiere Dependencies
	$(GO) get -u ./...
	$(GO) mod tidy

deps-verify: ## Verifiziere Dependencies
	$(GO) mod verify

## Documentation
doc: ## Generiere Dokumentation
	@mkdir -p doc
	@rm -f doc/api.txt
	@for pkg in $$($(GO) list ./...); do \
		$(GO) doc -all $$pkg >> doc/api.txt; \
	done

doc-serve: ## Starte lokalen Dokumentationsserver
	@echo "Dokumentation unter http://localhost:6060/pkg/$(MODULE)/"
	godoc -http=:6060

# Diátaxis docs site (MkDocs Material). Python is kept out of the Go dev env via
# uvx (install `uv` from https://docs.astral.sh/uv/). `--strict` fails on broken
# links / nav so `make docs` doubles as the docs check.
MKDOCS := uvx --with mkdocs-material mkdocs
docs: ## Doku strict bauen (MkDocs Material via uvx) — failt bei kaputten Links/Nav
	$(MKDOCS) build --strict
docs-serve: ## Doku lokal mit Live-Reload servieren (http://localhost:8000)
	$(MKDOCS) serve

doc-drift: ## Doku-Drift-Harness: prüft Code ↔ OpenAPI ↔ Docs (0 = keine Drift)
	@bash .claude/skills/doc-drift-check/scripts/check-doc-drift.sh

## Release
release-dry: ## Teste Release (dry-run)
	goreleaser release --snapshot --clean

release: ## Erstelle Release
	goreleaser release --clean

## GitHub Actions lokal (via act)
ci-local: ## Führe alle CI-Jobs lokal aus (native Architektur)
	act

ci-lint: ## Führe nur Lint-Job lokal aus
	act -j lint

ci-test: ## Führe nur Test-Job lokal aus
	act -j test

ci-build: ## Führe nur Build-Job lokal aus
	act -j build

ci-dry: ## Zeige welche Jobs ausgeführt würden (dry-run)
	act -n

ci-amd64: ## CI mit amd64-Emulation (wie GitHub Actions)
	act --container-architecture linux/amd64

ci-check: ## Validiere GitHub Actions Workflows (actionlint)
	actionlint

## Hilfe
help: ## Zeige diese Hilfe
	@echo "ortus - Verfügbare Make-Targets:\n"
	@grep -hE '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'
	@echo ""

# Per-ticket isolierte Dev-Umgebungen (make dev-*). Am Ende eingebunden, damit
# `build` das Default-Target bleibt. Targets erscheinen automatisch in `make help`.
include deploy/dev/dev.mk
