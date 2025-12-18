# ortus Makefile
# Alle Standardaufgaben für Entwicklung und CI/CD

.PHONY: all build build-all install run clean help
.PHONY: test test-unit test-integration test-coverage test-race test-bench
.PHONY: lint lint-go lint-fix vet
.PHONY: security-check vuln-check gosec
.PHONY: fmt format
.PHONY: check check-ci
.PHONY: deps deps-update deps-verify
.PHONY: doc doc-serve
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

## Format Targets
fmt: ## Formatiere Go Code
	$(GO) fmt ./...
	goimports -w -local $(MODULE) ./cmd ./internal ./pkg ./api

format: fmt ## Alias für fmt

## Quality Gate
check: fmt vet lint test ## Alle Qualitätsprüfungen (vor Commit)
	@echo "\n✅ Alle Prüfungen bestanden!"

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
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'
	@echo ""
