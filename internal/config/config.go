// Package config provides configuration management using Viper.
package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config holds all application configuration.
type Config struct {
	Server  ServerConfig  `mapstructure:"server"`
	Storage StorageConfig `mapstructure:"storage"`
	Query   QueryConfig   `mapstructure:"query"`
	TLS     TLSConfig     `mapstructure:"tls"`
	Metrics MetricsConfig `mapstructure:"metrics"`
	Logging LoggingConfig `mapstructure:"logging"`
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
	DefaultSRID int           `mapstructure:"default_srid"`
	Timeout     time.Duration `mapstructure:"timeout"`
	MaxFeatures int           `mapstructure:"max_features"`
}

// TLSConfig holds TLS/CertMagic configuration.
type TLSConfig struct {
	Enabled  bool     `mapstructure:"enabled"`
	Domains  []string `mapstructure:"domains"`
	Email    string   `mapstructure:"email"`
	CacheDir string   `mapstructure:"cache_dir"`
	Staging  bool     `mapstructure:"staging"` // Use Let's Encrypt staging
}

// MetricsConfig holds Prometheus metrics configuration.
type MetricsConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Path    string `mapstructure:"path"`
}

// LoggingConfig holds logging configuration.
type LoggingConfig struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"` // json, text
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

	// Storage defaults
	viper.SetDefault("storage.type", "local")
	viper.SetDefault("storage.local_path", "./data")
	viper.SetDefault("storage.http.index_file", "index.txt")
	viper.SetDefault("storage.http.timeout", 5*time.Minute)

	// Query defaults
	viper.SetDefault("query.default_srid", 4326)
	viper.SetDefault("query.timeout", 30*time.Second)
	viper.SetDefault("query.max_features", 1000)

	// TLS defaults
	viper.SetDefault("tls.enabled", false)
	viper.SetDefault("tls.cache_dir", "./.certmagic")
	viper.SetDefault("tls.staging", false)

	// Metrics defaults
	viper.SetDefault("metrics.enabled", true)
	viper.SetDefault("metrics.path", "/metrics")

	// Logging defaults
	viper.SetDefault("logging.level", "info")
	viper.SetDefault("logging.format", "json")
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

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validating config: %w", err)
	}

	return &cfg, nil
}

// Validate validates the configuration.
func (c *Config) Validate() error {
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return fmt.Errorf("invalid server port: %d", c.Server.Port)
	}

	if c.TLS.Enabled {
		if len(c.TLS.Domains) == 0 {
			return fmt.Errorf("TLS enabled but no domains specified")
		}
		if c.TLS.Email == "" {
			return fmt.Errorf("TLS enabled but no email specified")
		}
	}

	switch c.Storage.Type {
	case "local":
		if c.Storage.LocalPath == "" {
			return fmt.Errorf("local storage path is required")
		}
	case "s3":
		if c.Storage.S3.Bucket == "" {
			return fmt.Errorf("S3 bucket is required")
		}
		if c.Storage.S3.Region == "" {
			return fmt.Errorf("S3 region is required")
		}
	case "azure":
		if c.Storage.Azure.Container == "" {
			return fmt.Errorf("azure container is required")
		}
		if c.Storage.Azure.AccountName == "" && c.Storage.Azure.ConnectionString == "" {
			return fmt.Errorf("azure account name or connection string is required")
		}
	case "http":
		if c.Storage.HTTP.BaseURL == "" {
			return fmt.Errorf("HTTP base URL is required")
		}
	default:
		return fmt.Errorf("unknown storage type: %s", c.Storage.Type)
	}

	return nil
}

// Address returns the server address string.
func (c *ServerConfig) Address() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}
