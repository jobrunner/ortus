package geopackage

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"math"
	"runtime"

	"github.com/mattn/go-sqlite3"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"

	"github.com/jobrunner/ortus/internal/domain"
	"github.com/jobrunner/ortus/internal/ports/output"
)

// transformerDriver is a dedicated driver for the transformer's in-memory
// database. Its ConnectHook initializes the SpatiaLite metadata on EVERY new
// connection: ":memory:" is a separate, empty database per connection, and
// database/sql opens and closes connections as load demands. Initializing only
// the first connection (as before) left any further connection without
// spatial_ref_sys, and once the pool had dropped the initialized one, every
// transform failed until restart.
const transformerDriver = "sqlite3_transformer"

// transformerInitSQL populates spatial_ref_sys with the full EPSG definitions
// required by Transform. A variable so tests can simulate a failing init.
var transformerInitSQL = "SELECT InitSpatialMetaDataFull(1)"

func init() {
	sql.Register(transformerDriver, &sqlite3.SQLiteDriver{
		Extensions:  getSpatiaLiteLibraryPaths(),
		ConnectHook: initTransformerConn,
	})
}

// initTransformerConn initializes one fresh transformer connection. SpatiaLite
// reports an init failure through the return value (0) rather than an SQL
// error, so the value is checked explicitly; a returned error makes the driver
// discard the connection instead of handing an unusable one to the pool.
func initTransformerConn(conn *sqlite3.SQLiteConn) error {
	rows, err := conn.Query(transformerInitSQL, nil)
	if err != nil {
		return fmt.Errorf("initializing SpatiaLite metadata for transformer: %w", err)
	}
	defer func() { _ = rows.Close() }()

	dest := make([]driver.Value, len(rows.Columns()))
	if err := rows.Next(dest); err != nil {
		if errors.Is(err, io.EOF) {
			return errors.New("initializing SpatiaLite metadata for transformer: no result")
		}
		return fmt.Errorf("initializing SpatiaLite metadata for transformer: %w", err)
	}
	if rc, ok := dest[0].(int64); !ok || rc != 1 {
		return fmt.Errorf("initializing SpatiaLite metadata for transformer: returned %v, want 1", dest[0])
	}
	return nil
}

// Canary pair (Frankfurt am Main) used to prove the transformer actually
// reprojects: at startup and on every health check.
var (
	canaryWGS84 = domain.NewWGS84Coordinate(8.68, 50.1)
	canaryUTM   = domain.Coordinate{X: 477114.161254, Y: 5549798.285525, SRID: 25832}
)

// canaryToleranceM is generous against PROJ version drift yet far below any
// real transformation error (a wrong datum is off by tens of meters).
const canaryToleranceM = 0.5

// RepositoryTransformer implements CoordinateTransformer using an in-memory SpatiaLite database.
// We use a separate in-memory database because GeoPackage files are opened read-only
// and don't have the spatial_ref_sys table required by ST_Transform.
type RepositoryTransformer struct {
	db       *sql.DB
	tracer   output.Tracer
	failures metric.Int64Counter
	// srids holds every SRID spatial_ref_sys defines, loaded once at startup
	// (InitSpatialMetaDataFull is deterministic, so every connection has the
	// same set). IsSupported answers from it without a query.
	srids map[int]struct{}
}

// NewRepositoryTransformer creates a transformer with an in-memory SpatiaLite
// database. It fails if the database can't be opened or a canary transform
// doesn't reproduce a known reference coordinate — otherwise the transformer
// would look healthy but fail every later ST_Transform.
func NewRepositoryTransformer(_ *Repository) (*RepositoryTransformer, error) {
	db, err := sql.Open(transformerDriver, ":memory:")
	if err != nil {
		return nil, fmt.Errorf("opening in-memory SpatiaLite db for transformer: %w", err)
	}

	// Keep every connection once opened: each one pays the metadata init
	// (thousands of spatial_ref_sys rows), so churning them would make
	// transforms slow. One connection per CPU is plenty for a sub-millisecond
	// query; GOMAXPROCS honors a container CPU limit.
	n := runtime.GOMAXPROCS(0)
	db.SetMaxOpenConns(n)
	db.SetMaxIdleConns(n)
	db.SetConnMaxLifetime(0)
	db.SetConnMaxIdleTime(0)

	t := &RepositoryTransformer{db: db, tracer: output.NoOpTracer{}}
	t.SetMeter(nil)
	if err := t.canary(context.Background()); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("coordinate transformer self-test: %w", err)
	}
	if t.srids, err = loadSRIDs(context.Background(), db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return t, nil
}

// loadSRIDs reads the SRIDs the transformer can reproject from and to. The
// pseudo-SRIDs 0 and -1 ("undefined" cartesian/geographic) are left out: no
// projection is defined for them.
func loadSRIDs(ctx context.Context, db *sql.DB) (map[int]struct{}, error) {
	rows, err := db.QueryContext(ctx, "SELECT srid FROM spatial_ref_sys WHERE srid > 0")
	if err != nil {
		return nil, fmt.Errorf("loading transformer SRIDs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	srids := make(map[int]struct{})
	for rows.Next() {
		var srid int
		if err := rows.Scan(&srid); err != nil {
			return nil, fmt.Errorf("loading transformer SRIDs: %w", err)
		}
		srids[srid] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("loading transformer SRIDs: %w", err)
	}
	return srids, nil
}

// SetTracer wires a tracer into the repository transformer.
func (t *RepositoryTransformer) SetTracer(tr output.Tracer) {
	if tr == nil {
		tr = output.NoOpTracer{}
	}
	t.tracer = tr
}

// SetMeter wires the failure counter into the transformer; nil disables it.
func (t *RepositoryTransformer) SetMeter(m metric.Meter) {
	if m == nil {
		m = noop.NewMeterProvider().Meter("ortus/geopackage")
	}
	t.failures, _ = m.Int64Counter(
		"ortus.coordinate.transform.failures",
		metric.WithDescription("Coordinate transformations that failed inside the server"),
	)
}

// Transform transforms a coordinate from one SRID to another.
func (t *RepositoryTransformer) Transform(ctx context.Context, coord domain.Coordinate, targetSRID int) (domain.Coordinate, error) {
	if coord.SRID == targetSRID {
		return coord, nil
	}

	ctx, span := t.tracer.Start(ctx, "RepositoryTransformer.Transform",
		output.WithSpanKind(output.SpanKindClient),
		output.WithAttributes(
			output.String("db.system", "sqlite"),
			output.String("db.statement", "SELECT X(Transform(GeomFromText(?, ?), ?)), Y(Transform(GeomFromText(?, ?), ?))"),
			output.Int("ortus.coordinate.from_srid", coord.SRID),
			output.Int("ortus.coordinate.to_srid", targetSRID),
		),
	)
	defer span.End()

	if t.db == nil {
		err := fmt.Errorf("transformer database not initialized")
		span.RecordError(err)
		span.SetStatus(output.StatusError, "db not initialized")
		return domain.Coordinate{}, err
	}

	result, err := TransformCoordinate(ctx, t.db, coord, targetSRID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(output.StatusError, "transform failed")
		if !errors.Is(err, context.Canceled) {
			t.failures.Add(ctx, 1, metric.WithAttributes(
				attribute.Int("from_srid", coord.SRID),
				attribute.Int("to_srid", targetSRID),
			))
		}
		return result, err
	}
	return result, nil
}

// Check verifies the transformer still reprojects correctly by running the
// canary transform. Used by the health endpoint, so a transformer that broke
// at runtime is reported instead of silently degrading query responses.
func (t *RepositoryTransformer) Check(ctx context.Context) error {
	ctx, span := t.tracer.Start(ctx, "RepositoryTransformer.Check")
	defer span.End()

	if err := t.canary(ctx); err != nil {
		span.RecordError(err)
		span.SetStatus(output.StatusError, "canary failed")
		return err
	}
	return nil
}

// canary transforms the reference point and compares against its known
// projection. Checking the value (not just the absence of an error) also
// catches a PROJ setup that silently returns untransformed coordinates.
func (t *RepositoryTransformer) canary(ctx context.Context) error {
	if t.db == nil {
		return fmt.Errorf("transformer database not initialized")
	}
	got, err := TransformCoordinate(ctx, t.db, canaryWGS84, canaryUTM.SRID)
	if err != nil {
		return err
	}
	if math.Abs(got.X-canaryUTM.X) > canaryToleranceM || math.Abs(got.Y-canaryUTM.Y) > canaryToleranceM {
		return fmt.Errorf("canary EPSG:4326 -> EPSG:%d = (%.3f, %.3f), want (%.3f, %.3f)",
			canaryUTM.SRID, got.X, got.Y, canaryUTM.X, canaryUTM.Y)
	}
	return nil
}

// IsSupported reports whether both SRIDs are defined in spatial_ref_sys, i.e.
// whether a transform between them can succeed. A client-supplied SRID that
// fails this check is invalid input, not a transformer fault.
func (t *RepositoryTransformer) IsSupported(sourceSRID, targetSRID int) bool {
	_, src := t.srids[sourceSRID]
	_, dst := t.srids[targetSRID]
	return src && dst
}

// Close closes the transformer's database connection.
func (t *RepositoryTransformer) Close() error {
	if t.db != nil {
		return t.db.Close()
	}
	return nil
}

// TransformCoordinate transforms a coordinate using a provided database. This
// helper is used by RepositoryTransformer; tracing is added by the calling
// code (RepositoryTransformer.Transform) so this stays a thin SQL helper.
func TransformCoordinate(ctx context.Context, db *sql.DB, coord domain.Coordinate, targetSRID int) (domain.Coordinate, error) {
	if coord.SRID == targetSRID {
		return coord, nil
	}

	query := `SELECT X(Transform(GeomFromText(?, ?), ?)), Y(Transform(GeomFromText(?, ?), ?))`
	wkt := coord.WKT()

	var x, y float64
	err := db.QueryRowContext(ctx, query,
		wkt, coord.SRID, targetSRID,
		wkt, coord.SRID, targetSRID,
	).Scan(&x, &y)
	if err != nil {
		return domain.Coordinate{}, fmt.Errorf("transforming coordinate: %w", err)
	}

	return domain.Coordinate{
		X:    x,
		Y:    y,
		SRID: targetSRID,
	}, nil
}
