package http

import (
	"net/http"
	"testing"

	"github.com/jobrunner/ortus/internal/domain"
)

// gazetteerBody runs a gazetteer query against the given fake and returns the
// decoded response.
func gazetteerBody(t *testing.T, gaz fakeGazetteer) map[string]any {
	t.Helper()
	srv := newGazetteerServer(t, gaz)
	rec, body := doGET(t, srv, "/api/v1/gazetteer?lat=49.79&lon=9.95")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	return body
}

func available(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	a, ok := body["available"].(map[string]any)
	if !ok {
		t.Fatalf("no available block in the response: %v", body)
	}
	return a
}

// The point of the block: two responses that both carry mountains:null, told
// apart by whether this deployment can answer the block at all.
func TestGazetteer_NullIsDisambiguatedByAvailability(t *testing.T) {
	noResult := gazetteerBody(t, fakeGazetteer{
		loc: sampleLocality(), fix: sampleFix(),
		caps: &domain.GazetteerCapabilities{Mountains: true},
	})
	notDeployed := gazetteerBody(t, fakeGazetteer{
		loc: sampleLocality(), fix: sampleFix(),
		caps: &domain.GazetteerCapabilities{Mountains: false},
	})

	if noResult["mountains"] != nil || notDeployed["mountains"] != nil {
		t.Fatal("both cases must render mountains as null — that is the premise")
	}
	if available(t, noResult)["mountains"] != true {
		t.Error("a deployment WITH the layer must report mountains available, so null reads as 'no mountain here'")
	}
	if available(t, notDeployed)["mountains"] != false {
		t.Error("a deployment WITHOUT the layer must report mountains unavailable, so null is not mistaken for a real answer")
	}
}

func TestGazetteer_AvailabilityCoversEveryOptionalBlock(t *testing.T) {
	body := gazetteerBody(t, fakeGazetteer{
		loc: sampleLocality(), fix: sampleFix(),
		caps: &domain.GazetteerCapabilities{
			Islands: true, Mountains: false, Elevation: true, Exposure: false,
		},
	})
	a := available(t, body)
	for key, want := range map[string]bool{
		"islands": true, "mountains": false, "elevation": true, "exposure": false,
	} {
		got, ok := a[key]
		if !ok {
			t.Errorf("available is missing %q — every nullable optional block needs an entry", key)
			continue
		}
		if got != want {
			t.Errorf("available[%q] = %v, want %v", key, got, want)
		}
	}
}

func TestGazetteer_AvailabilityIsIndependentOfWhetherAPointHasData(t *testing.T) {
	// A deployment that has the layer keeps reporting it available even where a
	// particular point resolves to nothing — otherwise the flag would just restate
	// the block's nullness and carry no information.
	withData := gazetteerBody(t, fakeGazetteer{
		loc: sampleLocality(), fix: sampleFix(),
		mountains: &domain.MountainResult{},
		caps:      &domain.GazetteerCapabilities{Mountains: true},
	})
	withoutData := gazetteerBody(t, fakeGazetteer{
		loc: sampleLocality(), fix: sampleFix(),
		caps: &domain.GazetteerCapabilities{Mountains: true},
	})
	if available(t, withData)["mountains"] != true || available(t, withoutData)["mountains"] != true {
		t.Error("availability must not depend on the per-point result")
	}
}
