# Enable TLS / HTTPS

ortus can terminate HTTPS with automatic Let's Encrypt certificates (CertMagic).

## Via flags

```bash
./ortus \
  --tls \
  --tls-domains=ortus.example.com \
  --tls-email=admin@example.com
```

## Via config

```yaml
tls:
  enabled: true
  domains:
    - ortus.example.com
  email: admin@example.com
  cache_dir: ./.certmagic
```

The `cache_dir` stores issued certificates — persist it across restarts so you
don't re-issue (and hit rate limits). The host must be reachable on the ACME
challenge port for issuance.
