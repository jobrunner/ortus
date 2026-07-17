# Quickstart für Dummies — ein Feature bauen, Schritt für Schritt

Diese Anleitung setzt **nichts** voraus außer: du hast das Repo geklont und ein
Terminal offen. Copy-paste die Befehle der Reihe nach. Details/Referenz stehen in
[README.md](README.md).

> Kurzfassung des ganzen Ablaufs:
> ```sh
> make dev-up                         # 1x pro Rechner-Neustart
> make dev NAME=mein-feature          # Feature starten -> landet im Claude-Container
> # ... arbeiten ...
> make dev-destroy NAME=mein-feature  # wenn fertig/gemergt: aufräumen
> ```

---

## Teil 0 — Voraussetzungen (einmal prüfen)

1. **Docker Desktop läuft.** Icon in der Menüleiste muss „running" sein. Test:
   ```sh
   docker ps
   ```
   Zeigt das eine (evtl. leere) Tabelle statt eines Fehlers? Gut.
2. Du bist im Projektordner:
   ```sh
   cd /Users/jbrunner/work/projects/ortus
   ```

---

## Teil 1 — Einmalige Einrichtung (nur beim allerersten Mal)

### 1a) DNS einrichten (damit `*.ortus.local` funktioniert)

Copy-paste diese fünf Zeilen (bei `sudo` fragt der Mac nach deinem Passwort):
```sh
brew install dnsmasq
printf 'port=5353\naddress=/ortus.local/127.0.0.1\n' >> "$(brew --prefix)/etc/dnsmasq.conf"
sudo mkdir -p /etc/resolver
printf 'nameserver 127.0.0.1\nport 5353\n' | sudo tee /etc/resolver/ortus.local
brew services restart dnsmasq
```
**Testen:**
```sh
dscacheutil -q host -a name probe.ortus.local
```
Erwartung: irgendwo steht `ip_address: 127.0.0.1`. ✅
(`make dev-dns-setup` zeigt dieselben Befehle nochmal an.)

### 1b) Basis-Dienste starten

```sh
make dev-up
```
Erwartung: am Ende `Infra up. Dashboard: http://traefik.ortus.local ...`. ✅

### 1c) Bei Claude anmelden (für die Handy-App / Remote Control)

```sh
make dev-login
```
Es öffnet sich Claude im Terminal. Tippe `/login`, folge dem Browser-Login, und
beende danach mit `Ctrl-D`. Das gilt **für alle Features** und hält ein paar Tage.

> Fertig. Teil 1 machst du nie wieder — außer der Login läuft ab (dann nur 1c).

---

## Teil 2 — Ein Feature bauen (das machst du täglich)

```sh
make dev NAME=mein-feature
```
Ersetze `mein-feature` durch einen kurzen Namen (Kleinbuchstaben/Bindestriche).
**Was jetzt passiert (beim ersten Mal dauert der Build ein paar Minuten):**
1. Ein eigener Arbeitsordner (Git-Worktree) + eigener Branch wird angelegt.
2. Zwei Container starten: dein ortus (baut sich bei Codeänderungen selbst neu)
   und ein Claude-Code-Container mit allen Plugins/Tools schon drin.
3. Du landest **direkt im Claude-Container**, und der `plan-feature`-Skill startet
   — er stellt dir ein paar Fragen, schreibt einen Plan und prüft ihn gegen den
   echten Code, **bevor** etwas programmiert wird.

**Deine URLs** (im Browser öffnen):
- `http://mein-feature.ortus.local` — die App / API
- `http://logs.ortus.local` — Live-Logs (Dozzle)

**Raus aus der Claude-Session:** `Ctrl-D` (die Container laufen weiter).
**Wieder rein:** `make dev NAME=mein-feature` (startet nicht neu, verbindet nur).

### Vom Handy aus weiterarbeiten (optional)
```sh
make dev-remote NAME=mein-feature
```
Die Session taucht in der Claude-App unter **„Code"** auf.

---

## Teil 3 — Performance messen (optional)

```sh
make dev-obs                          # 1x: Grafana/Tempo/Prometheus starten
make dev-perf NAME=mein-feature       # Lasttest laufen lassen
```
Danach zeigt das Terminal die Latenz-Zahlen, und unter
`http://grafana.ortus.local` siehst du Traces/Dashboards (Explore → Tempo →
`service.name = "mein-feature"`). Mehr dazu: Skill `perf-test`.

---

## Teil 4 — Aufräumen (wenn das Feature fertig/gemergt ist)

```sh
make dev-destroy NAME=mein-feature
```
Entfernt Container, Build-Volume und Arbeitsordner. Der Branch wird nur gelöscht,
wenn er schon gemergt ist (sonst Warnung — deine Arbeit ist sicher).

**Alles anzeigen, was gerade läuft:**
```sh
make dev-list
```

---

## Teil 5 — Wenn etwas klemmt

**Erste Hilfe — prüft alles auf einmal:**
```sh
make dev-doctor
```
Alles `OK`? Dann liegt's woanders. Sonst sagt jede `FAIL`-Zeile, welcher Befehl fehlt.

| Problem | Lösung |
|---|---|
| `http://...ortus.local` lädt nicht | `make dev-doctor`; wenn DNS `FAIL`: Teil 1a wiederholen |
| „Netz ortus-dev fehlt" | `make dev-up` |
| Claude will Login / Remote Control geht nicht | `make dev-login` (Token abgelaufen) |
| Container „unhealthy" / erster Start dauert ewig | erster Build ist der langsamste — kurz warten; Logs: `http://logs.ortus.local` |
| Name vergessen | `make dev-list` zeigt alle laufenden Umgebungen |
| Ganz neu anfangen | `make dev-destroy NAME=...` und dann wieder `make dev NAME=...` |

**Wichtig:** Du musst auf deinem Mac **nichts** für dieses Projekt pflegen
(keine Go-Tools, keine Plugins) — alles steckt fix und fertig im Container. Nur
Docker Desktop, die DNS-Einrichtung (Teil 1a) und der Claude-Login (Teil 1c) sind
Host-Sache.
