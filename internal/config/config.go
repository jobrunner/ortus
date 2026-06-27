// Package config provides configuration management using Viper.
package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Storage type constants.
const (
	StorageTypeLocal = "local"
	StorageTypeS3    = "s3"
	StorageTypeAzure = "azure"
	StorageTypeHTTP  = "http"
)

// dnsProviderAzure is the only supported ACME DNS-01 challenge provider.
const dnsProviderAzure = "azure"

// mcpLoopbackHost is the canonical loopback address; MCP binds here by default
// and treats it (with localhost/::1) as trusted, token-optional.
const mcpLoopbackHost = "127.0.0.1"

// Config holds all application configuration.
type Config struct {
	Server  ServerConfig  `mapstructure:"server"`
	Storage StorageConfig `mapstructure:"storage"`
	Query   QueryConfig   `mapstructure:"query"`
	TLS     TLSConfig     `mapstructure:"tls"`
	Metrics MetricsConfig `mapstructure:"metrics"`
	Logging LoggingConfig `mapstructure:"logging"`
	Sync    SyncConfig    `mapstructure:"sync"`
	Tracing TracingConfig `mapstructure:"tracing"`
	MCP     MCPConfig     `mapstructure:"mcp"`

	// Build is populated by main.go from -ldflags at startup; not loaded
	// from config files. Used for the MCP Implementation.Version field
	// and any future runtime identification needs.
	Build BuildInfo `mapstructure:"-"`
}

// BuildInfo captures the binary's build identity. Populated from
// -ldflags in main.go (or left as "dev"/"none" for local builds).
type BuildInfo struct {
	Version   string
	Commit    string
	BuildDate string
}

// ServerConfig holds HTTP server configuration.
type ServerConfig struct {
	Host            string          `mapstructure:"host"`
	Port            int             `mapstructure:"port"`
	ReadTimeout     time.Duration   `mapstructure:"read_timeout"`
	WriteTimeout    time.Duration   `mapstructure:"write_timeout"`
	ShutdownTimeout time.Duration   `mapstructure:"shutdown_timeout"`
	RateLimit       RateLimitConfig `mapstructure:"rate_limit"`
	CORS            CORSConfig      `mapstructure:"cors"`
	FrontendEnabled bool            `mapstructure:"frontend_enabled"` // Enable web frontend at /
	// ReadyWhenEmpty: when true (default), readiness reports ready once the
	// initial load pass is done even with zero sources ("no data today"). When
	// false, readiness additionally requires at least one ready source.
	ReadyWhenEmpty bool `mapstructure:"ready_when_empty"`
}

// CORSConfig holds CORS configuration.
type CORSConfig struct {
	AllowedOrigins []string `mapstructure:"allowed_origins"` // e.g., ["https://example.com", "*.sub.domain.tld"]
}

// Enabled returns true if CORS is configured with at least one allowed origin.
func (c *CORSConfig) Enabled() bool {
	return len(c.AllowedOrigins) > 0
}

// RateLimitConfig holds rate limiting configuration.
type RateLimitConfig struct {
	Enabled bool    `mapstructure:"enabled"`
	Rate    float64 `mapstructure:"rate"`
	Burst   int     `mapstructure:"burst"`
}

// StorageConfig holds object storage configuration.
type StorageConfig struct {
	Type      string      `mapstructure:"type"` // s3, azure, http, local
	LocalPath string      `mapstructure:"local_path"`
	S3        S3Config    `mapstructure:"s3"`
	Azure     AzureConfig `mapstructure:"azure"`
	HTTP      HTTPConfig  `mapstructure:"http"`
}

// S3Config holds AWS S3 configuration.
type S3Config struct {
	Bucket          string `mapstructure:"bucket"`
	Region          string `mapstructure:"region"`
	Prefix          string `mapstructure:"prefix"`
	Endpoint        string `mapstructure:"endpoint"`
	AccessKeyID     string `mapstructure:"access_key_id"`
	SecretAccessKey string `mapstructure:"secret_access_key"`
}

// AzureConfig holds Azure Blob Storage configuration.
type AzureConfig struct {
	Container        string `mapstructure:"container"`
	AccountName      string `mapstructure:"account_name"`
	AccountKey       string `mapstructure:"account_key"`
	ConnectionString string `mapstructure:"connection_string"`
	Prefix           string `mapstructure:"prefix"`
}

// HTTPConfig holds HTTP download configuration.
type HTTPConfig struct {
	BaseURL   string        `mapstructure:"base_url"`
	IndexFile string        `mapstructure:"index_file"` // default: index.txt
	Timeout   time.Duration `mapstructure:"timeout"`
	Username  string        `mapstructure:"username"`
	Password  string        `mapstructure:"password"`
}

// QueryConfig holds query-related configuration.
type QueryConfig struct {
	DefaultSRID  int           `mapstructure:"default_srid"`
	Timeout      time.Duration `mapstructure:"timeout"`
	MaxFeatures  int           `mapstructure:"max_features"`
	WithGeometry bool          `mapstructure:"with_geometry"` // Include geometry in results (default: false)
}

// TLSConfig holds TLS/CertMagic configuration.
type TLSConfig struct {
	Enabled  bool      `mapstructure:"enabled"`
	Domains  []string  `mapstructure:"domains"`
	Email    string    `mapstructure:"email"`
	CacheDir string    `mapstructure:"cache_dir"`
	Staging  bool      `mapstructure:"staging"` // Use Let's Encrypt staging
	DNS      DNSConfig `mapstructure:"dns"`
}

// DNSConfig holds DNS-01 challenge provider configuration for Azure DNS.
type DNSConfig struct {
	Provider          string `mapstructure:"provider"`            // DNS provider (azure)
	SubscriptionID    string `mapstructure:"subscription_id"`     // Azure subscription ID
	ResourceGroupName string `mapstructure:"resource_group_name"` // Azure resource group containing DNS zone
	ClientID          string `mapstructure:"client_id"`           // User Assigned Managed Identity client ID (optional)
}

// MetricsConfig holds metrics configuration: the Prometheus scrape
// endpoint (always on when Enabled) plus the optional OTLP push export
// (configured via OTLP).
type MetricsConfig struct {
	Enabled bool       `mapstructure:"enabled"`
	Port    int        `mapstructure:"port"`
	Path    string     `mapstructure:"path"`
	OTLP    OTLPConfig `mapstructure:"otlp"`
}

// OTLPConfig configures OTLP export for a single signal (metrics or others).
// An empty Endpoint falls back to the tracing.endpoint setting so a single
// collector can serve both signals without duplicate configuration.
type OTLPConfig struct {
	Enabled   bool              `mapstructure:"enabled"`
	Endpoint  string            `mapstructure:"endpoint"`  // host:port; empty ⇒ fall back to tracing.endpoint
	Transport string            `mapstructure:"transport"` // "http" or "grpc"
	Insecure  bool              `mapstructure:"insecure"`
	Headers   map[string]string `mapstructure:"headers"`
	Interval  time.Duration     `mapstructure:"interval"` // PeriodicReader collection interval (default 60s)
}

// LoggingConfig holds logging configuration.
type LoggingConfig struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"` // json, text
}

// SyncConfig holds remote storage sync configuration.
type SyncConfig struct {
	Enabled  bool          `mapstructure:"enabled"`
	Interval time.Duration `mapstructure:"interval"` // e.g., "1h", "24h", "30m"
}

// MCPConfig configures the in-process Model Context Protocol server. When
// enabled, ortus exposes a streamable-HTTP MCP endpoint on a separate port
// so AI agents (Claude Desktop, Claude Code, …) can query traces, package
// metadata, and perform point queries against this service. The bearer
// token is intentionally NOT in the config file — it's pulled from the
// ORTUS_MCP_TOKEN environment variable so it can't be checked in by
// accident.
type MCPConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Host    string `mapstructure:"host"`
	Port    int    `mapstructure:"port"`
	Path    string `mapstructure:"path"`
	// Token is populated from ORTUS_MCP_TOKEN at Load() time, NOT from the
	// config file. Required for non-loopback hosts.
	Token string `mapstructure:"-"`
}

// TracingTransport selects the OTLP transport (http/protobuf or grpc).
const (
	TracingTransportHTTP = "http"
	TracingTransportGRPC = "grpc"
)

// TracingConfig holds OpenTelemetry tracing configuration.
type TracingConfig struct {
	Enabled     bool              `mapstructure:"enabled"`
	ServiceName string            `mapstructure:"service_name"`
	Environment string            `mapstructure:"environment"`  // e.g., "dev", "prod" — sets deployment.environment.name
	Endpoint    string            `mapstructure:"endpoint"`     // OTLP collector endpoint as host:port; passed verbatim to otlptracehttp.WithEndpoint / otlptracegrpc.WithEndpoint
	Transport   string            `mapstructure:"transport"`    // "http" or "grpc"
	Insecure    bool              `mapstructure:"insecure"`     // disable TLS to the collector
	Headers     map[string]string `mapstructure:"headers"`      // OTLP exporter headers (e.g., auth tokens)
	SampleRatio float64           `mapstructure:"sample_ratio"` // 0.0..1.0; >=1.0 => AlwaysOn, <=0 => NeverSample, else ratio-based (parent-respecting)
	BufferSize  int               `mapstructure:"buffer_size"`  // number of traces retained in the in-memory ring buffer for MCP queries
	Attributes  map[string]string `mapstructure:"attributes"`   // additional static resource attributes
}

// Defaults sets the default configuration values.
func Defaults() {
	// Server defaults
	viper.SetDefault("server.host", "0.0.0.0")
	viper.SetDefault("server.port", 8080)
	viper.SetDefault("server.read_timeout", 30*time.Second)
	viper.SetDefault("server.write_timeout", 30*time.Second)
	viper.SetDefault("server.shutdown_timeout", 10*time.Second)
	viper.SetDefault("server.rate_limit.enabled", false)
	viper.SetDefault("server.rate_limit.rate", 100.0)
	viper.SetDefault("server.rate_limit.burst", 200)
	viper.SetDefault("server.cors.allowed_origins", []string{})
	viper.SetDefault("server.frontend_enabled", true)
	viper.SetDefault("server.ready_when_empty", true)

	// Storage defaults
	viper.SetDefault("storage.type", StorageTypeLocal)
	viper.SetDefault("storage.local_path", "./data")
	viper.SetDefault("storage.http.index_file", "index.txt")
	viper.SetDefault("storage.http.timeout", 5*time.Minute)

	// Query defaults
	viper.SetDefault("query.default_srid", 4326)
	viper.SetDefault("query.timeout", 30*time.Second)
	viper.SetDefault("query.max_features", 1000)
	viper.SetDefault("query.with_geometry", false)

	// TLS defaults
	viper.SetDefault("tls.enabled", false)
	viper.SetDefault("tls.cache_dir", "./.certmagic")
	viper.SetDefault("tls.staging", false)
	viper.SetDefault("tls.dns.provider", dnsProviderAzure)

	// Metrics defaults
	viper.SetDefault("metrics.enabled", true)
	viper.SetDefault("metrics.port", 9090)
	viper.SetDefault("metrics.path", "/metrics")
	viper.SetDefault("metrics.otlp.enabled", false)
	viper.SetDefault("metrics.otlp.transport", TracingTransportHTTP)
	viper.SetDefault("metrics.otlp.insecure", true)
	viper.SetDefault("metrics.otlp.interval", 60*time.Second)

	// Logging defaults
	viper.SetDefault("logging.level", "info")
	viper.SetDefault("logging.format", "json")

	// Sync defaults
	viper.SetDefault("sync.enabled", false)
	viper.SetDefault("sync.interval", time.Hour)

	// MCP defaults
	viper.SetDefault("mcp.enabled", false)
	viper.SetDefault("mcp.host", mcpLoopbackHost)
	viper.SetDefault("mcp.port", 9091)
	viper.SetDefault("mcp.path", "/mcp")

	// Tracing defaults
	viper.SetDefault("tracing.enabled", false)
	viper.SetDefault("tracing.service_name", "ortus")
	viper.SetDefault("tracing.environment", "")
	viper.SetDefault("tracing.endpoint", "")
	viper.SetDefault("tracing.transport", TracingTransportHTTP)
	viper.SetDefault("tracing.insecure", true)
	viper.SetDefault("tracing.sample_ratio", 1.0)
	viper.SetDefault("tracing.buffer_size", 256)
}

// Load loads configuration from environment and config file.
func Load(configPath string) (*Config, error) {
	Defaults()

	// Environment variable binding
	viper.SetEnvPrefix("ORTUS")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()

	// Config file
	if configPath != "" {
		viper.SetConfigFile(configPath)
	} else {
		viper.SetConfigName("config")
		viper.SetConfigType("yaml")
		viper.AddConfigPath(".")
		viper.AddConfigPath("./config")
		viper.AddConfigPath("/etc/ortus")
	}

	// Try to read config file (not required)
	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("reading config file: %w", err)
		}
	}

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshaling config: %w", err)
	}

	// Secrets that should NEVER be in a config file get loaded from env
	// directly so they don't get printed by `viper.Debug()` / leaked into
	// a marshaled config dump.
	cfg.MCP.Token = os.Getenv("ORTUS_MCP_TOKEN")

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validating config: %w", err)
	}

	return &cfg, nil
}

// Validate validates the configuration.
func (c *Config) Validate() error {
	if err := c.validateServer(); err != nil {
		return err
	}
	if err := c.validateTLS(); err != nil {
		return err
	}
	if err := c.validateStorage(); err != nil {
		return err
	}
	if err := c.validateTracing(); err != nil {
		return err
	}
	if err := c.validateMetricsOTLP(); err != nil {
		return err
	}
	return c.validateMCP()
}

func (c *Config) validateMCP() error {
	if !c.MCP.Enabled {
		return nil
	}
	if c.MCP.Port < 1 || c.MCP.Port > 65535 {
		return fmt.Errorf("invalid mcp.port: %d", c.MCP.Port)
	}
	if c.MCP.Path == "" {
		return fmt.Errorf("mcp.path must not be empty")
	}
	if c.MCP.Path[0] != '/' {
		// http.ServeMux.Handle panics on patterns without a leading slash —
		// fail fast at startup with a clear message rather than crashing
		// when the listener tries to bind.
		return fmt.Errorf("mcp.path %q must start with '/'", c.MCP.Path)
	}
	// Token is required when binding to anything but loopback. Loopback-only
	// listeners are unreachable from outside the host, so a missing token
	// only allows local processes (which are usually trusted) to call.
	// Empty host is NOT treated as loopback — Go's net.Listen binds to all
	// interfaces with an empty host, which would silently expose the
	// MCP endpoint without auth. Defaults set host to "127.0.0.1" so this
	// only matters when a user explicitly overrides it to an empty string.
	loopback := c.MCP.Host == mcpLoopbackHost || c.MCP.Host == "localhost" || c.MCP.Host == "::1"
	if !loopback && c.MCP.Token == "" {
		return fmt.Errorf("mcp.enabled is true and host %q is not loopback — ORTUS_MCP_TOKEN must be set", c.MCP.Host)
	}
	return nil
}

// MetricsOTLPEndpoint returns the effective endpoint for metric OTLP export.
// Falls back to tracing.endpoint when metrics.otlp.endpoint is empty so a
// single collector can serve both signals.
func (c *Config) MetricsOTLPEndpoint() string {
	if c.Metrics.OTLP.Endpoint != "" {
		return c.Metrics.OTLP.Endpoint
	}
	return c.Tracing.Endpoint
}

func (c *Config) validateMetricsOTLP() error {
	if !c.Metrics.OTLP.Enabled {
		return nil
	}
	if c.MetricsOTLPEndpoint() == "" {
		return fmt.Errorf("metrics.otlp.enabled is true but no endpoint is configured (set metrics.otlp.endpoint or tracing.endpoint)")
	}
	switch c.Metrics.OTLP.Transport {
	case "", TracingTransportHTTP, TracingTransportGRPC:
		// ok
	default:
		return fmt.Errorf("invalid metrics.otlp.transport %q (expected %q or %q)", c.Metrics.OTLP.Transport, TracingTransportHTTP, TracingTransportGRPC)
	}
	if c.Metrics.OTLP.Interval < 0 {
		return fmt.Errorf("metrics.otlp.interval must be >= 0, got %s", c.Metrics.OTLP.Interval)
	}
	return nil
}

func (c *Config) validateTracing() error {
	if !c.Tracing.Enabled {
		return nil
	}
	switch c.Tracing.Transport {
	case "", TracingTransportHTTP, TracingTransportGRPC:
		// ok
	default:
		return fmt.Errorf("invalid tracing transport %q (expected %q or %q)", c.Tracing.Transport, TracingTransportHTTP, TracingTransportGRPC)
	}
	if c.Tracing.BufferSize < 0 {
		return fmt.Errorf("tracing.buffer_size must be >= 0, got %d", c.Tracing.BufferSize)
	}
	if c.Tracing.SampleRatio < 0 || c.Tracing.SampleRatio > 1 {
		return fmt.Errorf("tracing.sample_ratio must be in [0, 1], got %f", c.Tracing.SampleRatio)
	}
	return nil
}

func (c *Config) validateServer() error {
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return fmt.Errorf("invalid server port: %d", c.Server.Port)
	}
	return nil
}

func (c *Config) validateTLS() error {
	if !c.TLS.Enabled {
		return nil
	}
	if len(c.TLS.Domains) == 0 {
		return fmt.Errorf("TLS enabled but no domains specified")
	}
	if c.TLS.Email == "" {
		return fmt.Errorf("TLS enabled but no email specified")
	}
	// Validate DNS-01 challenge configuration
	if c.TLS.DNS.Provider != dnsProviderAzure {
		return fmt.Errorf("unsupported DNS provider: %s (only 'azure' is supported)", c.TLS.DNS.Provider)
	}
	if c.TLS.DNS.SubscriptionID == "" {
		return fmt.Errorf("TLS enabled but no DNS subscription_id specified")
	}
	if c.TLS.DNS.ResourceGroupName == "" {
		return fmt.Errorf("TLS enabled but no DNS resource_group_name specified")
	}
	return nil
}

func (c *Config) validateStorage() error {
	switch c.Storage.Type {
	case StorageTypeLocal:
		return c.validateLocalStorage()
	case StorageTypeS3:
		return c.validateS3Storage()
	case StorageTypeAzure:
		return c.validateAzureStorage()
	case StorageTypeHTTP:
		return c.validateHTTPStorage()
	default:
		return fmt.Errorf("unknown storage type: %s", c.Storage.Type)
	}
}

func (c *Config) validateLocalStorage() error {
	if c.Storage.LocalPath == "" {
		return fmt.Errorf("local storage path is required")
	}
	return nil
}

func (c *Config) validateS3Storage() error {
	if c.Storage.S3.Bucket == "" {
		return fmt.Errorf("S3 bucket is required")
	}
	if c.Storage.S3.Region == "" {
		return fmt.Errorf("S3 region is required")
	}
	return nil
}

func (c *Config) validateAzureStorage() error {
	if c.Storage.Azure.Container == "" {
		return fmt.Errorf("azure container is required")
	}
	if c.Storage.Azure.AccountName == "" && c.Storage.Azure.ConnectionString == "" {
		return fmt.Errorf("azure account name or connection string is required")
	}
	return nil
}

func (c *Config) validateHTTPStorage() error {
	if c.Storage.HTTP.BaseURL == "" {
		return fmt.Errorf("HTTP base URL is required")
	}
	return nil
}

// Address returns the server address string.
func (c *ServerConfig) Address() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}
