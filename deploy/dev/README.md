# Per-Feature Dev-Umgebungen (`make dev-*`)

Isolierte ortus-Entwicklungsumgebungen pro Feature — **kein Ticketsystem nötig**:
eigener Git-Worktree/Branch, eigener Live-Reload-ortus-Container **und** ein
Container mit Claude Code, erreichbar über `http://<name>.ortus.local`, mit MCP,
Metrics, Logs, Traces und fernsteuerbar aus der **Claude Mobile App** (Remote
Control).

Der Claude-Container ist **deterministisch aus dem Image**: Plugins (superpowers,
playwright, context7, gopls-lsp), der Go-Language-Server und die Repo-Skills sind
fest gebacken/gemountet. **Für dieses Projekt musst du auf dem Host nichts pflegen**
— einzige einmalige, *kontobezogene* Host-Aktion ist der Claude-OAuth-Login.

Alles läuft über einen geteilten **Traefik**-Proxy; nur Traefik bindet einen
Host-Port (`127.0.0.1:80`), die Feature-Container nicht → **keine Port-Kollisionen**.
Die großen Datenpakete werden **read-only geteilt** (nie pro Feature dupliziert).

## Einmalige Einrichtung (Mac) — nur diese drei Dinge

1. **DNS** (`*.ortus.local` → 127.0.0.1) via dnsmasq auf dem unprivilegierten
   Port 5353 (kein `sudo brew`):
   ```sh
   brew install dnsmasq
   printf 'port=5353\naddress=/ortus.local/127.0.0.1\n' >> "$(brew --prefix)/etc/dnsmasq.conf"
   sudo mkdir -p /etc/resolver
   printf 'nameserver 127.0.0.1\nport 5353\n' | sudo tee /etc/resolver/ortus.local
   brew services restart dnsmasq
   dscacheutil -q host -a name probe.ortus.local   # -> 127.0.0.1
   ```
   (`make dev-dns-setup` zeigt diese Schritte.)
2. **Geteilte Infra** starten: `make dev-up` (Netz `ortus-dev`, Volumes, Traefik + Dozzle).
3. **Claude-Login** (kontobezogen, für Remote Control, OAuth-Abo — kein API-Key):
   `make dev-login`, im Container `/login` ausführen, dann `Ctrl-D`. Landet im
   Volume `ortus-dev-claude-auth` und gilt für alle Features. OAuth-Token laufen
   nach einigen Tagen ab → dann erneut `make dev-login`.

Optional: `make dev-doctor` prüft DNS, Netz, Traefik, Dozzle, Auth-Volume.

## Feature-Lebenszyklus (ohne Tickets)

```sh
make dev         NAME=elevation-cache    # Env anlegen + Claude-Container betreten + Plan-Skill starten
make dev-obs                             # Observability-Stack (Grafana/Tempo/Loki/Prometheus) starten
make dev-perf    NAME=elevation-cache    # Vegeta-Lasttest + Traces/Metriken in Grafana
make dev-remote  NAME=elevation-cache    # Claude-Session mit Remote Control -> Mobile App
make dev-logs    NAME=elevation-cache    # ortus-Logs folgen (oder http://logs.ortus.local)
make dev-list                            # laufende Umgebungen + Worktrees
make dev-destroy NAME=elevation-cache    # Container + Build-Volume + Worktree + (gemergter) Branch entfernen
```

`NAME=` ist der sprechende Feature-Slug; `TICKET=` funktioniert weiterhin als
Alias. Ohne beides generieren `dev`/`dev-new` einen Namen (`feat-JJJJMMTT-HHMM`).
Der Name wird für Hostname/Compose saniert (Kleinbuchstaben, `[a-z0-9-]`).

**Der Happy Path:** `make dev NAME=<slug>` → der `plan-feature`-Skill interviewt
dich, schreibt einen Plan nach `.claude/plans/` und reviewt ihn gegen den Code →
„go" → implementieren → `make dev-perf` → PR → `make dev-destroy`.

Nach `make dev`/`make dev-new`:
- `http://<name>.ortus.local` — API + Frontend (`/health/live`, `/api/v1/...`, `/docs`)
- `http://metrics.<name>.ortus.local/metrics` — Prometheus-Metriken
- `http://mcp.<name>.ortus.local/mcp` — MCP (Bearer-Token; pro Feature generiert)
- `http://logs.ortus.local` — Dozzle-Log-Viewer
- `http://grafana.ortus.local` — Grafana (nach `make dev-obs`)

## Was im Claude-Container schon da ist (deterministisch)

- **Plugins** (gepinnt, ins Image gebacken, beim Start ins Auth-Volume geseedet):
  `superpowers`, `playwright` (inkl. Chromium), `context7`, `gopls-lsp`.
- **Language Server**: `gopls` (für `gopls-lsp`), nativ nutzbar.
- **Skills**: die Repo-Skills (`.claude/skills/`, gemountet) — u.a. `plan-feature`,
  `perf-test`, `doc-drift-check`, die Package-Build-Skills.
- **MCP**: `ortus` (live-reloadende Instanz über `.mcp.json`, Bearer aus
  `$ORTUS_MCP_TOKEN` — **kein Token auf Platte**) + `context7` (Library-Docs).
- **Toolchain**: Go + CGO + SpatiaLite (kann `make build`/`make test`/`make verify`).

Aktualisierung = Image neu bauen (`make dev-new` baut mit); der Entrypoint
(`claude-entrypoint.sh`) seedet die gebackenen Plugins/Settings bei jedem Start
neu ins Auth-Volume, **ohne** die OAuth-Credentials anzufassen.

## Remote Control (Claude Mobile App)

`make dev-remote NAME=<slug>` → die Session erscheint in der Claude-App unter
**Code** (und claude.ai/code). Nur **ausgehende** HTTPS-Verbindung zu Anthropic —
keine eingehenden Ports/Tunnel/VPN. Voraussetzung: `make dev-login` (OAuth). Das
exakte Flag ggf. mit `claude --help` im Container abgleichen (die CLI entwickelt sich).

## Observability & Performance

`make dev-obs` bringt Grafana/Tempo/Loki/Prometheus ans `ortus-dev`-Netz.
Prometheus findet alle Feature-`:9090` per **Docker-Service-Discovery** automatisch.
`make dev-perf NAME=<slug>` aktiviert Tracing für das Feature
(`service_name=<slug>`), feuert **Vegeta** über Traefik gegen `http://<slug>.ortus.local`
und gibt Report + Grafana-Hinweis aus. Details: Skill `perf-test`.

## Live-Reload

Der `ortus`-Container läuft `air` (`.air.toml`) auf dem gemounteten Worktree:
Code ändern → air baut neu (CGO + SpatiaLite) und startet neu. Erster Build ist der
langsamste; Modul-/Build-Cache sind Volumes.

## Sicherheit / Grenzen (Dev-only)

- Traefik + Dozzle + Prometheus mounten den **Docker-Socket** (`:ro` reduziert die
  Rechte nicht — API-Zugriff ist root-äquivalent). Nur lokal betreiben (Traefik
  bindet `127.0.0.1`).
- Kein TLS (nur `http`/`:80`). Upgrade-Pfad: `mkcert` + `*.ortus.local`-Cert +
  Traefik-`websecure:443`.
- Grafana läuft anonym mit Admin-Rolle (Dev). Alle Features teilen ein
  `claude-auth`-Volume (dasselbe Konto); Sessions per `--name` unterschieden.

## Dateien

- `docker-compose.infra.yaml` — geteiltes Traefik + Dozzle.
- `docker-compose.obs.yaml` + `prometheus.dev.yaml` — Observability am ortus-dev-Netz
  (Tempo/Loki/Grafana-Configs aus `deploy/loadtest` wiederverwendet).
- `docker-compose.dev.yaml` — Per-Feature-Vorlage (env-parametrisiert).
- `Dockerfile.dev` + `.air.toml` — Live-Reload-ortus-Image.
- `Dockerfile.claude` + `claude-entrypoint.sh` + `claude-settings.json` —
  deterministisches Claude-Code-Image (Plugins/LSP gebacken).
- `vegeta-targets.txt` — Lastziele für `make dev-perf`.
- `mcp.json.tmpl` — wird als `<worktree>/.mcp.json` kopiert.
- `dev.mk` — die `make dev-*`-Targets (aus dem Root-Makefile via `include`).
