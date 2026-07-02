//go:build ignore

// Command gazetteer-fixture builds a small, simplified GeoPackage fixture (plus a
// golden JSON) from the full osm-admin-places.gpkg, for the gazetteer integration
// test. For a curated set of points it extracts (a) every admin polygon that
// contains the point (the Locate chain), (b) the bearing-candidate places per
// class within reach, and (c) the parent chain of each candidate's admin unit
// (needed by the boundary constraint). Admin polygons are simplified; the
// generator then re-runs Locate+Bearing against the fixture and fails if any
// result diverges from the full dataset, so simplification can't change outcomes.
//
// Usage:
//
//	go run cmd/gazetteer-fixture/main.go \
//	  -src data/gazetteer/osm-admin-places.gpkg \
//	  -manifest data/gazetteer/ortus-gazetteer.yaml \
//	  -sidecar data/gazetteer/admin_levels_west_palearctic.yaml \
//	  -out internal/app/testdata
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/jobrunner/ortus/internal/adapters/geopackage"
	"github.com/jobrunner/ortus/internal/application/gazetteer"
	"github.com/jobrunner/ortus/internal/domain"
)

type point struct {
	Label  string  `json:"label"`
	Region string  `json:"region"`
	Lat    float64 `json:"lat"`
	Lon    float64 `json:"lon"`
}

// namedPoints are the hand-picked cases; L8 language points are auto-selected.
var namedPoints = []point{
	{"Cyprus/Nicosia", "CY", 35.1856, 33.3823},
	{"Kreuzwertheim", "BY", 49.76157, 9.52404},
	{"Wertheim", "BW", 49.76028, 9.52316},
	{"Greece/Thessaloniki", "GR", 40.6401, 22.9444},
	{"Israel/Tel Aviv", "IL", 32.0853, 34.7818},
	{"Jordan/Amman", "JO", 31.9539, 35.9106},
	{"Russia/Kaliningrad", "RU", 54.7104, 20.4522},
}

// languageL8 selects N admin_level-8 units per country (deterministic by fid) and
// uses their interior point, to cover local-script names (Greek/Hebrew/Arabic/Cyrillic).
var languageL8 = map[string]int{"GR": 5, "IL": 5, "AE": 5, "RU": 5}

// reachKM mirrors DefaultBearingPolicy so extraction captures every candidate the
// bearing could pick.
var reachKM = map[string]float64{"city": 60, "town": 18, "village": 5}

func main() {
	src := flag.String("src", "data/gazetteer/osm-admin-places.gpkg", "source GeoPackage")
	manifestPath := flag.String("manifest", "data/gazetteer/ortus-gazetteer.yaml", "manifest")
	sidecarPath := flag.String("sidecar", "data/gazetteer/admin_levels_west_palearctic.yaml", "level sidecar")
	out := flag.String("out", "internal/app/testdata", "output dir")
	simplify := flag.Float64("simplify", 0.0008, "polygon simplify tolerance (degrees)")
	flag.Parse()

	if err := run(*src, *manifestPath, *sidecarPath, *out, *simplify); err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		os.Exit(1)
	}
}

func run(src, manifestPath, sidecarPath, out string, simplify float64) error {
	ctx := context.Background()
	db, err := sql.Open("sqlite3_with_extensions", "file:"+src+"?mode=ro")
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	points, err := selectPoints(ctx, db)
	if err != nil {
		return err
	}
	fmt.Printf("points: %d\n", len(points))

	adminFids, placeFids, err := collectFids(ctx, db, points)
	if err != nil {
		return err
	}
	fmt.Printf("extracting admin=%d places=%d features\n", len(adminFids), len(placeFids))

	fixture := filepath.Join(out, "gazetteer-fixture.gpkg")
	if err := buildFixture(src, fixture, adminFids, placeFids, simplify); err != nil {
		return err
	}

	// Golden values from the FULL dataset.
	golden, err := goldenValues(ctx, src, manifestPath, sidecarPath, points)
	if err != nil {
		return fmt.Errorf("golden (source): %w", err)
	}
	// Verify the fixture reproduces them.
	if err := verifyFixture(ctx, fixture, manifestPath, sidecarPath, golden); err != nil {
		return fmt.Errorf("fixture verification failed (simplification changed a result — lower -simplify): %w", err)
	}

	if err := writeGolden(filepath.Join(out, "gazetteer-golden.json"), golden); err != nil {
		return err
	}

	// Self-contained test inputs: copy the manifest, and trim the sidecar to the
	// countries actually present in the fixture (the full one is ~274 KB).
	mdata, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "gazetteer-manifest.yaml"), mdata, 0o600); err != nil {
		return err
	}
	countries, err := fixtureCountries(ctx, fixture)
	if err != nil {
		return err
	}
	if err := trimSidecar(sidecarPath, filepath.Join(out, "gazetteer-sidecar.yaml"), countries); err != nil {
		return err
	}
	fmt.Printf("OK: fixture + golden + manifest + sidecar (%d countries) written and verified\n", len(countries))
	return nil
}

// fixtureCountries returns the distinct country_iso values in the fixture (both
// layers), so the trimmed sidecar covers every equivalent the test will resolve.
func fixtureCountries(ctx context.Context, fixture string) (map[string]bool, error) {
	db, err := sql.Open("sqlite3_with_extensions", "file:"+fixture+"?mode=ro")
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	keep := map[string]bool{}
	for _, layer := range []string{"admin_levels", "places"} {
		rows, err := db.QueryContext(ctx, `SELECT DISTINCT country_iso FROM "`+layer+`" WHERE country_iso<>''`)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var iso string
			if err := rows.Scan(&iso); err != nil {
				_ = rows.Close()
				return nil, err
			}
			keep[iso] = true
		}
		_ = rows.Close()
	}
	return keep, nil
}

// trimSidecar keeps only the selected countries (plus version + equivalent_levels).
func trimSidecar(full, out string, keep map[string]bool) error {
	data, err := os.ReadFile(full)
	if err != nil {
		return err
	}
	var doc struct {
		Version          int                  `yaml:"version"`
		EquivalentLevels map[string]any       `yaml:"equivalent_levels"`
		Countries        map[string]yaml.Node `yaml:"countries"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return err
	}
	trimmed := map[string]yaml.Node{}
	for iso, node := range doc.Countries {
		if keep[iso] {
			trimmed[iso] = node
		}
	}
	doc.Countries = trimmed
	y, err := yaml.Marshal(doc)
	if err != nil {
		return err
	}
	return os.WriteFile(out, y, 0o600)
}

// selectPoints combines the named points with auto-selected L8 language points.
func selectPoints(ctx context.Context, db *sql.DB) ([]point, error) {
	points := append([]point(nil), namedPoints...)
	isos := make([]string, 0, len(languageL8))
	for iso := range languageL8 {
		isos = append(isos, iso)
	}
	sort.Strings(isos)
	for _, iso := range isos {
		rows, err := db.QueryContext(ctx, `
			SELECT name, ST_Y(ST_PointOnSurface(CastAutomagic(geom))), ST_X(ST_PointOnSurface(CastAutomagic(geom)))
			FROM admin_levels WHERE admin_level='8' AND country_iso=? AND name<>''
			ORDER BY fid LIMIT ?`, iso, languageL8[iso])
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var name string
			var lat, lon float64
			if err := rows.Scan(&name, &lat, &lon); err != nil {
				_ = rows.Close()
				return nil, err
			}
			points = append(points, point{Label: iso + "/L8 " + name, Region: iso, Lat: lat, Lon: lon})
		}
		_ = rows.Close()
	}
	return points, nil
}

// collectFids gathers the admin + place feature ids the fixture must contain.
func collectFids(ctx context.Context, db *sql.DB, points []point) (admin, place []int64, err error) {
	adminSet, placeSet := map[int64]bool{}, map[int64]bool{}
	for _, p := range points {
		// Admin polygons containing the point (the Locate chain).
		if err = addContaining(ctx, db, p, adminSet); err != nil {
			return nil, nil, err
		}
		// Bearing candidates per class + their admin parent chains.
		for class, reach := range reachKM {
			if err = addCandidates(ctx, db, p, class, reach, placeSet, adminSet); err != nil {
				return nil, nil, err
			}
		}
	}
	return sortedKeys(adminSet), sortedKeys(placeSet), nil
}

func addContaining(ctx context.Context, db *sql.DB, p point, adminSet map[int64]bool) error {
	rows, err := db.QueryContext(ctx, fmt.Sprintf(`
		SELECT fid FROM admin_levels
		WHERE fid IN (SELECT id FROM rtree_admin_levels_geom
		              WHERE minx<=%[1]f AND maxx>=%[1]f AND miny<=%[2]f AND maxy>=%[2]f)
		  AND ST_Contains(CastAutomagic(geom), MakePoint(%[1]f,%[2]f,4326))`, p.Lon, p.Lat))
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var fid int64
		if err := rows.Scan(&fid); err != nil {
			return err
		}
		adminSet[fid] = true
	}
	return rows.Err()
}

func addCandidates(ctx context.Context, db *sql.DB, p point, class string, reach float64, placeSet, adminSet map[int64]bool) error {
	dLat := reach / 111.32
	dLon := reach / (111.32 * cosLat(p.Lat))
	rows, err := db.QueryContext(ctx, fmt.Sprintf(`
		SELECT t.fid, t.admin_id FROM places t
		WHERE t.fid IN (SELECT id FROM rtree_places_geom
		                WHERE maxx>=%[3]f AND minx<=%[4]f AND maxy>=%[5]f AND miny<=%[6]f)
		  AND t.place='%[7]s'
		  AND Distance(CastAutomagic(t.geom), MakePoint(%[1]f,%[2]f,4326), 1) <= %[8]f
		ORDER BY Distance(CastAutomagic(t.geom), MakePoint(%[1]f,%[2]f,4326), 1) ASC LIMIT 10`,
		p.Lon, p.Lat, p.Lon-dLon, p.Lon+dLon, p.Lat-dLat, p.Lat+dLat, class, reach*1000))
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	var adminIDs []int64
	for rows.Next() {
		var fid int64
		var adminID sql.NullInt64
		if err := rows.Scan(&fid, &adminID); err != nil {
			return err
		}
		placeSet[fid] = true
		if adminID.Valid && adminID.Int64 != 0 {
			adminIDs = append(adminIDs, adminID.Int64)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range adminIDs {
		if err := addAncestors(ctx, db, id, adminSet); err != nil {
			return err
		}
	}
	return nil
}

// addAncestors walks parent_id from a starting admin fid up to the top.
func addAncestors(ctx context.Context, db *sql.DB, start int64, adminSet map[int64]bool) error {
	rows, err := db.QueryContext(ctx, `
		WITH RECURSIVE anc(fid) AS (
			SELECT ?
			UNION
			SELECT a.parent_id FROM admin_levels a JOIN anc ON a.fid = anc.fid
			WHERE COALESCE(a.parent_id,0) <> 0
		) SELECT fid FROM anc WHERE fid IS NOT NULL`, start)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var fid int64
		if err := rows.Scan(&fid); err != nil {
			return err
		}
		adminSet[fid] = true
	}
	return rows.Err()
}

// buildFixture extracts the fid sets into a new GeoPackage via ogr2ogr, simplifying
// polygons, then adds a minimal native spatial_ref_sys so ellipsoidal Distance
// resolves SRID 4326 without warnings.
func buildFixture(src, dst string, adminFids, placeFids []int64, simplify float64) error {
	_ = os.Remove(dst)
	admin := exec.Command("ogr2ogr", "-f", "GPKG", dst, src, "admin_levels",
		"-where", "fid IN ("+joinInts(adminFids)+")",
		"-simplify", fmt.Sprintf("%g", simplify),
		"-nln", "admin_levels", "-lco", "SPATIAL_INDEX=YES")
	if out, err := admin.CombinedOutput(); err != nil {
		return fmt.Errorf("ogr2ogr admin: %v\n%s", err, out)
	}
	places := exec.Command("ogr2ogr", "-update", "-append", dst, src, "places",
		"-where", "fid IN ("+joinInts(placeFids)+")",
		"-nln", "places", "-lco", "SPATIAL_INDEX=YES")
	if out, err := places.CombinedOutput(); err != nil {
		return fmt.Errorf("ogr2ogr places: %v\n%s", err, out)
	}
	// Minimal spatial_ref_sys (WGS84 only) so Distance(...,1) resolves cleanly.
	srs := exec.Command("spatialite", dst, `
		CREATE TABLE IF NOT EXISTS spatial_ref_sys (srid INTEGER PRIMARY KEY, auth_name TEXT, auth_srid INTEGER, ref_sys_name TEXT, proj4text TEXT, srtext TEXT);
		INSERT OR REPLACE INTO spatial_ref_sys VALUES (4326,'EPSG',4326,'WGS 84','+proj=longlat +datum=WGS84 +no_defs','');`)
	if out, err := srs.CombinedOutput(); err != nil {
		return fmt.Errorf("spatial_ref_sys: %v\n%s", err, out)
	}
	return nil
}

type goldenEntry struct {
	Point   point        `json:"point"`
	Country string       `json:"country_iso"`
	Chain   []chainLevel `json:"chain"`
	Bearing string       `json:"bearing"`
}
type chainLevel struct {
	Level      int    `json:"level"`
	Equivalent string `json:"equivalent"`
	Name       string `json:"name"`
}

func goldenValues(ctx context.Context, gpkg, manifestPath, sidecarPath string, points []point) ([]goldenEntry, error) {
	svc, closeFn, err := openService(ctx, gpkg, manifestPath, sidecarPath)
	if err != nil {
		return nil, err
	}
	defer closeFn()
	var golden []goldenEntry
	for _, p := range points {
		coord := domain.NewWGS84Coordinate(p.Lon, p.Lat)
		loc, err := svc.Locate(ctx, coord)
		if err != nil {
			return nil, fmt.Errorf("%s Locate: %w", p.Label, err)
		}
		e := goldenEntry{Point: p, Country: loc.CountryISO}
		for _, u := range loc.Chain {
			e.Chain = append(e.Chain, chainLevel{u.Level, u.Equivalent, u.Name})
		}
		fix, err := svc.Bearing(ctx, coord, domain.DefaultBearingPolicy())
		if err != nil {
			return nil, fmt.Errorf("%s Bearing: %w", p.Label, err)
		}
		e.Bearing = fix.Label
		golden = append(golden, e)
	}
	return golden, nil
}

func verifyFixture(ctx context.Context, fixture, manifestPath, sidecarPath string, want []goldenEntry) error {
	svc, closeFn, err := openService(ctx, fixture, manifestPath, sidecarPath)
	if err != nil {
		return err
	}
	defer closeFn()
	for _, e := range want {
		coord := domain.NewWGS84Coordinate(e.Point.Lon, e.Point.Lat)
		loc, err := svc.Locate(ctx, coord)
		if err != nil {
			return fmt.Errorf("%s Locate: %w", e.Point.Label, err)
		}
		got := loc.CountryISO + "|"
		for _, u := range loc.Chain {
			got += fmt.Sprintf("%d:%s;", u.Level, u.Name)
		}
		exp := e.Country + "|"
		for _, u := range e.Chain {
			exp += fmt.Sprintf("%d:%s;", u.Level, u.Name)
		}
		if got != exp {
			return fmt.Errorf("%s chain mismatch:\n got %s\nwant %s", e.Point.Label, got, exp)
		}
		fix, err := svc.Bearing(ctx, coord, domain.DefaultBearingPolicy())
		if err != nil {
			return fmt.Errorf("%s Bearing: %w", e.Point.Label, err)
		}
		if fix.Label != e.Bearing {
			return fmt.Errorf("%s bearing mismatch: got %q want %q", e.Point.Label, fix.Label, e.Bearing)
		}
	}
	return nil
}

func openService(ctx context.Context, gpkg, manifestPath, sidecarPath string) (*gazetteer.Service, func(), error) {
	mdata, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, nil, err
	}
	manifest, err := gazetteer.ParseManifest(mdata)
	if err != nil {
		return nil, nil, err
	}
	sdata, err := os.ReadFile(sidecarPath)
	if err != nil {
		return nil, nil, err
	}
	levels, err := gazetteer.ParseLevelReference(sdata)
	if err != nil {
		return nil, nil, err
	}
	idx, err := geopackage.OpenGazetteerIndex(ctx, gpkg, geopackage.Options{})
	if err != nil {
		return nil, nil, err
	}
	return gazetteer.NewService(idx, manifest, levels, nil, true), func() { _ = idx.Close() }, nil
}

func writeGolden(path string, golden []goldenEntry) error {
	data, err := json.MarshalIndent(golden, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

func cosLat(lat float64) float64 {
	c := math.Cos(lat * math.Pi / 180)
	if c < 0.01 {
		return 0.01
	}
	return c
}

func sortedKeys(m map[int64]bool) []int64 {
	out := make([]int64, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func joinInts(v []int64) string {
	s := make([]string, len(v))
	for i, x := range v {
		s[i] = fmt.Sprintf("%d", x)
	}
	return strings.Join(s, ",")
}
