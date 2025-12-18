# ADR-0008: TLS und Let's Encrypt mit CertMagic

## Status

Akzeptiert

## Kontext

Für Produktionsumgebungen ist verschlüsselte Kommunikation (HTTPS) erforderlich. Die Anwendung soll sowohl hinter einem Reverse-Proxy als auch standalone betrieben werden können.

### Szenarien

1. **Hinter Reverse-Proxy (nginx, Traefik, Cloud Load Balancer):**
   - TLS-Terminierung am Proxy
   - Anwendung lauscht auf HTTP

2. **Standalone Container:**
   - Anwendung muss selbst TLS terminieren
   - Zertifikatsverwaltung erforderlich

3. **Entwicklung:**
   - Kein TLS erforderlich
   - Einfacher HTTP-Zugriff

## Entscheidung

Wir verwenden **CertMagic** für automatische TLS-Zertifikatsverwaltung mit Let's Encrypt.

### Begründung

- **Industriestandard:** CertMagic ist die bewährte Zertifikatsmanagement-Bibliothek aus dem Caddy-Webserver-Projekt. Sie wird in tausenden Produktionssystemen eingesetzt.
- **Erweiterbarkeit:** CertMagic unterstützt verschiedene ACME-Provider, nicht nur Let's Encrypt. Zukünftige Anforderungen (z.B. private CAs) können ohne Architekturänderungen umgesetzt werden.
- **Zuverlässigkeit:** Automatische Zertifikatserneuerung, Retry-Logik und Cluster-Support sind eingebaut.
- **Einfachheit:** Im Vergleich zu golang.org/x/crypto/acme/autocert bietet CertMagic eine modernere API und bessere Defaults.

### TLS-Modi

1. **Kein TLS (Default):** HTTP auf konfigurierbarem Port
2. **Eigene Zertifikate:** TLS mit bereitgestellten Cert/Key-Dateien
3. **Let's Encrypt:** Automatische Zertifikatsverwaltung via CertMagic

### Architektur

```
+-------------------+          +-------------------+
|   Client          |          |   Ortus Server   |
+-------------------+          +-------------------+
         |                              |
         | HTTPS (TLS 1.2+)             |
         +----------------------------->|
         |                              |
         |    +---------------------+   |
         |    |  TLS Manager        |   |
         |    +---------------------+   |
         |    |                     |   |
         |    | - CertMagic         |   |
         |    | - Let's Encrypt     |   |
         |    | - Manual Certs      |   |
         |    |                     |   |
         |    +---------------------+   |
         |                              |
```

### CertMagic-Integration

```go
// internal/infrastructure/tls/certmagic.go
package tls

import (
    "crypto/tls"
    "fmt"

    "github.com/caddyserver/certmagic"
)

type Manager struct {
    config    Config
    magic     *certmagic.Config
}

func NewManager(cfg Config) (*Manager, error) {
    m := &Manager{config: cfg}

    if cfg.LetsEncrypt {
        // CertMagic konfigurieren
        certmagic.DefaultACME.Email = cfg.LetsEncryptEmail
        certmagic.DefaultACME.Agreed = true

        // Cache-Verzeichnis setzen
        certmagic.Default.Storage = &certmagic.FileStorage{
            Path: cfg.CacheDir,
        }

        m.magic = certmagic.NewDefault()
    }

    return m, nil
}

func (m *Manager) TLSConfig() (*tls.Config, error) {
    if m.config.LetsEncrypt {
        // CertMagic TLS-Config verwenden
        return m.magic.TLSConfig(), nil
    }

    // Eigene Zertifikate
    cert, err := tls.LoadX509KeyPair(m.config.CertFile, m.config.KeyFile)
    if err != nil {
        return nil, fmt.Errorf("load certificate: %w", err)
    }

    return &tls.Config{
        Certificates: []tls.Certificate{cert},
        MinVersion:   tls.VersionTLS12,
        CipherSuites: []uint16{
            tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
            tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
            tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
            tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
        },
    }, nil
}

// ManageDomains registriert die Domains bei CertMagic
func (m *Manager) ManageDomains(domains []string) error {
    if m.magic == nil {
        return nil
    }
    return m.magic.ManageSync(domains)
}
```

### Server-Integration

```go
// internal/adapters/primary/http/server.go
func (s *Server) Start(ctx context.Context) error {
    if !s.config.TLS.Enabled {
        // Plain HTTP
        return s.httpServer.ListenAndServe()
    }

    if s.config.TLS.LetsEncrypt {
        // Domains bei CertMagic registrieren
        if err := s.tlsManager.ManageDomains(s.config.TLS.Domains); err != nil {
            return fmt.Errorf("manage domains: %w", err)
        }

        // CertMagic-HTTPS-Server starten
        return certmagic.HTTPS(s.config.TLS.Domains, s.router)
    }

    // Eigene Zertifikate
    tlsConfig, err := s.tlsManager.TLSConfig()
    if err != nil {
        return fmt.Errorf("tls config: %w", err)
    }

    s.httpServer.TLSConfig = tlsConfig
    return s.httpServer.ListenAndServeTLS("", "")
}
```

### Konfiguration

```yaml
tls:
  enabled: true

  # Option 1: Eigene Zertifikate
  certFile: "/certs/server.crt"
  keyFile: "/certs/server.key"

  # Option 2: Let's Encrypt via CertMagic
  letsEncrypt: true
  letsEncryptEmail: "admin@example.com"
  domains:
    - "ortus.example.com"
    - "geo.example.com"
  cacheDir: "/var/cache/ortus/certs"
```

### Let's Encrypt Anforderungen

Für Let's Encrypt müssen folgende Bedingungen erfüllt sein:

1. **Domain:** Gültige Domain, die auf den Server zeigt
2. **Port 443:** Muss aus dem Internet erreichbar sein
3. **Port 80:** Für HTTP-01 Challenge erforderlich
4. **E-Mail:** Gültige E-Mail-Adresse für Benachrichtigungen

```
                                    Let's Encrypt
                                         |
                                         | 1. Request Certificate
                                         v
+-------------------+          +-------------------+
|   Ortus          |<---------|   ACME Server     |
|   :80 / :443      |          |                   |
+-------------------+          +-------------------+
         |                              |
         | 2. HTTP-01 Challenge         |
         |    /.well-known/acme-        |
         |    challenge/<token>         |
         |<-----------------------------+
         |                              |
         | 3. Challenge Response        |
         +----------------------------->|
         |                              |
         | 4. Certificate               |
         |<-----------------------------+
```

### Sicherheitskonfiguration

CertMagic verwendet standardmäßig sichere TLS-Defaults:

- TLS 1.2 als Minimum
- Moderne Cipher-Suites
- OCSP-Stapling
- Automatische Zertifikatserneuerung (vor Ablauf)

## Konsequenzen

### Positiv

- **Automatisierung:** Vollautomatische Zertifikatsverwaltung mit Let's Encrypt
- **Erweiterbar:** Support für andere ACME-Provider und Cluster-Setups
- **Sicherheit:** Moderne TLS-Defaults, automatische Erneuerung
- **Battle-tested:** Bewährte Bibliothek aus dem Caddy-Projekt

### Negativ

- **Abhängigkeit:** Externe Abhängigkeit zu CertMagic
- **Port-Anforderungen:** Let's Encrypt benötigt Port 80 und 443
- **Netzwerkzugang:** Let's Encrypt erfordert Internetzugang

### Mitigationen

- Fallback auf eigene Zertifikate wenn Let's Encrypt nicht möglich
- Klare Dokumentation der Anforderungen pro Modus
- Validierung der Konfiguration beim Start

## Container-Konfiguration

### Docker Compose (mit Let's Encrypt)

```yaml
services:
  ortus:
    ports:
      - "80:80"     # ACME Challenge
      - "443:443"   # HTTPS
      - "9090:9090" # Metrics
    environment:
      - ORTUS_TLS_ENABLED=true
      - ORTUS_LETSENCRYPT=true
      - ORTUS_LETSENCRYPT_EMAIL=admin@example.com
      - ORTUS_DOMAINS=ortus.example.com
    volumes:
      - cert-cache:/var/cache/ortus/certs

volumes:
  cert-cache:
```

### Kubernetes (mit eigenem Zertifikat)

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: ortus-tls
type: kubernetes.io/tls
data:
  tls.crt: <base64-encoded-cert>
  tls.key: <base64-encoded-key>
---
apiVersion: apps/v1
kind: Deployment
spec:
  template:
    spec:
      containers:
        - name: ortus
          env:
            - name: ORTUS_TLS_ENABLED
              value: "true"
            - name: ORTUS_TLS_CERT_FILE
              value: "/certs/tls.crt"
            - name: ORTUS_TLS_KEY_FILE
              value: "/certs/tls.key"
          volumeMounts:
            - name: tls-certs
              mountPath: /certs
              readOnly: true
      volumes:
        - name: tls-certs
          secret:
            secretName: ortus-tls
```

## Referenzen

- [CertMagic](https://github.com/caddyserver/certmagic)
- [Let's Encrypt](https://letsencrypt.org/)
- [ACME Protocol](https://tools.ietf.org/html/rfc8555)
- [Mozilla SSL Configuration Generator](https://ssl-config.mozilla.org/)
