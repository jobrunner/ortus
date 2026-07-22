package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/viper"
)

// resetViper clears the global viper state so each Load-based test starts
// clean (viper is a process-global singleton).
func resetViper(t *testing.T) {
	t.Helper()
	viper.Reset()
	t.Cleanup(viper.Reset)
}

func TestLoadDefaults(t *testing.T) {
	resetViper(t)
	// An explicit, missing config path is an error; the no-path branch is the
	// one that tolerates a missing file (exercised below).
	if _, err := Load(filepath.Join(t.TempDir(), "does-not-exist.yaml")); err == nil {
		t.Fatal("expected error for explicitly missing config file")
	}

	resetViper(t)
	// No explicit path + no config file in cwd → defaults only, no error.
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load(\"\") with defaults: %v", err)
	}
	if cfg.Server.Host != "0.0.0.0" || cfg.Server.Port != 8080 {
		t.Errorf("server defaults = %s:%d, want 0.0.0.0:8080", cfg.Server.Host, cfg.Server.Port)
	}
	// Gazetteer startup warmup: on by default, at the shipped-dataset default point.
	if w := cfg.Gazetteer.Warmup; !w.Enabled || w.Lon != 9.93 || w.Lat != 49.79 {
		t.Errorf("gazetteer.warmup defaults = %+v, want {Enabled:true Lon:9.93 Lat:49.79}", w)
	}
	// Elevation is off by default: the gazetteer-owned DEM bundle path is unset.
	if bp := cfg.Gazetteer.Elevation.BundlePath; bp != "" {
		t.Errorf("gazetteer.elevation.bundle_path default = %q, want empty (feature off)", bp)
	}
	if cfg.Storage.Type != StorageTypeLocal || cfg.Storage.LocalPath != "./data" {
		t.Errorf("storage defaults = %+v", cfg.Storage)
	}
	if cfg.Query.MaxFeatures != 1000 {
		t.Errorf("query defaults = %+v", cfg.Query)
	}
	if b := cfg.Query.Batch; b.MaxPoints != 10000 || b.MaxSyncPoints != 1000 || b.Concurrency != 4 {
		t.Errorf("query.batch defaults = %+v, want {MaxPoints:10000 MaxSyncPoints:1000 Concurrency:4}", b)
	}
	if !cfg.Metrics.Enabled || cfg.Metrics.Port != 9090 {
		t.Errorf("metrics defaults = %+v", cfg.Metrics)
	}
	if cfg.Sync.Interval != time.Hour {
		t.Errorf("sync.interval default = %s, want 1h", cfg.Sync.Interval)
	}
}

func TestLoadFromFile(t *testing.T) {
	resetViper(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	yaml := `
server:
  host: "10.0.0.5"
  port: 18080
storage:
  type: local
  local_path: /srv/data
query:
  max_features: 42
logging:
  level: debug
  format: text
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Host != "10.0.0.5" || cfg.Server.Port != 18080 {
		t.Errorf("server = %s:%d, want 10.0.0.5:18080", cfg.Server.Host, cfg.Server.Port)
	}
	if cfg.Storage.LocalPath != "/srv/data" {
		t.Errorf("local_path = %q", cfg.Storage.LocalPath)
	}
	if cfg.Query.MaxFeatures != 42 {
		t.Errorf("max_features = %d, want 42", cfg.Query.MaxFeatures)
	}
	if cfg.Logging.Level != "debug" || cfg.Logging.Format != "text" {
		t.Errorf("logging = %+v", cfg.Logging)
	}
}

func TestLoadMCPTokenFromEnv(t *testing.T) {
	resetViper(t)
	t.Setenv("ORTUS_MCP_TOKEN", "s3cr3t")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.MCP.Token != "s3cr3t" {
		t.Errorf("MCP.Token = %q, want s3cr3t (from ORTUS_MCP_TOKEN)", cfg.MCP.Token)
	}
}

func TestLoadInvalidConfigFails(t *testing.T) {
	resetViper(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	// Invalid: unknown storage type → Validate() must reject at Load time.
	if err := os.WriteFile(path, []byte("storage:\n  type: ftp\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Error("Load should fail validation for unknown storage type")
	}
}

func TestValidateStorage(t *testing.T) {
	base := func() *Config {
		c := &Config{}
		c.Server.Port = 8080
		return c
	}

	cases := []struct {
		name    string
		mutate  func(*Config)
		wantErr bool
	}{
		{"local ok", func(c *Config) { c.Storage.Type = StorageTypeLocal; c.Storage.LocalPath = "./data" }, false},
		{"local missing path", func(c *Config) { c.Storage.Type = StorageTypeLocal }, true},
		{"s3 ok", func(c *Config) { c.Storage.Type = StorageTypeS3; c.Storage.S3.Bucket = "b"; c.Storage.S3.Region = "eu" }, false},
		{"s3 missing bucket", func(c *Config) { c.Storage.Type = StorageTypeS3; c.Storage.S3.Region = "eu" }, true},
		{"s3 missing region", func(c *Config) { c.Storage.Type = StorageTypeS3; c.Storage.S3.Bucket = "b" }, true},
		{"azure ok (account)", func(c *Config) {
			c.Storage.Type = StorageTypeAzure
			c.Storage.Azure.Container = "c"
			c.Storage.Azure.AccountName = "a"
		}, false},
		{"azure ok (connstr)", func(c *Config) {
			c.Storage.Type = StorageTypeAzure
			c.Storage.Azure.Container = "c"
			c.Storage.Azure.ConnectionString = "x"
		}, false},
		{"azure missing container", func(c *Config) { c.Storage.Type = StorageTypeAzure; c.Storage.Azure.AccountName = "a" }, true},
		{"azure missing creds", func(c *Config) { c.Storage.Type = StorageTypeAzure; c.Storage.Azure.Container = "c" }, true},
		{"http ok", func(c *Config) { c.Storage.Type = StorageTypeHTTP; c.Storage.HTTP.BaseURL = "https://x" }, false},
		{"http missing url", func(c *Config) { c.Storage.Type = StorageTypeHTTP }, true},
		{"unknown type", func(c *Config) { c.Storage.Type = "ftp" }, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := base()
			tc.mutate(c)
			err := c.Validate()
			if (err != nil) != tc.wantErr {
				t.Errorf("Validate() err = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestValidateServerPort(t *testing.T) {
	for _, port := range []int{0, -1, 70000} {
		c := &Config{}
		c.Server.Port = port
		c.Storage.Type = StorageTypeLocal
		c.Storage.LocalPath = "./data"
		if err := c.Validate(); err == nil {
			t.Errorf("port %d should be invalid", port)
		}
	}
}

func TestValidateTLS(t *testing.T) {
	mk := func() *Config {
		c := &Config{}
		c.Server.Port = 8080
		c.Storage.Type = StorageTypeLocal
		c.Storage.LocalPath = "./data"
		c.TLS.Enabled = true
		c.TLS.Domains = []string{"example.com"}
		c.TLS.Email = "a@b.c"
		c.TLS.DNS.Provider = dnsProviderAzure
		c.TLS.DNS.SubscriptionID = "sub"
		c.TLS.DNS.ResourceGroupName = "rg"
		return c
	}
	if err := mk().Validate(); err != nil {
		t.Fatalf("valid TLS config rejected: %v", err)
	}

	bad := []func(*Config){
		func(c *Config) { c.TLS.Domains = nil },
		func(c *Config) { c.TLS.Email = "" },
		func(c *Config) { c.TLS.DNS.Provider = "route53" },
		func(c *Config) { c.TLS.DNS.SubscriptionID = "" },
		func(c *Config) { c.TLS.DNS.ResourceGroupName = "" },
	}
	for i, mutate := range bad {
		c := mk()
		mutate(c)
		if err := c.Validate(); err == nil {
			t.Errorf("bad TLS config #%d should be rejected", i)
		}
	}
}

func TestValidateMCP(t *testing.T) {
	mk := func() *Config {
		c := &Config{}
		c.Server.Port = 8080
		c.Storage.Type = StorageTypeLocal
		c.Storage.LocalPath = "./data"
		c.MCP.Enabled = true
		c.MCP.Host = mcpLoopbackHost
		c.MCP.Port = 9091
		c.MCP.Path = "/mcp"
		return c
	}
	if err := mk().Validate(); err != nil {
		t.Fatalf("valid loopback MCP rejected: %v", err)
	}

	// Non-loopback without token must fail.
	c := mk()
	c.MCP.Host = "0.0.0.0"
	if err := c.Validate(); err == nil {
		t.Error("non-loopback MCP without token should fail")
	}
	// Non-loopback WITH token is fine.
	c.MCP.Token = "tok"
	if err := c.Validate(); err != nil {
		t.Errorf("non-loopback MCP with token should pass: %v", err)
	}
	// Path must start with '/'.
	c2 := mk()
	c2.MCP.Path = "mcp"
	if err := c2.Validate(); err == nil {
		t.Error("MCP path without leading slash should fail")
	}
}

func TestValidateGazetteer(t *testing.T) {
	mk := func() *Config {
		c := &Config{}
		c.Server.Port = 8080
		c.Storage.Type = StorageTypeLocal
		c.Storage.LocalPath = "./data"
		return c
	}

	// Disabled → no path requirements.
	if err := mk().Validate(); err != nil {
		t.Fatalf("disabled gazetteer rejected: %v", err)
	}

	// Enabled but missing geopackage_path must fail.
	c := mk()
	c.Gazetteer.Enabled = true
	c.Gazetteer.ManifestPath = "m.yaml"
	if err := c.Validate(); err == nil {
		t.Error("enabled gazetteer without geopackage_path should fail")
	}

	// Enabled but missing manifest_path must fail.
	c = mk()
	c.Gazetteer.Enabled = true
	c.Gazetteer.GeoPackagePath = "g.gpkg"
	if err := c.Validate(); err == nil {
		t.Error("enabled gazetteer without manifest_path should fail")
	}

	// Enabled with both paths is valid (level reference stays optional).
	c = mk()
	c.Gazetteer.Enabled = true
	c.Gazetteer.GeoPackagePath = "g.gpkg"
	c.Gazetteer.ManifestPath = "m.yaml"
	if err := c.Validate(); err != nil {
		t.Errorf("enabled gazetteer with both paths should pass: %v", err)
	}
}

func TestValidateMetricsOTLPAndTracing(t *testing.T) {
	mk := func() *Config {
		c := &Config{}
		c.Server.Port = 8080
		c.Storage.Type = StorageTypeLocal
		c.Storage.LocalPath = "./data"
		return c
	}

	// OTLP enabled without endpoint → error.
	c := mk()
	c.Metrics.OTLP.Enabled = true
	if err := c.Validate(); err == nil {
		t.Error("metrics OTLP without endpoint should fail")
	}
	// Falls back to tracing.endpoint.
	c.Tracing.Endpoint = "collector:4317"
	if err := c.Validate(); err != nil {
		t.Errorf("OTLP endpoint fallback to tracing.endpoint should pass: %v", err)
	}

	// Invalid tracing transport.
	c2 := mk()
	c2.Tracing.Enabled = true
	c2.Tracing.Transport = "carrier-pigeon"
	if err := c2.Validate(); err == nil {
		t.Error("invalid tracing transport should fail")
	}
	// Sample ratio out of range.
	c3 := mk()
	c3.Tracing.Enabled = true
	c3.Tracing.SampleRatio = 2.0
	if err := c3.Validate(); err == nil {
		t.Error("sample_ratio > 1 should fail")
	}
}

func TestMetricsOTLPEndpointFallback(t *testing.T) {
	c := &Config{}
	c.Tracing.Endpoint = "trace:4317"
	if got := c.MetricsOTLPEndpoint(); got != "trace:4317" {
		t.Errorf("fallback = %q, want trace:4317", got)
	}
	c.Metrics.OTLP.Endpoint = "metrics:4318"
	if got := c.MetricsOTLPEndpoint(); got != "metrics:4318" {
		t.Errorf("explicit = %q, want metrics:4318", got)
	}
}

func TestServerAddress(t *testing.T) {
	c := ServerConfig{Host: "example.org", Port: 8080}
	if got := c.Address(); got != "example.org:8080" {
		t.Errorf("Address() = %q, want example.org:8080", got)
	}
}

func TestCORSEnabled(t *testing.T) {
	var c CORSConfig
	if c.Enabled() {
		t.Error("empty CORS should be disabled")
	}
	c.AllowedOrigins = []string{"https://example.com"}
	if !c.Enabled() {
		t.Error("CORS with origins should be enabled")
	}
}
