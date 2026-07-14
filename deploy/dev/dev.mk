# Per-ticket isolated dev environments (see deploy/dev/README.md).
# Included from the root Makefile. Each ticket gets its own git worktree, its own
# ortus (live-reload) + claude containers, and a URL <ticket>.ortus.local via the
# shared Traefik proxy. Claude Code inside supports MCP + Remote Control (mobile).

DEV_DIR       := deploy/dev
DEV_COMPOSE   := docker compose -f $(DEV_DIR)/docker-compose.dev.yaml
INFRA_COMPOSE := docker compose -f $(DEV_DIR)/docker-compose.infra.yaml
DEV_NET       := ortus-dev
DEV_GOMOD_VOL := ortus-dev-gomod
DEV_AUTH_VOL  := ortus-dev-claude-auth
WORKTREE_ROOT ?= ../ortus-worktrees
WORKTREE_ABS  := $(abspath $(WORKTREE_ROOT))
SHARED_DATA   ?= $(abspath ./data)
DEV_BASE      ?= master
# Single source of truth for the Claude Code CLI version (keeps dev-login and the
# claude image in sync). Bump here; overridable on the command line.
CLAUDE_CODE_VERSION ?= 2.1.209
export CLAUDE_CODE_VERSION

# TICKET is provided on the command line (e.g. `make dev-new TICKET=ORT-123`).
# Export it so recipes sanitize it at RUN-time via the environment ($$TICKET),
# instead of make-level string interpolation into a shell command — the latter
# would allow shell injection and would run on every make invocation.
export TICKET

# Runtime prelude sourced at the top of every dev-* recipe. Derives the safe
# ticket label and the compose env from $TICKET, and exports them for all compose
# calls in the same recipe shell. Fails if TICKET is empty or sanitizes to "".
# ORTUS_MCP_TOKEN defaults to "-" so compose interpolation doesn't warn on
# lifecycle ops; dev-new overrides it with a freshly generated token.
define DEV_VARS
	set -e; \
	TICKET_SAFE=$$(export LC_ALL=C; printf '%s' "$$TICKET" | tr '[:upper:]' '[:lower:]' | tr -c 'a-z0-9-' '-' | sed 's/^-*//; s/-*$$//'); \
	if [ -z "$$TICKET_SAFE" ]; then echo "ERROR: TICKET=<name> erforderlich (saniert zu [a-z0-9-]), z.B. make dev-new TICKET=ORT-123"; exit 1; fi; \
	PROJECT="ortus-dev-$$TICKET_SAFE"; \
	WT="$(WORKTREE_ABS)/$$TICKET_SAFE"; \
	export COMPOSE_PROJECT_NAME="$$PROJECT" TICKET="$$TICKET_SAFE" ORTUS_WORKTREE="$$WT" ORTUS_SHARED_DATA="$(SHARED_DATA)" ORTUS_MCP_TOKEN="$${ORTUS_MCP_TOKEN:--}"
endef

.PHONY: dev-up dev-login dev-new dev-attach dev-remote dev-logs dev-list dev-destroy dev-dns-setup dev-doctor

dev-up: ## Dev: geteilte Infra (Traefik+Dozzle) + Netz/Volumes starten
	@docker network inspect $(DEV_NET) >/dev/null 2>&1 || docker network create $(DEV_NET)
	@docker volume inspect $(DEV_GOMOD_VOL) >/dev/null 2>&1 || docker volume create $(DEV_GOMOD_VOL)
	@docker volume inspect $(DEV_AUTH_VOL) >/dev/null 2>&1 || docker volume create $(DEV_AUTH_VOL)
	$(INFRA_COMPOSE) up -d
	@echo "Infra up.  Dashboard: http://traefik.ortus.local   Logs: http://logs.ortus.local"

dev-login: ## Dev: einmaliger Claude-OAuth-Login ins claude-auth Volume (fuer Remote Control)
	@docker volume inspect $(DEV_AUTH_VOL) >/dev/null 2>&1 || docker volume create $(DEV_AUTH_VOL)
	@echo "Claude startet interaktiv - fuehre den Login (/login) aus, danach mit Ctrl-D beenden."
	docker run --rm -it -e HOME=/root -v $(DEV_AUTH_VOL):/root/.claude \
		node:22.23.1-alpine sh -lc "npm i -g @anthropic-ai/claude-code@$(CLAUDE_CODE_VERSION) && claude"
	@echo "Login im Volume $(DEV_AUTH_VOL) gespeichert. Remote Control ist jetzt moeglich."

dev-new: ## Dev: isolierte Ticket-Umgebung erstellen (TICKET=<name> [DEV_BASE=master])
	@$(DEV_VARS); \
	 for res in "network $(DEV_NET)" "volume $(DEV_GOMOD_VOL)" "volume $(DEV_AUTH_VOL)"; do \
	   docker $${res%% *} inspect $${res##* } >/dev/null 2>&1 || { echo "ERROR: $$res fehlt - zuerst 'make dev-up' ausfuehren."; exit 1; }; \
	 done; \
	 if git show-ref --verify --quiet "refs/heads/dev/$$TICKET_SAFE"; then \
	   git worktree add "$$WT" "dev/$$TICKET_SAFE"; \
	 else \
	   git worktree add -b "dev/$$TICKET_SAFE" "$$WT" "$(DEV_BASE)"; \
	 fi; \
	 if [ ! -f "$$WT/deploy/dev/Dockerfile.dev" ]; then \
	   echo "Hinweis: deploy/dev fehlt im Worktree (Base-Branch aelter als dieses Feature) - kopiere aus dem Hauptcheckout."; \
	   mkdir -p "$$WT/deploy/dev"; cp -R "$(DEV_DIR)/." "$$WT/deploy/dev/"; \
	 fi; \
	 cp "$(DEV_DIR)/mcp.json.tmpl" "$$WT/.mcp.json"; \
	 excl=$$(git -C "$$WT" rev-parse --git-path info/exclude); \
	 grep -qxF '.mcp.json' "$$excl" 2>/dev/null || echo '.mcp.json' >> "$$excl"; \
	 TOKEN=$$(openssl rand -hex 24); export ORTUS_MCP_TOKEN="$$TOKEN"; \
	 $(DEV_COMPOSE) up -d --build; \
	 printf '\n%s\n' "Ticket '$$TICKET_SAFE' laeuft:"; \
	 echo "  API/Frontend : http://$$TICKET_SAFE.ortus.local"; \
	 echo "  Metrics      : http://metrics.$$TICKET_SAFE.ortus.local/metrics"; \
	 echo "  MCP          : http://mcp.$$TICKET_SAFE.ortus.local/mcp"; \
	 echo "  MCP-Token    : $$TOKEN  (auch als \$$ORTUS_MCP_TOKEN in den Containern)"; \
	 echo "  Logs         : http://logs.ortus.local"; \
	 echo "  Claude lokal : make dev-attach TICKET=$$TICKET_SAFE"; \
	 echo "  Claude Handy : make dev-remote TICKET=$$TICKET_SAFE  -> erscheint in der Claude-App unter 'Code'"

dev-attach: ## Dev: lokale interaktive Claude-Code-Session im Ticket-Container (TICKET=<name>)
	@$(DEV_VARS); \
	 $(DEV_COMPOSE) exec claude claude

dev-remote: ## Dev: Claude-Code mit Remote Control starten -> Claude Mobile App (TICKET=<name>)
	@$(DEV_VARS); \
	 echo "Falls das Flag abweicht, in-Container 'claude --help' pruefen."; \
	 $(DEV_COMPOSE) exec claude claude --remote-control --name "$$TICKET_SAFE"

dev-logs: ## Dev: ortus-Logs des Tickets folgen (TICKET=<name>)
	@$(DEV_VARS); \
	 $(DEV_COMPOSE) logs -f ortus

dev-list: ## Dev: laufende Ticket-Umgebungen + Worktrees auflisten
	@docker ps --filter "name=ortus-dev-" --format 'table {{.Names}}\t{{.Status}}' | grep -v 'ortus-dev-infra' || true
	@echo "--- worktrees ---"; git worktree list | grep -F "$(WORKTREE_ABS)" || true

dev-destroy: ## Dev: Ticket-Umgebung + Worktree + Branch entfernen (TICKET=<name>)
	@$(DEV_VARS); \
	 $(DEV_COMPOSE) down -v || true; \
	 git worktree remove --force "$$WT" || true; \
	 git worktree prune; \
	 if git show-ref --verify --quiet "refs/heads/dev/$$TICKET_SAFE"; then \
	   if ! git branch -d "dev/$$TICKET_SAFE" 2>/dev/null; then \
	     echo "WARN: Branch dev/$$TICKET_SAFE ist nicht gemergt - NICHT geloescht. Manuell: git branch -D dev/$$TICKET_SAFE"; \
	   fi; \
	 fi; \
	 echo "Entfernt: $$TICKET_SAFE (Container, per-Ticket Build-Volume, Worktree; Branch nur wenn gemergt)."

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
