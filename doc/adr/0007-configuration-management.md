# ADR-0007: Configuration Management mit Viper und Cobra

## Status

Akzeptiert

## Kontext

Die Anwendung benötigt eine flexible Konfiguration für verschiedene Deployment-Szenarien:

1. **Lokale Entwicklung:** Einfache Defaults, schneller Start
2. **Container/Kubernetes:** Umgebungsvariablen, ConfigMaps
3. **Standalone:** CLI-Flags für einmalige Anpassungen
4. **12-Factor App:** Konfiguration aus der Umgebung

### Anforderungen

- Klare Priorität zwischen Konfigurationsquellen
- Keine Secrets in Code oder Container-Images
- Validierung der Konfiguration beim Start
- Dokumentation aller Optionen
- Einheitliches Namensschema

## Entscheidung

Wir verwenden **Viper** für Konfigurationsmanagement und **Cobra** für CLI-Commands/Flags.

### Begründung

- **Industriestandard:** Viper und Cobra sind die etabliertesten Go-Bibliotheken für Konfiguration und CLI. Sie werden in tausenden Produktionsprojekten eingesetzt (Docker, Kubernetes CLI, Hugo, etc.).
- **Erweiterbarkeit:** Das Zusammenspiel von Viper und Cobra ermöglicht es, neue Konfigurationsoptionen und CLI-Commands ohne Architekturänderungen hinzuzufügen.
- **Wartbarkeit:** Gut dokumentierte, stabile APIs mit langfristiger Unterstützung durch die Open-Source-Community.
- **Flexibilität:** Native Unterstützung für JSON, YAML, TOML, .env und Umgebungsvariablen.

### Konfigurations-Priorität

```
CLI Flags  >  Umgebungsvariablen  >  Config-File  >  Defaults
(höchste)                                          (niedrigste)
```

### Namenskonvention

| Quelle | Format | Beispiel |
|--------|--------|----------|
| CLI Flag | kebab-case | `--rate-limit` |
| Env Variable | SCREAMING_SNAKE_CASE mit Prefix | `ORTUS_RATE_LIMIT` |
| Config-File | camelCase oder kebab-case | `rateLimit: 10` |

### Cobra CLI-Struktur

```go
// cmd/ortus/main.go
package main

import (
    "os"

    "github.com/spf13/cobra"
    "github.com/spf13/viper"
)

var rootCmd = &cobra.Command{
    Use:   "ortus",
    Short: "GeoPackage Point Query Service",
    Long:  `Ortus ist ein Go-Service für Punktabfragen auf GeoPackage-Dateien.`,
}

var serveCmd = &cobra.Command{
    Use:   "serve",
    Short: "Startet den HTTP-Server",
    RunE:  runServe,
}

func init() {
    cobra.OnInitialize(initConfig)

    rootCmd.AddCommand(serveCmd)

    // Persistent Flags (für alle Commands)
    rootCmd.PersistentFlags().String("config", "", "Config-Datei (default: ./config.yaml)")
    rootCmd.PersistentFlags().String("log-level", "info", "Log-Level: none, error, info, debug, verbose")

    // Serve-spezifische Flags
    serveCmd.Flags().String("host", "0.0.0.0", "HTTP-Server Host")
    serveCmd.Flags().Int("port", 8080, "HTTP-Server Port")
    serveCmd.Flags().String("gpkg-dir", "/data/gpkg", "GeoPackage-Verzeichnis")
    serveCmd.Flags().Float64("rate-limit", 10, "Requests pro Sekunde")
    serveCmd.Flags().Bool("tls", false, "TLS aktivieren")
    serveCmd.Flags().Bool("letsencrypt", false, "Let's Encrypt via CertMagic aktivieren")
    serveCmd.Flags().String("storage-type", "local", "Storage-Typ: local, s3, azure, http")
    serveCmd.Flags().Int("metrics-port", 9090, "Prometheus-Metriken-Port")

    // Flags an Viper binden
    viper.BindPFlags(rootCmd.PersistentFlags())
    viper.BindPFlags(serveCmd.Flags())
}

func initConfig() {
    if cfgFile := viper.GetString("config"); cfgFile != "" {
        viper.SetConfigFile(cfgFile)
    } else {
        viper.SetConfigName("config")
        viper.SetConfigType("yaml")
        viper.AddConfigPath(".")
        viper.AddConfigPath("/etc/ortus")
    }

    // Umgebungsvariablen mit Prefix ORTUS_
    viper.SetEnvPrefix("ORTUS")
    viper.AutomaticEnv()

    // Config-Datei lesen (optional)
    viper.ReadInConfig()
}

func main() {
    if err := rootCmd.Execute(); err != nil {
        os.Exit(1)
    }
}
```

### Viper Config-Loader

```go
// internal/config/loader.go
package config

import (
    "fmt"

    "github.com/spf13/viper"
)

func LoadConfig() (*Config, error) {
    // Defaults setzen
    setDefaults()

    var cfg Config
    if err := viper.Unmarshal(&cfg); err != nil {
        return nil, fmt.Errorf("unmarshal config: %w", err)
    }

    // Validierung
    if err := cfg.Validate(); err != nil {
        return nil, fmt.Errorf("validate config: %w", err)
    }

    return &cfg, nil
}

func setDefaults() {
    // Server
    viper.SetDefault("server.host", "0.0.0.0")
    viper.SetDefault("server.port", 8080)
    viper.SetDefault("server.readTimeout", "30s")
    viper.SetDefault("server.writeTimeout", "30s")
    viper.SetDefault("server.shutdownTimeout", "30s")

    // GeoPackage
    viper.SetDefault("geopackage.directory", "/data/gpkg")
    viper.SetDefault("geopackage.watchInterval", "10s")
    viper.SetDefault("geopackage.indexTimeout", "10m")

    // Storage
    viper.SetDefault("storage.type", "local")

    // TLS
    viper.SetDefault("tls.enabled", false)
    viper.SetDefault("tls.letsEncrypt", false)
    viper.SetDefault("tls.cacheDir", "/var/cache/ortus/certs")

    // Logging
    viper.SetDefault("logging.level", "info")
    viper.SetDefault("logging.format", "json")
    viper.SetDefault("logging.output", "stdout")
    viper.SetDefault("logging.logRequests", true)

    // Metrics
    viper.SetDefault("metrics.enabled", true)
    viper.SetDefault("metrics.port", 9090)
    viper.SetDefault("metrics.path", "/metrics")

    // Rate Limit
    viper.SetDefault("rateLimit.enabled", true)
    viper.SetDefault("rateLimit.requestsPerSecond", 10)
    viper.SetDefault("rateLimit.burst", 20)
}
```

### Config-Struktur

```go
// internal/config/config.go
package config

import "time"

type Config struct {
    Server     ServerConfig     `mapstructure:"server"`
    GeoPackage GeoPackageConfig `mapstructure:"geopackage"`
    Storage    StorageConfig    `mapstructure:"storage"`
    TLS        TLSConfig        `mapstructure:"tls"`
    Logging    LoggingConfig    `mapstructure:"logging"`
    Metrics    MetricsConfig    `mapstructure:"metrics"`
    RateLimit  RateLimitConfig  `mapstructure:"rateLimit"`
}

type ServerConfig struct {
    Host            string        `mapstructure:"host"`
    Port            int           `mapstructure:"port"`
    ReadTimeout     time.Duration `mapstructure:"readTimeout"`
    WriteTimeout    time.Duration `mapstructure:"writeTimeout"`
    ShutdownTimeout time.Duration `mapstructure:"shutdownTimeout"`
}

// ... weitere Config-Structs
```

### Beispiel config.yaml

```yaml
server:
  host: "0.0.0.0"
  port: 8080

geopackage:
  directory: "/data/gpkg"
  watchInterval: "10s"

storage:
  type: "s3"
  s3Bucket: "my-geopackages"
  s3Region: "eu-central-1"

tls:
  enabled: true
  letsEncrypt: true
  letsEncryptEmail: "admin@example.com"
  domains:
    - "ortus.example.com"

logging:
  level: "info"
  format: "json"

rateLimit:
  enabled: true
  requestsPerSecond: 10
```

## Konsequenzen

### Positiv

- **Erweiterbar:** Neue Config-Optionen ohne Architekturänderungen hinzufügbar
- **Standardkonform:** Bewährte Patterns der Go-Community
- **Flexibel:** Multiple Konfigurationsquellen (CLI, Env, File) mit klarer Priorität
- **Dokumentiert:** Automatische --help-Generierung durch Cobra
- **12-Factor:** Umgebungsvariablen als primäre Konfigurationsquelle für Container

### Negativ

- **Lernkurve:** Viper/Cobra haben umfangreiche APIs
- **Indirektion:** Konfigurationswerte werden über Viper-Keys referenziert

### Mitigationen

- Zentrale Config-Struktur als Single Source of Truth
- Startup-Log zeigt effektive Konfiguration (ohne Secrets)
- `--help` zeigt alle Optionen mit Defaults

## Umgebungsvariablen-Referenz

| Variable | Default | Beschreibung |
|----------|---------|--------------|
| `ORTUS_HOST` | `0.0.0.0` | HTTP-Server-Host |
| `ORTUS_PORT` | `8080` | HTTP-Server-Port |
| `ORTUS_GPKG_DIR` | `/data/gpkg` | GeoPackage-Verzeichnis |
| `ORTUS_STORAGE_TYPE` | `local` | Storage-Typ (local/s3/azure/http) |
| `ORTUS_S3_BUCKET` | - | AWS S3-Bucket-Name |
| `ORTUS_S3_REGION` | - | AWS S3-Region |
| `ORTUS_HTTP_BASE_URL` | - | Base-URL für HTTP-Download |
| `ORTUS_TLS_ENABLED` | `false` | TLS aktivieren |
| `ORTUS_LETSENCRYPT` | `false` | Let's Encrypt aktivieren |
| `ORTUS_LOG_LEVEL` | `info` | Log-Level |
| `ORTUS_RATE_LIMIT` | `10` | Requests pro Sekunde |
| `ORTUS_METRICS_ENABLED` | `true` | Prometheus-Metriken |
| `ORTUS_METRICS_PORT` | `9090` | Metriken-Port |
| `ORTUS_SERVER_CORS_ALLOWED_ORIGINS` | `[]` | Erlaubte CORS Origins (kommasepariert) |

### CLI-Flags

| Flag | Env-Variable | Beschreibung |
|------|--------------|--------------|
| `--cors` | `ORTUS_SERVER_CORS_ALLOWED_ORIGINS` | Erlaubte CORS Origins, z.B. `--cors=https://example.com,*.sub.domain.tld` |

## Referenzen

- [spf13/viper](https://github.com/spf13/viper)
- [spf13/cobra](https://github.com/spf13/cobra)
- [12-Factor App Config](https://12factor.net/config)
