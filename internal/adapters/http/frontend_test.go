package http

import (
	"strings"
	"testing"
)

// TestFrontendCoordinateInputWiring guards the embedded mini-frontend against
// accidental removal of the lat-first field order and the smart coordinate-paste
// logic. The parsing heuristic itself is client-side JS (vanilla, no framework),
// so this asserts the markers are present rather than exercising the JS.
func TestFrontendCoordinateInputWiring(t *testing.T) {
	html := frontendHTML

	// Classic navigation order: the latitude group (groupY) is rendered before the
	// longitude group (groupX) by default (WGS84).
	iY := strings.Index(html, `id="groupY"`)
	iX := strings.Index(html, `id="groupX"`)
	if iY < 0 || iX < 0 {
		t.Fatalf("coordinate groups missing: groupY=%d groupX=%d", iY, iX)
	}
	if iY > iX {
		t.Errorf("expected latitude field (groupY) before longitude field (groupX); got groupY at %d, groupX at %d", iY, iX)
	}

	for _, marker := range []string{
		"applyFieldOrder",              // SRID-aware field reordering
		"function parseCoordinatePair", // pair splitter
		"handleCoordinatePaste",        // paste handler
		"addEventListener('paste'",     // wired to the inputs
	} {
		if !strings.Contains(html, marker) {
			t.Errorf("frontend is missing expected marker %q", marker)
		}
	}
}
