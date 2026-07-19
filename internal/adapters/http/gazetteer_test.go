package http

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"go.opentelemetry.io/otel/metric/noop"

	"github.com/jobrunner/ortus/internal/application"
	"github.com/jobrunner/ortus/internal/config"
	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/input"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// fakeGazetteer is a canned input.Gazetteer for handler tests.
type fakeGazetteer struct {
	loc        *domain.Locality
	locErr     error
	islands    []domain.Island
	islandsErr error
	fix        *domain.Fix
	fixErr     error
	elev       *domain.Elevation
	elevErr    error
}

func (f fakeGazetteer) Locate(context.Context, domain.Coordinate) (*domain.Locality, error) {
	return f.loc, f.locErr
}
func (f fakeGazetteer) Bearing(context.Context, domain.Coordinate, domain.BearingPolicy) (*domain.Fix, error) {
	return f.fix, f.fixErr
}
func (f fakeGazetteer) Islands(context.Context, domain.Coordinate) ([]domain.Island, error) {
	return f.islands, f.islandsErr
}
func (f fakeGazetteer) Elevation(context.Context, domain.Coordinate) (*domain.Elevation, error) {
	return f.elev, f.elevErr
}

func newGazetteerServer(t *testing.T, gaz input.Gazetteer) *Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	reg := application.NewSourceRegistry(
		[]output.SpatialSource{&mockRepository{}}, &mockStorage{},
		noop.NewMeterProvider().Meter("test"), output.NoOpTracer{}, logger, "/tmp",
	)
	_ = reg.LoadAll(context.Background())
	health := application.NewHealthService(reg, true, output.NoOpTracer{})
	query := application.NewQueryService(reg, nil, noop.NewMeterProvider().Meter("test"),
		output.NoOpTracer{}, logger, application.QueryServiceConfig{})

	return NewServer(
		config.ServerConfig{Host: "localhost", Port: 8080, ReadTimeout: time.Second, WriteTimeout: time.Second},
		query, reg, health, nil, logger, false,
		ServerOptions{Gazetteer: gaz, GazetteerLicense: sampleGazetteerLicense()},
	)
}

func sampleGazetteerLicense() domain.License {
	return domain.License{
		Name:        "ODbL-1.0",
		URL:         "https://opendatacommons.org/licenses/odbl/1-0/",
		Attribution: "© OpenStreetMap contributors (ODbL 1.0)",
	}
}

func sampleLocality() *domain.Locality {
	return &domain.Locality{CountryISO: "DE", Chain: []domain.AdminUnit{
		{Level: 8, Name: "Würzburg", Equivalent: "municipality"},
		{Level: 4, Name: "Bayern", Equivalent: "state"},
	}}
}

func sampleFix() *domain.Fix {
	return &domain.Fix{
		Reference:  domain.Place{Name: "Würzburg", Class: domain.ClassCity},
		DistanceKM: 4, Azimuth: 90, Compass: "E", Label: "4 km E Würzburg",
	}
}

func doGET(t *testing.T, srv *Server, path string) (rec *httptest.ResponseRecorder, body map[string]any) {
	t.Helper()
	rec = httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
	}
	return rec, body
}

func TestGazetteerEndpoint(t *testing.T) {
	srv := newGazetteerServer(t, fakeGazetteer{loc: sampleLocality(), fix: sampleFix()})
	rec, body := doGET(t, srv, "/api/v1/gazetteer?lon=9.93&lat=49.79")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	admin, ok := body["admin"].(map[string]any)
	if !ok || admin["country_iso"] != "DE" {
		t.Errorf("admin = %v, want country_iso DE", body["admin"])
	}
	bearing, ok := body["bearing"].(map[string]any)
	if !ok || bearing["label"] != "4 km E Würzburg" {
		t.Errorf("bearing = %v, want label '4 km E Würzburg'", body["bearing"])
	}
}

func TestGazetteerSourcesBlock(t *testing.T) {
	// A locality whose two units share one romanization code, plus a bearing
	// anchor with a different code → the response-wide "sources" block lists each
	// distinct code once, and every record carries its code + native name.
	loc := &domain.Locality{CountryISO: "GR", Chain: []domain.AdminUnit{
		{Level: 7, Name: "Dimos Thessalonikis", NameNative: "Δήμος Θεσσαλονίκης",
			NameSource: domain.NameProvenance{Code: "translit-el-843", Short: "Greek ELOT 743", Long: "Romanized from Greek using ELOT 743.", Standard: "ELOT 743 / UN / ISO 843"},
			Equivalent: "municipality", LocalTerm: "Δήμοι", EquivalentDesc: "Municipality / commune"},
		{Level: 4, Name: "Kentriki Makedonia", NameNative: "Κεντρική Μακεδονία",
			NameSource: domain.NameProvenance{Code: "translit-el-843", Short: "Greek ELOT 743", Long: "Romanized from Greek using ELOT 743.", Standard: "ELOT 743 / UN / ISO 843"},
			Equivalent: "region"},
	}}
	fix := &domain.Fix{
		Reference: domain.Place{Name: "Thessaloniki", NameNative: "Θεσσαλονίκη", Class: domain.ClassCity,
			NameSource: domain.NameProvenance{Code: "latin-osm", Short: "OSM name", Long: "OSM name tag.", Standard: ""}},
		DistanceKM: 0.4, Label: "in Thessaloniki", Inside: true,
	}
	srv := newGazetteerServer(t, fakeGazetteer{loc: loc, fix: fix})
	rec, body := doGET(t, srv, "/api/v1/gazetteer?lon=22.94&lat=40.64")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	// Two distinct codes appear across the two units + the anchor.
	sources, ok := body["sources"].([]any)
	if !ok || len(sources) != 2 {
		t.Fatalf("sources = %v, want 2 distinct entries", body["sources"])
	}
	got := map[string]map[string]any{}
	for _, s := range sources {
		m := s.(map[string]any)
		got[m["code"].(string)] = m
	}
	if el, ok := got["translit-el-843"]; !ok || el["standard"] != "ELOT 743 / UN / ISO 843" {
		t.Errorf("sources[translit-el-843] = %v, want standard set", got["translit-el-843"])
	}
	if _, ok := got["latin-osm"]; !ok {
		t.Errorf("sources missing latin-osm; got %v", got)
	}

	// Per-record inline code + native name.
	admin := body["admin"].(map[string]any)
	hierarchy := admin["hierarchy"].([]any)
	first := hierarchy[0].(map[string]any)
	if first["name_source"] != "translit-el-843" || first["name_native"] != "Δήμος Θεσσαλονίκης" {
		t.Errorf("hierarchy[0] = %v, want el code + native name", first)
	}
	if first["local_term"] != "Δήμοι" || first["equivalent_description"] != "Municipality / commune" {
		t.Errorf("hierarchy[0] meaning = %v, want local_term + equivalent_description", first)
	}
	bearing := body["bearing"].(map[string]any)
	if bearing["name_source"] != "latin-osm" || bearing["name_native"] != "Θεσσαλονίκη" {
		t.Errorf("bearing = %v, want latin-osm code + native name", bearing)
	}
	// "in X" (inside the place's admin unit) is signaled by inside=true.
	if bearing["inside"] != true || bearing["label"] != "in Thessaloniki" {
		t.Errorf("bearing inside/label = %v / %v, want true / 'in Thessaloniki'", bearing["inside"], bearing["label"])
	}

	// Dataset-wide attribution/license in the same response.
	license, ok := body["license"].(map[string]any)
	if !ok || license["name"] != "ODbL-1.0" || license["attribution"] != "© OpenStreetMap contributors (ODbL 1.0)" {
		t.Errorf("license = %v, want ODbL attribution", body["license"])
	}
}

func TestGazetteerElevationBlock(t *testing.T) {
	elev := &domain.Elevation{
		Meters: 177.0, AccuracyM: 4.0, AccuracyBasis: "GLO-30 LE90 (absolute)",
		HorizontalM: 6.0, VerticalDatum: "EGM2008", SeaLevel: false, SurfaceModel: "DSM",
		License: domain.License{Name: "Copernicus DEM GLO-30", URL: "https://example/glo30", Attribution: "© DLR/Airbus/ESA"},
	}
	srv := newGazetteerServer(t, fakeGazetteer{loc: sampleLocality(), fix: sampleFix(), elev: elev})
	rec, body := doGET(t, srv, "/api/v1/gazetteer?lon=9.93&lat=49.79")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	e, ok := body["elevation"].(map[string]any)
	if !ok {
		t.Fatalf("elevation missing; got %v", body["elevation"])
	}
	if e["meters"] != 177.0 || e["vertical_datum"] != "EGM2008" || e["sea_level"] != false {
		t.Errorf("elevation core = %v", e)
	}
	if e["accuracy_m"] != 4.0 || e["accuracy_basis"] != "GLO-30 LE90 (absolute)" || e["horizontal_accuracy_m"] != 6.0 {
		t.Errorf("elevation accuracy = %v", e)
	}
	if e["surface_model"] != "DSM" {
		t.Errorf("surface_model = %v, want DSM", e["surface_model"])
	}
	// The DEM source attribution is nested under "source", distinct from the
	// response-wide gazetteer "license" (OSM/ODbL).
	src, ok := e["source"].(map[string]any)
	if !ok || src["name"] != "Copernicus DEM GLO-30" {
		t.Fatalf("elevation.source = %v, want Copernicus", e["source"])
	}
	if lic, ok := body["license"].(map[string]any); ok && lic["name"] == src["name"] {
		t.Errorf("DEM source must be distinct from gazetteer license, both = %v", lic["name"])
	}
}

func TestGazetteerIslandsBlock(t *testing.T) {
	islands := []domain.Island{
		{Name: "Sylt", NameNative: "Söl'ring", NameSource: domain.NameProvenance{Code: "latin-osm"}},
		{Name: "Amrum"},
	}
	srv := newGazetteerServer(t, fakeGazetteer{loc: sampleLocality(), fix: sampleFix(), islands: islands})
	rec, body := doGET(t, srv, "/api/v1/gazetteer?lon=8.31&lat=54.9")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	arr, ok := body["islands"].([]any)
	if !ok {
		t.Fatalf("islands missing or not an array; got %v", body["islands"])
	}
	if len(arr) != 2 {
		t.Fatalf("islands len = %d, want 2", len(arr))
	}
	first, _ := arr[0].(map[string]any)
	if first["name"] != "Sylt" || first["name_native"] != "Söl'ring" || first["name_source"] != "latin-osm" {
		t.Errorf("islands[0] = %v, want Sylt/Söl'ring/latin-osm", first)
	}
	// The island's name_source is echoed in the response-wide sources block.
	if srcs, ok := body["sources"].([]any); !ok || len(srcs) == 0 {
		t.Errorf("sources should include the island name_source, got %v", body["sources"])
	}
}

func TestGazetteerIslandsOmittedWhenAbsent(t *testing.T) {
	// No islands → the block is null (point on no island / layer unconfigured).
	srv := newGazetteerServer(t, fakeGazetteer{loc: sampleLocality(), fix: sampleFix()})
	_, body := doGET(t, srv, "/api/v1/gazetteer?lon=9.93&lat=49.79")
	if body["islands"] != nil {
		t.Errorf("islands = %v, want null when the point is on no island", body["islands"])
	}
}

func TestGazetteerElevationSeaLevel(t *testing.T) {
	// No DEM tile → sea-level convention: meters 0, sea_level true.
	elev := &domain.Elevation{Meters: 0, SeaLevel: true, VerticalDatum: "EGM2008"}
	srv := newGazetteerServer(t, fakeGazetteer{loc: sampleLocality(), fix: sampleFix(), elev: elev})
	rec, body := doGET(t, srv, "/api/v1/gazetteer?lon=8.0&lat=54.0")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	e, ok := body["elevation"].(map[string]any)
	if !ok || e["sea_level"] != true || e["meters"] != 0.0 {
		t.Errorf("elevation = %v, want sea_level true / meters 0", body["elevation"])
	}
}

func TestGazetteerElevationOmittedWhenUnwired(t *testing.T) {
	// Elevation returns (nil, nil) when the feature is off → block absent.
	srv := newGazetteerServer(t, fakeGazetteer{loc: sampleLocality(), fix: sampleFix()})
	_, body := doGET(t, srv, "/api/v1/gazetteer?lon=9.93&lat=49.79")
	if body["elevation"] != nil {
		t.Errorf("elevation = %v, want null when unwired", body["elevation"])
	}
}

func TestGazetteerEndpointPartial(t *testing.T) {
	// No admin coverage (ErrNotFound) but a bearing exists → admin null, bearing set.
	srv := newGazetteerServer(t, fakeGazetteer{locErr: domain.ErrNotFound, fix: sampleFix()})
	rec, body := doGET(t, srv, "/api/v1/gazetteer?lon=9.93&lat=49.79")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if body["admin"] != nil {
		t.Errorf("admin = %v, want null", body["admin"])
	}
	if _, ok := body["bearing"].(map[string]any); !ok {
		t.Errorf("bearing missing, want set")
	}
}

func TestGazetteerEndpointError(t *testing.T) {
	srv := newGazetteerServer(t, fakeGazetteer{locErr: errors.New("db down")})
	rec, _ := doGET(t, srv, "/api/v1/gazetteer?lon=9.93&lat=49.79")
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

func TestGazetteerRouteAbsentWhenDisabled(t *testing.T) {
	srv := newGazetteerServer(t, nil) // no gazetteer wired
	rec, _ := doGET(t, srv, "/api/v1/gazetteer?lon=9.93&lat=49.79")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (route not registered)", rec.Code)
	}
}

func TestQueryGazetteerDefaultOn(t *testing.T) {
	srv := newGazetteerServer(t, fakeGazetteer{loc: sampleLocality(), fix: sampleFix()})

	// Default (no flag) → gazetteer section present, with the full metadata a
	// client needs: admin hierarchy, bearing, sources and dataset license.
	_, body := doGET(t, srv, "/api/v1/query?lon=9.93&lat=49.79")
	gaz, ok := body["gazetteer"].(map[string]any)
	if !ok {
		t.Fatalf("default: gazetteer section missing; got keys %v", keysOf(body))
	}
	if _, ok := gaz["admin"].(map[string]any); !ok {
		t.Errorf("gazetteer.admin missing")
	}
	if _, ok := gaz["bearing"].(map[string]any); !ok {
		t.Errorf("gazetteer.bearing missing")
	}
	if _, ok := gaz["sources"].([]any); !ok {
		t.Errorf("gazetteer.sources missing")
	}
	if lic, ok := gaz["license"].(map[string]any); !ok || lic["name"] != "ODbL-1.0" {
		t.Errorf("gazetteer.license = %v, want ODbL", gaz["license"])
	}
	// The core PiP results are still present alongside it.
	if _, ok := body["results"]; !ok {
		t.Errorf("query results missing")
	}

	// Explicit opt-out → no gazetteer section.
	for _, off := range []string{"0", "false", "no", "off"} {
		_, body = doGET(t, srv, "/api/v1/query?lon=9.93&lat=49.79&with-gazetteer="+off)
		if _, present := body["gazetteer"]; present {
			t.Errorf("with-gazetteer=%s: gazetteer section should be absent", off)
		}
	}
}

func keysOf(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}

func TestGazetteerEnrichmentRequested(t *testing.T) {
	// Default on: absent, truthy, and any unrecognized value keep enrichment on.
	for _, v := range []string{"", "1", "true", "yes", "on", "2", "maybe"} {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/query?with-gazetteer="+v, nil)
		if !gazetteerEnrichmentRequested(r) {
			t.Errorf("with-gazetteer=%q = false, want on (default)", v)
		}
	}
	// Off only on explicit falsy (case-insensitive).
	for _, v := range []string{"0", "false", "no", "off", "FALSE", "Off"} {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/query?with-gazetteer="+v, nil)
		if gazetteerEnrichmentRequested(r) {
			t.Errorf("with-gazetteer=%q = true, want off", v)
		}
	}
}

func TestIsWGS84(t *testing.T) {
	if !isWGS84(domain.Coordinate{SRID: 0}) {
		t.Error("SRID 0 (unset) should count as WGS84")
	}
	if !isWGS84(domain.Coordinate{SRID: domain.SRIDWGS84}) {
		t.Error("SRID 4326 should count as WGS84")
	}
	if isWGS84(domain.Coordinate{SRID: 25832}) {
		t.Error("SRID 25832 should not count as WGS84")
	}
}
