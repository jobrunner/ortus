# Per-feature isolated dev environments (see deploy/dev/README.md).
# Included from the root Makefile. Each feature gets its own git worktree, its own
# ortus (live-reload) + claude containers, and a URL <name>.ortus.local via the
# shared Traefik proxy. Claude Code inside supports MCP + Remote Control (mobile).

DEV_DIR       := deploy/dev
DEV_COMPOSE   := docker compose -f $(DEV_DIR)/docker-compose.dev.yaml
INFRA_COMPOSE := docker compose -f $(DEV_DIR)/docker-compose.infra.yaml
DEV_NET       := ortus-dev
DEV_GOMOD_VOL := ortus-dev-gomod
DEV_AUTH_VOL  := ortus-dev-claude-auth
DEV_GH_VOL    := ortus-dev-gh-auth
WORKTREE_ROOT ?= ../ortus-worktrees
WORKTREE_ABS  := $(abspath $(WORKTREE_ROOT))
SHARED_DATA   ?= $(abspath ./data)
DEV_BASE      ?= master
# Single source of truth for the Claude Code CLI version (keeps dev-login and the
# claude image in sync). Bump here; overridable on the command line.
CLAUDE_CODE_VERSION ?= 2.1.209
export CLAUDE_CODE_VERSION

# The environment label is provided on the command line. NAME is the friendly
# alias (no ticket system needed): `make dev NAME=elevation-cache`. TICKET is kept
# as an equivalent alias for backwards-compat. If neither is given, dev/dev-new
# auto-generate one. Export TICKET so recipes sanitize it at RUN-time via the
# environment ($$TICKET) instead of make-level string interpolation (which would
# allow shell injection and would run on every make invocation).
TICKET ?= $(NAME)
export TICKET

# Vegeta load driver image (digest-pinned; matches deploy/loadtest).
DEV_VEGETA_IMAGE := peterevans/vegeta@sha256:eb65f499cd1b0f1402a56794d7711c49121db6ff8ea7d878513d76601ee0d502
OBS_COMPOSE      := docker compose -f $(DEV_DIR)/docker-compose.obs.yaml

# Runtime prelude sourced at the top of every dev-* recipe. Derives the safe
# feature label and the compose env from $TICKET, and exports them for all compose
# calls in the same recipe shell. Fails if the name is empty or sanitizes to "".
# ORTUS_MCP_TOKEN defaults to "-" so compose interpolation doesn't warn on
# lifecycle ops; dev-new overrides it with a freshly generated token.
define DEV_VARS
	set -e; \
	TICKET_SAFE=$$(export LC_ALL=C; printf '%s' "$$TICKET" | tr '[:upper:]' '[:lower:]' | tr -c 'a-z0-9-' '-' | sed 's/^-*//; s/-*$$//'); \
	if [ -z "$$TICKET_SAFE" ]; then echo "ERROR: NAME=<slug> erforderlich (saniert zu [a-z0-9-]), z.B. make dev NAME=elevation-cache"; exit 1; fi; \
	PROJECT="ortus-dev-$$TICKET_SAFE"; \
	WT="$(WORKTREE_ABS)/$$TICKET_SAFE"; \
	MAIN_GITDIR=$$(git rev-parse --git-common-dir 2>/dev/null || echo .git); \
	case "$$MAIN_GITDIR" in /*) ;; *) MAIN_GITDIR="$(CURDIR)/$$MAIN_GITDIR";; esac; \
	export COMPOSE_PROJECT_NAME="$$PROJECT" TICKET="$$TICKET_SAFE" ORTUS_WORKTREE="$$WT" ORTUS_SHARED_DATA="$(SHARED_DATA)" ORTUS_MAIN_GITDIR="$$MAIN_GITDIR" ORTUS_MCP_TOKEN="$${ORTUS_MCP_TOKEN:--}"
endef

# Auto-generate a name when neither NAME nor TICKET is given (only dev/dev-new).
# Used inside a recipe shell before $(DEV_VARS); exports TICKET so DEV_VARS sees it.
define AUTONAME
	if [ -z "$$TICKET" ]; then TICKET="feat-$$(date +%Y%m%d-%H%M%S)"; export TICKET; \
	  echo "Kein NAME angegeben -> generiere '$$TICKET'"; fi
endef

# Read the current MCP token from the running ortus container, or fail hard.
# Avoids silently re-upping with a "-" token (which breaks MCP auth pairing).
define READ_MCP_TOKEN
	TOKEN=$$($(DEV_COMPOSE) exec -T ortus printenv ORTUS_MCP_TOKEN 2>/dev/null || true); \
	if [ -z "$$TOKEN" ] || [ "$$TOKEN" = "-" ]; then echo "ERROR: MCP-Token nicht lesbar - laeuft ortus? ('make dev-new'/'make dev')"; exit 1; fi
endef

.PHONY: dev dev-up dev-obs dev-login dev-gh-login dev-new dev-attach dev-remote dev-remote-persist dev-remote-stop dev-perf dev-logs dev-list dev-destroy dev-dns-setup dev-doctor

dev: ## Dev: Feature anlegen/aktualisieren + Claude-Container betreten & Plan-Skill starten (NAME=<slug>)
	@$(AUTONAME); \
	 $(DEV_VARS); \
	 if [ -z "$$($(DEV_COMPOSE) ps -q ortus 2>/dev/null)" ] || [ -z "$$($(DEV_COMPOSE) ps -q claude 2>/dev/null)" ]; then \
	   $(MAKE) --no-print-directory dev-new TICKET="$$TICKET_SAFE"; \
	 fi; \
	 echo "Starte Claude im Container (Plan-Skill fuer '$$TICKET_SAFE') ..."; \
	 $(DEV_COMPOSE) exec claude claude "/plan-feature $$TICKET_SAFE"

dev-up: ## Dev: geteilte Infra (Traefik+Dozzle) + Netz/Volumes starten
	@docker network inspect $(DEV_NET) >/dev/null 2>&1 || docker network create $(DEV_NET)
	@docker volume inspect $(DEV_GOMOD_VOL) >/dev/null 2>&1 || docker volume create $(DEV_GOMOD_VOL)
	@docker volume inspect $(DEV_AUTH_VOL) >/dev/null 2>&1 || docker volume create $(DEV_AUTH_VOL)
	@docker volume inspect $(DEV_GH_VOL) >/dev/null 2>&1 || docker volume create $(DEV_GH_VOL)
	$(INFRA_COMPOSE) up -d
	@echo "Infra up.  Dashboard: http://traefik.ortus.local   Logs: http://logs.ortus.local"

dev-obs: ## Dev: Observability-Stack (Grafana/Tempo/Loki/Prometheus) am ortus-dev-Netz starten
	@docker network inspect $(DEV_NET) >/dev/null 2>&1 || { echo "ERROR: Netz $(DEV_NET) fehlt - zuerst 'make dev-up'."; exit 1; }
	$(OBS_COMPOSE) up -d
	@echo "Observability up.  Grafana: http://grafana.ortus.local"
	@echo "Traces erscheinen, sobald ein Feature mit Tracing laeuft (make dev-perf aktiviert es automatisch)."

dev-login: ## Dev: einmaliger Claude-OAuth-Login ins claude-auth Volume (fuer Remote Control)
	@docker volume inspect $(DEV_AUTH_VOL) >/dev/null 2>&1 || docker volume create $(DEV_AUTH_VOL)
	@echo "Claude startet interaktiv - fuehre den Login (/login) aus, danach mit Ctrl-D beenden."
	docker run --rm -it -e HOME=/root -v $(DEV_AUTH_VOL):/root/.claude \
		node:22.23.1-alpine sh -lc "npm i -g @anthropic-ai/claude-code@$(CLAUDE_CODE_VERSION) && claude"
	@echo "Login im Volume $(DEV_AUTH_VOL) gespeichert. Remote Control ist jetzt moeglich."

dev-gh-login: ## Dev: einmaliger GitHub-Login ins gh-auth Volume (fuer gh CLI + GitHub-MCP)
	@docker volume inspect $(DEV_GH_VOL) >/dev/null 2>&1 || docker volume create $(DEV_GH_VOL)
	@echo "gh startet interaktiv - fuehre 'gh auth login' aus (danach sind gh + GitHub-MCP nutzbar)."
	docker run --rm -it --user root -e HOME=/root -v $(DEV_GH_VOL):/root/.config/gh \
		ghcr.io/jobrunner/spatialite-base-image:alpine-dev-1.5.0 \
		sh -lc "apk add --no-cache github-cli >/dev/null 2>&1 && gh auth login && gh auth setup-git"
	@echo "GitHub-Login im Volume $(DEV_GH_VOL) gespeichert (gilt fuer alle Features)."

dev-new: ## Dev: isolierte Feature-Umgebung erstellen (NAME=<slug> [DEV_BASE=master])
	@$(AUTONAME); \
	 $(DEV_VARS); \
	 for res in "network $(DEV_NET)" "volume $(DEV_GOMOD_VOL)" "volume $(DEV_AUTH_VOL)" "volume $(DEV_GH_VOL)"; do \
	   docker $${res%% *} inspect $${res##* } >/dev/null 2>&1 || { echo "ERROR: $$res fehlt - zuerst 'make dev-up' ausfuehren."; exit 1; }; \
	 done; \
	 if git worktree list --porcelain 2>/dev/null | grep -qxF "worktree $$WT"; then \
	   echo "Worktree existiert bereits: $$WT"; \
	 elif git show-ref --verify --quiet "refs/heads/dev/$$TICKET_SAFE"; then \
	   git worktree add "$$WT" "dev/$$TICKET_SAFE"; \
	 else \
	   git worktree add -b "dev/$$TICKET_SAFE" "$$WT" "$(DEV_BASE)"; \
	 fi; \
	 if [ ! -f "$$WT/deploy/dev/Dockerfile.dev" ]; then \
	   echo "Hinweis: deploy/dev fehlt im Worktree (Base-Branch aelter als dieses Feature) - kopiere aus dem Hauptcheckout."; \
	   mkdir -p "$$WT/deploy/dev"; cp -R "$(DEV_DIR)/." "$$WT/deploy/dev/"; \
	 fi; \
	 [ -f "$$WT/.mcp.json" ] || cp "$(DEV_DIR)/mcp.json.tmpl" "$$WT/.mcp.json"; \
	 excl=$$(git -C "$$WT" rev-parse --git-path info/exclude); \
	 grep -qxF '.mcp.json' "$$excl" 2>/dev/null || echo '.mcp.json' >> "$$excl"; \
	 TOKEN=$$($(DEV_COMPOSE) exec -T ortus printenv ORTUS_MCP_TOKEN 2>/dev/null || true); \
	 if [ -z "$$TOKEN" ] || [ "$$TOKEN" = "-" ]; then TOKEN=$$(openssl rand -hex 24); fi; \
	 export ORTUS_MCP_TOKEN="$$TOKEN"; \
	 $(DEV_COMPOSE) up -d --build; \
	 printf '\n%s\n' "Feature '$$TICKET_SAFE' laeuft:"; \
	 echo "  API/Frontend : http://$$TICKET_SAFE.ortus.local"; \
	 echo "  Metrics      : http://metrics.$$TICKET_SAFE.ortus.local/metrics"; \
	 echo "  MCP          : http://mcp.$$TICKET_SAFE.ortus.local/mcp"; \
	 echo "  MCP-Token    : $$TOKEN  (auch als \$$ORTUS_MCP_TOKEN in den Containern)"; \
	 echo "  Logs         : http://logs.ortus.local"; \
	 echo "  Grafana      : http://grafana.ortus.local  (nach 'make dev-obs')"; \
	 echo "  Claude lokal : make dev NAME=$$TICKET_SAFE   (oder: make dev-attach NAME=$$TICKET_SAFE)"; \
	 echo "  Claude Handy : make dev-remote NAME=$$TICKET_SAFE  -> erscheint in der Claude-App unter 'Code'"; \
	 echo "  Perf-Test    : make dev-perf NAME=$$TICKET_SAFE"

dev-attach: ## Dev: lokale interaktive Claude-Code-Session im Feature-Container (NAME=<slug>)
	@$(DEV_VARS); \
	 $(DEV_COMPOSE) exec claude claude

dev-remote: ## Dev: Remote Control detached starten -> ueberlebt Terminal-Schliessen (NAME=<slug>)
	@$(DEV_VARS); \
	 $(DEV_COMPOSE) exec -d claude claude --remote-control --name "$$TICKET_SAFE"; \
	 echo "Remote Control (detached) fuer '$$TICKET_SAFE' gestartet - erscheint in der Claude-App unter 'Code'."; \
	 echo "Ueberlebt das Schliessen des Terminals; fuer Docker-/Mac-Neustart: make dev-remote-persist NAME=$$TICKET_SAFE"

dev-remote-persist: ## Dev: Remote Control als Container-Hauptprozess (ueberlebt Docker-/Mac-Neustart) (NAME=<slug>)
	@$(DEV_VARS); \
	 if [ -z "$$($(DEV_COMPOSE) ps -q claude 2>/dev/null)" ]; then echo "ERROR: Feature '$$TICKET_SAFE' laeuft nicht - zuerst 'make dev-new' oder 'make dev'."; exit 1; fi; \
	 if ! $(DEV_COMPOSE) exec -T claude test -f /root/.claude/.credentials.json 2>/dev/null; then echo "ERROR: Kein Claude-Login im Volume - zuerst 'make dev-login'."; exit 1; fi; \
	 $(READ_MCP_TOKEN); \
	 echo "Aktiviere persistente Remote Control fuer '$$TICKET_SAFE' ..."; \
	 ORTUS_MCP_TOKEN="$$TOKEN" CLAUDE_REMOTE_PERSIST=true $(DEV_COMPOSE) up -d claude; \
	 echo "Remote Control laeuft als Hauptprozess (restart: unless-stopped) - kommt nach Neustart automatisch zurueck in die App als '$$TICKET_SAFE'."; \
	 echo "Abschalten: make dev-remote-stop NAME=$$TICKET_SAFE"

dev-remote-stop: ## Dev: persistente Remote Control abschalten, Container zurueck auf idle (NAME=<slug>)
	@$(DEV_VARS); \
	 if [ -z "$$($(DEV_COMPOSE) ps -q claude 2>/dev/null)" ]; then echo "ERROR: Feature '$$TICKET_SAFE' laeuft nicht."; exit 1; fi; \
	 $(READ_MCP_TOKEN); \
	 ORTUS_MCP_TOKEN="$$TOKEN" CLAUDE_REMOTE_PERSIST=false $(DEV_COMPOSE) up -d claude; \
	 echo "Persistente Remote Control aus. Container idle; lokal: make dev-attach; ad-hoc remote: make dev-remote."

dev-perf: ## Dev: Vegeta-Lasttest gegen das Feature + Traces/Metriken in Grafana (NAME=<slug> [RATE=200 DURATION=30s])
	@$(DEV_VARS); \
	 if [ -z "$$($(DEV_COMPOSE) ps -q ortus 2>/dev/null)" ]; then echo "ERROR: Feature '$$TICKET_SAFE' laeuft nicht - zuerst 'make dev-new' oder 'make dev'."; exit 1; fi; \
	 docker ps --filter name=ortus-dev-obs --format '{{.Names}}' | grep -q . || echo "Hinweis: Observability nicht aktiv - 'make dev-obs' fuer Traces/Dashboards."; \
	 $(READ_MCP_TOKEN); \
	 R=$${RATE:-200}; case "$$R" in ''|*[!0-9]*) echo "ERROR: RATE muss eine Zahl sein (req/s), z.B. RATE=500."; exit 1;; esac; \
	 D=$${DURATION:-30s}; case "$$D" in ''|*[!0-9smhun]*) echo "ERROR: DURATION ungueltig, z.B. DURATION=30s / 1m."; exit 1;; esac; \
	 echo "Aktiviere Tracing fuer '$$TICKET_SAFE' (service_name=$$TICKET_SAFE) ..."; \
	 ORTUS_MCP_TOKEN="$$TOKEN" ORTUS_TRACING_ENABLED=true $(DEV_COMPOSE) up -d ortus >/dev/null; \
	 sleep 3; \
	 echo "Vegeta: rate=$$R duration=$$D -> http://$$TICKET_SAFE.ortus.local"; \
	 docker run --rm --platform linux/amd64 --network $(DEV_NET) \
	   -e VG_RATE="$$R" -e VG_DUR="$$D" -e VG_HOST="$$TICKET_SAFE.ortus.local" \
	   -v "$(abspath $(DEV_DIR)/vegeta-targets.txt)":/targets.txt:ro $(DEV_VEGETA_IMAGE) \
	   sh -c 'vegeta attack -targets=/targets.txt -header "Host: $$VG_HOST" -rate=$$VG_RATE -duration=$$VG_DUR | vegeta report'; \
	 echo ""; \
	 echo "Traces/Dashboards: http://grafana.ortus.local  (Explore -> Tempo, service.name=\"$$TICKET_SAFE\")"

dev-logs: ## Dev: ortus-Logs des Features folgen (NAME=<slug>)
	@$(DEV_VARS); \
	 $(DEV_COMPOSE) logs -f ortus

dev-list: ## Dev: laufende Feature-Umgebungen + Worktrees auflisten
	@docker ps --filter "name=ortus-dev-" --format 'table {{.Names}}\t{{.Status}}' | grep -v 'ortus-dev-infra' || true
	@echo "--- worktrees ---"; git worktree list | grep -F "$(WORKTREE_ABS)" || true

dev-destroy: ## Dev: Feature-Umgebung + Worktree + Branch entfernen (NAME=<slug>)
	@$(DEV_VARS); \
	 $(DEV_COMPOSE) down -v || true; \
	 git worktree remove --force "$$WT" || true; \
	 git worktree prune; \
	 if git show-ref --verify --quiet "refs/heads/dev/$$TICKET_SAFE"; then \
	   if ! git branch -d "dev/$$TICKET_SAFE" 2>/dev/null; then \
	     echo "WARN: Branch dev/$$TICKET_SAFE ist nicht gemergt - NICHT geloescht. Manuell: git branch -D dev/$$TICKET_SAFE"; \
	   fi; \
	 fi; \
	 echo "Entfernt: $$TICKET_SAFE (Container, per-Feature Build-Volume, Worktree; Branch nur wenn gemergt)."

dev-dns-setup: ## Dev: Anleitung fuer einmalige dnsmasq-Einrichtung (*.ortus.local -> 127.0.0.1)
	@echo "Einmalig auf dem Mac (siehe deploy/dev/README.md):"
	@echo "  brew install dnsmasq"
	@echo "  # Port 5353 (unprivilegiert) -> kein root/sudo fuer brew noetig"
	@echo "  printf 'port=5353\\naddress=/ortus.local/127.0.0.1\\n' >> \$$(brew --prefix)/etc/dnsmasq.conf"
	@echo "  sudo mkdir -p /etc/resolver"
	@echo "  printf 'nameserver 127.0.0.1\\nport 5353\\n' | sudo tee /etc/resolver/ortus.local"
	@echo "  brew services restart dnsmasq"
	@echo "  # pruefen: dscacheutil -q host -a name probe.ortus.local  -> 127.0.0.1"

dev-doctor: ## Dev: DNS + Netzwerk + Traefik + Dozzle + Auth-Volume pruefen
	@printf 'DNS *.ortus.local -> 127.0.0.1 ... '; \
	 if command -v dscacheutil >/dev/null 2>&1; then \
	   dscacheutil -q host -a name probe.ortus.local 2>/dev/null | grep -q '127.0.0.1' && echo OK || echo "FAIL (make dev-dns-setup)"; \
	 elif command -v getent >/dev/null 2>&1; then \
	   getent hosts probe.ortus.local 2>/dev/null | grep -q '127.0.0.1' && echo OK || echo "FAIL (make dev-dns-setup)"; \
	 else echo "SKIP (weder dscacheutil noch getent vorhanden)"; fi
	@printf 'network %s ............... ' "$(DEV_NET)"; docker network inspect $(DEV_NET) >/dev/null 2>&1 && echo OK || echo "FAIL (make dev-up)"
	@printf 'traefik ...................... '; docker ps --filter name=ortus-dev-infra --format '{{.Names}}' | grep -q traefik && echo OK || echo "FAIL (make dev-up)"
	@printf 'dozzle ....................... '; docker ps --filter name=ortus-dev-infra --format '{{.Names}}' | grep -q dozzle && echo OK || echo "FAIL (make dev-up)"
	@printf 'claude-auth volume .......... '; docker volume inspect $(DEV_AUTH_VOL) >/dev/null 2>&1 && echo OK || echo "FAIL (make dev-up + dev-login)"
