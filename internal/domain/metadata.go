package domain

import "time"

// Metadata contains GeoPackage metadata.
type Metadata struct {
	Title       string            // Title
	Description string            // Description
	Creator     string            // Creator/Author
	CreatedAt   time.Time         // Creation date
	Version     string            // Version string
	Keywords    []string          // Keywords/Tags
	Custom      map[string]string // Custom metadata fields
}

// HasKeyword checks if a keyword is present.
func (m *Metadata) HasKeyword(keyword string) bool {
	for _, k := range m.Keywords {
		if k == keyword {
			return true
		}
	}
	return false
}

// GetCustom returns a custom metadata value.
func (m *Metadata) GetCustom(key string) (string, bool) {
	if m.Custom == nil {
		return "", false
	}
	v, ok := m.Custom[key]
	return v, ok
}

// License contains license information for a GeoPackage.
type License struct {
	Name        string // License name (e.g., "CC BY 4.0")
	URL         string // Link to the license text
	Attribution string // Attribution text to display
}

// IsEmpty returns true if no license information is set.
func (l *License) IsEmpty() bool {
	return l.Name == "" && l.URL == "" && l.Attribution == ""
}

// DatasetInfo identifies a built data package: which version of the data is
// loaded, and when it was produced.
//
// It exists because a deployed .gpkg is otherwise indistinguishable from any
// other build of the same file name. The build-side package check compares the
// files on the build machine and cannot see what a server actually loaded, so
// "which vintage is live?" had no answer short of shelling onto the host.
// Both fields are optional: packages built before they existed report empty,
// which is why IsEmpty exists rather than defaulting to a fake version.
type DatasetInfo struct {
	Version string // e.g. "0.2.0"; the data version, not the ortus version
	Built   string // ISO date the package was produced, e.g. "2026-08-23"
}

// IsEmpty reports whether the package carries no identity at all.
func (d *DatasetInfo) IsEmpty() bool {
	return d.Version == "" && d.Built == ""
}

// GazetteerCapabilities reports which optional gazetteer blocks the loaded
// dataset can answer at all.
//
// Every optional block renders as null in two unrelated situations: the feature
// is not part of this deployment (layer absent from the package, DEM not wired),
// or it is and the point simply has no result. Those were indistinguishable, and
// the second case is not rare — a point on flat ground legitimately belongs to no
// mountain, which is precisely the answer the territory run-out cap was built to
// give. Without this, a stale package that silently dropped the mountains layer
// looks exactly like correct behavior.
//
// True means "this deployment can answer the block"; it says nothing about
// whether a particular point has a result.
type GazetteerCapabilities struct {
	Islands   bool // an islands layer is declared and present
	Mountains bool // a mountains layer is declared and present
	Elevation bool // a DEM is wired, so elevation can be sampled
	Exposure  bool // slope/aspect can be derived (needs the same DEM)
}

// String returns the attribution text or license name.
func (l *License) String() string {
	if l.Attribution != "" {
		return l.Attribution
	}
	return l.Name
}

// QueryResult represents the result of a point query.
type QueryResult struct {
	SourceID    string        // source identifier
	SourceName  string        // source display name
	Features    []Feature     // Found features
	License     License       // License information
	Attribution string        // Attribution text
	QueryTime   time.Duration // Query execution time
}

// FeatureCount returns the number of features in the result.
func (r *QueryResult) FeatureCount() int {
	return len(r.Features)
}

// HasFeatures returns true if features were found.
func (r *QueryResult) HasFeatures() bool {
	return len(r.Features) > 0
}

// QueryRequest represents a point query request.
type QueryRequest struct {
	Coordinate Coordinate // Query coordinate
	SourceSRID int        // Source coordinate system
	Properties []string   // Properties to return (empty = all)
	SourceID   string     // Specific source (empty = all)
}

// QueryResponse represents the full query response.
type QueryResponse struct {
	Results        []QueryResult // Results per source
	TotalFeatures  int           // Total feature count
	ProcessingTime time.Duration // Total processing time
	Coordinate     Coordinate    // Queried coordinate
}

// AddResult adds a query result to the response.
func (r *QueryResponse) AddResult(result QueryResult) {
	r.Results = append(r.Results, result)
	r.TotalFeatures += result.FeatureCount()
}
