package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"

	"github.com/jobrunner/ortus/internal/domain"
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

// TestConfigExampleCORSOriginsAreValid checks the CORS origins that are
// COMMENTED OUT in the example.
//
// TestConfigExampleLoadsAndValidates cannot see them: the active value is
// `allowed_origins: []`, so the examples people uncomment first are invisible
// to it. That is how the example kept advertising scheme-less wildcards after
// validateCORS started rejecting them — copy the suggested line, and the
// service refuses to start. Check them with the parser the validator uses.
func TestConfigExampleCORSOriginsAreValid(t *testing.T) {
	data, err := os.ReadFile(exampleConfigPath)
	if err != nil {
		t.Fatalf("reading %s: %v", exampleConfigPath, err)
	}

	// A YAML list item holding a quoted string, commented or not:
	//     #   - "https://example.com"
	item := regexp.MustCompile(`^\s*#?\s*-\s*"([^"]+)"\s*$`)

	var origins []string
	inCORS := false
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "cors:") {
			inCORS = true
			continue
		}
		if !inCORS {
			continue
		}
		// The CORS block ends where the next top-level section begins.
		if line != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "#") {
			break
		}
		if m := item.FindStringSubmatch(line); m != nil {
			origins = append(origins, m[1])
		}
	}

	if len(origins) == 0 {
		t.Fatal("no CORS origin examples found in config.yaml.example — " +
			"either they are gone or this scraper no longer matches the file")
	}

	for _, origin := range origins {
		if _, err := domain.ParseOriginPattern(origin); err != nil {
			t.Errorf("example origin %q would be rejected at startup: %v", origin, err)
		}
	}
}
