package application

import (
	"context"
	"errors"
	"testing"

	"github.com/jobrunner/ortus/internal/ports/output"
)

type fakeTransformerCheck struct{ err error }

func (f fakeTransformerCheck) Check(context.Context) error { return f.err }

// TestHealthTransformerComponent: a broken coordinate transformer must show up
// in /health (components.transformer) and take the instance out of readiness —
// otherwise it only surfaces as silently incomplete query answers.
func TestHealthTransformerComponent(t *testing.T) {
	tests := []struct {
		name      string
		check     transformerChecker
		wantComp  string // "" = component absent
		wantReady bool
	}{
		{"no transformer wired", nil, "", true},
		{"transformer healthy", fakeTransformerCheck{}, "ok", true},
		{"transformer broken", fakeTransformerCheck{err: errors.New("canary failed")}, "error", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := newTestRegistry()
			markLoaded(registry)
			setSources(registry, map[string]*sourceEntry{"a": readyEntry("a")})
			svc := NewHealthService(registry, true, output.NoOpTracer{})
			if tt.check != nil {
				svc.SetTransformerCheck(tt.check)
			}

			if got := svc.IsReady(context.Background()); got != tt.wantReady {
				t.Errorf("IsReady = %v, want %v", got, tt.wantReady)
			}
			d := svc.GetHealthDetails(context.Background())
			if got := d.Components["transformer"]; got != tt.wantComp {
				t.Errorf("components.transformer = %q, want %q", got, tt.wantComp)
			}
			if d.Ready != tt.wantReady {
				t.Errorf("details.Ready = %v, want %v", d.Ready, tt.wantReady)
			}
		})
	}
}
