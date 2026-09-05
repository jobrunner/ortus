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
		"applyFieldOrder",                     // SRID-aware field reordering
		"function parseCoordinatePair",        // pair splitter
		"handleCoordinatePaste",               // paste handler
		"coordX.addEventListener('paste'",     // wired to the longitude input
		"coordY.addEventListener('paste'",     // wired to the latitude input
		"function renderGazetteer",            // location-context renderer
		"function hasGazetteerContent",        // empty-block guard
		"hasGazetteerContent(data.gazetteer)", // wired into displayResults
		"equivalent_description",              // admin-level meaning rendered
		"Namensquellen",                       // name_source explanations section
		"Datenlizenz",                         // dataset attribution rendered
		"gaz-elevation",                       // elevation rendered in the gazetteer block
		"gaz.islands",                         // islands rendered in the gazetteer block
		"gaz.exposure",                        // exposure rendered in the gazetteer block
		"Exposition",                          // exposure section label
		"function httpUrl",                    // scheme guard for source links
		"'Lat: ' + coord.y",                   // WGS84 result shows Lat before Lon
		"data.wgs84",                          // reprojected WGS84 shown for projected SRIDs
		`<option value="mgrs">`,               // MGRS entry in the coordinate-system dropdown
		`id="coordMgrs"`,                      // single MGRS text input
		`id="groupMgrs"`,                      // its form-group, swapped in for coordGrid
		"32U NA 01234 56789",                  // MGRS placeholder example
		"srid === 'mgrs'",                     // MGRS branch in the change/submit/display logic
	} {
		if !strings.Contains(html, marker) {
			t.Errorf("frontend is missing expected marker %q", marker)
		}
	}
}

// TestFrontendGazetteerSectionOrder pins the gazetteer render order:
// islands < elevation < bearing < exposure (containment, then height, then
// bearing, with exposure next to the bearing).
func TestFrontendGazetteerSectionOrder(t *testing.T) {
	html := frontendHTML
	iIslands := strings.Index(html, `Insel`)       // islands section label
	iElev := strings.Index(html, `Höhe`)           // elevation section label
	iBearing := strings.Index(html, `Peilung`)     // bearing section label
	iExposure := strings.Index(html, `Exposition`) // exposure section label
	if iIslands < 0 || iElev < 0 || iBearing < 0 || iExposure < 0 {
		t.Fatalf("gazetteer section labels missing: islands=%d elevation=%d bearing=%d exposure=%d", iIslands, iElev, iBearing, iExposure)
	}
	if iIslands >= iElev || iElev >= iBearing || iBearing >= iExposure {
		t.Errorf("expected islands < elevation < bearing < exposure; got islands=%d elevation=%d bearing=%d exposure=%d", iIslands, iElev, iBearing, iExposure)
	}
}

// TestRenderFrontendInjectsVersion checks the footer version substitution: the
// placeholder is gone, the version is present, and an HTML-metachar version is
// escaped rather than injected verbatim.
func TestRenderFrontendInjectsVersion(t *testing.T) {
	page := string(renderFrontend("v1.2.3"))
	if strings.Contains(page, "__ORTUS_VERSION__") {
		t.Error("version placeholder was not substituted")
	}
	if !strings.Contains(page, "ortus v1.2.3") {
		t.Error("rendered page is missing the footer version")
	}

	escaped := string(renderFrontend("<script>x</script>"))
	if strings.Contains(escaped, "<script>x</script>") {
		t.Error("version was not HTML-escaped")
	}
	if !strings.Contains(escaped, "&lt;script&gt;x&lt;/script&gt;") {
		t.Error("expected the escaped version in the rendered page")
	}
}

// TestFrontendTabsAndOptions guards the two-tab layout (single coordinate /
// batch) and the collapsed-by-default options accordion with the independent
// with-gazetteer / with-sources checkboxes (both checked by default).
func TestFrontendTabsAndOptions(t *testing.T) {
	html := frontendHTML
	for _, marker := range []string{
		`role="tablist"`,          // accessible tab bar
		`role="tabpanel"`,         // panels wired to the tabs
		`>Einzelkoordinate<`,      // tab 1 label
		`>Batch<`,                 // tab 2 label
		`id="tabSingle"`,          // tab buttons
		`id="tabBatch"`,           //
		`id="panelSingle"`,        // tab panels
		`id="panelBatch"`,         //
		`id="withGazetteer"`,      // single-tab option checkboxes
		`id="withSources"`,        //
		`id="batchWithGazetteer"`, // batch-tab option checkboxes
		`id="batchWithSources"`,   //
		`with-gazetteer=0`,        // unchecked → opt-out param on the query URL
		`with-sources=0`,          //
		`body['with-gazetteer']`,  // batch body fields
		`body['with-sources']`,    //
	} {
		if !strings.Contains(html, marker) {
			t.Errorf("frontend is missing tabs/options marker %q", marker)
		}
	}
	// Both checkboxes default to checked, and the options accordions start closed.
	for _, id := range []string{"withGazetteer", "withSources", "batchWithGazetteer", "batchWithSources"} {
		i := strings.Index(html, `id="`+id+`"`)
		if i < 0 {
			continue // reported above
		}
		// The checked attribute must sit inside the same <input> tag.
		tagEnd := strings.Index(html[i:], ">")
		if tagEnd < 0 || !strings.Contains(html[i:i+tagEnd], "checked") {
			t.Errorf("checkbox %q is not checked by default", id)
		}
	}
	if !strings.Contains(html, `class="options-card`) {
		t.Errorf("options accordion card missing")
	}
	if strings.Contains(html, `class="options-card source-card expanded`) {
		t.Errorf("options accordion must start closed (no expanded class)")
	}
}

// TestFrontendBatchElements guards the batch tab: textarea with a format
// placeholder, CSV upload, the compact result table and the export buttons
// (CSV download, copy as CSV, copy as JSON).
func TestFrontendBatchElements(t *testing.T) {
	html := frontendHTML
	for _, marker := range []string{
		`id="batchInput"`,          // coordinates textarea
		`52.52, 13.405`,            // placeholder shows the line format
		`id="batchFile"`,           // hidden file input
		`accept=".csv,text/csv"`,   // CSV upload
		`id="batchCsvUploadBtn"`,   // upload button
		`id="batchSrid"`,           // batch has its own coordinate-system select
		`id="batchResults"`,        // result area below the form
		`id="batchDownloadBtn"`,    // CSV download
		`id="batchCopyCsvBtn"`,     // copy as CSV
		`id="batchCopyJsonBtn"`,    // copy as JSON
		`function buildBatchCSV`,   // CSV flattener
		`admin_path`,               // gazetteer summary column
		`/api/v1/query/batch`,      // posts to the batch endpoint
		`function parseBatchLines`, // line parser (id + coordinate, tolerant)
		`FileReader`,               // client-side CSV read
		`navigator.clipboard`,      // clipboard copy
	} {
		if !strings.Contains(html, marker) {
			t.Errorf("frontend is missing batch marker %q", marker)
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
