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

// String returns the attribution text or license name.
func (l *License) String() string {
	if l.Attribution != "" {
		return l.Attribution
	}
	return l.Name
}

// QueryResult represents the result of a point query.
type QueryResult struct {
	PackageID   string        // GeoPackage identifier
	PackageName string        // GeoPackage display name
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
	PackageID  string     // Specific package (empty = all)
}

// QueryResponse represents the full query response.
type QueryResponse struct {
	Results        []QueryResult // Results per GeoPackage
	TotalFeatures  int           // Total feature count
	ProcessingTime time.Duration // Total processing time
	Coordinate     Coordinate    // Queried coordinate
}

// AddResult adds a query result to the response.
func (r *QueryResponse) AddResult(result QueryResult) {
	r.Results = append(r.Results, result)
	r.TotalFeatures += result.FeatureCount()
}
