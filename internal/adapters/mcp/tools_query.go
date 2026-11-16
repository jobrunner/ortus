package mcp

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jobrunner/ortus/internal/domain"
)

// registerQueryTools mounts the read-only business tools that let an AI
// agent actually USE ortus (rather than just observe it).
func registerQueryTools(srv *mcp.Server, deps Deps, logger *slog.Logger) {
	addQueryPoint(srv, deps, logger)
	addListSources(srv, deps, logger)
	addGetSource(srv, deps, logger)
	addGetSourceLayers(srv, deps, logger)
	addGetFeatureGeometry(srv, deps, logger)
	if deps.Gazetteer != nil {
		addGazetteer(srv, deps, logger)
	}
}

// selectCoordinate resolves a coordinate from optional lon/lat and x/y pairs.
// lon/lat wins when both pairs are present; a half-pair (only lon, only x) is an
// error, not a silent half-query; (0,0) is valid. srid defaults to WGS84.
func selectCoordinate(lon, lat, x, y *float64, srid int) (coordinate, error) {
	if srid == 0 {
		srid = domain.SRIDWGS84
	}
	switch {
	case lon != nil && lat != nil:
		return coordinate{X: *lon, Y: *lat, SRID: srid}, nil
	case x != nil && y != nil:
		return coordinate{X: *x, Y: *y, SRID: srid}, nil
	case lon != nil || lat != nil:
		return coordinate{}, fmt.Errorf("both 'lon' and 'lat' must be set")
	case x != nil || y != nil:
		return coordinate{}, fmt.Errorf("both 'x' and 'y' must be set")
	default:
		return coordinate{}, fmt.Errorf("coordinate required: provide lon/lat or x/y")
	}
}

// ---- query_point ----------------------------------------------------------

type queryPointIn struct {
	// Either the lon/lat pair (WGS84 shortcut) OR the x/y pair must be
	// supplied. `srid` is always optional and defaults to 4326 (WGS84)
	// when omitted — provide it when the x/y values are in a different
	// projection. Pointer fields so we can distinguish "omitted" from a
	// legitimate 0 (e.g. on the equator or the Greenwich meridian).
	// lon/lat wins when both pairs are present.
	Lon        *float64 `json:"lon,omitempty" jsonschema:"longitude in WGS84 (EPSG:4326); pair with 'lat'"`
	Lat        *float64 `json:"lat,omitempty" jsonschema:"latitude in WGS84 (EPSG:4326); pair with 'lon'"`
	X          *float64 `json:"x,omitempty" jsonschema:"easting in the given SRID; pair with 'y'"`
	Y          *float64 `json:"y,omitempty" jsonschema:"northing in the given SRID; pair with 'x'"`
	SRID       int      `json:"srid,omitempty" jsonschema:"spatial reference id for x/y; defaults to 4326 (WGS84) when omitted"`
	Properties []string `json:"properties,omitempty" jsonschema:"if set, returned features include only these property keys"`
	SourceID   string   `json:"source_id,omitempty" jsonschema:"if set, query only this single source instead of all loaded sources"`
}

func addQueryPoint(srv *mcp.Server, deps Deps, _ *slog.Logger) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "query_point",
		Description: "Point-in-polygon query: returns every geographic feature " +
			"containing the given coordinate across all loaded sources " +
			"(or a single source if source_id is set). Accepts WGS84 lon/lat " +
			"or an arbitrary x/y/srid combination. Backed by the same QueryService " +
			"as the GET /api/v1/query REST endpoint, but with stricter coordinate " +
			"validation: complete pairs only, and (0,0) is a valid input.",
	}, func(ctx toolCtx, _ *callRequest, in queryPointIn) (*callResult, *queryResponse, error) {
		// Coordinate selection: lon/lat takes precedence over x/y when both
		// pairs are present. Stricter than the REST handler — we require
		// BOTH members of a pair (passing just `lon` without `lat` is a
		// bug, not a silent half-query) and we treat (0,0) as a valid
		// input rather than a stand-in for "missing".
		coord, err := selectCoordinate(in.Lon, in.Lat, in.X, in.Y, in.SRID)
		if err != nil {
			return nil, nil, err
		}

		req := queryRequest{
			Coordinate: coord,
			SourceSRID: coord.SRID,
			Properties: in.Properties,
			SourceID:   in.SourceID,
		}
		resp, err := deps.QueryService.QueryPoint(ctx, req)
		if err != nil {
			return nil, nil, err
		}
		return nil, resp, nil
	})
}

// ---- list_sources ---------------------------------------------------------

type sourceSummary struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	LayerCount int    `json:"layer_count"`
	Indexed    bool   `json:"indexed"`
	Ready      bool   `json:"ready"`
}

type listSourcesOut struct {
	Sources []sourceSummary `json:"sources"`
	Count   int             `json:"count"`
}

func addListSources(srv *mcp.Server, deps Deps, _ *slog.Logger) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "list_sources",
		Description: "List every source currently loaded into ortus, with ready " +
			"state, layer count, and ID. Equivalent to GET /api/v1/sources.",
	}, func(ctx toolCtx, _ *callRequest, _ any) (*callResult, listSourcesOut, error) {
		pkgs, err := deps.Registry.ListSources(ctx)
		if err != nil {
			return nil, listSourcesOut{}, err
		}
		out := make([]sourceSummary, 0, len(pkgs))
		for i := range pkgs {
			p := &pkgs[i]
			out = append(out, sourceSummary{
				ID:         p.ID,
				Name:       p.Name,
				LayerCount: p.LayerCount(),
				Indexed:    p.Indexed,
				Ready:      p.IsReady(),
			})
		}
		return nil, listSourcesOut{Sources: out, Count: len(out)}, nil
	})
}

// ---- get_source ----------------------------------------------------------

type getSourceIn struct {
	SourceID string `json:"source_id" jsonschema:"id of the source (matches GET /api/v1/sources/{id})"`
}

func addGetSource(srv *mcp.Server, deps Deps, _ *slog.Logger) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_source",
		Description: "Fetch the full metadata for one source: layers, extent, " +
			"size, license, last-queried timestamp. Equivalent to GET /api/v1/sources/{id}.",
	}, func(ctx toolCtx, _ *callRequest, in getSourceIn) (*callResult, *domain.Source, error) {
		if strings.TrimSpace(in.SourceID) == "" {
			return nil, nil, fmt.Errorf("source_id is required")
		}
		pkg, err := deps.Registry.GetSource(ctx, in.SourceID)
		if err != nil {
			return nil, nil, err
		}
		return nil, pkg, nil
	})
}

// ---- get_source_layers ---------------------------------------------------

type getSourceLayersIn struct {
	SourceID string `json:"source_id" jsonschema:"id of the source to list layers from"`
}

type layerSummary struct {
	Name           string         `json:"name"`
	Description    string         `json:"description,omitempty"`
	GeometryType   string         `json:"geometry_type"`
	GeometryColumn string         `json:"geometry_column"`
	SRID           int            `json:"srid"`
	HasIndex       bool           `json:"has_index"`
	FeatureCount   int64          `json:"feature_count"`
	Extent         *domain.Extent `json:"extent,omitempty"`
}

type getSourceLayersOut struct {
	SourceID string         `json:"source_id"`
	Layers   []layerSummary `json:"layers"`
	Count    int            `json:"count"`
}

func addGetSourceLayers(srv *mcp.Server, deps Deps, _ *slog.Logger) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_source_layers",
		Description: "List the layers in a single source, with geometry type, SRID, " +
			"feature count, and bounding-box extent. Equivalent to GET /api/v1/sources/{id}/layers.",
	}, func(ctx toolCtx, _ *callRequest, in getSourceLayersIn) (*callResult, getSourceLayersOut, error) {
		if strings.TrimSpace(in.SourceID) == "" {
			return nil, getSourceLayersOut{}, fmt.Errorf("source_id is required")
		}
		pkg, err := deps.Registry.GetSource(ctx, in.SourceID)
		if err != nil {
			return nil, getSourceLayersOut{}, err
		}
		out := make([]layerSummary, 0, len(pkg.Layers))
		for i := range pkg.Layers {
			l := &pkg.Layers[i]
			out = append(out, layerSummary{
				Name:           l.Name,
				Description:    l.Description,
				GeometryType:   l.GeometryType,
				GeometryColumn: l.GeometryColumn,
				SRID:           l.SRID,
				HasIndex:       l.HasIndex,
				FeatureCount:   l.FeatureCount,
				Extent:         l.Extent,
			})
		}
		return nil, getSourceLayersOut{
			SourceID: in.SourceID,
			Layers:   out,
			Count:    len(out),
		}, nil
	})
}

// ---- get_feature_geometry -------------------------------------------------

type getFeatureGeometryIn struct {
	SourceID  string `json:"source_id" jsonschema:"id of the vector source ('SourceID' of an entry in a query_point result's 'Results')"`
	Layer     string `json:"layer" jsonschema:"layer name ('LayerName' of a feature in a query_point result)"`
	FeatureID int64  `json:"feature_id" jsonschema:"feature id ('ID' of a feature in a query_point result)"`
	SRID      int    `json:"srid,omitempty" jsonschema:"EPSG code for geometry and bbox; defaults to 4326 (WGS84) when omitted"`
}

// featureGeometryOut is the GeoJSON Feature GET
// /api/v1/sources/{id}/layers/{layer}/features/{fid} returns.
type featureGeometryOut struct {
	Type       string         `json:"type"`
	ID         int64          `json:"id"`
	BBox       []float64      `json:"bbox" jsonschema:"[min_x, min_y, max_x, max_y] in the returned srid"`
	Geometry   map[string]any `json:"geometry" jsonschema:"GeoJSON MultiPolygon in the returned srid"`
	Properties map[string]any `json:"properties"`
	SourceID   string         `json:"source_id"`
	Layer      string         `json:"layer"`
	SRID       int            `json:"srid"`
	License    *licenseOut    `json:"license,omitempty"`
}

func addGetFeatureGeometry(srv *mcp.Server, deps Deps, _ *slog.Logger) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_feature_geometry",
		Description: "Fetch the complete outline of one feature a point query found, as a GeoJSON " +
			"Feature: MultiPolygon geometry, bounding box and the license of its package, " +
			"reprojected to 'srid' (default 4326). A query_point result names the feature: " +
			"Results[].SourceID, and per feature LayerName and ID. For sources tiled with ST_Subdivide all fragments of the " +
			"feature are unioned. Polygon layers of vector sources only — raster sources and " +
			"gazetteer features are not served. Large areas return large geometries. " +
			"Equivalent to GET /api/v1/sources/{id}/layers/{layer}/features/{fid}.",
	}, func(ctx toolCtx, _ *callRequest, in getFeatureGeometryIn) (*callResult, *featureGeometryOut, error) {
		if strings.TrimSpace(in.SourceID) == "" || strings.TrimSpace(in.Layer) == "" {
			return nil, nil, fmt.Errorf("source_id and layer are required")
		}
		fg, err := deps.QueryService.FeatureGeometry(ctx, domain.FeatureRef{
			SourceID: in.SourceID, Layer: in.Layer, ID: in.FeatureID, SRID: in.SRID,
		})
		if err != nil {
			return nil, nil, err
		}
		out := &featureGeometryOut{
			Type:       "Feature",
			ID:         fg.ID,
			BBox:       []float64{fg.Extent.MinX, fg.Extent.MinY, fg.Extent.MaxX, fg.Extent.MaxY},
			Properties: fg.Properties,
			SourceID:   fg.SourceID,
			Layer:      fg.Layer,
			SRID:       fg.Extent.SRID,
		}
		if err := json.Unmarshal(fg.GeoJSON, &out.Geometry); err != nil {
			return nil, nil, fmt.Errorf("decoding geometry: %w", err)
		}
		if out.Properties == nil {
			out.Properties = map[string]any{}
		}
		if !fg.License.IsEmpty() {
			out.License = &licenseOut{Name: fg.License.Name, URL: fg.License.URL, Attribution: fg.License.Attribution}
		}
		return nil, out, nil
	})
}
