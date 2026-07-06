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
		"applyFieldOrder",                 // SRID-aware field reordering
		"function parseCoordinatePair",    // pair splitter
		"handleCoordinatePaste",           // paste handler
		"coordX.addEventListener('paste'", // wired to the longitude input
		"coordY.addEventListener('paste'", // wired to the latitude input
		"function renderGazetteer",        // location-context renderer
		"if (data.gazetteer)",             // wired into displayResults
		"equivalent_description",          // admin-level meaning rendered
		"Namensquellen",                   // name_source explanations section
		"Datenlizenz",                     // dataset attribution rendered
	} {
		if !strings.Contains(html, marker) {
			t.Errorf("frontend is missing expected marker %q", marker)
		}
	}
}

// TestFrontendAccessibilityMarkers guards the accessibility affordances so they
// aren't silently dropped: announced status/error regions, an accessible name on
// the icon-only location button, keyboard-operable collapsible source headers, and
// reduced-motion support.
func TestFrontendAccessibilityMarkers(t *testing.T) {
	html := frontendHTML
	for _, marker := range []string{
		`role="alert"`,                             // error region announced
		`role="status"`,                            // loading + results summary announced
		`aria-label="Aktuellen Standort`,           // icon-only button has a name
		`role="button" tabindex="0" aria-expanded`, // collapsible header is keyboard-operable
		`addEventListener('keydown'`,               // Enter/Space toggle
		`prefers-reduced-motion`,                   // honors reduced-motion
		`:focus-visible`,                           // visible keyboard focus
		`min-height: 44px`,                         // touch-target size
	} {
		if !strings.Contains(html, marker) {
			t.Errorf("frontend is missing accessibility marker %q", marker)
		}
	}
}
