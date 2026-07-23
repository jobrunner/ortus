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
	exp        *domain.Exposure
	expErr     error
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
func (f fakeGazetteer) Exposure(context.Context, domain.Coordinate) (*domain.Exposure, error) {
	return f.exp, f.expErr
}
func (f fakeGazetteer) Elevation(context.Context, domain.Coordinate) (*domain.Elevation, error) {
	return f.elev, f.elevErr
}

func newGazetteerServer(t *testing.T, gaz input.Gazetteer) *Server {
	return newGazetteerServerT(t, gaz, nil)
}

// newGazetteerServerT is newGazetteerServer with an optional coordinate transformer
// (for the non-WGS84 → WGS84 reprojection tests).
func newGazetteerServerT(t *testing.T, gaz input.Gazetteer, tf output.CoordinateTransformer) *Server {
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
		ServerOptions{Gazetteer: gaz, GazetteerLicense: sampleGazetteerLicense(), Transformer: tf},
	)
}

// fakeTransformer is a canned output.CoordinateTransformer for the reprojection
// tests: it maps any supported source SRID to a fixed WGS84 lon/lat.
type fakeTransformer struct {
	lon, lat     float64
	supported    bool
	transformErr error
}

func (f fakeTransformer) Transform(_ context.Context, _ domain.Coordinate, _ int) (domain.Coordinate, error) {
	if f.transformErr != nil {
		return domain.Coordinate{}, f.transformErr
	}
	return domain.NewWGS84Coordinate(f.lon, f.lat), nil
}
func (f fakeTransformer) IsSupported(_, _ int) bool { return f.supported }

// TestQueryWGS84Block: a WGS84 /query carries the always-present wgs84 block
// (lon/lat = the input), no transformer needed.
func TestQueryWGS84Block(t *testing.T) {
	srv := newGazetteerServer(t, fakeGazetteer{loc: sampleLocality(), fix: sampleFix()})
	_, body := doGET(t, srv, "/api/v1/query?lon=9.93&lat=49.79")
	w, ok := body["wgs84"].(map[string]any)
	if !ok || w["lon"] != 9.93 || w["lat"] != 49.79 {
		t.Fatalf("wgs84 = %v, want {lon:9.93, lat:49.79}", body["wgs84"])
	}
}

// TestQueryReprojectsNonWGS84: a 3857 /query is reprojected to WGS84 → the wgs84
// block AND the gazetteer block are both present (previously gazetteer was skipped
// for non-WGS84).
func TestQueryReprojectsNonWGS84(t *testing.T) {
	tf := fakeTransformer{lon: -16.856, lat: 28.60, supported: true}
	srv := newGazetteerServerT(t, fakeGazetteer{loc: sampleLocality(), fix: sampleFix()}, tf)
	_, body := doGET(t, srv, "/api/v1/query?x=-1876403.675&y=3291468.780&srid=3857")
	w, ok := body["wgs84"].(map[string]any)
	if !ok || w["lon"] != -16.856 || w["lat"] != 28.60 {
		t.Fatalf("wgs84 = %v, want reprojected {-16.856, 28.60}", body["wgs84"])
	}
	if _, ok := body["gazetteer"].(map[string]any); !ok {
		t.Fatalf("gazetteer block missing for a reprojected 3857 query; got %v", body["gazetteer"])
	}
}

// TestQueryNonWGS84NoTransformer: without a transformer a projected query can't be
// reprojected → no wgs84 and no gazetteer (graceful skip, core query still returns).
func TestQueryNonWGS84NoTransformer(t *testing.T) {
	srv := newGazetteerServer(t, fakeGazetteer{loc: sampleLocality(), fix: sampleFix()})
	_, body := doGET(t, srv, "/api/v1/query?x=-1876403.675&y=3291468.780&srid=3857")
	if body["wgs84"] != nil {
		t.Errorf("wgs84 = %v, want absent without a transformer", body["wgs84"])
	}
	if body["gazetteer"] != nil {
		t.Errorf("gazetteer = %v, want absent for a non-transformable SRID", body["gazetteer"])
	}
}

// readyQuerier is a minimal query-service registry reporting a single ready
// source with no features, so a per-source /query returns 200 (empty results).
// That is enough to assert the per-source response envelope (the wgs84 block)
// without standing up a storage fixture — the default gazetteer test harness
// loads no sources, so /api/v1/query/{sourceId} would otherwise 404.
type readyQuerier struct{ id string }

func (r readyQuerier) ReadySourceIDs() []string { return []string{r.id} }
func (r readyQuerier) GetSource(context.Context, string) (*domain.Source, error) {
	return &domain.Source{ID: r.id, Name: r.id}, nil
}
func (r readyQuerier) Query(context.Context, string, string, domain.Coordinate) ([]domain.Feature, error) {
	return nil, nil
}
func (r readyQuerier) QueryPoints(_ context.Context, _, _ string, coords []domain.Coordinate) ([][]domain.Feature, error) {
	return make([][]domain.Feature, len(coords)), nil
}

// newQuerySourceServer builds a Server whose query service has one ready source,
// so GET /api/v1/query/{sourceId} reaches 200.
func newQuerySourceServer(t *testing.T) *Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	reg := application.NewSourceRegistry(
		[]output.SpatialSource{&mockRepository{}}, &mockStorage{},
		noop.NewMeterProvider().Meter("test"), output.NoOpTracer{}, logger, "/tmp",
	)
	_ = reg.LoadAll(context.Background())
	health := application.NewHealthService(reg, true, output.NoOpTracer{})
	query := application.NewQueryService(readyQuerier{id: "districts"}, nil,
		noop.NewMeterProvider().Meter("test"), output.NoOpTracer{}, logger, application.QueryServiceConfig{})
	return NewServer(
		config.ServerConfig{Host: "localhost", Port: 8080, ReadTimeout: time.Second, WriteTimeout: time.Second},
		query, reg, health, nil, logger, false, ServerOptions{},
	)
}

// TestQuerySourceWGS84Block: a per-source query (/api/v1/query/{sourceId}) carries
// the wgs84 block just like /query, but never attaches the gazetteer block.
func TestQuerySourceWGS84Block(t *testing.T) {
	srv := newQuerySourceServer(t)
	rec, body := doGET(t, srv, "/api/v1/query/districts?lon=9.93&lat=49.79")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	w, ok := body["wgs84"].(map[string]any)
	if !ok || w["lon"] != 9.93 || w["lat"] != 49.79 {
		t.Fatalf("per-source wgs84 = %v, want {lon:9.93, lat:49.79}", body["wgs84"])
	}
	if _, hasGaz := body["gazetteer"]; hasGaz {
		t.Errorf("per-source query must NOT attach a gazetteer block; got %v", body["gazetteer"])
	}
}

// TestGazetteerEndpointReprojects3857: the dedicated endpoint now serves a
// projected coordinate by reprojecting it (was 422 before), and carries wgs84.
func TestGazetteerEndpointReprojects3857(t *testing.T) {
	tf := fakeTransformer{lon: -16.856, lat: 28.60, supported: true}
	srv := newGazetteerServerT(t, fakeGazetteer{loc: sampleLocality(), fix: sampleFix()}, tf)
	rec, body := doGET(t, srv, "/api/v1/gazetteer?x=-1876403.675&y=3291468.780&srid=3857")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	w, ok := body["wgs84"].(map[string]any)
	if !ok || w["lon"] != -16.856 {
		t.Errorf("wgs84 = %v, want reprojected", body["wgs84"])
	}
	if _, ok := body["admin"].(map[string]any); !ok {
		t.Errorf("admin block missing; got %v", body["admin"])
	}
}

// TestGazetteerEndpointRejectsUntransformable: a projected coordinate with no
// transformer available is refused (422), not silently empty.
func TestGazetteerEndpointRejectsUntransformable(t *testing.T) {
	srv := newGazetteerServer(t, fakeGazetteer{loc: sampleLocality(), fix: sampleFix()})
	rec, _ := doGET(t, srv, "/api/v1/gazetteer?x=-1876403.675&y=3291468.780&srid=3857")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 for a non-transformable SRID", rec.Code)
	}
}

// TestGazetteerEndpointTransformErrorIs5xx: a transformer that SUPPORTS the pair
// but fails the transform is an internal error → 5xx, not a client 422.
func TestGazetteerEndpointTransformErrorIs5xx(t *testing.T) {
	tf := fakeTransformer{supported: true, transformErr: errors.New("proj boom")}
	srv := newGazetteerServerT(t, fakeGazetteer{loc: sampleLocality(), fix: sampleFix()}, tf)
	rec, _ := doGET(t, srv, "/api/v1/gazetteer?x=-1876403.675&y=3291468.780&srid=3857")
	if rec.Code < 500 {
		t.Fatalf("status = %d, want 5xx for an internal transform failure (not 422)", rec.Code)
	}
}

// TestQueryTransformErrorStillReturnsCore: an internal transform failure on /query
// omits wgs84 + gazetteer but must NOT fail the core query (still 200).
func TestQueryTransformErrorStillReturnsCore(t *testing.T) {
	tf := fakeTransformer{supported: true, transformErr: errors.New("proj boom")}
	srv := newGazetteerServerT(t, fakeGazetteer{loc: sampleLocality(), fix: sampleFix()}, tf)
	rec, body := doGET(t, srv, "/api/v1/query?x=-1876403.675&y=3291468.780&srid=3857")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (transform failure must not sink the query)", rec.Code)
	}
	if body["wgs84"] != nil || body["gazetteer"] != nil {
		t.Errorf("wgs84/gazetteer should be omitted on transform failure; got %v / %v", body["wgs84"], body["gazetteer"])
	}
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
	// Assert by content, not index: ordering isn't part of the HTTP contract.
	var sylt map[string]any
	for _, it := range arr {
		if m, ok := it.(map[string]any); ok && m["name"] == "Sylt" {
			sylt = m
		}
	}
	if sylt == nil {
		t.Fatalf("islands missing a Sylt entry; got %v", arr)
	}
	if sylt["name_native"] != "Söl'ring" || sylt["name_source"] != "latin-osm" {
		t.Errorf("Sylt island = %v, want Söl'ring/latin-osm", sylt)
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

func TestGazetteerExposureBlock(t *testing.T) {
	exp := &domain.Exposure{
		SlopeDeg: 12.5, SlopePercent: 22.2, AspectDeg: 135, AspectCompass: "SE",
		Flat: false, SampleSpacingM: 30,
		License: domain.License{Name: "Copernicus DEM GLO-30", URL: "https://example/glo30", Attribution: "© DLR/Airbus/ESA"},
	}
	srv := newGazetteerServer(t, fakeGazetteer{loc: sampleLocality(), fix: sampleFix(), exp: exp})
	rec, body := doGET(t, srv, "/api/v1/gazetteer?lon=9.93&lat=49.79")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	e, ok := body["exposure"].(map[string]any)
	if !ok {
		t.Fatalf("exposure missing; got %v", body["exposure"])
	}
	if e["slope_deg"] != 12.5 || e["slope_percent"] != 22.2 {
		t.Errorf("exposure slope = %v/%v, want 12.5/22.2", e["slope_deg"], e["slope_percent"])
	}
	if e["aspect_deg"] != 135.0 || e["aspect_compass"] != "SE" || e["flat"] != false {
		t.Errorf("exposure aspect = %v/%v flat=%v, want 135/SE/false", e["aspect_deg"], e["aspect_compass"], e["flat"])
	}
	if e["sample_spacing_m"] != 30.0 {
		t.Errorf("sample_spacing_m = %v, want 30", e["sample_spacing_m"])
	}
	src, ok := e["source"].(map[string]any)
	if !ok || src["name"] != "Copernicus DEM GLO-30" {
		t.Fatalf("exposure.source = %v, want Copernicus DEM", e["source"])
	}
}

func TestGazetteerExposureFlatOmitsAspect(t *testing.T) {
	// A flat point: aspect is undefined → aspect_deg null, aspect_compass empty.
	exp := &domain.Exposure{SlopeDeg: 0.4, SlopePercent: 0.7, Flat: true, SampleSpacingM: 30}
	srv := newGazetteerServer(t, fakeGazetteer{loc: sampleLocality(), fix: sampleFix(), exp: exp})
	_, body := doGET(t, srv, "/api/v1/gazetteer?lon=9.93&lat=49.79")
	e, ok := body["exposure"].(map[string]any)
	if !ok {
		t.Fatalf("exposure missing; got %v", body["exposure"])
	}
	if e["flat"] != true || e["aspect_deg"] != nil || e["aspect_compass"] != "" {
		t.Errorf("flat exposure = flat:%v aspect_deg:%v compass:%v, want true/null/empty", e["flat"], e["aspect_deg"], e["aspect_compass"])
	}
}

func TestGazetteerExposureOmittedWhenAbsent(t *testing.T) {
	srv := newGazetteerServer(t, fakeGazetteer{loc: sampleLocality(), fix: sampleFix()})
	_, body := doGET(t, srv, "/api/v1/gazetteer?lon=9.93&lat=49.79")
	if body["exposure"] != nil {
		t.Errorf("exposure = %v, want null when unavailable", body["exposure"])
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
