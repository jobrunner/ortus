package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
)

const exampleConfigPath = "../../config.yaml.example"

// TestConfigExampleNoDrift guards config.yaml.example against drift: every key
// in the example must map to a real field on the Config struct. viper silently
// ignores unknown keys, so without this a stale key (like the `default_srid`
// that lingered after it was removed from the struct) goes unnoticed —
// mapstructure's ErrorUnused turns exactly that into a failure.
func TestConfigExampleNoDrift(t *testing.T) {
	v := viper.New()
	v.SetConfigType("yaml") // the .example extension isn't auto-detected
	v.SetConfigFile(exampleConfigPath)
	if err := v.ReadInConfig(); err != nil {
		t.Fatalf("reading %s: %v", exampleConfigPath, err)
	}

	var cfg Config
	err := v.Unmarshal(&cfg, func(dc *mapstructure.DecoderConfig) {
		dc.ErrorUnused = true
	})
	if err != nil {
		t.Errorf("config.yaml.example has key(s) with no matching Config field "+
			"(stale/drifted — remove them or wire them into the struct):\n%v", err)
	}
}

// TestConfigExampleLoadsAndValidates ensures the documented example is not just
// structurally in sync but actually a usable, valid configuration.
func TestConfigExampleLoadsAndValidates(t *testing.T) {
	// Load() infers the format from the extension, so copy the example to a
	// real .yaml file before loading it.
	data, err := os.ReadFile(exampleConfigPath)
	if err != nil {
		t.Fatalf("reading %s: %v", exampleConfigPath, err)
	}
	dst := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		t.Fatalf("writing temp config: %v", err)
	}

	resetViper(t)
	if _, err := Load(dst); err != nil {
		t.Fatalf("config.yaml.example failed to load/validate: %v", err)
	}
}
