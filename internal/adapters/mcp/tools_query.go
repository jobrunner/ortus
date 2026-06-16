package mcp

import (
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
	addListPackages(srv, deps, logger)
	addGetPackage(srv, deps, logger)
	addGetPackageLayers(srv, deps, logger)
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
	PackageID  string   `json:"package_id,omitempty" jsonschema:"if set, query only this single package instead of all loaded packages"`
}

func addQueryPoint(srv *mcp.Server, deps Deps, _ *slog.Logger) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "query_point",
		Description: "Point-in-polygon query: returns every geographic feature " +
			"containing the given coordinate across all loaded GeoPackages " +
			"(or a single package if package_id is set). Accepts WGS84 lon/lat " +
			"or an arbitrary x/y/srid combination. Backed by the same QueryService " +
			"as the GET /api/v1/query REST endpoint, but with stricter coordinate " +
			"validation: complete pairs only, and (0,0) is a valid input.",
	}, func(ctx toolCtx, _ *callRequest, in queryPointIn) (*callResult, *queryResponse, error) {
		// Coordinate selection: lon/lat takes precedence over x/y when both
		// pairs are present. Stricter than the REST handler — we require
		// BOTH members of a pair (passing just `lon` without `lat` is a
		// bug, not a silent half-query) and we treat (0,0) as a valid
		// input rather than a stand-in for "missing".
		srid := in.SRID
		if srid == 0 {
			srid = domain.SRIDWGS84
		}
		var coord coordinate
		switch {
		case in.Lon != nil && in.Lat != nil:
			coord = coordinate{X: *in.Lon, Y: *in.Lat, SRID: srid}
		case in.X != nil && in.Y != nil:
			coord = coordinate{X: *in.X, Y: *in.Y, SRID: srid}
		case in.Lon != nil || in.Lat != nil:
			return nil, nil, fmt.Errorf("both 'lon' and 'lat' must be set")
		case in.X != nil || in.Y != nil:
			return nil, nil, fmt.Errorf("both 'x' and 'y' must be set")
		default:
			return nil, nil, fmt.Errorf("coordinate required: provide lon/lat or x/y")
		}

		req := queryRequest{
			Coordinate: coord,
			SourceSRID: srid,
			Properties: in.Properties,
			PackageID:  in.PackageID,
		}
		resp, err := deps.QueryService.QueryPoint(ctx, req)
		if err != nil {
			return nil, nil, err
		}
		return nil, resp, nil
	})
}

// ---- list_packages --------------------------------------------------------

type packageSummary struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	LayerCount int    `json:"layer_count"`
	Indexed    bool   `json:"indexed"`
	Ready      bool   `json:"ready"`
}

type listPackagesOut struct {
	Packages []packageSummary `json:"packages"`
	Count    int              `json:"count"`
}

func addListPackages(srv *mcp.Server, deps Deps, _ *slog.Logger) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "list_packages",
		Description: "List every GeoPackage currently loaded into ortus, with ready " +
			"state, layer count, and ID. Equivalent to GET /api/v1/packages.",
	}, func(ctx toolCtx, _ *callRequest, _ any) (*callResult, listPackagesOut, error) {
		pkgs, err := deps.Registry.ListPackages(ctx)
		if err != nil {
			return nil, listPackagesOut{}, err
		}
		out := make([]packageSummary, 0, len(pkgs))
		for i := range pkgs {
			p := &pkgs[i]
			out = append(out, packageSummary{
				ID:         p.ID,
				Name:       p.Name,
				LayerCount: p.LayerCount(),
				Indexed:    p.Indexed,
				Ready:      p.IsReady(),
			})
		}
		return nil, listPackagesOut{Packages: out, Count: len(out)}, nil
	})
}

// ---- get_package ----------------------------------------------------------

type getPackageIn struct {
	PackageID string `json:"package_id" jsonschema:"id of the package (matches GET /api/v1/packages/{id})"`
}

func addGetPackage(srv *mcp.Server, deps Deps, _ *slog.Logger) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_package",
		Description: "Fetch the full metadata for one GeoPackage: layers, extent, " +
			"size, license, last-queried timestamp. Equivalent to GET /api/v1/packages/{id}.",
	}, func(ctx toolCtx, _ *callRequest, in getPackageIn) (*callResult, *domain.GeoPackage, error) {
		if strings.TrimSpace(in.PackageID) == "" {
			return nil, nil, fmt.Errorf("package_id is required")
		}
		pkg, err := deps.Registry.GetPackage(ctx, in.PackageID)
		if err != nil {
			return nil, nil, err
		}
		return nil, pkg, nil
	})
}

// ---- get_package_layers ---------------------------------------------------

type getPackageLayersIn struct {
	PackageID string `json:"package_id" jsonschema:"id of the package to list layers from"`
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

type getPackageLayersOut struct {
	PackageID string         `json:"package_id"`
	Layers    []layerSummary `json:"layers"`
	Count     int            `json:"count"`
}

func addGetPackageLayers(srv *mcp.Server, deps Deps, _ *slog.Logger) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_package_layers",
		Description: "List the layers in a single GeoPackage, with geometry type, SRID, " +
			"feature count, and bounding-box extent. Equivalent to GET /api/v1/packages/{id}/layers.",
	}, func(ctx toolCtx, _ *callRequest, in getPackageLayersIn) (*callResult, getPackageLayersOut, error) {
		if strings.TrimSpace(in.PackageID) == "" {
			return nil, getPackageLayersOut{}, fmt.Errorf("package_id is required")
		}
		pkg, err := deps.Registry.GetPackage(ctx, in.PackageID)
		if err != nil {
			return nil, getPackageLayersOut{}, err
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
		return nil, getPackageLayersOut{
			PackageID: in.PackageID,
			Layers:    out,
			Count:     len(out),
		}, nil
	})
}
