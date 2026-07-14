# Per-Ticket Dev-Umgebungen (`make dev-*`)

Isolierte ortus-Entwicklungsumgebungen pro Ticket: eigener Git-Worktree/Branch, eigener
Live-Reload-ortus-Container **und** ein Container mit Claude Code — erreichbar über
`http://<ticket>.ortus.local`, mit MCP, Metrics und Logs, und fernsteuerbar aus der
**Claude Mobile App** (Remote Control).

Alles läuft über einen geteilten **Traefik**-Proxy; nur Traefik bindet einen Host-Port (`:80`),
die Ticket-Container nicht → **keine Port-Kollisionen**. Die großen Datenpakete werden
**read-only geteilt** (nie pro Ticket dupliziert).

## Einmalige Einrichtung (Mac)

1. **DNS** (`*.ortus.local` → 127.0.0.1) via dnsmasq:
   ```sh
   brew install dnsmasq
   # Listen on the unprivileged port 5353 so dnsmasq needs no root (no `sudo brew`).
   printf 'port=5353\naddress=/ortus.local/127.0.0.1\n' >> "$(brew --prefix)/etc/dnsmasq.conf"
   sudo mkdir -p /etc/resolver
   printf 'nameserver 127.0.0.1\nport 5353\n' | sudo tee /etc/resolver/ortus.local
   brew services restart dnsmasq
   dscacheutil -q host -a name probe.ortus.local   # -> 127.0.0.1
   ```
   (`make dev-dns-setup` zeigt diese Schritte.)
2. **Geteilte Infra** starten: `make dev-up` (Netz `ortus-dev`, Volumes, Traefik + Dozzle).
3. **Claude-Login** (für Remote Control, OAuth-Abo — kein API-Key): `make dev-login`,
   im Container `/login` ausführen, dann `Ctrl-D`. Die Credentials landen im Volume
   `ortus-dev-claude-auth` und gelten für alle Tickets.
4. `make dev-doctor` — prüft DNS, Netz, Traefik, Dozzle, Auth-Volume.

## Ticket-Lebenszyklus

```sh
make dev-new     TICKET=ORT-123          # Worktree + Stack anlegen, URLs ausgeben
make dev-attach  TICKET=ORT-123          # lokale interaktive Claude-Code-Session
make dev-remote  TICKET=ORT-123          # Claude-Code mit Remote Control -> Mobile App
make dev-logs    TICKET=ORT-123          # ortus-Logs folgen (oder http://logs.ortus.local)
make dev-list                            # laufende Umgebungen + Worktrees
make dev-destroy TICKET=ORT-123          # Container + Build-Volume + Worktree + Branch entfernen
```

Nach `make dev-new TICKET=ORT-123`:
- `http://ort-123.ortus.local` — API + Frontend (`/health/live`, `/api/v1/...`, `/docs`)
- `http://metrics.ort-123.ortus.local/metrics` — Prometheus-Metriken
- `http://mcp.ort-123.ortus.local/mcp` — MCP (Bearer-Token; pro Ticket generiert)
- `http://logs.ortus.local` — Dozzle-Log-Viewer (alle Ticket-Container)

Der Ticket-Name wird für Hostname/Compose saniert (Kleinbuchstaben, `[a-z0-9-]`).

## Claude Code im Container

- **MCP:** `<worktree>/.mcp.json` zeigt auf die laufende ortus-Instanz (`http://ortus:9091/mcp`
  im internen Netz). Der Bearer-Token wird von Claude Code aus `$ORTUS_MCP_TOKEN` (Container-Env)
  expandiert — **kein Token auf Platte**. Die ortus-MCP-Tools (health, query_point, …) laufen
  gegen die live-reloadende Instanz mit den echten Datenquellen.
- **Remote Control** (`make dev-remote`): die Session erscheint in der Claude-App unter **Code**
  (und claude.ai/code). Nur **ausgehende** HTTPS-Verbindung zu Anthropic — keine eingehenden
  Ports/Tunnel/VPN. Voraussetzung: `make dev-login` (OAuth). Das exakte Flag ggf. mit
  `claude --help` im Container abgleichen (die CLI entwickelt sich).
- **Auth-Ablauf:** OAuth-Token laufen nach einigen Tagen ab → `make dev-login` erneut ausführen.

## Live-Reload

Der `ortus`-Container läuft `air` (`deploy/dev/.air.toml`) auf dem gemounteten Worktree: Bearbeitet
Claude Code (oder du) den Go-Code, baut air neu (CGO + SpatiaLite) und startet ortus neu. Der erste
Build ist der langsamste; Modul- und Build-Cache sind als Volumes persistiert.

## Sicherheit / Grenzen (Dev-only)

- Traefik + Dozzle mounten den **Docker-Socket** (root-äquivalent). Nur lokal betreiben.
- Kein TLS (nur `http`/`:80`). Optionaler Upgrade-Pfad: `mkcert` + `*.ortus.local`-Cert +
  Traefik-`websecure:443`.
- Alle Tickets teilen ein `claude-auth`-Volume (dasselbe Konto); Sessions werden per `--name`
  unterschieden.

## Dateien

- `docker-compose.infra.yaml` — geteiltes Traefik + Dozzle.
- `docker-compose.dev.yaml` — Per-Ticket-Vorlage (env-parametrisiert).
- `Dockerfile.dev` + `.air.toml` — Live-Reload-ortus-Image.
- `Dockerfile.claude` — Claude-Code-Image (auf SpatiaLite-Dev-Base, kann auch `make build/test`).
- `mcp.json.tmpl` — wird als `<worktree>/.mcp.json` kopiert.
- `dev.mk` — die `make dev-*`-Targets (aus dem Root-Makefile via `include` eingebunden).
