# Configure rate limiting

Per-client-IP rate limiting is **off by default** — ortus is meant to run "dumb"
behind whatever your infrastructure provides. Enable it when ortus is exposed
directly on a public IP without a rate-limiting gateway in front:

```yaml
server:
  rate_limit:
    enabled: true
    rate: 50          # sustained requests/second per client IP
    burst: 100        # token-bucket burst per client IP
    # Only set when a proxy/LB sits in front: its CIDR(s). Then the client IP is
    # taken from X-Forwarded-For. Left empty (default), the header is ignored and
    # the direct peer is used — correct (un-spoofable) for direct public exposure.
    trusted_proxies: []
```

Notes:

- Applies to the **`/api/v1`** surface only — `/health*` probes are never throttled.
- Over-limit requests get **429** with `Retry-After: 1`.
- Idle per-IP buckets are evicted automatically (bounded memory, no background goroutine).
