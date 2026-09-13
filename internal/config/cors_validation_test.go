package config

import (
	"strings"
	"testing"
)

// A wildcard origin is only meaningful as part of a scheme/host/port triple.
// Written without a scheme it cannot be matched strictly, and accepting it
// would either widen access silently or match nothing at all — both worse than
// telling the operator at startup.
func TestValidateCORSRejectsSchemelessWildcard(t *testing.T) {
	tests := []struct {
		name    string
		origins []string
		wantErr bool
	}{
		{
			name:    "wildcard with scheme is fine",
			origins: []string{"https://*.example.com"},
		},
		{
			name:    "exact origin without wildcard is fine",
			origins: []string{"https://example.com"},
		},
		{
			name:    "wildcard with scheme and port is fine",
			origins: []string{"http://*.localhost:3000"},
		},
		{
			name:    "no origins at all is fine (CORS disabled)",
			origins: nil,
		},
		{
			name:    "scheme-less wildcard is rejected",
			origins: []string{"*.example.com"},
			wantErr: true,
		},
		{
			name:    "scheme-less wildcard among valid ones is rejected",
			origins: []string{"https://example.com", "*.sub.domain.tld"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				Server: ServerConfig{
					Port: 8080,
					CORS: CORSConfig{AllowedOrigins: tt.origins},
				},
			}

			err := cfg.validateCORS()

			if tt.wantErr {
				if err == nil {
					t.Fatalf("validateCORS() = nil; want an error for %v", tt.origins)
				}
				// The message has to tell the operator how to fix it, not just
				// that something is wrong.
				if !strings.Contains(err.Error(), "https://*.") {
					t.Errorf("error %q does not show the expected form (https://*.example.com)", err)
				}
				return
			}
			if err != nil {
				t.Errorf("validateCORS() = %v; want nil for %v", err, tt.origins)
			}
		})
	}
}

// Validate() must run the CORS check — a validator nothing calls is no gate.
func TestValidateRunsCORSCheck(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{
			Port: 8080,
			CORS: CORSConfig{AllowedOrigins: []string{"*.example.com"}},
		},
		Storage: StorageConfig{Type: StorageTypeLocal, LocalPath: "/tmp"},
	}

	if err := cfg.Validate(); err == nil {
		t.Error("Validate() accepted a scheme-less wildcard origin")
	}
}
