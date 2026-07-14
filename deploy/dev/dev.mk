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
SHARED_DATA   ?= $(abspath ./data)
DEV_BASE      ?= master

# Sanitize TICKET to a DNS/compose-safe label (lowercase, [a-z0-9-], trimmed).
TICKET_SAFE := $(shell printf '%s' '$(TICKET)' | tr '[:upper:]' '[:lower:]' | tr -c 'a-z0-9-' '-' | sed 's/^-*//; s/-*$$//')
PROJECT     := ortus-dev-$(TICKET_SAFE)
WT          := $(abspath $(WORKTREE_ROOT)/$(TICKET_SAFE))
# Env the per-ticket compose template needs. ORTUS_MCP_TOKEN is only meaningful for
# dev-new; lifecycle ops pass a dummy so compose interpolation doesn't warn.
DEV_ENV      = COMPOSE_PROJECT_NAME=$(PROJECT) TICKET=$(TICKET_SAFE) ORTUS_WORKTREE="$(WT)" ORTUS_SHARED_DATA="$(SHARED_DATA)" ORTUS_MCP_TOKEN=$(or $(ORTUS_MCP_TOKEN),-)

.PHONY: dev-up dev-login dev-new dev-attach dev-remote dev-logs dev-list dev-destroy dev-dns-setup dev-doctor

define require_ticket
	@if [ -z "$(TICKET)" ]; then echo "ERROR: TICKET=<name> erforderlich, z.B. make dev-new TICKET=ORT-123"; exit 1; fi
endef

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
		node:22-alpine sh -lc "npm i -g @anthropic-ai/claude-code >/dev/null 2>&1 && claude"
	@echo "Login im Volume $(DEV_AUTH_VOL) gespeichert. Remote Control ist jetzt moeglich."

dev-new: ## Dev: isolierte Ticket-Umgebung erstellen (TICKET=<name> [DEV_BASE=master])
	$(call require_ticket)
	@git show-ref --verify --quiet refs/heads/dev/$(TICKET_SAFE) \
		&& git worktree add "$(WT)" dev/$(TICKET_SAFE) \
		|| git worktree add -b dev/$(TICKET_SAFE) "$(WT)" $(DEV_BASE)
	@cp $(DEV_DIR)/mcp.json.tmpl "$(WT)/.mcp.json"
	@grep -qxF '.mcp.json' "$(WT)/.git/info/exclude" 2>/dev/null || echo '.mcp.json' >> "$(WT)/.git/info/exclude"
	@TOKEN=$$(openssl rand -hex 24); \
	 COMPOSE_PROJECT_NAME=$(PROJECT) TICKET=$(TICKET_SAFE) ORTUS_WORKTREE="$(WT)" \
	   ORTUS_SHARED_DATA="$(SHARED_DATA)" ORTUS_MCP_TOKEN=$$TOKEN $(DEV_COMPOSE) up -d --build
	@echo ""
	@echo "Ticket '$(TICKET_SAFE)' laeuft:"
	@echo "  API/Frontend : http://$(TICKET_SAFE).ortus.local"
	@echo "  Metrics      : http://metrics.$(TICKET_SAFE).ortus.local/metrics"
	@echo "  MCP          : http://mcp.$(TICKET_SAFE).ortus.local/mcp  (Bearer-Token generiert)"
	@echo "  Logs         : http://logs.ortus.local"
	@echo "  Claude lokal : make dev-attach TICKET=$(TICKET_SAFE)"
	@echo "  Claude Handy : make dev-remote TICKET=$(TICKET_SAFE)  -> erscheint in der Claude-App unter 'Code'"

dev-attach: ## Dev: lokale interaktive Claude-Code-Session im Ticket-Container (TICKET=<name>)
	$(call require_ticket)
	$(DEV_ENV) $(DEV_COMPOSE) exec claude claude

dev-remote: ## Dev: Claude-Code mit Remote Control starten -> Claude Mobile App (TICKET=<name>)
	$(call require_ticket)
	@echo "Falls das Flag abweicht, in-Container 'claude --help' pruefen."
	$(DEV_ENV) $(DEV_COMPOSE) exec claude claude --remote-control --name "$(TICKET_SAFE)"

dev-logs: ## Dev: ortus-Logs des Tickets folgen (TICKET=<name>)
	$(call require_ticket)
	$(DEV_ENV) $(DEV_COMPOSE) logs -f ortus

dev-list: ## Dev: laufende Ticket-Umgebungen + Worktrees auflisten
	@docker ps --filter "name=ortus-dev-" --format 'table {{.Names}}\t{{.Status}}'
	@echo "--- worktrees ---"; git worktree list | grep -i ortus-worktrees || true

dev-destroy: ## Dev: Ticket-Umgebung + Worktree + Branch entfernen (TICKET=<name>)
	$(call require_ticket)
	-$(DEV_ENV) $(DEV_COMPOSE) down -v
	-git worktree remove --force "$(WT)"
	-git branch -D dev/$(TICKET_SAFE)
	@git worktree prune
	@echo "Entfernt: $(TICKET_SAFE) (Container, per-Ticket Build-Volume, Worktree, Branch)."

dev-dns-setup: ## Dev: Anleitung fuer einmalige dnsmasq-Einrichtung (*.ortus.local -> 127.0.0.1)
	@echo "Einmalig auf dem Mac (siehe deploy/dev/README.md):"
	@echo "  brew install dnsmasq"
	@echo "  echo 'address=/ortus.local/127.0.0.1' >> \$$(brew --prefix)/etc/dnsmasq.conf"
	@echo "  sudo mkdir -p /etc/resolver"
	@echo "  echo 'nameserver 127.0.0.1' | sudo tee /etc/resolver/ortus.local"
	@echo "  sudo brew services restart dnsmasq"
	@echo "  # pruefen: dscacheutil -q host -a name probe.ortus.local  -> 127.0.0.1"

dev-doctor: ## Dev: DNS + Netzwerk + Traefik + Dozzle + Auth-Volume pruefen
	@printf 'DNS *.ortus.local -> 127.0.0.1 ... '; \
	 dscacheutil -q host -a name probe.ortus.local 2>/dev/null | grep -q '127.0.0.1' && echo OK || echo "FAIL (make dev-dns-setup)"
	@printf 'network %s ............... ' "$(DEV_NET)"; docker network inspect $(DEV_NET) >/dev/null 2>&1 && echo OK || echo "FAIL (make dev-up)"
	@printf 'traefik ...................... '; docker ps --filter name=ortus-dev-infra --format '{{.Names}}' | grep -q traefik && echo OK || echo "FAIL (make dev-up)"
	@printf 'dozzle ....................... '; docker ps --filter name=ortus-dev-infra --format '{{.Names}}' | grep -q dozzle && echo OK || echo "FAIL (make dev-up)"
	@printf 'claude-auth volume .......... '; docker volume inspect $(DEV_AUTH_VOL) >/dev/null 2>&1 && echo OK || echo "FAIL (make dev-up + dev-login)"
