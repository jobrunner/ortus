# MCP — Model Context Protocol Server

Ortus exposes a Model Context Protocol server so AI agents (Claude
Desktop, Claude Code, MCP-aware IDEs, custom agent SDK apps, …) can
both **observe** and **use** the service through a typed tool surface.

There are two ways to talk to it:

| Mode | When | Auth | How agents connect |
|---|---|---|---|
| HTTP (streamable) | Production / shared service | Bearer token | Direct HTTP for Claude Code; `mcp-remote` bridge for Claude Desktop |
| stdio | Local / dev | Process boundary | Claude Desktop config spawns `./ortus mcp` |

Both modes expose the same tool set; the difference is purely transport. (The
`gazetteer` tool is present only when the gazetteer feature is enabled — see the
note under the tool catalogue.)

## Quick start

### Enable the HTTP MCP server

```yaml
# config.yaml
mcp:
  enabled: true
  host: "0.0.0.0"     # 127.0.0.1 keeps it host-local; LAN binding requires a token
  port: 9091
  path: "/mcp"
```

```bash
export ORTUS_MCP_TOKEN="$(openssl rand -hex 32)"   # required for non-loopback
./ortus
```

The MCP endpoint is now available at `http://<host>:9091/mcp` with
`Authorization: Bearer $ORTUS_MCP_TOKEN`.

### Run as stdio (Claude Desktop)

Add to `~/Library/Application Support/Claude/claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "ortus": {
      "command": "/usr/local/bin/ortus",
      "args": ["mcp"],
      "env": {
        "ORTUS_STORAGE_LOCAL_PATH": "/path/to/your/data"
      }
    }
  }
}
```

Restart Claude Desktop — ortus will appear as an MCP source with the
tools listed below. The query tools and the diagnostic/`health` tools are always
registered; the diagnostic tools return "tracing is disabled" until
`tracing.enabled` is on. The `gazetteer` tool is registered only when the
gazetteer feature is enabled.

## Tool catalogue

All tools are **read-only**. There is intentionally no management tool
(sync trigger, package reload, …) in this surface — those stay on the
authenticated REST API.

### Diagnostic tools

These read from the in-memory tracing ring buffer (see
[Observability](observability.md)) plus the health service. They give an AI
agent the data it needs to debug ortus from inside a conversation.

| Tool | What it returns |
|---|---|
| `list_traces` | Recent completed traces, newest first. Filters: `min_duration_ms`, `status` (`Ok`/`Error`/`Unset`), `name_contains`, `since_iso`, `limit`. Searches BOTH the success and error pool so error traces can't get drowned out by routine success. |
| `get_trace` | A single trace by hex `trace_id`, with every span, attributes, events, recorded errors (incl. stack traces from `RecordError`). |
| `list_active_spans` | Snapshot of in-flight spans — the answer to "what is currently running / hanging?". Returns `age_ms` per span. |
| `tracing_stats` | Ring-buffer occupancy, eviction counter, OTLP-exporter error count. Always call this first to confirm tracing is healthy before relying on the others. |
| `health` | Same content as `GET /health` but as a typed MCP response. |

### Query tools

| Tool | What it does |
|---|---|
| `query_point` | Point-in-polygon query across sources. Accepts WGS84 (`lon`/`lat`) or arbitrary (`x`/`y`/`srid`). Optional `source_id` filter and `properties` projection. |
| `list_sources` | All currently loaded sources with ready state and layer count. |
| `get_source` | Full metadata for one source: layers, extent, size, license. |
| `get_source_layers` | Layers in a source with geometry type, SRID, feature count, bounding box. |
| `gazetteer` | Reverse-geocode a coordinate to its admin hierarchy (`admin`) and a bearing to the most salient nearby place (`bearing`). Mirrors `GET /api/v1/gazetteer`: each unit/anchor carries `name_native` + a `name_source` code, admin units also `local_term` + `equivalent_description`, plus a response-wide deduplicated `sources` block and the dataset `license`/attribution. Only registered when the gazetteer feature is enabled. |

## Auth model

- **Token comes from `ORTUS_MCP_TOKEN` env var, never from the config
  file.** A leaked config file therefore can't leak the MCP token.
- **Loopback exemption**: when `mcp.host` is `127.0.0.1`/`::1`/`localhost`,
  the token check is bypassed because no remote process can reach the
  listener anyway.
- **Constant-time comparison** of the `Authorization: Bearer …` header
  prevents timing attacks.
- **Token rotation** = restart the process with a new env var. v0.x
  accepts the operational simplicity of restart-rotation; multi-token
  support is a possible follow-up.

## Observability of the MCP server itself

The MCP server is instrumented exactly like the REST API: every tool
call produces a span, the same ring buffer captures them, the same
`/metrics` endpoint counts them. That means an agent can call
`list_traces` to inspect its own previous calls — useful for
debugging tool argument shapes.

## Architecture notes

- The MCP server runs **in-process** in the ortus binary. Direct
  access to the ring buffer is the whole reason this isn't a separate
  service.
- It binds to its own port (default 9091) so a NetworkPolicy can
  isolate it from the public REST API on 8080 — agents from CI, an
  internal LAN, or a service mesh can reach 9091 without ever seeing
  8080.
- The streamable-HTTP transport follows MCP 2025-03-26 spec. The Go
  SDK (`github.com/modelcontextprotocol/go-sdk`) implements this
  natively.

## Limitations / roadmap

- **No Resources or Prompts yet.** Tools only. Resources (e.g.
  `ortus://traces/{id}`) and prompts (`investigate_trace`) are planned
  in v0.9.
- **No subscriptions.** Agents must poll `list_active_spans` rather
  than receive live updates. SSE-style push could be added later.
- **Single token.** Per-consumer tokens (multi-tenant) are a future
  iteration if the integration story demands it.

## Troubleshooting

| Symptom | Cause |
|---|---|
| `401 unauthorized` with a token set | Header must be exactly `Authorization: Bearer <token>` — case-sensitive prefix, single space, no quotes |
| Tools return "tracing is disabled" | `tracing.enabled` is `false` in ortus config. Diagnostic tools require it; query tools do not |
| `mcp.enabled is true … ORTUS_MCP_TOKEN must be set` at startup | Non-loopback `mcp.host` with no token — set the env var or rebind to `127.0.0.1` |
| Claude Desktop doesn't see the tools | `./ortus mcp` writes its protocol on **stdout** — make sure nothing else does. Logging is routed to stderr automatically |
